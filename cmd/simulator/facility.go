package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/srcfl/device-simulator/internal/modbus"
	"github.com/srcfl/device-simulator/internal/modbus/devices"
	"github.com/srcfl/device-simulator/internal/plant"
)

func handleFacilitiesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"facilities": plant.Presets()})
}

func handleSiteFacility(w http.ResponseWriter, r *http.Request, site *Site) {
	switch r.Method {
	case http.MethodGet:
		site.mu.RLock()
		id := site.FacilityID
		site.mu.RUnlock()
		var fac plant.Facility
		if f, ok := plant.Preset(id); ok {
			fac = f
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"facility_id": id,
			"facility":    fac,
		})
	case http.MethodPost:
		var req struct {
			Preset string `json:"preset"`
			Start  *bool  `json:"start"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if err := applyFacilityPreset(site, req.Preset); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		start := true
		if req.Start != nil {
			start = *req.Start
		}
		if start {
			if err := startSiteSimulationWithServers(site); err != nil {
				log.Printf("[Site %d] start after facility: %v", site.ID, err)
			}
		}
		markStateDirty()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sitePlantJSON(site))
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleSitePlant(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sitePlantJSON(site))
}

func sitePlantJSON(site *Site) map[string]interface{} {
	site.mu.RLock()
	snap := site.lastPlant
	id := site.FacilityID
	backend := ""
	if site.sitePlant != nil {
		backend = site.sitePlant.Backend()
	}
	site.mu.RUnlock()
	if snap.Backend == "" {
		snap.Backend = backend
	}
	if snap.FacilityID == "" {
		snap.FacilityID = id
		if f, ok := plant.Preset(id); ok {
			snap.FacilityName = f.Name
		}
	}
	return map[string]interface{}{
		"facility_id": id,
		"plant":       snap,
		"facilities":  plant.Presets(),
	}
}

func applyFacilityPreset(site *Site, presetID string) error {
	spec, ok := plant.Preset(presetID)
	if !ok {
		return fmt.Errorf("unknown facility preset %q", presetID)
	}

	site.mu.Lock()
	site.FacilityID = spec.ID
	site.Scenario = "physical"
	site.sitePlant = nil
	site.lastPlant = plant.Snapshot{}
	// Occupancy schedule is the household. Extra slider load starts at 0 so
	// the default 1 kW site load does not double-count the plant.
	site.SitePhaseLoads = PhaseLoads{}
	site.BaseLoadW = 0
	site.mu.Unlock()

	if err := ensureFacilityInverter(site, spec); err != nil {
		return err
	}
	if spec.HasHeatPump() {
		if err := ensureFacilityHeatPump(site); err != nil {
			return err
		}
	}

	applyFacilityRatings(site, spec)

	model := ensureSitePlant(site)
	if host, ok := model.(interface{ SetFacility(plant.Facility) }); ok {
		host.SetFacility(spec)
	}

	setSiteInverterPhysical(site)
	log.Printf("[Site %d] facility %s", site.ID, spec.ID)
	return nil
}

func applyFacilityRatings(site *Site, spec plant.Facility) {
	site.mu.RLock()
	ids := append([]int{}, site.SimulatorIDs...)
	meter := site.MeterID
	site.mu.RUnlock()
	ids = append(ids, meter)
	for _, id := range ids {
		sim := getSimulator(id)
		if sim == nil {
			continue
		}
		sim.mu.RLock()
		cat := sim.Category
		sim.mu.RUnlock()
		if cat != "inverter" {
			continue
		}
		if spec.BatteryKWh > 0 {
			sim.mu.Lock()
			sim.BatteryCapacityKWh = spec.BatteryKWh
			sim.mu.Unlock()
		}
		u := ensurePlant(sim)
		if spec.PVRatedW > 0 {
			u.Params.RatedPVW = spec.PVRatedW
			u.Params.RatedACW = spec.PVRatedW
		}
		if spec.BatteryKWh > 0 {
			u.Params.BatteryCapacityWh = spec.BatteryKWh * 1000
		}
	}
}

func ensureFacilityInverter(site *Site, spec plant.Facility) error {
	if siteHasCategory(site, "inverter") {
		return nil
	}
	sim, err := createSimulator("inverter")
	if err != nil {
		return err
	}
	applyProfileLocked(sim, "sungrow-hybrid")
	if spec.BatteryKWh > 0 {
		sim.mu.Lock()
		sim.BatteryCapacityKWh = spec.BatteryKWh
		sim.mu.Unlock()
	}
	attachSimulatorToSite(site, sim)
	return nil
}

func ensureFacilityHeatPump(site *Site) error {
	if siteHasCategory(site, "heat_pump") {
		return nil
	}
	sim, err := createSimulator("heat_pump")
	if err != nil {
		return err
	}
	attachSimulatorToSite(site, sim)
	return nil
}

func attachSimulatorToSite(site *Site, sim *Simulator) {
	site.mu.Lock()
	site.SimulatorIDs = append(site.SimulatorIDs, sim.ID)
	siteID := site.ID
	site.mu.Unlock()
	sim.mu.Lock()
	sim.SiteID = siteID
	sim.mu.Unlock()
}

func siteHasCategory(site *Site, category string) bool {
	site.mu.RLock()
	ids := append([]int{}, site.SimulatorIDs...)
	meter := site.MeterID
	site.mu.RUnlock()
	check := func(id int) bool {
		sim := getSimulator(id)
		if sim == nil {
			return false
		}
		sim.mu.RLock()
		defer sim.mu.RUnlock()
		return sim.Category == category
	}
	if check(meter) {
		return true
	}
	for _, id := range ids {
		if check(id) {
			return true
		}
	}
	return false
}

func setSiteInverterPhysical(site *Site) {
	for _, sim := range collectSiteInverters(site) {
		sim.Automation.mu.Lock()
		sim.Automation.Scenario = "physical"
		sim.Automation.mu.Unlock()
	}
}

func applyProfileLocked(sim *Simulator, name string) {
	sim.mu.Lock()
	defer sim.mu.Unlock()
	profile := sim.ProfilesManager.Get(name)
	if profile == nil {
		return
	}
	sim.ProfilesManager.SetActive(name)
	if sim.Protocol != "modbus" || sim.Server == nil {
		return
	}
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
			_ = sim.Server.WriteRegisterValue(def, value)
		}
	}
}

func collectSiteHeatPumps(site *Site) []*Simulator {
	site.mu.RLock()
	ids := append([]int{}, site.SimulatorIDs...)
	site.mu.RUnlock()
	var out []*Simulator
	for _, id := range ids {
		sim := getSimulator(id)
		if sim == nil {
			continue
		}
		sim.mu.RLock()
		ok := sim.Running && sim.DeviceOn && sim.Category == "heat_pump" && sim.Server != nil
		sim.mu.RUnlock()
		if ok {
			out = append(out, sim)
		}
	}
	return out
}

func heatPumpCommandFromSite(site *Site) plant.HeatPumpCommand {
	pumps := collectSiteHeatPumps(site)
	if len(pumps) == 0 {
		return plant.HeatPumpCommand{}
	}
	sim := pumps[0]
	return plant.HeatPumpCommand{
		Connected: true,
		Enable:    getSemanticValue(sim, "hp_enable") >= 0.5,
		SetpointC: getSemanticValue(sim, "hp_setpoint"),
		Mode:      int(getSemanticValue(sim, "hp_mode")),
		SGReady:   int(getSemanticValue(sim, "hp_sg_ready")),
	}
}

func applyHeatPumpOutputs(sim *Simulator, th plant.Thermal) {
	setSemanticValue(sim, "indoor_temp", th.IndoorC)
	setSemanticValue(sim, "outdoor_temp", th.AmbientC)
	setSemanticValue(sim, "dhw_temp", th.DHWC)
	setSemanticValue(sim, "hp_power", th.ElectricalW+th.BackupW)
	setSemanticValue(sim, "hp_thermal", th.QHpW)
	setSemanticValue(sim, "hp_cop", th.COP)
	status := 0.0
	if th.QDHWW > th.QSpaceW && th.QHpW > 10 {
		status = 2
	} else if th.QHpW > 10 {
		status = 1
	}
	setSemanticValue(sim, "hp_status", status)
}
