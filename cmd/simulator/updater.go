package main

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"

	"github.com/blang/semver"
	"github.com/google/go-github/v50/github"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
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

// getUpdater returns a configured selfupdate.Updater with token if available
func getUpdater() (*selfupdate.Updater, error) {
	config := selfupdate.Config{}
	if GitHubToken != "" {
		config.APIToken = GitHubToken
	}
	return selfupdate.NewUpdater(config)
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
	osName := runtime.GOOS
	archName := runtime.GOARCH
	for _, asset := range release.Assets {
		if asset.Name != nil && asset.BrowserDownloadURL != nil {
			name := strings.ToLower(*asset.Name)
			if strings.Contains(name, osName) && strings.Contains(name, archName) {
				info.AssetURL = *asset.BrowserDownloadURL
				break
			}
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
	current, err := semver.Parse(Version)
	if err != nil {
		return fmt.Errorf("cannot update dev version: %s", Version)
	}

	updater, err := getUpdater()
	if err != nil {
		return fmt.Errorf("failed to create updater: %v", err)
	}

	latest, found, err := updater.DetectLatest(repoOwner + "/" + repoName)
	if err != nil {
		return fmt.Errorf("failed to detect latest version: %v", err)
	}

	if !found {
		return fmt.Errorf("no releases found")
	}

	if !latest.Version.GT(current) {
		return fmt.Errorf("already up to date (current: %s, latest: %s)", Version, latest.Version)
	}

	log.Printf("Updating to version %s...", latest.Version)

	rel, err := updater.UpdateSelf(latest.Version, repoOwner+"/"+repoName)
	if err != nil {
		return fmt.Errorf("update failed: %v", err)
	}

	log.Printf("Successfully updated to version %s", rel.Version)
	log.Printf("Please restart the application to use the new version")

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
