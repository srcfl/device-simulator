package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/srcfl/device-simulator/fuse"
	"github.com/srcfl/device-simulator/internal/modbus"
	"github.com/srcfl/device-simulator/internal/modbus/devices"
	"github.com/srcfl/device-simulator/internal/settings"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct holds the Wails application context
type App struct {
	ctx context.Context
}

// NewApp creates a new App struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// SimulatorInfo represents the data returned for a simulator
type SimulatorInfo struct {
	ID         int                    `json:"id"`
	Serial     string                 `json:"serial"`
	Protocol   string                 `json:"protocol"`
	Category   string                 `json:"category"`
	Port       int                    `json:"port"`
	Running    bool                   `json:"running"`
	Automation bool                   `json:"automation"`
	Profile    string                 `json:"profile"`
	Extra      map[string]interface{} `json:"extra,omitempty"`
}

// SiteInfo represents the data returned for a site
type SiteInfo struct {
	ID           int          `json:"id"`
	Name         string       `json:"name"`
	MeterID      int          `json:"meter_id"`
	SimulatorIDs []int        `json:"simulator_ids"`
	BaseLoadW    float64      `json:"base_load_w"`
	Enabled      bool         `json:"enabled"`
	FuseSize     int          `json:"fuse_size"`
	PhaseFactors PhaseFactors `json:"phase_factors"`
	BoundMeter   bool         `json:"bound_meter"`
}

// ========== SIMULATOR METHODS ==========

// GetSimulators returns all simulators with optional filters
func (a *App) GetSimulators(filters map[string]string) []map[string]interface{} {
	protocolFilter := filters["protocol"]
	categoryFilter := filters["category"]

	simulatorsMu.RLock()
	defer simulatorsMu.RUnlock()

	var list []map[string]interface{}
	for _, sim := range simulators {
		if protocolFilter != "" && sim.Protocol != protocolFilter {
			continue
		}
		if categoryFilter != "" && sim.Category != categoryFilter {
			continue
		}

		sim.mu.RLock()
		entry := map[string]interface{}{
			"id":            sim.ID,
			"serial":        sim.Serial,
			"protocol":      sim.Protocol,
			"category":      sim.Category,
			"port":          sim.Port,
			"running":       sim.Running,
			"automation":    sim.Automation.Enabled,
			"profile":       sim.ProfilesManager.GetActiveName(),
			"site_id":       sim.SiteID,
			"device_on":     sim.DeviceOn,
			"mdns_hostname": mdnsAdvertiser.ActiveHostname(sim.ID),
		}

		switch sim.Protocol {
		case "modbus":
			entry["slave_id"] = sim.SlaveID
			if sim.Category == "energy_meter" {
				entry["meter"] = getSemanticValue(sim, "meter_power")
			} else {
				entry["pv_power"] = getSemanticValue(sim, "pv_power")
				entry["soc"] = getSemanticValue(sim, "battery_soc")
				batteryPower, batteryDirection := getBatteryPowerAndDirection(sim)
				entry["load"] = batteryPower
				entry["meter"] = getSemanticValue(sim, "meter_power")
				entry["battery_direction"] = batteryDirection
				entry["battery_capacity_kwh"] = sim.BatteryCapacityKWh
				entry["scenario"] = sim.Automation.Scenario
			}
		case "mqtt":
			state := sim.MQTTServer.GetState()
			entry["session_state"] = state["sessionState"]
			entry["power_ac"] = state["powerAc"]
			entry["soc"] = state["soc"]
			entry["ev_connected"] = state["evConnected"]
		case "ocpp":
			state := sim.OCPPServer.GetState()
			entry["connected"] = state["connected"]
			entry["connectors"] = state["connectors"]
			entry["ev_connected"] = state["evConnected"]
			entry["total_power"] = state["totalPower"]
			entry["has_active_transaction"] = state["hasActiveTransaction"]
			entry["max_power_kw"] = state["maxPowerKW"]
		}
		sim.mu.RUnlock()
		list = append(list, entry)
	}

	if list == nil {
		list = []map[string]interface{}{}
	}
	return list
}

// CreateSimulator creates a new simulator of the given type
func (a *App) CreateSimulator(simType string) (map[string]interface{}, error) {
	sim, err := createSimulator(simType)
	if err != nil {
		return nil, err
	}

	result := map[string]interface{}{
		"id":            sim.ID,
		"serial":        sim.Serial,
		"protocol":      sim.Protocol,
		"category":      sim.Category,
		"port":          sim.Port,
		"running":       sim.Running,
		"mdns_hostname": mdnsAdvertiser.ActiveHostname(sim.ID),
	}

	// Emit event for UI update
	a.emitSimulatorUpdate()

	return result, nil
}

// DeleteSimulator deletes a simulator by ID
func (a *App) DeleteSimulator(id int) error {
	err := deleteSimulator(id)
	if err == nil {
		a.emitSimulatorUpdate()
	}
	return err
}

// GetSimulator returns detailed info for a single simulator
func (a *App) GetSimulator(id int) (map[string]interface{}, error) {
	sim := getSimulator(id)
	if sim == nil {
		return nil, fmt.Errorf("simulator %d not found", id)
	}

	sim.mu.RLock()
	defer sim.mu.RUnlock()

	result := map[string]interface{}{
		"id":                   sim.ID,
		"serial":               sim.Serial,
		"protocol":             sim.Protocol,
		"category":             sim.Category,
		"port":                 sim.Port,
		"running":              sim.Running,
		"slave_id":             sim.SlaveID,
		"profile":              sim.ProfilesManager.GetActiveName(),
		"battery_capacity_kwh": sim.BatteryCapacityKWh,
		"runtime_serial":       getRuntimeSerial(sim),
		"mdns_hostname":        mdnsAdvertiser.ActiveHostname(sim.ID),
		"automation": map[string]interface{}{
			"enabled":         sim.Automation.Enabled,
			"time_multiplier": sim.Automation.TimeMultiplier,
			"simulated_time":  sim.Automation.SimulatedTime.Format("2006-01-02 15:04:05"),
			"scenario":        sim.Automation.Scenario,
		},
	}

	// Protocol-specific state
	switch sim.Protocol {
	case "mqtt":
		result["state"] = sim.MQTTServer.GetState()
	case "ocpp":
		state := sim.OCPPServer.GetState()
		result["state"] = state
		result["ocpp_url"] = sim.OCPPServer.GetOCPPURL()
	}

	return result, nil
}

