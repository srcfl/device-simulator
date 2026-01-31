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

// CheckForUpdate checks GitHub releases for a newer version
func CheckForUpdate() (*UpdateInfo, error) {
	info := &UpdateInfo{
		Available:      false,
		CurrentVersion: Version,
	}

	// Parse current version
	current, err := semver.Parse(Version)
	if err != nil {
		// Dev version, skip update check
		log.Printf("Skipping update check for dev version: %s", Version)
		return info, nil
	}

	// Check for latest release
	latest, found, err := selfupdate.DetectLatest(repoOwner + "/" + repoName)
	if err != nil {
		return nil, fmt.Errorf("failed to check for updates: %v", err)
	}

	if !found {
		return info, nil
	}

	info.LatestVersion = latest.Version.String()
	info.ReleaseURL = latest.URL
	info.ReleaseNotes = latest.ReleaseNotes

	// Find the asset URL for the current platform
	info.AssetURL = latest.AssetURL

	// Compare versions
	if latest.Version.GT(current) {
		info.Available = true
		log.Printf("Update available: %s -> %s", Version, latest.Version)
	}

	return info, nil
}

// DoUpdate downloads and applies the update
func DoUpdate() error {
	current, err := semver.Parse(Version)
	if err != nil {
		return fmt.Errorf("cannot update dev version: %s", Version)
	}

	latest, found, err := selfupdate.DetectLatest(repoOwner + "/" + repoName)
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

	exe, err := selfupdate.UpdateSelf(latest.Version, repoOwner+"/"+repoName)
	if err != nil {
		return fmt.Errorf("update failed: %v", err)
	}

	log.Printf("Successfully updated to version %s", latest.Version)
	log.Printf("New executable: %s", exe)
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
