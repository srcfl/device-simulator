package api

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srcfl/device-simulator/internal/modbus"
	"github.com/srcfl/device-simulator/internal/profiles"
)

// Server handles HTTP API requests
type Server struct {
	modbusServer    *modbus.Server
	mux             *http.ServeMux
	automation      *AutomationState
	profilesManager *profiles.Manager
}

// AutomationState holds the current automation configuration
type AutomationState struct {
	mu             sync.RWMutex
	Enabled        bool      `json:"enabled"`
	TimeMultiplier float64   `json:"time_multiplier"`
	SimulatedTime  time.Time `json:"simulated_time"`
	Profile        string    `json:"profile"`
	UpdateInterval int       `json:"update_interval_ms"`
	stopChan       chan struct{}
}

// WriteRequest is the request body for writing a register
type WriteRequest struct {
	Value float64 `json:"value"`
}

// NewServer creates a new API server
func NewServer(modbusServer *modbus.Server) *Server {
	pm := profiles.NewManager("profiles")
	pm.InitEmbedded()
	pm.LoadAll() // Load any JSON profiles from disk

	s := &Server{
		modbusServer:    modbusServer,
		mux:             http.NewServeMux(),
		profilesManager: pm,
		automation: &AutomationState{
			TimeMultiplier: 1.0,
			UpdateInterval: 1000,
			SimulatedTime:  time.Now(),
		},
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	// Register endpoints
	s.mux.HandleFunc("/api/registers", s.handleRegisters)
	s.mux.HandleFunc("/api/registers/", s.handleRegister)
	s.mux.HandleFunc("/api/reset", s.handleReset)

	// Logging endpoints
	s.mux.HandleFunc("/api/logs", s.handleLogs)

	// Profile endpoints
	s.mux.HandleFunc("/api/profiles", s.handleProfiles)
	s.mux.HandleFunc("/api/profiles/", s.handleProfile)

	// Automation endpoints
	s.mux.HandleFunc("/api/automation", s.handleAutomation)
	s.mux.HandleFunc("/api/automation/start", s.handleAutomationStart)
	s.mux.HandleFunc("/api/automation/stop", s.handleAutomationStop)
	s.mux.HandleFunc("/api/automation/config", s.handleAutomationConfig)

	// Status endpoint for landing page
	s.mux.HandleFunc("/api/status", s.handleStatus)

	// Health check
	s.mux.HandleFunc("/health", s.handleHealth)
}

// Handler returns the HTTP handler
func (s *Server) Handler() http.Handler {
	return corsMiddleware(s.mux)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleRegisters(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payloads []*modbus.RegisterPayload
	for _, def := range modbus.RegisterDefinitions {
		var regs []uint16
		if def.UseHolding {
			regs = s.modbusServer.GetHoldingRegisters(def.Address, def.Words)
		} else {
			regs = s.modbusServer.GetInputRegisters(def.Address, def.Words)
		}

		payload, err := def.ToPayload(regs)
		if err != nil {
			log.Printf("Error creating payload for %d: %v", def.Address, err)
			continue
		}
		payloads = append(payloads, payload)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payloads)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	// Extract address from URL path
	path := r.URL.Path
	addrStr := path[len("/api/registers/"):]
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
			regs = s.modbusServer.GetHoldingRegisters(def.Address, def.Words)
		} else {
			regs = s.modbusServer.GetInputRegisters(def.Address, def.Words)
		}

		payload, err := def.ToPayload(regs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload)

	case "POST":
		var req WriteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if err := s.modbusServer.WriteRegisterValue(def, req.Value); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if def.Address == 13050 {
			flags := 0.0
			switch int(req.Value) {
			case 170:
				flags = 2 // charging
			case 187:
				flags = 4 // discharging
			default:
				flags = 0
			}
			if flagsDef := modbus.RegisterLookup[13000]; flagsDef != nil {
				_ = s.modbusServer.WriteRegisterValue(flagsDef, flags)
			}
		}

		// Read back and return
		var regs []uint16
		if def.UseHolding {
			regs = s.modbusServer.GetHoldingRegisters(def.Address, def.Words)
		} else {
			regs = s.modbusServer.GetInputRegisters(def.Address, def.Words)
		}

		payload, err := def.ToPayload(regs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.modbusServer.InitDefaults()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "reset"})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	logs := s.modbusServer.GetLogs(limit)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": logs,
	})
}

func (s *Server) handleAutomation(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.automation.mu.RLock()
	defer s.automation.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.automation)
}

func (s *Server) handleAutomationStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.automation.mu.Lock()
	if !s.automation.Enabled {
		s.automation.Enabled = true
		s.automation.stopChan = make(chan struct{})
		s.automation.SimulatedTime = time.Now()
		go s.runAutomationLoop()
	}
	s.automation.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func (s *Server) handleAutomationStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.automation.mu.Lock()
	if s.automation.Enabled && s.automation.stopChan != nil {
		close(s.automation.stopChan)
		s.automation.Enabled = false
	}
	s.automation.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