// UpdateConfig updates simulator configuration
func (a *App) UpdateConfig(id int, config map[string]interface{}) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}

	sim.mu.Lock()
	defer sim.mu.Unlock()

	if serial, ok := config["serial"].(string); ok && serial != "" {
		sim.Serial = serial
		switch sim.Protocol {
		case "modbus":
			if sim.Server != nil {
				sim.Server.SetSerialNumber(serial)
			}
		case "mqtt":
			if sim.MQTTServer != nil {
				sim.MQTTServer.SetSerialNumber(serial)
			}
		case "ocpp":
			if sim.OCPPServer != nil {
				sim.OCPPServer.SetSerialNumber(serial)
			}
		}
	}
	if slaveID, ok := config["slave_id"].(float64); ok {
		sim.SlaveID = uint8(slaveID)
	}
	if capacity, ok := config["battery_capacity_kwh"].(float64); ok && capacity > 0 {
		sim.BatteryCapacityKWh = capacity
	}
	if ocppURL, ok := config["ocpp_url"].(string); ok && sim.OCPPServer != nil {
		sim.OCPPServer.SetOCPPURL(ocppURL)
	}

	markStateDirty()
	a.emitSimulatorUpdate()
	return nil
}

// GetRegisters returns all registers for a Modbus simulator
func (a *App) GetRegisters(id int) ([]*modbus.RegisterPayload, error) {
	sim := getSimulator(id)
	if sim == nil {
		return nil, fmt.Errorf("simulator %d not found", id)
	}

	if sim.Protocol != "modbus" {
		return nil, fmt.Errorf("simulator is not a Modbus device")
	}

	sim.mu.RLock()
	defer sim.mu.RUnlock()

	// Get active profile for category filtering
	activeProfile := sim.ProfilesManager.GetActive()
	var activeCategories []string
	if activeProfile != nil {
		activeCategories = activeProfile.Categories
	}

	var payloads []*modbus.RegisterPayload
	for _, def := range modbus.RegisterDefinitions {
		// Filter by profile categories if set
		if len(activeCategories) > 0 && !containsCategory(activeCategories, def.Category) {
			continue
		}

		var regs []uint16
		if def.UseHolding {
			regs = sim.Server.GetHoldingRegisters(def.Address, def.Words)
		} else {
			regs = sim.Server.GetInputRegisters(def.Address, def.Words)
		}

		payload, err := def.ToPayload(regs)
		if err != nil {
			continue
		}
		payloads = append(payloads, payload)
	}

	return payloads, nil
}

// WriteRegister writes a value to a specific register
func (a *App) WriteRegister(id int, address uint16, value float64) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}

	if sim.Protocol != "modbus" {
		return fmt.Errorf("simulator is not a Modbus device")
	}

	// Look up in device-specific RegisterSet first, then fall back to global
	var def *modbus.RegisterDef
	if sim.RegisterSet != nil {
		def = sim.RegisterSet.Lookup[address]
	}
	if def == nil {
		def = modbus.RegisterLookup[address]
	}
	if def == nil {
		return fmt.Errorf("register %d not found", address)
	}

	sim.mu.Lock()
	defer sim.mu.Unlock()

	if err := sim.Server.WriteRegisterValue(def, value); err != nil {
		return err
	}

	// Keep battery status flags in sync with charge/discharge commands
	if def.SemanticName == "battery_cmd" {
		flags := 0.0
		switch int(value) {
		case 170:
			flags = 2 // charging
		case 187:
			flags = 4 // discharging
		default:
			flags = 0
		}
		setSemanticValue(sim, "battery_flags", flags)
	}

	return nil
}

// GetLogs returns operation logs for a simulator
func (a *App) GetLogs(id int, limit int) (map[string]interface{}, error) {
	sim := getSimulator(id)
	if sim == nil {
		return nil, fmt.Errorf("simulator %d not found", id)
	}

	if limit <= 0 {
		limit = 50
	}

	sim.mu.RLock()
	defer sim.mu.RUnlock()

	var logs interface{}

	switch sim.Protocol {
	case "modbus":
		logs = sim.Server.GetLogs(limit)
	case "mqtt":
		logs = sim.MQTTServer.GetLogs(limit)
	case "ocpp":
		logs = sim.OCPPServer.GetLogs(limit)
	}

	return map[string]interface{}{"logs": logs}, nil
}

// GetProfiles returns available profiles for a simulator
func (a *App) GetProfiles(id int) (map[string]interface{}, error) {
	sim := getSimulator(id)
	if sim == nil {
		return nil, fmt.Errorf("simulator %d not found", id)
	}

	sim.mu.RLock()
	defer sim.mu.RUnlock()

	profiles := sim.ProfilesManager.ListByProtocol(sim.Protocol)
	active := sim.ProfilesManager.GetActiveName()

	return map[string]interface{}{
		"profiles": profiles,
		"active":   active,
	}, nil
}

