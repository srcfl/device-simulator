package profiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// GeneratorConfig defines a value generator
type GeneratorConfig struct {
	Type   string                 `json:"type"`
	Config map[string]interface{} `json:"config,omitempty"`
}

// Profile defines a device simulation profile
type Profile struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	DeviceType  string                     `json:"device_type"`
	Categories  []string                   `json:"categories,omitempty"`  // pv, battery, meter, grid, control
	EVCCRef     string                     `json:"evcc_ref,omitempty"`    // Link to EVCC template
	Defaults    map[uint16]float64         `json:"defaults,omitempty"`
	Generators  map[string]GeneratorConfig `json:"generators,omitempty"`
}

// Manager handles profile loading and storage
type Manager struct {
	mu          sync.RWMutex
	profilesDir string
	profiles    map[string]*Profile
	active      string
}

// NewManager creates a new profile manager
func NewManager(profilesDir string) *Manager {
	return &Manager{
		profilesDir: profilesDir,
		profiles:    make(map[string]*Profile),
	}
}

// LoadAll loads all profiles from the profiles directory
func (m *Manager) LoadAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	files, err := filepath.Glob(filepath.Join(m.profilesDir, "*.json"))
	if err != nil {
		return fmt.Errorf("failed to glob profiles: %w", err)
	}

	for _, file := range files {
		profile, err := m.loadFile(file)
		if err != nil {
			return fmt.Errorf("failed to load %s: %w", file, err)
		}
		m.profiles[profile.Name] = profile
	}

	return nil
}

func (m *Manager) loadFile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var profile Profile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, err
	}

	return &profile, nil
}

// Get returns a profile by name
func (m *Manager) Get(name string) *Profile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profiles[name]
}

// List returns all profile names sorted alphabetically
func (m *Manager) List() []ProfileInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []ProfileInfo
	for _, p := range m.profiles {
		list = append(list, ProfileInfo{
			Name:        p.Name,
			Description: p.Description,
			DeviceType:  p.DeviceType,
		})
	}

	// Sort by name for consistent ordering
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})

	return list
}

// ProfileInfo is a summary of a profile
type ProfileInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	DeviceType  string `json:"device_type"`
}

