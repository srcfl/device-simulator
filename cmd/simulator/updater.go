package main

import (
	"fmt"
	"log"
	"runtime"

	"github.com/blang/semver"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
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

// CheckForUpdate checks GitHub releases for a newer version
func CheckForUpdate() (*UpdateInfo, error) {
	info := &UpdateInfo{
		Available:      false,
		CurrentVersion: Version,
	}

	// Parse current version (may fail for dev versions)
	current, parseErr := semver.Parse(Version)
	isDevVersion := parseErr != nil

	// Get configured updater
	updater, err := getUpdater()
	if err != nil {
		log.Printf("Failed to create updater: %v", err)
		info.LatestVersion = "error"
		return info, nil
	}

	// Check for latest release (do this even for dev versions to show latest)
	latest, found, err := updater.DetectLatest(repoOwner + "/" + repoName)
	if err != nil {
		log.Printf("Update check failed: %v", err)
		info.LatestVersion = "unavailable"
		info.ReleaseNotes = fmt.Sprintf("Could not check for updates: %v\n\nNote: Private repositories require authentication.", err)
		return info, nil // Return info with error message instead of failing
	}

	if !found {
		if GitHubToken == "" {
			info.LatestVersion = "unknown"
			info.ReleaseNotes = "Cannot check updates: No access token configured for private repository."
		} else {
			info.LatestVersion = "no releases found"
		}
		return info, nil
	}

	info.LatestVersion = latest.Version.String()
	info.ReleaseURL = latest.URL
	info.ReleaseNotes = latest.ReleaseNotes
	info.AssetURL = latest.AssetURL

	// Compare versions (only if we have a valid current version)
	if !isDevVersion && latest.Version.GT(current) {
		info.Available = true
		log.Printf("Update available: %s -> %s", Version, latest.Version)
	} else if isDevVersion {
		log.Printf("Dev version detected, latest release is %s", latest.Version)
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
