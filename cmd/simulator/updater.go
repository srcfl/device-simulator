package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/blang/semver"
	"github.com/google/go-github/v50/github"
	"golang.org/x/oauth2"
)

// Version is set at build time via ldflags
var Version = "0.0.0-dev"

// GitHubToken is set at build time via ldflags for private repo access
var GitHubToken = ""

const (
	// GitHub repository for updates
	repoOwner = "srcfl"
	repoName  = "device-simulator"
)

// UpdateInfo contains information about an available update
type UpdateInfo struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	ReleaseURL     string `json:"release_url"`
	ReleaseNotes   string `json:"release_notes"`
	AssetURL       string `json:"asset_url"`
}

// getGitHubClient returns a GitHub API client, with auth if token is available
func getGitHubClient() *github.Client {
	ctx := context.Background()
	if GitHubToken != "" {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: GitHubToken})
		tc := oauth2.NewClient(ctx, ts)
		return github.NewClient(tc)
	}
	return github.NewClient(nil)
}

// fetchLatestRelease gets the latest release info directly from GitHub API
func fetchLatestRelease() (*github.RepositoryRelease, error) {
	client := getGitHubClient()
	release, _, err := client.Repositories.GetLatestRelease(context.Background(), repoOwner, repoName)
	return release, err
}

// extractChangelog extracts and cleans up the changelog from release notes
func extractChangelog(body string) string {
	lines := strings.Split(body, "\n")
	var changelog []string
	inChangelog := false

	for _, line := range lines {
		// Start capturing after "In this version:" header
		if strings.Contains(line, "In this version:") {
			inChangelog = true
			continue
		}
		// Stop at next section header
		if inChangelog && strings.HasPrefix(line, "## ") {
			break
		}
		// Capture changelog lines
		if inChangelog {
			// Clean up the line
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// Convert markdown bullet to clean bullet
			if strings.HasPrefix(line, "- ") {
				line = "• " + strings.TrimPrefix(line, "- ")
			}
			changelog = append(changelog, line)
		}
	}

	if len(changelog) == 0 {
		return ""
	}

	return strings.Join(changelog, "\n")
}

// CheckForUpdate checks GitHub releases for a newer version
func CheckForUpdate() (*UpdateInfo, error) {
	info := &UpdateInfo{
		Available:      false,
		CurrentVersion: Version,
	}

	// Parse current version (may fail for dev versions)
	current, parseErr := semver.Parse(Version)
	isDevVersion := parseErr != nil

	// Log token status for debugging
	if GitHubToken == "" {
		log.Printf("Update check: No GitHub token configured")
	} else {
		log.Printf("Update check: GitHub token configured (length: %d)", len(GitHubToken))
	}

	// Fetch latest release directly from GitHub API
	release, err := fetchLatestRelease()
	if err != nil {
		log.Printf("Update check failed: %v", err)
		if GitHubToken == "" {
			info.LatestVersion = "no token"
			info.ReleaseNotes = "Cannot check updates: No access token configured.\n\nThis is a development build. Release builds have update checking enabled."
		} else {
			info.LatestVersion = "error"
			info.ReleaseNotes = fmt.Sprintf("Could not check for updates: %v", err)
		}
		return info, nil
	}

	log.Printf("Fetched release: %+v", release)

	if release == nil || release.TagName == nil {
		log.Printf("No release or tag name found")
		if GitHubToken == "" {
			info.LatestVersion = "unknown"
			info.ReleaseNotes = "Cannot check updates: No access token configured for private repository."
		} else {
			info.LatestVersion = "no releases"
		}
		return info, nil
	}

	// Parse the latest version from tag (strip 'v' prefix if present)
	latestTag := *release.TagName
	latestVersionStr := strings.TrimPrefix(latestTag, "v")
	info.LatestVersion = latestVersionStr

	if release.HTMLURL != nil {
		info.ReleaseURL = *release.HTMLURL
	}
	if release.Body != nil {
		info.ReleaseNotes = extractChangelog(*release.Body)
	}

	// Find asset URL for current platform
	// Use API URL for private repos (works with token auth), browser URL as fallback
	osName := runtime.GOOS
	archName := runtime.GOARCH

	// Build list of acceptable architectures (with fallbacks)
	archOptions := []string{archName}
	// On Apple Silicon (darwin/arm64), also accept amd64 (runs via Rosetta 2)
	if osName == "darwin" && archName == "arm64" {
		archOptions = append(archOptions, "amd64")
	}

	for _, arch := range archOptions {
		for _, asset := range release.Assets {
			if asset.Name != nil {
				name := strings.ToLower(*asset.Name)
				if strings.Contains(name, osName) && strings.Contains(name, arch) {
					// Prefer API URL for private repo downloads (requires token)
					if asset.URL != nil && GitHubToken != "" {
						info.AssetURL = *asset.URL
					} else if asset.BrowserDownloadURL != nil {
						info.AssetURL = *asset.BrowserDownloadURL
					}
					break
				}
			}
		}
		if info.AssetURL != "" {
			break
		}
	}

	// Compare versions (only if we have a valid current version)
	if !isDevVersion {
		latestVersion, err := semver.Parse(latestVersionStr)
		if err == nil && latestVersion.GT(current) {
			info.Available = true
			log.Printf("Update available: %s -> %s", Version, latestVersionStr)
		}
	} else {
		log.Printf("Dev version detected, latest release is %s", latestVersionStr)
	}

	return info, nil
}