// ApplyProfile applies a profile to a simulator
func (a *App) ApplyProfile(id int, name string) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}

	sim.mu.Lock()
	defer sim.mu.Unlock()

	profile := sim.ProfilesManager.Get(name)
	if profile == nil {
		return fmt.Errorf("profile %s not found", name)
	}

	sim.ProfilesManager.SetActive(name)

	// Apply profile defaults based on protocol
	if sim.Protocol == "modbus" && sim.Server != nil {
		// Update RegisterSet if the profile specifies one
		if profile.RegisterSetName != "" {
			if rs := devices.GetRegisterSet(profile.RegisterSetName); rs != nil {
				sim.RegisterSet = rs
				sim.Server.RegisterSet = rs
				sim.Server.InitDefaultsFromSet(rs)
			}
		}
		for addr, value := range profile.Defaults {
			var def *modbus.RegisterDef
			if sim.RegisterSet != nil {
				def = sim.RegisterSet.Lookup[addr]
			}
			if def == nil {
				def = modbus.RegisterLookup[addr]
			}
			if def != nil {
				if err := sim.Server.WriteRegisterValue(def, value); err != nil {
					log.Printf("[%s] Failed to write register %d: %v", sim.Serial, addr, err)
				}
			}
		}
	}

	a.emitSimulatorUpdate()
	return nil
}

// GetAutomation returns automation status for a simulator
func (a *App) GetAutomation(id int) (map[string]interface{}, error) {
	sim := getSimulator(id)
	if sim == nil {
		return nil, fmt.Errorf("simulator %d not found", id)
	}

	sim.Automation.mu.RLock()
	defer sim.Automation.mu.RUnlock()

	return map[string]interface{}{
		"enabled":         sim.Automation.Enabled,
		"time_multiplier": sim.Automation.TimeMultiplier,
		"simulated_time":  sim.Automation.SimulatedTime.Format("2006-01-02 15:04:05"),
		"profile":         sim.Automation.Profile,
		"scenario":        sim.Automation.Scenario,
	}, nil
}

// StartAutomation starts the automation for a simulator
func (a *App) StartAutomation(id int) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}
	if sim.SiteID != 0 || isSimulatorMeter(sim.ID) {
		return fmt.Errorf("simulation is controlled by the site for assigned devices")
	}

	// Start the server if not running
	if !sim.Running {
		if err := startSimulatorServer(sim); err != nil {
			return err
		}
	}

	// Start automation
	sim.Automation.mu.Lock()
	if !sim.Automation.Enabled {
		sim.Automation.Enabled = true
		sim.Automation.stopChan = make(chan struct{})
		sim.Automation.SimulatedTime = time.Now()
		go runAutomationLoop(sim)
	}
	sim.Automation.mu.Unlock()

	markStateDirty()
	a.emitSimulatorUpdate()
	return nil
}

// StopAutomation stops the automation for a simulator
func (a *App) StopAutomation(id int) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}

	// Stop automation first
	stopSimulatorAutomation(sim)

	// Then stop the server
	stopSimulatorServer(sim)

	markStateDirty()
	a.emitSimulatorUpdate()
	return nil
}

// UpdateAutomationConfig updates automation configuration
func (a *App) UpdateAutomationConfig(id int, config map[string]interface{}) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}

	sim.Automation.mu.Lock()
	defer sim.Automation.mu.Unlock()

	if mult, ok := config["time_multiplier"].(float64); ok {
		sim.Automation.TimeMultiplier = mult
	}
	if scenario, ok := config["scenario"].(string); ok {
		sim.Automation.Scenario = scenario
	}

	markStateDirty()
	return nil
}

// StartEmulator starts the protocol server for a simulator
func (a *App) StartEmulator(id int) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}

	err := startSimulatorServer(sim)
	if err == nil {
		markStateDirty()
		a.emitSimulatorUpdate()
	}
	return err
}

// StopEmulator stops the protocol server for a simulator
func (a *App) StopEmulator(id int) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}

	stopSimulatorServer(sim)
	markStateDirty()
	a.emitSimulatorUpdate()
	return nil
}

// StartSimulation starts the data simulation for a simulator
func (a *App) StartSimulation(id int) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}
	if sim.SiteID != 0 || isSimulatorMeter(sim.ID) {
		return fmt.Errorf("simulation is controlled by the site for assigned devices")
	}

	if !sim.Running {
		return fmt.Errorf("server must be running to start simulation")
	}

	// Start automation
	sim.Automation.mu.Lock()
	if !sim.Automation.Enabled {
		sim.Automation.Enabled = true
		sim.Automation.stopChan = make(chan struct{})
		sim.Automation.SimulatedTime = time.Now()
		go runAutomationLoop(sim)
	}
	sim.Automation.mu.Unlock()

	markStateDirty()
	a.emitSimulatorUpdate()
	return nil
}

// StopSimulation stops the data simulation for a simulator
func (a *App) StopSimulation(id int) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}

	// Stop automation
	stopSimulatorAutomation(sim)

	markStateDirty()
	return nil
}

// SetState sets a state value for a simulator
func (a *App) SetState(id int, key string, value interface{}) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}

	var err error
	switch sim.Protocol {
	case "mqtt":
		if sim.MQTTServer != nil {
			err = sim.MQTTServer.SetValue(key, value)
		}
	case "ocpp":
		if sim.OCPPServer != nil {
			err = sim.OCPPServer.SetValue(key, value)
		}
	default:
		return fmt.Errorf("state changes not supported for this protocol")
	}

	return err
}

