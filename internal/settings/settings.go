package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AppSettings holds persistent application settings
type AppSettings struct {
	ModbusBasePort      int `json:"modbusBasePort"`
	MQTTBasePort        int `json:"mqttBasePort"`
	OCPPBasePort        int `json:"ocppBasePort"`
	MatterBasePort      int `json:"matterBasePort"`
	ModbusDisplayOffset int `json:"modbusDisplayOffset"`
	MQTTDisplayOffset   int `json:"mqttDisplayOffset"`
	OCPPDisplayOffset   int `json:"ocppDisplayOffset"`
	SiteTickMs          int `json:"siteTickMs"`
}

// DefaultSettings returns settings with default values
func DefaultSettings() *AppSettings {
	return &AppSettings{
		ModbusBasePort:      5000,
		MQTTBasePort:        1883,
		OCPPBasePort:        9000,
		MatterBasePort:      5540,
		ModbusDisplayOffset: 0,
		MQTTDisplayOffset:   0,
		OCPPDisplayOffset:   0,
		SiteTickMs:          500,
	}
}

// GetPath returns the OS-appropriate path for settings.json
// Returns empty string if config directory cannot be determined
func GetPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "Device Simulator", "settings.json")
}

// Load loads settings from the config file
// Returns default settings if file doesn't exist or can't be read
func Load() *AppSettings {
	return LoadFrom(GetPath())
}

// LoadFrom loads settings from a specific path
// Returns default settings if file doesn't exist or can't be read
func LoadFrom(path string) *AppSettings {
	settings := DefaultSettings()

	if path == "" {
		return settings
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return settings
	}

	json.Unmarshal(data, settings)
	return settings
}

// Save saves settings to the config file
func Save(settings *AppSettings) error {
	return SaveTo(settings, GetPath())
}

// SaveTo saves settings to a specific path
func SaveTo(settings *AppSettings, path string) error {
	if path == "" {
		return fmt.Errorf("could not determine config directory")
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