// DoUpdate downloads and applies the update
func DoUpdate() error {
	// First check for update to get asset URL
	info, err := CheckForUpdate()
	if err != nil {
		return fmt.Errorf("failed to check for update: %v", err)
	}

	if !info.Available {
		return fmt.Errorf("no update available")
	}

	if info.AssetURL == "" {
		return fmt.Errorf("no download asset found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	log.Printf("Downloading update from: %s", info.AssetURL)

	// Create temp directory for download
	tempDir, err := os.MkdirTemp("", "device-simulator-update-")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Download the asset
	assetPath, err := downloadAsset(info.AssetURL, tempDir)
	if err != nil {
		return fmt.Errorf("failed to download update: %v", err)
	}
	log.Printf("Downloaded to: %s", assetPath)

	// Extract the archive
	extractDir := filepath.Join(tempDir, "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return fmt.Errorf("failed to create extract directory: %v", err)
	}

	if strings.HasSuffix(assetPath, ".zip") {
		if err := extractZip(assetPath, extractDir); err != nil {
			return fmt.Errorf("failed to extract zip: %v", err)
		}
	} else if strings.HasSuffix(assetPath, ".tar.gz") {
		if err := extractTarGz(assetPath, extractDir); err != nil {
			return fmt.Errorf("failed to extract tar.gz: %v", err)
		}
	} else {
		return fmt.Errorf("unknown archive format: %s", assetPath)
	}
	log.Printf("Extracted to: %s", extractDir)

	// Apply the update based on platform
	if err := applyUpdate(extractDir); err != nil {
		return fmt.Errorf("failed to apply update: %v", err)
	}

	log.Printf("Successfully updated to version %s", info.LatestVersion)
	log.Printf("Please restart the application to use the new version")

	return nil
}

// downloadAsset downloads the release asset to temp directory
func downloadAsset(url string, destDir string) (string, error) {
	// For private repos, we need to use GitHub API to download assets
	// The BrowserDownloadURL redirects to S3 and doesn't pass auth headers

	// Create HTTP client that doesn't follow redirects automatically
	// so we can add auth to the redirected request
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Add auth header to redirected requests if we have a token
			if GitHubToken != "" && len(via) > 0 {
				req.Header.Set("Authorization", "token "+GitHubToken)
			}
			return nil
		},
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	if GitHubToken != "" {
		req.Header.Set("Authorization", "token "+GitHubToken)
	}
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read body for error details
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("download failed with status %s: %s", resp.Status, string(body))
	}

	// Determine filename from URL
	filename := filepath.Base(url)
	destPath := filepath.Join(destDir, filename)

	// Create destination file
	out, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer out.Close()

	// Copy with progress logging
	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return "", err
	}
	log.Printf("Downloaded %d bytes", written)

	return destPath, nil
}

// extractZip extracts a zip archive
func extractZip(src string, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)

		// Security check: prevent zip slip
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path in zip: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		// Extract file
		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}
	}

	return nil
}

// extractTarGz extracts a tar.gz archive
func extractTarGz(src string, dest string) error {
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		fpath := filepath.Join(dest, header.Name)

		// Security check: prevent path traversal
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path in tar: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(fpath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
				return err
			}

			outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}

			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, fpath); err != nil {
				return err
			}
		}
	}

	return nil
}

