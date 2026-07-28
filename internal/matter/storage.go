package matter

import (
	"os"
	"path/filepath"
	"strings"
)

// StorageRoot returns the base directory under which each Matter device keeps
// its matter.js storage (fabric tables, credentials, passcode). Persisting this
// means commissioning survives app restarts and bridge crashes.
//
// Resolution order mirrors internal/state so a single DATA_DIR controls both:
//  1. MATTER_STORAGE_ROOT (explicit override)
//  2. DEVICE_SIMULATOR_DATA_DIR/matter
//  3. <user config dir>/Device Simulator/matter
//  4. <temp>/device-simulator-matter (last resort)
func StorageRoot() string {
	if explicit := os.Getenv("MATTER_STORAGE_ROOT"); explicit != "" {
		return explicit
	}
	if dir := os.Getenv("DEVICE_SIMULATOR_DATA_DIR"); dir != "" {
		return filepath.Join(dir, "matter")
	}
	if configDir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(configDir, "Device Simulator", "matter")
	}
	return filepath.Join(os.TempDir(), "device-simulator-matter")
}

// deviceStorageDir returns (creating if needed) the per-device storage subdir.
func deviceStorageDir(serial string) (string, error) {
	dir := filepath.Join(StorageRoot(), sanitizeSegment(serial))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// removeDeviceStorage deletes a device's storage subdir (used on explicit
// decommission/delete so a re-created device gets a fresh pairing).
func removeDeviceStorage(serial string) error {
	dir := filepath.Join(StorageRoot(), sanitizeSegment(serial))
	return os.RemoveAll(dir)
}

// sanitizeSegment makes a string safe to use as a single path segment.
func sanitizeSegment(s string) string {
	if s == "" {
		return "_"
	}
	replacer := func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}
	return strings.Map(replacer, s)
}
