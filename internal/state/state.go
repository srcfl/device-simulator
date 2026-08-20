package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const CurrentVersion = 1

type AutomationState struct {
	Enabled        bool    `json:"enabled"`
	TimeMultiplier float64 `json:"time_multiplier"`
	Scenario       string  `json:"scenario"`
	SimulatedTime  string  `json:"simulated_time"`
}

type SimulatorState struct {
	ID                 int             `json:"id"`
	Serial             string          `json:"serial"`
	Protocol           string          `json:"protocol"`
	Category           string          `json:"category"`
	SlaveID            uint8           `json:"slave_id"`
	Port               int             `json:"port"`
	Running            bool            `json:"running"`
	SiteID             int             `json:"site_id"`
	DeviceOn           bool            `json:"device_on"`
	BatteryCapacityKWh float64         `json:"battery_capacity_kwh"`
	Profile            string          `json:"profile"`
	OCPPURL            string          `json:"ocpp_url"`
	Automation         AutomationState `json:"automation"`
}

type PhaseFactors struct {
	L1 float64 `json:"l1"`
	L2 float64 `json:"l2"`
	L3 float64 `json:"l3"`
}

type PhaseLoads struct {
	L1 float64 `json:"l1"`
	L2 float64 `json:"l2"`
	L3 float64 `json:"l3"`
}

type PhaseFuses struct {
	L1 bool `json:"l1"`
	L2 bool `json:"l2"`
	L3 bool `json:"l3"`
}

type SiteState struct {
	ID             int          `json:"id"`
	Name           string       `json:"name"`
	MeterID        int          `json:"meter_id"`
	MeterIDs       []int        `json:"meter_ids"`
	SimulatorIDs   []int        `json:"simulator_ids"`
	BaseLoadW      float64      `json:"base_load_w"`
	Enabled        bool         `json:"enabled"`
	FuseSize       int          `json:"fuse_size"`
	FuseProfile    string       `json:"fuse_profile"`
	PhaseFactors   PhaseFactors `json:"phase_factors"`
	FuseBlown      PhaseFuses   `json:"fuse_blown"`
	BoundMeter     bool         `json:"bound_meter"`
	SimulatedTime  string       `json:"simulated_time"`
	TimeMultiplier float64      `json:"time_multiplier"`
	SimRunning     bool         `json:"sim_running"`
	Scenario       string       `json:"scenario"`
	PhaseLoads     PhaseLoads   `json:"phase_loads"`
	LoadScenario   string       `json:"load_scenario"`
	LoadLinked     bool         `json:"load_linked"`
	P1Mode         string       `json:"p1_mode"`
	FacilityID     string       `json:"facility_id,omitempty"`
}

type AppState struct {
	Version         int              `json:"version"`
	NextSimulatorID int              `json:"next_simulator_id"`
	NextSiteID      int              `json:"next_site_id"`
	Simulators      []SimulatorState `json:"simulators"`
	Sites           []SiteState      `json:"sites"`
}

func DefaultState() *AppState {
	return &AppState{
		Version:    CurrentVersion,
		Simulators: []SimulatorState{},
		Sites:      []SiteState{},
	}
}

func GetPath() string {
	if explicit := os.Getenv("DEVICE_SIMULATOR_STATE_PATH"); explicit != "" {
		return explicit
	}
	if dir := os.Getenv("DEVICE_SIMULATOR_DATA_DIR"); dir != "" {
		return filepath.Join(dir, "state.json")
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "Device Simulator", "state.json")
}

func Load() (*AppState, error) {
	return LoadFrom(GetPath())
}

func LoadFrom(path string) (*AppState, error) {
	if path == "" {
		return DefaultState(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultState(), nil
		}
		return DefaultState(), err
	}

	state := DefaultState()
	if err := json.Unmarshal(data, state); err != nil {
		return DefaultState(), err
	}
	return state, nil
}

func Save(state *AppState) error {
	return SaveTo(state, GetPath())
}

func SaveTo(state *AppState, path string) error {
	if path == "" {
		return fmt.Errorf("could not determine state path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
