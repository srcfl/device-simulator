package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()

	if s.ModbusBasePort != 5000 {
		t.Errorf("Expected default ModbusBasePort 5000, got %d", s.ModbusBasePort)
	}
	if s.MQTTBasePort != 1883 {
		t.Errorf("Expected default MQTTBasePort 1883, got %d", s.MQTTBasePort)
	}
	if s.OCPPBasePort != 9000 {
		t.Errorf("Expected default OCPPBasePort 9000, got %d", s.OCPPBasePort)
	}
	if s.MQTTDisplayOffset != 0 {
		t.Errorf("Expected default MQTTDisplayOffset 0, got %d", s.MQTTDisplayOffset)
	}
	if s.SiteTickMs != 500 {
		t.Errorf("Expected default SiteTickMs 500, got %d", s.SiteTickMs)
	}
}

func TestLoadFromNonExistentFile(t *testing.T) {
	// Load from a path that doesn't exist
	s := LoadFrom("/nonexistent/path/settings.json")

	// Should return defaults
	if s.MQTTBasePort != 1883 {
		t.Errorf("Expected default MQTTBasePort 1883, got %d", s.MQTTBasePort)
	}
}

func TestLoadFromEmptyPath(t *testing.T) {
	s := LoadFrom("")

	// Should return defaults
	if s.MQTTBasePort != 1883 {
		t.Errorf("Expected default MQTTBasePort 1883, got %d", s.MQTTBasePort)
	}
}

func TestSaveAndLoad(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "settings-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	path := filepath.Join(tmpDir, "settings.json")

	// Save custom settings
	original := &AppSettings{
		ModbusBasePort:      5001,
		MQTTBasePort:        12345,
		OCPPBasePort:        9001,
		ModbusDisplayOffset: 0,
		MQTTDisplayOffset:   10000,
		OCPPDisplayOffset:   0,
		SiteTickMs:          250,
	}

	err = SaveTo(original, path)
	if err != nil {
		t.Fatalf("SaveTo failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("Settings file was not created")
	}

	// Load settings back
	loaded := LoadFrom(path)

	if loaded.ModbusBasePort != original.ModbusBasePort {
		t.Errorf("Expected ModbusBasePort %d, got %d", original.ModbusBasePort, loaded.ModbusBasePort)
	}
	if loaded.MQTTBasePort != original.MQTTBasePort {
		t.Errorf("Expected MQTTBasePort %d, got %d", original.MQTTBasePort, loaded.MQTTBasePort)
	}
	if loaded.OCPPBasePort != original.OCPPBasePort {
		t.Errorf("Expected OCPPBasePort %d, got %d", original.OCPPBasePort, loaded.OCPPBasePort)
	}
	if loaded.MQTTDisplayOffset != original.MQTTDisplayOffset {
		t.Errorf("Expected MQTTDisplayOffset %d, got %d", original.MQTTDisplayOffset, loaded.MQTTDisplayOffset)
	}
	if loaded.SiteTickMs != original.SiteTickMs {
		t.Errorf("Expected SiteTickMs %d, got %d", original.SiteTickMs, loaded.SiteTickMs)
	}
}

func TestSaveToEmptyPath(t *testing.T) {
	s := &AppSettings{MQTTBasePort: 1883}

	err := SaveTo(s, "")
	if err == nil {
		t.Error("Expected error when saving to empty path")
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "settings-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use a nested path that doesn't exist
	path := filepath.Join(tmpDir, "nested", "dir", "settings.json")

	s := &AppSettings{MQTTBasePort: 9999}

	err = SaveTo(s, path)
	if err != nil {
		t.Fatalf("SaveTo failed: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("Settings file was not created in nested directory")
	}
}

func TestSettingsJSONFormat(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "settings-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	path := filepath.Join(tmpDir, "settings.json")

	s := &AppSettings{MQTTBasePort: 1883}
	err = SaveTo(s, path)
	if err != nil {
		t.Fatalf("SaveTo failed: %v", err)
	}

	// Read raw file content
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	// Verify it's valid JSON
	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	if err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}

	// Verify key name
	if _, ok := parsed["mqttBasePort"]; !ok {
		t.Error("Expected 'mqttBasePort' key in JSON")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "settings-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	path := filepath.Join(tmpDir, "settings.json")

	// Write invalid JSON
	err = os.WriteFile(path, []byte("not valid json"), 0644)
	if err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	// Should return defaults when JSON is invalid
	s := LoadFrom(path)
	if s.MQTTBasePort != 1883 {
		t.Errorf("Expected default MQTTBasePort 1883 for invalid JSON, got %d", s.MQTTBasePort)
	}
}

func TestLoadPartialJSON(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "settings-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	path := filepath.Join(tmpDir, "settings.json")

	// Write JSON with extra/missing fields
	err = os.WriteFile(path, []byte(`{"mqttBasePort": 5555, "unknownField": "ignored"}`), 0644)
	if err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	s := LoadFrom(path)
	if s.MQTTBasePort != 5555 {
		t.Errorf("Expected MQTTBasePort 5555, got %d", s.MQTTBasePort)
	}
}

func TestGetPath(t *testing.T) {
	path := GetPath()

	// Should return a non-empty path on most systems
	if path == "" {
		t.Skip("Could not determine config directory on this system")
	}

	// Should contain expected components
	if filepath.Base(path) != "settings.json" {
		t.Errorf("Expected path to end with settings.json, got %s", path)
	}

	// Should contain "Device Simulator" directory
	dir := filepath.Dir(path)
	if filepath.Base(dir) != "Device Simulator" {
		t.Errorf("Expected parent dir 'Device Simulator', got %s", filepath.Base(dir))
	}
}