// GetClients returns connected clients for a simulator
func (a *App) GetClients(id int) (map[string]interface{}, error) {
	sim := getSimulator(id)
	if sim == nil {
		return nil, fmt.Errorf("simulator %d not found", id)
	}

	sim.mu.RLock()
	defer sim.mu.RUnlock()

	var clients interface{}
	var count int

	switch sim.Protocol {
	case "modbus":
		modbusClients := sim.Server.GetClients()
		clients = modbusClients
		count = len(modbusClients)
	case "mqtt":
		mqttClients := sim.MQTTServer.GetClients()
		clients = mqttClients
		count = len(mqttClients)
	default:
		return map[string]interface{}{"clients": []interface{}{}, "count": 0}, nil
	}

	return map[string]interface{}{"clients": clients, "count": count}, nil
}

// WakeUp wakes up or puts to sleep a V2X charger
func (a *App) WakeUp(id int, wake bool) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}

	if sim.Protocol == "mqtt" && sim.MQTTServer != nil {
		sim.MQTTServer.WakeUp(wake)
	}

	return nil
}

// SetPower sets the power for an MQTT V2X charger
func (a *App) SetPower(id int, power float64) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}

	if sim.Protocol == "mqtt" && sim.MQTTServer != nil {
		sim.MQTTServer.SetTargetPower(power)
	}

	return nil
}

// GetDebugMeterValues returns debug meter values for OCPP simulator
func (a *App) GetDebugMeterValues(id int) (interface{}, error) {
	sim := getSimulator(id)
	if sim == nil {
		return nil, fmt.Errorf("simulator %d not found", id)
	}

	if sim.Protocol != "ocpp" || sim.OCPPServer == nil {
		return nil, fmt.Errorf("not an OCPP simulator")
	}

	return sim.OCPPServer.GetMeterValuesDebug(), nil
}

// Reset resets a simulator to defaults
func (a *App) Reset(id int) error {
	sim := getSimulator(id)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", id)
	}

	sim.mu.Lock()
	defer sim.mu.Unlock()

	if sim.Protocol == "modbus" && sim.Server != nil {
		sim.Server.InitDefaults()
	}

	a.emitSimulatorUpdate()
	return nil
}

// ========== SITE METHODS ==========

// GetSites returns all sites
func (a *App) GetSites() []map[string]interface{} {
	sitesMu.RLock()
	defer sitesMu.RUnlock()

	var list []map[string]interface{}
	for _, site := range sites {
		site.mu.RLock()
		entry := map[string]interface{}{
			"id":              site.ID,
			"name":            site.Name,
			"meter_id":        site.MeterID,
			"meter_ids":       site.MeterIDs,
			"simulator_ids":   site.SimulatorIDs,
			"base_load_w":     site.BaseLoadW,
			"enabled":         site.Enabled,
			"fuse_size":       site.FuseSize,
			"fuse_profile":    site.FuseProfile,
			"phase_factors":   site.PhaseFactors,
			"fuse_blown":      site.FuseBlown,
			"bound_meter":     site.BoundMeter,
			"simulated_time":  site.SimulatedTime.Format("2006-01-02 15:04:05"),
			"time_multiplier": site.TimeMultiplier,
			"sim_running":     site.SimRunning,
			"scenario":        site.Scenario,
		}
		p1Mode := P1ModeDirect
		if site.P1Buffer != nil {
			p1Mode = site.P1Buffer.Mode
		}
		entry["p1_mode"] = string(p1Mode)
		site.mu.RUnlock()
		list = append(list, entry)
	}

	if list == nil {
		list = []map[string]interface{}{}
	}
	return list
}

// CreateSite creates a new site
func (a *App) CreateSite(name string, meterId int, simulatorIds []int, baseLoadW float64) (map[string]interface{}, error) {
	sitesMu.RLock()
	siteCount := len(sites)
	sitesMu.RUnlock()
	if siteCount >= 5 {
		return nil, fmt.Errorf("maximum of 5 sites allowed")
	}

	site, err := createSite(name, meterId, simulatorIds, baseLoadW)
	if err != nil {
		return nil, err
	}

	a.emitSiteUpdate()

	return map[string]interface{}{
		"id":            site.ID,
		"name":          site.Name,
		"meter_id":      site.MeterID,
		"simulator_ids": site.SimulatorIDs,
		"base_load_w":   site.BaseLoadW,
		"enabled":       site.Enabled,
	}, nil
}

// DeleteSite deletes a site by ID
func (a *App) DeleteSite(id int) error {
	err := deleteSite(id)
	if err == nil {
		a.emitSiteUpdate()
	}
	return err
}

