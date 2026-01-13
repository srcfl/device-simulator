package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/srcfl/device-simulator/internal/modbus"
	"github.com/srcfl/device-simulator/internal/profiles"
)

//go:embed templates/*
var templatesFS embed.FS

// Simulator represents a single simulator instance
type Simulator struct {
	ID              int              `json:"id"`
	Serial          string           `json:"serial"`
	SlaveID         uint8            `json:"slave_id"`
	ModbusPort      int              `json:"modbus_port"`
	Server          *modbus.Server   `json:"-"`
	ProfilesManager *profiles.Manager `json:"-"`
	Automation      *AutomationState `json:"-"`
	mu              sync.RWMutex
}

// AutomationState holds automation config
type AutomationState struct {
	mu             sync.RWMutex
	Enabled        bool      `json:"enabled"`
	TimeMultiplier float64   `json:"time_multiplier"`
	SimulatedTime  time.Time `json:"simulated_time"`
	Profile        string    `json:"profile"`
	Scenario       string    `json:"scenario"`
	stopChan       chan struct{}
}

var (
	simulators    []*Simulator
	simulatorsMu  sync.RWMutex
)

func main() {
	numInstances := flag.Int("instances", 10, "Number of simulator instances")
	modbusBasePort := flag.Int("modbus-base", 5000, "Base port for Modbus servers")
	httpPort := flag.Int("http", 8080, "HTTP server port")
	flag.Parse()

	// Environment variable overrides
	if env := os.Getenv("NUM_INSTANCES"); env != "" {
		if n, err := strconv.Atoi(env); err == nil {
			*numInstances = n
		}
	}
	if env := os.Getenv("MODBUS_BASE_PORT"); env != "" {
		if n, err := strconv.Atoi(env); err == nil {
			*modbusBasePort = n
		}
	}
	if env := os.Getenv("HTTP_PORT"); env != "" {
		if n, err := strconv.Atoi(env); err == nil {
			*httpPort = n
		}
	}

	log.Printf("Starting Device Simulator")
	log.Printf("Instances: %d", *numInstances)
	log.Printf("Modbus ports: %d-%d", *modbusBasePort, *modbusBasePort+*numInstances-1)
	log.Printf("HTTP port: %d", *httpPort)

	// Create simulators
	simulators = make([]*Simulator, *numInstances)
	for i := 0; i < *numInstances; i++ {
		serial := fmt.Sprintf("INVSIM%02d", i)
		modbusPort := *modbusBasePort + i

		server := modbus.NewServer(65535)
		server.SetSerialNumber(serial)
		server.InitDefaults()

		pm := profiles.NewManager("profiles")
		pm.InitEmbedded()
		pm.LoadAll()

		simulators[i] = &Simulator{
			ID:              i + 1,
			Serial:          serial,
			SlaveID:         1, // Default slave ID
			ModbusPort:      modbusPort,
			Server:          server,
			ProfilesManager: pm,
			Automation: &AutomationState{
				TimeMultiplier: 1.0,
				SimulatedTime:  time.Now(),
				Scenario:       "sunny",
			},
		}
	}

	// Start Modbus servers
	for _, sim := range simulators {
		go func(s *Simulator) {
			addr := fmt.Sprintf(":%d", s.ModbusPort)
			log.Printf("[%s] Starting Modbus server on %s (slave ID: %d)", s.Serial, addr, s.SlaveID)
			if err := s.Server.Listen(addr); err != nil {
				log.Printf("[%s] Modbus listen error: %v", s.Serial, err)
				return
			}
			if err := s.Server.Serve(); err != nil {
				log.Printf("[%s] Modbus serve error: %v", s.Serial, err)
			}
		}(sim)
	}

	// Start HTTP server
	go startHTTPServer(*httpPort)

	log.Println("All servers started. Press Ctrl+C to stop.")

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	for _, sim := range simulators {
		sim.Server.Close()
	}
}