// Save saves a profile to disk
func (m *Manager) Save(profile *Profile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(m.profilesDir, profile.Name+".json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}

	m.profiles[profile.Name] = profile
	return nil
}

// SetActive sets the active profile
func (m *Manager) SetActive(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.profiles[name]; !ok {
		return fmt.Errorf("profile %s not found", name)
	}
	m.active = name
	return nil
}

// GetActive returns the active profile
func (m *Manager) GetActive() *Profile {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.active == "" {
		return nil
	}
	return m.profiles[m.active]
}

// GetActiveName returns the name of the active profile
func (m *Manager) GetActiveName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// EmbeddedProfiles contains built-in profiles for common device types
var EmbeddedProfiles = map[string]*Profile{
	"sungrow-hybrid": {
		Name:        "sungrow-hybrid",
		Description: "Sungrow SH Series Hybrid Inverter",
		DeviceType:  "sungrow",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/sungrow-hybrid.yaml",
		Defaults: map[uint16]float64{
			5016:  0,     // PV Power
			13021: 0,     // Battery Power
			13022: 50,    // Battery SoC
			5600:  0,     // Meter Power
			13052: 500,   // Load Power
			13049: 0,     // Grid Mode (Self-consumption)
			13050: 204,   // Stop command
			13073: 10000, // PV Command
			33046: 500,   // Max Charge (5kW)
			33047: 500,   // Max Discharge (5kW)
		},
		Generators: map[string]GeneratorConfig{
			"solar_radiance": {
				Type: "daynight",
				Config: map[string]interface{}{
					"peak_power":   5000.0,
					"sunrise_hour": 6.0,
					"sunset_hour":  20.0,
				},
			},
			"load": {
				Type: "schedule",
				Config: map[string]interface{}{
					"base_load": 500.0,
					"schedule": []map[string]interface{}{
						{"hour_start": 7, "hour_end": 9, "load": 2000},
						{"hour_start": 17, "hour_end": 22, "load": 3000},
					},
				},
			},
		},
	},
	"solis-hybrid": {
		Name:        "solis-hybrid",
		Description: "Solis Hybrid Inverter",
		DeviceType:  "solis",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		Defaults: map[uint16]float64{
			5016:  0,
			13021: 0,
			13022: 60,
			5600:  0,
			13052: 400,
			33046: 600,
			33047: 600,
		},
	},
	"solaredge": {
		Name:        "solaredge",
		Description: "SolarEdge Inverter with StorEdge",
		DeviceType:  "solaredge",
		Categories:  []string{"pv", "battery", "meter", "grid"},
		EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/solaredge.yaml",
		Defaults: map[uint16]float64{
			5016:  0,
			13022: 55,
			13052: 600,
		},
	},
	"sma-tripower": {
		Name:        "sma-tripower",
		Description: "SMA Sunny Tripower Inverter",
		DeviceType:  "sma",
		Categories:  []string{"pv", "meter", "grid"},
		EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/sma-tripower.yaml",
		Defaults: map[uint16]float64{
			5016:  0,
			13022: 70,
			13052: 800,
		},
	},
	"huawei-sun2000": {
		Name:        "huawei-sun2000",
		Description: "Huawei SUN2000 Hybrid Inverter",
		DeviceType:  "huawei",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/huawei-sun2000.yaml",
		Defaults: map[uint16]float64{
			5016:  0,
			13021: 0,
			13022: 65,
			5600:  0,
			13052: 700,
		},
	},
	"fronius-gen24": {
		Name:        "fronius-gen24",
		Description: "Fronius GEN24 Plus Hybrid",
		DeviceType:  "fronius",
		Categories:  []string{"pv", "battery", "meter", "grid"},
		EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/fronius.yaml",
		Defaults: map[uint16]float64{
			5016:  0,
			13022: 45,
			13052: 550,
		},
	},
	"deye-hybrid": {
		Name:        "deye-hybrid",
		Description: "Deye Hybrid Inverter",
		DeviceType:  "deye",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		Defaults: map[uint16]float64{
			5016:  0,
			13021: 0,
			13022: 80,
			5600:  0,
			13052: 450,
		},
	},
	"pixii-powershaper": {
		Name:        "pixii-powershaper",
		Description: "Pixii PowerShaper 2 (Battery Only)",
		DeviceType:  "pixii",
		Categories:  []string{"battery", "meter", "control"},
		Defaults: map[uint16]float64{
			13021: 0,
			13022: 50,
			5600:  0,
		},
	},
	"ferroamp-energyhub": {
		Name:        "ferroamp-energyhub",
		Description: "FerroAmp EnergyHub",
		DeviceType:  "ferroamp",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		Defaults: map[uint16]float64{
			5016:  0,
			13022: 55,
			5600:  0,
			13052: 1000,
		},
	},
	"sdm630": {
		Name:        "sdm630",
		Description: "Eastron SDM630 Energy Meter (Meter Only)",
		DeviceType:  "sdm630",
		Categories:  []string{"meter"},
		EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/sdm.yaml",
		Defaults: map[uint16]float64{
			5600:  0,
			13052: 500,
		},
		Generators: map[string]GeneratorConfig{
			"load": {
				Type: "sine",
				Config: map[string]interface{}{
					"amplitude": 1000.0,
					"offset":    1500.0,
					"period":    24.0,
				},
			},
		},
	},
	"residential-5kw": {
		Name:        "residential-5kw",
		Description: "Typical 5kW Residential System",
		DeviceType:  "generic",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		Defaults: map[uint16]float64{
			5016:  0,
			13021: 0,
			13022: 50,
			5600:  0,
			13052: 500,
			33046: 500,
			33047: 500,
		},
		Generators: map[string]GeneratorConfig{
			"solar_radiance": {
				Type: "daynight",
				Config: map[string]interface{}{
					"peak_power":   5000.0,
					"sunrise_hour": 6.0,
					"sunset_hour":  20.0,
					"noise":        5.0,
				},
			},
			"load": {
				Type: "schedule",
				Config: map[string]interface{}{
					"base_load": 400.0,
					"schedule": []map[string]interface{}{
						{"hour_start": 6, "hour_end": 8, "load": 1500},
						{"hour_start": 17, "hour_end": 22, "load": 2500},
					},
				},
			},
		},
	},
	"commercial-50kw": {
		Name:        "commercial-50kw",
		Description: "50kW Commercial System",
		DeviceType:  "generic",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		Defaults: map[uint16]float64{
			5016:  0,
			13021: 0,
			13022: 60,
			5600:  0,
			13052: 5000,
			33046: 5000,
			33047: 5000,
		},
		Generators: map[string]GeneratorConfig{
			"solar_radiance": {
				Type: "daynight",
				Config: map[string]interface{}{
					"peak_power":   50000.0,
					"sunrise_hour": 6.0,
					"sunset_hour":  20.0,
				},
			},
			"load": {
				Type: "schedule",
				Config: map[string]interface{}{
					"base_load": 2000.0,
					"schedule": []map[string]interface{}{
						{"hour_start": 8, "hour_end": 18, "load": 15000},
					},
				},
			},
		},
	},
	"default": {
		Name:        "default",
		Description: "Default profile (all registers)",
		DeviceType:  "generic",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		Defaults:    map[uint16]float64{},
	},
}

// InitEmbedded adds embedded profiles to the manager
func (m *Manager) InitEmbedded() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, profile := range EmbeddedProfiles {
		m.profiles[name] = profile
	}
}