// GetSite returns detailed info for a single site
func (a *App) GetSite(id int) (map[string]interface{}, error) {
	site := getSite(id)
	if site == nil {
		return nil, fmt.Errorf("site %d not found", id)
	}

	site.mu.RLock()
	p1Mode := P1ModeDirect
	if site.P1Buffer != nil {
		p1Mode = site.P1Buffer.Mode
	}

	result := map[string]interface{}{
		"id":              site.ID,
		"name":            site.Name,
		"meter_id":        site.MeterID,
		"meter_ids":       site.MeterIDs,
		"simulator_ids":   site.SimulatorIDs,
		"base_load_w":     site.BaseLoadW,
		"enabled":         site.Enabled,
		"fuse_size":       site.FuseSize,
		"fuse_profile":    site.FuseProfile,
		"phase_factors":   site.PhaseFactors,
		"fuse_blown":      site.FuseBlown,
		"fuse_overload":   site.FuseOverload,
		"bound_meter":     site.BoundMeter,
		"p1_mode":         string(p1Mode),
		"simulated_time":  site.SimulatedTime.Format("2006-01-02 15:04:05"),
		"time_multiplier": site.TimeMultiplier,
		"sim_running":     site.SimRunning,
		"scenario":        site.Scenario,
		"phase_loads":     site.SitePhaseLoads,
		"load_scenario":   site.LoadScenario,
		"load_linked":     site.LoadLinked,
	}

	meterID := site.MeterID
	simIDs := make([]int, len(site.SimulatorIDs))
	copy(simIDs, site.SimulatorIDs)
	baseLoad := site.BaseLoadW
	site.mu.RUnlock()

	// Add current meter reading if meter is valid
	meterSim := getSimulator(meterID)
	if meterSim != nil && meterSim.Protocol == "modbus" && meterSim.Server != nil {
		result["meter_power"] = getSemanticValue(meterSim, "meter_power")
		result["phase_currents"] = PhaseCurrents{
			L1: getSemanticValue(meterSim, "meter_l1_current"),
			L2: getSemanticValue(meterSim, "meter_l2_current"),
			L3: getSemanticValue(meterSim, "meter_l3_current"),
		}
		result["phase_powers"] = PhasePowers{
			L1: getSemanticValue(meterSim, "meter_l1_power"),
			L2: getSemanticValue(meterSim, "meter_l2_power"),
			L3: getSemanticValue(meterSim, "meter_l3_power"),
		}

		// Include virtual devices if meter is an inverter
		if meterSim.Category == "inverter" && meterSim.Running {
			pvPower := getSemanticValue(meterSim, "pv_power")
			batteryPower, batteryDirection := getBatteryPowerAndDirection(meterSim)
			batterySoc := getSemanticValue(meterSim, "battery_soc")

			result["virtual_devices"] = []map[string]interface{}{
				{"type": "pv", "power": pvPower},
				{"type": "battery", "power": batteryPower, "soc": batterySoc, "direction": batteryDirection},
			}
		}
	}

	// Add power contributions from each simulator
	powerContributions := make(map[int]float64)
	for _, simID := range simIDs {
		if simID == meterID {
			continue
		}
		sim := getSimulator(simID)
		if sim != nil {
			powerContributions[simID] = getPowerContribution(sim)
		}
	}
	result["power_contributions"] = powerContributions

	// Calculate total power
	var totalPower float64 = baseLoad
	for _, power := range powerContributions {
		totalPower += power
	}
	result["total_power"] = totalPower

	return result, nil
}

// UpdateSiteConfig updates site configuration
func (a *App) UpdateSiteConfig(id int, config map[string]interface{}) error {
	site := getSite(id)
	if site == nil {
		return fmt.Errorf("site %d not found", id)
	}

	parseBool := func(value interface{}) (bool, bool) {
		switch v := value.(type) {
		case bool:
			return v, true
		case float64:
			return v != 0, true
		case int:
			return v != 0, true
		default:
			return false, false
		}
	}

	site.mu.Lock()
	defer site.mu.Unlock()

	if name, ok := config["name"].(string); ok && name != "" {
		site.Name = name
	}
	if baseLoad, ok := config["base_load_w"].(float64); ok {
		site.BaseLoadW = baseLoad
	}
	fuseSize, sizeSet := config["fuse_size"].(float64)
	fuseProfile, profileSet := config["fuse_profile"].(string)
	if sizeSet || profileSet {
		size, profile := site.FuseSize, site.FuseProfile
		if sizeSet {
			if fuseSize != math.Trunc(fuseSize) {
				return fmt.Errorf("fuse size %v A is not whole amperes", fuseSize)
			}
			size = int(fuseSize)
		}
		if profileSet {
			profile = fuseProfile
		}
		if err := checkFuse(size, profile); err != nil {
			return err
		}
	}
	if sizeSet {
		site.FuseSize = int(fuseSize)
		site.FuseDamage = PhaseLoads{}
		site.FuseHeat = [3]fuse.Phase{}
		site.FuseOverload = FuseOverload{}
	}
	if profileSet {
		if _, exists := fuseProfiles[fuseProfile]; !exists {
			return fmt.Errorf("unknown fuse profile")
		}
		if site.FuseProfile != fuseProfile {
			site.FuseProfile = fuseProfile
			site.FuseDamage = PhaseLoads{}
			site.FuseHeat = [3]fuse.Phase{}
			site.FuseOverload = FuseOverload{}
		}
	}
	if pf, ok := config["phase_factors"].(map[string]interface{}); ok {
		if l1, ok := pf["l1"].(float64); ok {
			site.PhaseFactors.L1 = l1
		}
		if l2, ok := pf["l2"].(float64); ok {
			site.PhaseFactors.L2 = l2
		}
		if l3, ok := pf["l3"].(float64); ok {
			site.PhaseFactors.L3 = l3
		}
	}
	if fb, ok := config["fuse_blown"].(map[string]interface{}); ok {
		if l1, ok := parseBool(fb["l1"]); ok {
			if site.FuseBlown.L1 && !l1 {
				site.FuseDamage.L1, site.FuseHeat[0] = 0, fuse.Phase{}
			}
			site.FuseBlown.L1 = l1
		}
		if l2, ok := parseBool(fb["l2"]); ok {
			if site.FuseBlown.L2 && !l2 {
				site.FuseDamage.L2, site.FuseHeat[1] = 0, fuse.Phase{}
			}
			site.FuseBlown.L2 = l2
		}
		if l3, ok := parseBool(fb["l3"]); ok {
			if site.FuseBlown.L3 && !l3 {
				site.FuseDamage.L3, site.FuseHeat[2] = 0, fuse.Phase{}
			}
			site.FuseBlown.L3 = l3
		}
	}

	markStateDirty()
	a.emitSiteUpdate()
	return nil
}