func startHTTPServer(port int) {
	mux := http.NewServeMux()

	// Dashboard
	mux.HandleFunc("/", handleDashboard)

	// Simulator list
	mux.HandleFunc("/api/simulators", handleSimulatorsList)

	// Simulator-specific endpoints
	mux.HandleFunc("/api/simulators/", handleSimulatorAPI)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("HTTP server listening on %s", addr)
	if err := http.ListenAndServe(addr, corsMiddleware(mux)); err != nil {
		log.Printf("HTTP server error: %v", err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	tmpl, err := template.ParseFS(templatesFS, "templates/index.html")
	if err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		log.Printf("Template error: %v", err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, nil)
}

func handleSimulatorsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	simulatorsMu.RLock()
	list := make([]map[string]interface{}, len(simulators))
	for i, sim := range simulators {
		sim.mu.RLock()
		list[i] = map[string]interface{}{
			"id":          sim.ID,
			"serial":      sim.Serial,
			"slave_id":    sim.SlaveID,
			"modbus_port": sim.ModbusPort,
			"pv_power":    getRegisterValue(sim.Server, 5016),
			"soc":         getRegisterValue(sim.Server, 13022),
			"load":        getRegisterValue(sim.Server, 13007),
			"meter":       getRegisterValue(sim.Server, 13009),
			"automation":  sim.Automation.Enabled,
			"profile":     sim.ProfilesManager.GetActiveName(),
		}
		sim.mu.RUnlock()
	}
	simulatorsMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func handleSimulatorAPI(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/simulators/{id}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/api/simulators/")
	parts := strings.SplitN(path, "/", 2)

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Simulator ID required", http.StatusBadRequest)
		return
	}

	simID, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "Invalid simulator ID", http.StatusBadRequest)
		return
	}

	sim := getSimulator(simID)
	if sim == nil {
		http.Error(w, "Simulator not found", http.StatusNotFound)
		return
	}

	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case action == "" || action == "status":
		handleSimulatorStatus(w, r, sim)
	case action == "config":
		handleSimulatorConfig(w, r, sim)
	case action == "registers":
		handleRegisters(w, r, sim)
	case strings.HasPrefix(action, "registers/"):
		addr := strings.TrimPrefix(action, "registers/")
		handleRegister(w, r, sim, addr)
	case action == "logs":
		handleLogs(w, r, sim)
	case action == "profiles":
		handleProfiles(w, r, sim)
	case strings.HasPrefix(action, "profiles/"):
		profilePath := strings.TrimPrefix(action, "profiles/")
		handleProfile(w, r, sim, profilePath)
	case action == "automation":
		handleAutomation(w, r, sim)
	case action == "automation/start":
		handleAutomationStart(w, r, sim)
	case action == "automation/stop":
		handleAutomationStop(w, r, sim)
	case action == "automation/config":
		handleAutomationConfig(w, r, sim)
	case action == "reset":
		handleReset(w, r, sim)
	default:
		http.Error(w, "Unknown action", http.StatusNotFound)
	}
}

func getSimulator(id int) *Simulator {
	simulatorsMu.RLock()
	defer simulatorsMu.RUnlock()
	for _, sim := range simulators {
		if sim.ID == id {
			return sim
		}
	}
	return nil
}