func (s *Server) handleAutomationConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var config struct {
		TimeMultiplier float64 `json:"time_multiplier"`
		UpdateInterval int     `json:"update_interval_ms"`
		Profile        string  `json:"profile"`
	}

	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	s.automation.mu.Lock()
	if config.TimeMultiplier > 0 {
		s.automation.TimeMultiplier = config.TimeMultiplier
	}
	if config.UpdateInterval > 0 {
		s.automation.UpdateInterval = config.UpdateInterval
	}
	if config.Profile != "" {
		s.automation.Profile = config.Profile
	}
	s.automation.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Quick status for landing page
	status := make(map[string]interface{})

	// Read key registers
	keyRegisters := []uint16{5017, 13022, 13023, 5599, 13052}
	for _, addr := range keyRegisters {
		def := modbus.RegisterLookup[addr]
		if def == nil {
			continue
		}
		var regs []uint16
		if def.UseHolding {
			regs = s.modbusServer.GetHoldingRegisters(def.Address, def.Words)
		} else {
			regs = s.modbusServer.GetInputRegisters(def.Address, def.Words)
		}
		if val, err := modbus.DecodeValue(regs, def); err == nil {
			status[def.Name] = val
		}
	}

	// Add automation status
	s.automation.mu.RLock()
	status["automation_enabled"] = s.automation.Enabled
	status["time_multiplier"] = s.automation.TimeMultiplier
	s.automation.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	list := s.profilesManager.List()
	active := s.profilesManager.GetActiveName()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"profiles": list,
		"active":   active,
	})
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	// Extract profile name from URL path
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/profiles/")
	parts := strings.Split(path, "/")
	name := parts[0]

	if name == "" {
		http.Error(w, "Profile name required", http.StatusBadRequest)
		return
	}

	// Check for /apply action
	if len(parts) > 1 && parts[1] == "apply" {
		s.handleProfileApply(w, r, name)
		return
	}

	switch r.Method {
	case "GET":
		profile := s.profilesManager.Get(name)
		if profile == nil {
			http.Error(w, "Profile not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(profile)

	case "POST":
		var profile profiles.Profile
		if err := json.NewDecoder(r.Body).Decode(&profile); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		profile.Name = name
		if err := s.profilesManager.Save(&profile); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "saved"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleProfileApply(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	profile := s.profilesManager.Get(name)
	if profile == nil {
		http.Error(w, "Profile not found", http.StatusNotFound)
		return
	}

	// Apply defaults from profile
	for addr, value := range profile.Defaults {
		def := modbus.RegisterLookup[addr]
		if def != nil {
			s.modbusServer.WriteRegisterValue(def, value)
		}
	}

	s.profilesManager.SetActive(name)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "applied", "profile": name})
}

func (s *Server) runAutomationLoop() {
	log.Println("Automation loop started")

	for {
		s.automation.mu.RLock()
		interval := time.Duration(s.automation.UpdateInterval) * time.Millisecond
		multiplier := s.automation.TimeMultiplier
		stopChan := s.automation.stopChan
		s.automation.mu.RUnlock()

		select {
		case <-stopChan:
			log.Println("Automation loop stopped")
			return
		case <-time.After(interval):
			// Advance simulated time
			s.automation.mu.Lock()
			elapsed := time.Duration(float64(interval) * multiplier)
			s.automation.SimulatedTime = s.automation.SimulatedTime.Add(elapsed)
			simTime := s.automation.SimulatedTime
			s.automation.mu.Unlock()

			// Generate values based on simulated time
			s.generateValues(simTime)
		}
	}
}

func (s *Server) generateValues(simTime time.Time) {
	// Simple daynight curve for solar
	hour := float64(simTime.Hour()) + float64(simTime.Minute())/60.0

	// Solar: peaks at noon (12:00)
	var solarPower float64
	if hour >= 6 && hour <= 20 {
		// Sine curve from sunrise (6) to sunset (20)
		progress := (hour - 6) / 14 * math.Pi
		solarPower = 5000 * math.Sin(progress)
	}

	// Load: higher in morning and evening
	var loadPower float64 = 500 // Base load
	if (hour >= 7 && hour <= 9) || (hour >= 17 && hour <= 22) {
		loadPower = 2500
	}

	// Update PV register (5017)
	pvDef := modbus.RegisterLookup[5017]
	if pvDef != nil {
		s.modbusServer.WriteRegisterValue(pvDef, solarPower)
	}

	// Update Load register (13052)
	loadDef := modbus.RegisterLookup[13052]
	if loadDef != nil {
		s.modbusServer.WriteRegisterValue(loadDef, loadPower)
	}

	// Calculate meter power (energy balance)
	// Meter = Load - PV + Battery
	batteryPower := 0.0 // TODO: implement battery logic
	meterPower := loadPower - solarPower + batteryPower

	meterDef := modbus.RegisterLookup[5599]
	if meterDef != nil {
		s.modbusServer.WriteRegisterValue(meterDef, meterPower)
	}
}