// SetSiteMeter sets the meter for a site
func (a *App) SetSiteMeter(id int, meterId int) error {
	site := getSite(id)
	if site == nil {
		return fmt.Errorf("site %d not found", id)
	}

	site.mu.RLock()
	bound := site.BoundMeter
	site.mu.RUnlock()
	if bound {
		return fmt.Errorf("cannot change meter for site with bound meter")
	}

	site.mu.Lock()
	oldMeterID := site.MeterID
	site.MeterID = meterId
	newMeterIDs := []int{meterId}
	for _, id := range site.MeterIDs {
		if id == oldMeterID || id == meterId {
			continue
		}
		newMeterIDs = append(newMeterIDs, id)
	}
	site.MeterIDs = newMeterIDs
	site.mu.Unlock()

	startSiteAggregation(site)
	markStateDirty()
	a.emitSiteUpdate()
	return nil
}

// AddSimulatorToSite adds a simulator to a site
func (a *App) AddSimulatorToSite(siteId int, simulatorId int) error {
	site := getSite(siteId)
	if site == nil {
		return fmt.Errorf("site %d not found", siteId)
	}

	sim := getSimulator(simulatorId)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", simulatorId)
	}

	// Check if already in this site
	site.mu.Lock()
	for _, id := range site.SimulatorIDs {
		if id == simulatorId {
			site.mu.Unlock()
			return fmt.Errorf("simulator already in site")
		}
	}

	// Check if in another site
	sitesMu.RLock()
	for _, otherSite := range sites {
		if otherSite.ID == site.ID {
			continue
		}
		otherSite.mu.RLock()
		for _, id := range otherSite.SimulatorIDs {
			if id == simulatorId {
				otherSite.mu.RUnlock()
				sitesMu.RUnlock()
				site.mu.Unlock()
				return fmt.Errorf("simulator already in site %d", otherSite.ID)
			}
		}
		otherSite.mu.RUnlock()
	}
	sitesMu.RUnlock()

	site.SimulatorIDs = append(site.SimulatorIDs, simulatorId)
	site.mu.Unlock()

	sim.mu.Lock()
	sim.SiteID = siteId
	sim.mu.Unlock()

	markStateDirty()
	a.emitSiteUpdate()
	return nil
}

// RemoveSimulatorFromSite removes a simulator from a site
func (a *App) RemoveSimulatorFromSite(siteId int, simulatorId int) error {
	site := getSite(siteId)
	if site == nil {
		return fmt.Errorf("site %d not found", siteId)
	}

	site.mu.Lock()
	found := false
	newIDs := make([]int, 0, len(site.SimulatorIDs))
	for _, id := range site.SimulatorIDs {
		if id == simulatorId {
			found = true
		} else {
			newIDs = append(newIDs, id)
		}
	}
	if !found {
		site.mu.Unlock()
		return fmt.Errorf("simulator not in site")
	}
	site.SimulatorIDs = newIDs
	site.mu.Unlock()

	// Reset simulator's SiteID
	sim := getSimulator(simulatorId)
	if sim != nil {
		sim.mu.Lock()
		sim.SiteID = 0
		sim.mu.Unlock()
	}

	markStateDirty()
	a.emitSiteUpdate()
	return nil
}

// StartSiteSimulation starts data generation for all devices in a site
func (a *App) StartSiteSimulation(id int) error {
	site := getSite(id)
	if site == nil {
		return fmt.Errorf("site %d not found", id)
	}

	if err := startSiteSimulationWithServers(site); err != nil {
		return err
	}

	markStateDirty()
	a.emitSiteUpdate()
	return nil
}

// StopSiteSimulation stops data generation for a site
func (a *App) StopSiteSimulation(id int) error {
	site := getSite(id)
	if site == nil {
		return fmt.Errorf("site %d not found", id)
	}

	stopSiteSimulation(site)
	markStateDirty()
	a.emitSiteUpdate()
	return nil
}

// UpdateSiteSimulationConfig updates simulation parameters for a site
func (a *App) UpdateSiteSimulationConfig(id int, config map[string]interface{}) error {
	site := getSite(id)
	if site == nil {
		return fmt.Errorf("site %d not found", id)
	}

	site.mu.Lock()
	if mult, ok := config["time_multiplier"].(float64); ok && mult > 0 {
		site.TimeMultiplier = mult
	}
	if scenario, ok := config["scenario"].(string); ok && scenario != "" {
		site.Scenario = scenario
	}
	if clockMs, ok := config["clock_ms"].(float64); ok && clockMs > 0 {
		site.SimulatedTime = time.Unix(0, int64(clockMs)*int64(time.Millisecond)).In(time.Local)
	}
	site.mu.Unlock()

	markStateDirty()
	a.emitSiteUpdate()
	return nil
}

// SetDevicePowerState toggles a device's power state within a site
func (a *App) SetDevicePowerState(simId int, deviceOn bool) error {
	sim := getSimulator(simId)
	if sim == nil {
		return fmt.Errorf("simulator %d not found", simId)
	}

	sim.mu.Lock()
	sim.DeviceOn = deviceOn
	sim.mu.Unlock()

	// If turning device off, stop its server
	if !deviceOn {
		stopSimulatorServer(sim)
	}

	markStateDirty()
	a.emitSimulatorUpdate()
	return nil
}

// CreateSimulatorInSite creates a new simulator and adds it to a site
func (a *App) CreateSimulatorInSite(siteId int, simType string) (map[string]interface{}, error) {
	site := getSite(siteId)
	if site == nil {
		return nil, fmt.Errorf("site %d not found", siteId)
	}

	sim, err := createSimulator(simType)
	if err != nil {
		return nil, err
	}

	// Add to site
	site.mu.Lock()
	site.SimulatorIDs = append(site.SimulatorIDs, sim.ID)
	site.mu.Unlock()

	// Update simulator's site ID
	sim.mu.Lock()
	sim.SiteID = siteId
	sim.mu.Unlock()

	markStateDirty()
	a.emitSimulatorUpdate()
	a.emitSiteUpdate()

	return map[string]interface{}{
		"id":             sim.ID,
		"serial":         sim.Serial,
		"protocol":       sim.Protocol,
		"category":       sim.Category,
		"port":           sim.Port,
		"running":        sim.Running,
		"site_id":        siteId,
		"device_on":      sim.DeviceOn,
		"mdns_hostname":  mdnsAdvertiser.ActiveHostname(sim.ID),
	}, nil
}

