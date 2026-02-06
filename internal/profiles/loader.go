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

// MQTTConfig holds MQTT-specific profile configuration
type MQTTConfig struct {
	BaseTopic         string                 `json:"base_topic"`
	PublishIntervalMs int                    `json:"publish_interval_ms,omitempty"`
	Defaults          map[string]interface{} `json:"defaults,omitempty"`
}

// OCPPConfig holds OCPP-specific profile configuration
type OCPPConfig struct {
	Version       string            `json:"version"` // "1.6" or "2.0.1"
	Vendor        string            `json:"vendor"`
	Model         string            `json:"model"`
	NumConnectors int               `json:"num_connectors"`
	MaxPowerKW    float64           `json:"max_power_kw"`
	Configuration map[string]string `json:"configuration,omitempty"`
	DefaultStatus string            `json:"default_status,omitempty"`
}

// Profile defines a device simulation profile
type Profile struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Protocol    string   `json:"protocol"` // modbus, mqtt, ocpp
	DeviceType  string   `json:"device_type"`
	Categories  []string `json:"categories,omitempty"` // pv, battery, meter, grid, control, ev, v2x, charger
	EVCCRef     string   `json:"evcc_ref,omitempty"`   // Link to EVCC template

	// Modbus-specific
	Defaults map[uint16]float64 `json:"defaults,omitempty"`

	// MQTT-specific (V2X chargers)
	MQTTConfig *MQTTConfig `json:"mqtt_config,omitempty"`

	// OCPP-specific
	OCPPConfig *OCPPConfig `json:"ocpp_config,omitempty"`

	// Generators work across all protocols
	Generators map[string]GeneratorConfig `json:"generators,omitempty"`
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
			Protocol:    p.Protocol,
			DeviceType:  p.DeviceType,
		})
	}

	// Sort by name for consistent ordering
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})

	return list
}

// ListByProtocol returns profiles filtered by protocol
func (m *Manager) ListByProtocol(protocol string) []ProfileInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []ProfileInfo
	for _, p := range m.profiles {
		// Match exact protocol, or include profiles with empty protocol for modbus (backwards compatibility)
		matches := p.Protocol == protocol || (protocol == "modbus" && p.Protocol == "")
		if matches {
			list = append(list, ProfileInfo{
				Name:        p.Name,
				Description: p.Description,
				Protocol:    p.Protocol,
				DeviceType:  p.DeviceType,
			})
		}
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})

	return list
}

// ListByProtocolAndCategory returns profiles filtered by protocol and category
func (m *Manager) ListByProtocolAndCategory(protocol string, category string) []ProfileInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []ProfileInfo
	for _, p := range m.profiles {
		// Match protocol
		protocolMatches := p.Protocol == protocol || (protocol == "modbus" && p.Protocol == "")
		if !protocolMatches {
			continue
		}

		// Match category - profile must have ONLY this category (meter-only profiles)
		categoryMatches := false
		if len(p.Categories) == 1 && p.Categories[0] == category {
			categoryMatches = true
		}
		if !categoryMatches {
			continue
		}

		list = append(list, ProfileInfo{
			Name:        p.Name,
			Description: p.Description,
			Protocol:    p.Protocol,
			DeviceType:  p.DeviceType,
		})
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})

	return list
}