func handleSimulatorStatus(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	sim.mu.RLock()
	status := map[string]interface{}{
		"id":          sim.ID,
		"serial":      sim.Serial,
		"slave_id":    sim.SlaveID,
		"modbus_port": sim.ModbusPort,
		"pv_power":    getRegisterValue(sim.Server, 5016),
		"soc":         getRegisterValue(sim.Server, 13022),
		"load":        getRegisterValue(sim.Server, 13007),
		"meter":       getRegisterValue(sim.Server, 13009),
		"frequency":   getRegisterValue(sim.Server, 5241),
		"automation": map[string]interface{}{
			"enabled":         sim.Automation.Enabled,
			"time_multiplier": sim.Automation.TimeMultiplier,
			"simulated_time":  sim.Automation.SimulatedTime,
		},
	}
	sim.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func handleSimulatorConfig(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var config struct {
		SlaveID *uint8  `json:"slave_id"`
		Serial  *string `json:"serial"`
	}

	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	sim.mu.Lock()
	if config.SlaveID != nil {
		sim.SlaveID = *config.SlaveID
		log.Printf("[%s] Slave ID changed to %d", sim.Serial, sim.SlaveID)
	}
	if config.Serial != nil {
		sim.Serial = *config.Serial
		sim.Server.SetSerialNumber(*config.Serial)
		log.Printf("[%s] Serial changed", sim.Serial)
	}
	sim.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func handleRegisters(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

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
			continue // Skip registers not matching profile categories
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payloads)
}

// containsCategory checks if a category is in the list
func containsCategory(categories []string, category string) bool {
	for _, c := range categories {
		if c == category {
			return true
		}
	}
	return false
}

func handleRegister(w http.ResponseWriter, r *http.Request, sim *Simulator, addrStr string) {
	addr, err := strconv.ParseUint(addrStr, 10, 16)
	if err != nil {
		http.Error(w, "Invalid address", http.StatusBadRequest)
		return
	}

	def := modbus.RegisterLookup[uint16(addr)]
	if def == nil {
		http.Error(w, "Register not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case "GET":
		var regs []uint16
		if def.UseHolding {
			regs = sim.Server.GetHoldingRegisters(def.Address, def.Words)
		} else {
			regs = sim.Server.GetInputRegisters(def.Address, def.Words)
		}
		payload, _ := def.ToPayload(regs)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload)

	case "POST":
		var req struct {
			Value float64 `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if err := sim.Server.WriteRegisterValue(def, req.Value); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "written"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleLogs(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	logs := sim.Server.GetLogs(limit)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"logs": logs})
}

func handleProfiles(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	list := sim.ProfilesManager.List()
	active := sim.ProfilesManager.GetActiveName()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"profiles": list,
		"active":   active,
	})
}

func handleProfile(w http.ResponseWriter, r *http.Request, sim *Simulator, profilePath string) {
	parts := strings.Split(profilePath, "/")
	name := parts[0]

	if len(parts) > 1 && parts[1] == "apply" {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		profile := sim.ProfilesManager.Get(name)
		if profile == nil {
			http.Error(w, "Profile not found", http.StatusNotFound)
			return
		}

		for addr, value := range profile.Defaults {
			def := modbus.RegisterLookup[addr]
			if def != nil {
				sim.Server.WriteRegisterValue(def, value)
			}
		}
		sim.ProfilesManager.SetActive(name)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "applied", "profile": name})
		return
	}

	profile := sim.ProfilesManager.Get(name)
	if profile == nil {
		http.Error(w, "Profile not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(profile)
}

func handleAutomation(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	sim.Automation.mu.RLock()
	data := map[string]interface{}{
		"enabled":         sim.Automation.Enabled,
		"time_multiplier": sim.Automation.TimeMultiplier,
		"simulated_time":  sim.Automation.SimulatedTime,
		"profile":         sim.Automation.Profile,
		"scenario":        sim.Automation.Scenario,
	}
	sim.Automation.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func handleAutomationStart(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sim.Automation.mu.Lock()
	if !sim.Automation.Enabled {
		sim.Automation.Enabled = true
		sim.Automation.stopChan = make(chan struct{})
		sim.Automation.SimulatedTime = time.Now()
		go runAutomationLoop(sim)
	}
	sim.Automation.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func handleAutomationStop(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sim.Automation.mu.Lock()
	if sim.Automation.Enabled && sim.Automation.stopChan != nil {
		close(sim.Automation.stopChan)
		sim.Automation.Enabled = false
	}
	sim.Automation.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

func handleAutomationConfig(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var config struct {
		TimeMultiplier float64 `json:"time_multiplier"`
		Profile        string  `json:"profile"`
		Scenario       string  `json:"scenario"`
	}

	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	sim.Automation.mu.Lock()
	if config.TimeMultiplier > 0 {
		sim.Automation.TimeMultiplier = config.TimeMultiplier
	}
	if config.Profile != "" {
		sim.Automation.Profile = config.Profile
	}
	if config.Scenario != "" {
		sim.Automation.Scenario = config.Scenario
	}
	sim.Automation.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func handleReset(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sim.Server.InitDefaults()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "reset"})
}

func runAutomationLoop(sim *Simulator) {
	log.Printf("[%s] Automation started", sim.Serial)

	for {
		sim.Automation.mu.RLock()
		interval := time.Second
		multiplier := sim.Automation.TimeMultiplier
		stopChan := sim.Automation.stopChan
		sim.Automation.mu.RUnlock()

		select {
		case <-stopChan:
			log.Printf("[%s] Automation stopped", sim.Serial)
			return
		case <-time.After(interval):
			sim.Automation.mu.Lock()
			elapsed := time.Duration(float64(interval) * multiplier)
			sim.Automation.SimulatedTime = sim.Automation.SimulatedTime.Add(elapsed)
			simTime := sim.Automation.SimulatedTime
			sim.Automation.mu.Unlock()

			generateValues(sim, simTime)
		}
	}
}

func generateValues(sim *Simulator, simTime time.Time) {
	hour := float64(simTime.Hour()) + float64(simTime.Minute())/60.0

	sim.Automation.mu.RLock()
	scenario := sim.Automation.Scenario
	sim.Automation.mu.RUnlock()

	var solarPower, loadPower, batteryPower float64
	var soc float64 = 50

	switch scenario {
	case "sunny":
		// Perfect sunny day - max solar
		if hour >= 6 && hour <= 20 {
			progress := (hour - 6) / 14 * math.Pi
			solarPower = 5000 * math.Sin(progress)
		}
		loadPower = 800
		if hour >= 17 && hour <= 22 {
			loadPower = 2000
		}
		soc = 80

	case "cloudy":
		// Cloudy - reduced solar with variations
		if hour >= 7 && hour <= 19 {
			progress := (hour - 7) / 12 * math.Pi
			solarPower = 2500 * math.Sin(progress) * (0.5 + 0.5*math.Sin(hour*3))
		}
		loadPower = 1000
		soc = 60

	case "overcast":
		// Very cloudy - minimal solar
		if hour >= 8 && hour <= 18 {
			solarPower = 500 + 300*math.Sin(hour)
		}
		loadPower = 1200
		soc = 45

	case "rainy":
		// Rainy - almost no solar, higher load
		solarPower = 100
		loadPower = 1500
		soc = 30

	case "night":
		// Night mode - no solar, battery discharge
		solarPower = 0
		loadPower = 600
		batteryPower = -500 // discharging
		soc = 40

	case "peak_load":
		// High demand scenario
		if hour >= 6 && hour <= 20 {
			progress := (hour - 6) / 14 * math.Pi
			solarPower = 4000 * math.Sin(progress)
		}
		loadPower = 4500
		soc = 55

	case "battery_test":
		// Battery cycling test
		solarPower = 3000
		loadPower = 1500
		// Cycle battery based on hour
		cyclePos := math.Sin(hour * math.Pi / 6)
		if cyclePos > 0 {
			batteryPower = 2000 * cyclePos // charging
			soc = 50 + 40*cyclePos
		} else {
			batteryPower = 1500 * cyclePos // discharging
			soc = 50 + 40*cyclePos
		}

	default:
		// Default sunny behavior
		if hour >= 6 && hour <= 20 {
			progress := (hour - 6) / 14 * math.Pi
			solarPower = 5000 * math.Sin(progress)
		}
		loadPower = 1000
		soc = 70
	}

	// Update registers
	if pvDef := modbus.RegisterLookup[5016]; pvDef != nil {
		sim.Server.WriteRegisterValue(pvDef, solarPower)
	}
	if loadDef := modbus.RegisterLookup[13052]; loadDef != nil {
		sim.Server.WriteRegisterValue(loadDef, loadPower)
	}
	if socDef := modbus.RegisterLookup[13022]; socDef != nil {
		sim.Server.WriteRegisterValue(socDef, soc)
	}
	if battDef := modbus.RegisterLookup[13021]; battDef != nil {
		sim.Server.WriteRegisterValue(battDef, batteryPower)
	}

	meterPower := loadPower - solarPower + batteryPower
	if meterDef := modbus.RegisterLookup[5600]; meterDef != nil {
		sim.Server.WriteRegisterValue(meterDef, meterPower)
	}
}

func getRegisterValue(server *modbus.Server, address uint16) float64 {
	def := modbus.RegisterLookup[address]
	if def == nil {
		return 0
	}
	val, _ := server.ReadRegisterValue(def)
	return val
}