// AddMeterToSite adds an additional meter to a site
func (a *App) AddMeterToSite(siteId int, meterId int) error {
	site := getSite(siteId)
	if site == nil {
		return fmt.Errorf("site %d not found", siteId)
	}

	meterSim := getSimulator(meterId)
	if meterSim == nil {
		return fmt.Errorf("meter %d not found", meterId)
	}
	if meterSim.Protocol != "modbus" {
		return fmt.Errorf("meter must be a Modbus simulator")
	}

	site.mu.Lock()
	for _, id := range site.MeterIDs {
		if id == meterId {
			site.mu.Unlock()
			return fmt.Errorf("meter already in site")
		}
	}
	site.MeterIDs = append(site.MeterIDs, meterId)
	site.mu.Unlock()

	startSiteAggregation(site)
	markStateDirty()
	a.emitSiteUpdate()
	return nil
}

// SetP1Mode sets the P1 port update mode for a site
func (a *App) SetP1Mode(id int, mode string) (map[string]interface{}, error) {
	site := getSite(id)
	if site == nil {
		return nil, fmt.Errorf("site %d not found", id)
	}

	// Validate mode
	var p1Mode P1UpdateMode
	switch mode {
	case "direct":
		p1Mode = P1ModeDirect
	case "1s":
		p1Mode = P1Mode1s
	case "10s":
		p1Mode = P1Mode10s
	default:
		return nil, fmt.Errorf("invalid mode. Must be 'direct', '1s', or '10s'")
	}

	site.mu.Lock()
	if site.P1Buffer == nil {
		site.P1Buffer = newP1IntervalBuffer()
	}
	site.P1Buffer.Mode = p1Mode
	site.P1Buffer.resetEnergy()
	site.mu.Unlock()

	markStateDirty()
	return map[string]interface{}{"mode": mode}, nil
}

// GetVirtualDevices returns virtual devices for a site
func (a *App) GetVirtualDevices(siteId int) (map[string]interface{}, error) {
	site := getSite(siteId)
	if site == nil {
		return nil, fmt.Errorf("site %d not found", siteId)
	}

	site.mu.RLock()
	meterID := site.MeterID
	site.mu.RUnlock()

	meterSim := getSimulator(meterID)
	if meterSim == nil {
		return map[string]interface{}{"virtual_devices": []interface{}{}}, nil
	}

	meterSim.mu.RLock()
	category := meterSim.Category
	running := meterSim.Running
	hasServer := meterSim.Server != nil
	meterSim.mu.RUnlock()

	if category != "inverter" || !running || !hasServer {
		return map[string]interface{}{"virtual_devices": []interface{}{}}, nil
	}

	pvPower := getSemanticValue(meterSim, "pv_power")
	batteryPower, batteryDirection := getBatteryPowerAndDirection(meterSim)
	batterySoc := getSemanticValue(meterSim, "battery_soc")

	virtualDevices := []map[string]interface{}{
		{"type": "pv", "power": pvPower},
		{"type": "battery", "power": batteryPower, "soc": batterySoc, "direction": batteryDirection},
	}

	return map[string]interface{}{"virtual_devices": virtualDevices}, nil
}

// WriteVirtualDevice writes to a virtual device
func (a *App) WriteVirtualDevice(siteId int, deviceType string, data map[string]interface{}) (map[string]interface{}, error) {
	site := getSite(siteId)
	if site == nil {
		return nil, fmt.Errorf("site %d not found", siteId)
	}

	site.mu.RLock()
	meterID := site.MeterID
	site.mu.RUnlock()

	meterSim := getSimulator(meterID)
	if meterSim == nil || meterSim.Category != "inverter" {
		return nil, fmt.Errorf("meter is not an inverter")
	}

	meterSim.mu.Lock()
	defer meterSim.mu.Unlock()

	if meterSim.Server == nil {
		return nil, fmt.Errorf("meter server not available")
	}

	switch deviceType {
	case "pv":
		if power, ok := data["power"].(float64); ok {
			setSemanticValue(meterSim, "pv_power", power)
		}
	case "battery":
		if power, ok := data["power"].(float64); ok {
			setSemanticValue(meterSim, "battery_power", math.Abs(power))
			// Update direction command based on sign
			var cmd float64 = 204
			if power > 0 {
				cmd = 170
			} else if power < 0 {
				cmd = 187
			}
			setSemanticValue(meterSim, "battery_cmd", cmd)
			flags := 0.0
			if cmd == 187 {
				flags = 4 // discharging
			} else if cmd == 170 {
				flags = 2 // charging
			}
			setSemanticValue(meterSim, "battery_flags", flags)
		}
		if soc, ok := data["soc"].(float64); ok {
			setSemanticValue(meterSim, "battery_soc", soc)
		}
		if direction, ok := data["direction"].(string); ok {
			var cmd float64
			switch direction {
			case "charging":
				cmd = 170
			case "discharging":
				cmd = 187
			default:
				cmd = 204 // idle
			}
			setSemanticValue(meterSim, "battery_cmd", cmd)
			flags := 0.0
			if cmd == 187 {
				flags = 4 // discharging
			} else if cmd == 170 {
				flags = 2 // charging
			}
			setSemanticValue(meterSim, "battery_flags", flags)
		}
	default:
		return nil, fmt.Errorf("unknown device type: %s", deviceType)
	}

	markStateDirty()
	return map[string]interface{}{"status": "ok"}, nil
}