// ProfileInfo is a summary of a profile
type ProfileInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Protocol    string `json:"protocol"`
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
		Protocol:    "modbus",
		DeviceType:  "sungrow",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/sungrow-hybrid.yaml",
		Defaults: map[uint16]float64{
			5017:  0,     // PV Power
			13022: 0,     // Battery Power
			13023: 50,    // Battery SoC
			5599:  0,     // Meter Power
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
		Protocol:    "modbus",
		DeviceType:  "solis",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		Defaults: map[uint16]float64{
			5017:  0,
			13022: 0,
			13023: 60,
			5599:  0,
			13052: 400,
			33046: 600,
			33047: 600,
		},
	},
	"solaredge": {
		Name:        "solaredge",
		Description: "SolarEdge Inverter with StorEdge",
		Protocol:    "modbus",
		DeviceType:  "solaredge",
		Categories:  []string{"pv", "battery", "meter", "grid"},
		EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/solaredge.yaml",
		Defaults: map[uint16]float64{
			5017:  0,
			13023: 55,
			13052: 600,
		},
	},
	"sma-tripower": {
		Name:        "sma-tripower",
		Description: "SMA Sunny Tripower Inverter",
		Protocol:    "modbus",
		DeviceType:  "sma",
		Categories:  []string{"pv", "meter", "grid"},
		EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/sma-tripower.yaml",
		Defaults: map[uint16]float64{
			5017:  0,
			13023: 70,
			13052: 800,
		},
	},
	"huawei-sun2000": {
		Name:        "huawei-sun2000",
		Description: "Huawei SUN2000 Hybrid Inverter",
		Protocol:    "modbus",
		DeviceType:  "huawei",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/huawei-sun2000.yaml",
		Defaults: map[uint16]float64{
			5017:  0,
			13022: 0,
			13023: 65,
			5599:  0,
			13052: 700,
		},
	},
	"fronius-gen24": {
		Name:        "fronius-gen24",
		Description: "Fronius GEN24 Plus Hybrid",
		Protocol:    "modbus",
		DeviceType:  "fronius",
		Categories:  []string{"pv", "battery", "meter", "grid"},
		EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/fronius.yaml",
		Defaults: map[uint16]float64{
			5017:  0,
			13023: 45,
			13052: 550,
		},
	},
	"deye-hybrid": {
		Name:        "deye-hybrid",
		Description: "Deye Hybrid Inverter",
		Protocol:    "modbus",
		DeviceType:  "deye",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		Defaults: map[uint16]float64{
			5017:  0,
			13022: 0,
			13023: 80,
			5599:  0,
			13052: 450,
		},
	},
	"pixii-powershaper": {
		Name:        "pixii-powershaper",
		Description: "Pixii PowerShaper 2 (Battery Only)",
		Protocol:    "modbus",
		DeviceType:  "pixii",
		Categories:  []string{"battery", "meter", "control"},
		Defaults: map[uint16]float64{
			13022: 0,
			13023: 50,
			5599:  0,
		},
	},
	"ferroamp-energyhub": {
		Name:        "ferroamp-energyhub",
		Description: "FerroAmp EnergyHub",
		Protocol:    "modbus",
		DeviceType:  "ferroamp",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		Defaults: map[uint16]float64{
			5017:  0,
			13023: 55,
			5599:  0,
			13052: 1000,
		},
	},
	"sdm630": {
		Name:        "sdm630",
		Description: "Eastron SDM630 Energy Meter (Meter Only)",
		Protocol:    "modbus",
		DeviceType:  "sdm630",
		Categories:  []string{"meter"},
		EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/sdm.yaml",
		Defaults: map[uint16]float64{
			// SDM630 registers (F32, Big Endian, Input Registers FC 0x04)
			0:  230.0, // L1 Voltage (V)
			2:  230.0, // L2 Voltage (V)
			4:  230.0, // L3 Voltage (V)
			6:  0.0,   // L1 Current (A)
			8:  0.0,   // L2 Current (A)
			10: 0.0,   // L3 Current (A)
			12: 0.0,   // L1 Power (W)
			14: 0.0,   // L2 Power (W)
			16: 0.0,   // L3 Power (W)
			52: 0.0,   // Total System Power (W)
			70: 50.0,  // Frequency (Hz)
			72: 100.0, // Import Energy (kWh)
			74: 50.0,  // Export Energy (kWh)
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
	// "fronius-meter" - disabled, use sdm630 instead
	// {
	// 	Name:        "fronius-meter",
	// 	Description: "Fronius Smart Meter (SunSpec)",
	// 	Protocol:    "modbus",
	// 	DeviceType:  "fronius",
	// 	Categories:  []string{"meter"},
	// 	Defaults: map[uint16]float64{
	// 		40074: 0,   // L1 Current (A)
	// 		40076: 0,   // L2 Current (A)
	// 		40078: 0,   // L3 Current (A)
	// 		40086: 230, // L1 Voltage (V)
	// 		40088: 230, // L2 Voltage (V)
	// 		40090: 230, // L3 Voltage (V)
	// 		40092: 0,   // AC Power (W)
	// 		40094: 50,  // Frequency (Hz)
	// 	},
	// },
	"residential-5kw": {
		Name:        "residential-5kw",
		Description: "Typical 5kW Residential System",
		Protocol:    "modbus",
		DeviceType:  "generic",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		Defaults: map[uint16]float64{
			5017:  0,
			13022: 0,
			13023: 50,
			5599:  0,
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
		Protocol:    "modbus",
		DeviceType:  "generic",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		Defaults: map[uint16]float64{
			5017:  0,
			13022: 0,
			13023: 60,
			5599:  0,
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
		Protocol:    "modbus",
		DeviceType:  "generic",
		Categories:  []string{"pv", "battery", "meter", "grid", "control"},
		Defaults:    map[uint16]float64{},
	},

	// V2X Charger profiles (MQTT)
	"ambibox": {
		Name:        "ambibox",
		Description: "Ambibox V2X Bidirectional Charger",
		Protocol:    "mqtt",
		DeviceType:  "ambibox",
		Categories:  []string{"ev", "v2x", "bidirectional"},
		MQTTConfig: &MQTTConfig{
			BaseTopic:         "device/evCharger/0",
			PublishIntervalMs: 1000,
			Defaults: map[string]interface{}{
				"sessionState":      "IDLE",
				"controlMode":       "CONTROLLABLE",
				"evConnected":       false,
				"chargeProtocol":    "CCS",
				"powerAc":           float64(0),
				"powerDc":           float64(0),
				"currentAc1":        float64(0),
				"currentAc2":        float64(0),
				"currentAc3":        float64(0),
				"voltageAc1":        float64(230),
				"voltageAc2":        float64(230),
				"voltageAc3":        float64(230),
				"frequency":         float64(50),
				"soc":               float64(50),
				"chargePowerMax":    float64(11000),
				"dischargePowerMax": float64(11000),
				"chargePowerMin":    float64(1400),
				"dischargePowerMin": float64(1400),
				"energyAcImport":    float64(0),
				"energyAcExport":    float64(0),
			},
		},
	},

	// OCPP Charger profiles
	"ocpp-charger": {
		Name:        "ocpp-charger",
		Description: "Generic OCPP 1.6 EV Charger",
		Protocol:    "ocpp",
		DeviceType:  "ocpp-charger",
		Categories:  []string{"ev", "charger"},
		OCPPConfig: &OCPPConfig{
			Version:       "1.6",
			Vendor:        "SimulatorCo",
			Model:         "SimCharger-22",
			NumConnectors: 2,
			MaxPowerKW:    22,
			Configuration: map[string]string{
				"HeartbeatInterval":        "60",
				"MeterValueSampleInterval": "30",
				"NumberOfConnectors":       "2",
			},
			DefaultStatus: "Available",
		},
	},

	// Charge Amps DAWN
	"chargeamps-dawn": {
		Name:        "chargeamps-dawn",
		Description: "Charge Amps DAWN (7.7kW, 3-phase)",
		Protocol:    "ocpp",
		DeviceType:  "chargeamps",
		Categories:  []string{"ev", "charger"},
		OCPPConfig: &OCPPConfig{
			Version:       "1.6",
			Vendor:        "Charge Amps",
			Model:         "DAWN",
			NumConnectors: 1,
			MaxPowerKW:    7.7,
			Configuration: map[string]string{
				"HeartbeatInterval":                       "60",
				"MeterValueSampleInterval":                "30",
				"NumberOfConnectors":                      "1",
				"ConnectorSwitch3to1PhaseSupported":       "false",
				"ChargingScheduleAllowedChargingRateUnit": "Current",
				"SupportedFeatureProfiles":                "Core,SmartCharging",
			},
			DefaultStatus: "Available",
		},
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