// applyUpdate replaces the current application with the extracted update
func applyUpdate(extractDir string) error {
	// Get path to current executable
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %v", err)
	}

	log.Printf("Current executable: %s", execPath)

	switch runtime.GOOS {
	case "darwin":
		return applyUpdateMacOS(extractDir, execPath)
	case "linux":
		return applyUpdateLinux(extractDir, execPath)
	case "windows":
		return applyUpdateWindows(extractDir, execPath)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// applyUpdateMacOS replaces the .app bundle on macOS
func applyUpdateMacOS(extractDir string, execPath string) error {
	// Find the .app bundle containing our executable
	// execPath is like: /Applications/Device Simulator.app/Contents/MacOS/device-simulator
	appBundlePath := execPath
	for i := 0; i < 4; i++ {
		appBundlePath = filepath.Dir(appBundlePath)
		if strings.HasSuffix(appBundlePath, ".app") {
			break
		}
	}

	if !strings.HasSuffix(appBundlePath, ".app") {
		// Not running from .app bundle, just replace the binary
		return replaceExecutable(extractDir, execPath)
	}

	log.Printf("App bundle path: %s", appBundlePath)

	// Find the new .app in extracted files
	var newAppPath string
	err := filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && strings.HasSuffix(info.Name(), ".app") {
			newAppPath = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return err
	}

	if newAppPath == "" {
		return fmt.Errorf("no .app bundle found in update")
	}

	log.Printf("New app bundle: %s", newAppPath)

	// Create backup of current app
	backupPath := appBundlePath + ".backup"
	os.RemoveAll(backupPath) // Remove any existing backup

	// Rename current app to backup
	if err := os.Rename(appBundlePath, backupPath); err != nil {
		return fmt.Errorf("failed to backup current app: %v", err)
	}

	// Move new app into place
	if err := os.Rename(newAppPath, appBundlePath); err != nil {
		// Try to restore backup
		os.Rename(backupPath, appBundlePath)
		return fmt.Errorf("failed to install new app: %v", err)
	}

	// Remove backup on success
	os.RemoveAll(backupPath)

	log.Printf("macOS app bundle updated successfully")
	return nil
}

// applyUpdateLinux replaces the executable on Linux
func applyUpdateLinux(extractDir string, execPath string) error {
	// Find the new executable in extracted files (in linux/ subdirectory)
	var newExecPath string
	err := filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == "device-simulator" && info.Mode().IsRegular() {
			newExecPath = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return err
	}

	if newExecPath == "" {
		return fmt.Errorf("no device-simulator executable found in update")
	}

	return replaceExecutable(newExecPath, execPath)
}

// applyUpdateWindows replaces the executable on Windows
func applyUpdateWindows(extractDir string, execPath string) error {
	// Find the new executable in extracted files
	var newExecPath string
	err := filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.EqualFold(info.Name(), "device-simulator.exe") {
			newExecPath = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return err
	}

	if newExecPath == "" {
		return fmt.Errorf("no device-simulator.exe found in update")
	}

	// On Windows, we can't replace a running executable directly
	// Rename current to .old, copy new, schedule .old for deletion on restart
	oldPath := execPath + ".old"
	os.Remove(oldPath) // Remove any existing .old file

	if err := os.Rename(execPath, oldPath); err != nil {
		return fmt.Errorf("failed to rename current executable: %v", err)
	}

	// Copy new executable
	if err := copyFile(newExecPath, execPath); err != nil {
		// Try to restore
		os.Rename(oldPath, execPath)
		return fmt.Errorf("failed to copy new executable: %v", err)
	}

	log.Printf("Windows executable updated successfully")
	return nil
}

// replaceExecutable replaces a single executable file
func replaceExecutable(newPath string, currentPath string) error {
	// Create backup
	backupPath := currentPath + ".backup"
	os.Remove(backupPath)

	if err := os.Rename(currentPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup current executable: %v", err)
	}

	// Copy new executable
	if err := copyFile(newPath, currentPath); err != nil {
		// Try to restore backup
		os.Rename(backupPath, currentPath)
		return fmt.Errorf("failed to copy new executable: %v", err)
	}

	// Make executable
	if err := os.Chmod(currentPath, 0755); err != nil {
		log.Printf("Warning: failed to set executable permission: %v", err)
	}

	// Remove backup
	os.Remove(backupPath)

	log.Printf("Executable updated successfully")
	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	// Preserve file mode
	sourceInfo, err := os.Stat(src)
	if err == nil {
		os.Chmod(dst, sourceInfo.Mode())
	}

	return nil
}

// GetVersionInfo returns version and platform information
func GetVersionInfo() map[string]string {
	return map[string]string{
		"version":  Version,
		"platform": runtime.GOOS,
		"arch":     runtime.GOARCH,
	}
}