// ========== EVENT EMITTERS ==========

func (a *App) emitSimulatorUpdate() {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "simulator:updated")
	}
}

func (a *App) emitSiteUpdate() {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "site:updated")
	}
}

// GetSystemInfo returns system information
func (a *App) GetSystemInfo() map[string]interface{} {
	return map[string]interface{}{
		"local_ip": getLocalIP(),
	}
}

// GetVersionInfo returns the current app version and platform info
func (a *App) GetVersionInfo() map[string]string {
	return GetVersionInfo()
}

// CheckForUpdate checks if a newer version is available on GitHub
func (a *App) CheckForUpdate() (*UpdateInfo, error) {
	return CheckForUpdate()
}

// ApplyUpdate downloads and applies the latest update
func (a *App) ApplyUpdate() error {
	return DoUpdate()
}

// RestartApp restarts the application process
func (a *App) RestartApp() error {
	return scheduleRestart()
}

// GetWarnings returns current runtime warnings
func (a *App) GetWarnings() map[string]interface{} {
	return map[string]interface{}{
		"warnings": getWarnings(),
	}
}

// ClearWarnings clears runtime warnings
func (a *App) ClearWarnings() map[string]string {
	clearWarnings()
	return map[string]string{"status": "cleared"}
}

// GetSettings returns current app settings
func (a *App) GetSettings() (map[string]interface{}, error) {
	s := runtimeSettings
	if s == nil {
		s = settings.Load()
	}
	return map[string]interface{}{
		"modbusBasePort":      s.ModbusBasePort,
		"mqttBasePort":        s.MQTTBasePort,
		"ocppBasePort":        s.OCPPBasePort,
		"modbusDisplayOffset": s.ModbusDisplayOffset,
		"mqttDisplayOffset":   s.MQTTDisplayOffset,
		"ocppDisplayOffset":   s.OCPPDisplayOffset,
		"siteTickMs":          s.SiteTickMs,
	}, nil
}

// SaveSettings saves app settings to config file
func (a *App) SaveSettings(s map[string]interface{}) error {
	appSettings := settings.Load()

	if modbusPort, ok := s["modbusBasePort"].(float64); ok {
		appSettings.ModbusBasePort = int(modbusPort)
	}
	if mqttPort, ok := s["mqttBasePort"].(float64); ok {
		appSettings.MQTTBasePort = int(mqttPort)
	}
	if ocppPort, ok := s["ocppBasePort"].(float64); ok {
		appSettings.OCPPBasePort = int(ocppPort)
	}
	if modbusOffset, ok := s["modbusDisplayOffset"].(float64); ok {
		appSettings.ModbusDisplayOffset = int(modbusOffset)
	}
	if mqttOffset, ok := s["mqttDisplayOffset"].(float64); ok {
		appSettings.MQTTDisplayOffset = int(mqttOffset)
	}
	if ocppOffset, ok := s["ocppDisplayOffset"].(float64); ok {
		appSettings.OCPPDisplayOffset = int(ocppOffset)
	}
	if tickMs, ok := s["siteTickMs"].(float64); ok {
		if int(tickMs) > 0 {
			appSettings.SiteTickMs = int(tickMs)
		} else {
			appSettings.SiteTickMs = 500
		}
	}

	return settings.Save(appSettings)
}

// GetLoadScenarios returns all available load scenarios
func (a *App) GetLoadScenarios() []*LoadScenario {
	return GetLoadScenariosSlice()
}

// SetSitePhaseLoads sets the per-phase loads for a site
func (a *App) SetSitePhaseLoads(id int, l1, l2, l3 float64, linked bool) (map[string]interface{}, error) {
	site := getSite(id)
	if site == nil {
		return nil, fmt.Errorf("site %d not found", id)
	}

	site.mu.Lock()
	site.SitePhaseLoads.L1 = l1
	site.SitePhaseLoads.L2 = l2
	site.SitePhaseLoads.L3 = l3
	site.LoadLinked = linked
	site.BaseLoadW = l1 + l2 + l3
	loads := site.SitePhaseLoads
	site.mu.Unlock()

	a.emitSiteUpdate()

	return map[string]interface{}{
		"phase_loads": loads,
		"load_linked": linked,
		"base_load_w": loads.Total(),
	}, nil
}

// SetSiteLoadScenario sets the active load scenario for a site
func (a *App) SetSiteLoadScenario(id int, scenario string) (map[string]interface{}, error) {
	site := getSite(id)
	if site == nil {
		return nil, fmt.Errorf("site %d not found", id)
	}

	// Allow empty scenario for backward compatibility
	if scenario != "" {
		if _, ok := LoadScenarios[scenario]; !ok {
			return nil, fmt.Errorf("unknown load scenario: %s", scenario)
		}
	}

	site.mu.Lock()
	site.LoadScenario = scenario

	// If switching to a scenario, initialize phase loads to current time slot
	// For "static", keep current phase load values
	if scenario != "" && scenario != "static" {
		if ls, ok := LoadScenarios[scenario]; ok {
			hour := site.SimulatedTime.Hour()
			if site.SimulatedTime.IsZero() {
				hour = time.Now().Hour()
			}
			loads := ls.GetLoadForTime(hour)
			site.SitePhaseLoads = loads
			site.BaseLoadW = loads.Total()
		}
	}

	result := map[string]interface{}{
		"load_scenario": site.LoadScenario,
		"phase_loads":   site.SitePhaseLoads,
		"base_load_w":   site.SitePhaseLoads.Total(),
	}
	site.mu.Unlock()

	a.emitSiteUpdate()

	return result, nil
}
