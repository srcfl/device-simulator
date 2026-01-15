package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/srcfl/device-simulator/internal/device"
	"github.com/srcfl/device-simulator/internal/modbus"
	"github.com/srcfl/device-simulator/internal/mqtt"
	"github.com/srcfl/device-simulator/internal/ocpp"
	"github.com/srcfl/device-simulator/internal/profiles"
)

//go:embed templates/*
var templatesFS embed.FS

// Simulator represents a single simulator instance
type Simulator struct {
	ID              int                   `json:"id"`
	Serial          string                `json:"serial"`
	Protocol        string                `json:"protocol"`
	Category        string                `json:"category"`
	SlaveID         uint8                 `json:"slave_id,omitempty"`
	Port            int                   `json:"port"`
	Running         bool                  `json:"running"`
	Server          *modbus.Server        `json:"-"`
	MQTTServer      *mqtt.Server          `json:"-"`
	OCPPServer      *ocpp.Server          `json:"-"`
	Device          device.DeviceServer   `json:"-"`
	ProfilesManager *profiles.Manager     `json:"-"`
	Automation      *AutomationState      `json:"-"`
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

// PortAllocator manages dynamic port allocation for simulators
type PortAllocator struct {
	mu             sync.Mutex
	modbusBase     int
	mqttBase       int
	ocppBase       int
	allocatedPorts map[int]bool
}

func NewPortAllocator(modbusBase, mqttBase, ocppBase int) *PortAllocator {
	return &PortAllocator{
		modbusBase:     modbusBase,
		mqttBase:       mqttBase,
		ocppBase:       ocppBase,
		allocatedPorts: make(map[int]bool),
	}
}

func (p *PortAllocator) Allocate(protocol string) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	var base int
	switch protocol {
	case "modbus":
		base = p.modbusBase
	case "mqtt":
		base = p.mqttBase
	case "ocpp":
		base = p.ocppBase
	default:
		return 0
	}

	// Find next available port starting from base
	for port := base; port < base+1000; port++ {
		if !p.allocatedPorts[port] {
			p.allocatedPorts[port] = true
			return port
		}
	}
	return 0 // No available port
}

func (p *PortAllocator) Release(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.allocatedPorts, port)
}

var (
	simulators      []*Simulator
	simulatorsMu    sync.RWMutex
	nextSimulatorID int
	nextIDMu        sync.Mutex
	portAllocator   *PortAllocator
)

func main() {
	// Base port configuration
	modbusBasePort := flag.Int("modbus-base", 5000, "Base port for Modbus servers")
	mqttBasePort := flag.Int("mqtt-base", 1883, "Base port for MQTT brokers")
	ocppBasePort := flag.Int("ocpp-base", 9000, "Base port for OCPP servers")
	httpPort := flag.Int("http", 8080, "HTTP server port")
	flag.Parse()

	// Environment variable overrides
	if env := os.Getenv("MODBUS_BASE_PORT"); env != "" {
		if n, err := strconv.Atoi(env); err == nil {
			*modbusBasePort = n
		}
	}
	if env := os.Getenv("MQTT_BASE_PORT"); env != "" {
		if n, err := strconv.Atoi(env); err == nil {
			*mqttBasePort = n
		}
	}
	if env := os.Getenv("OCPP_BASE_PORT"); env != "" {
		if n, err := strconv.Atoi(env); err == nil {
			*ocppBasePort = n
		}
	}
	if env := os.Getenv("HTTP_PORT"); env != "" {
		if n, err := strconv.Atoi(env); err == nil {
			*httpPort = n
		}
	}

	log.Printf("Starting Device Simulator")
	log.Printf("HTTP port: %d", *httpPort)
	log.Printf("Port ranges: Modbus %d+, MQTT %d+, OCPP %d+", *modbusBasePort, *mqttBasePort, *ocppBasePort)

	// Initialize port allocator
	portAllocator = NewPortAllocator(*modbusBasePort, *mqttBasePort, *ocppBasePort)

	// Initialize empty simulator list
	simulators = make([]*Simulator, 0)
	nextSimulatorID = 1

	// Start HTTP server
	go startHTTPServer(*httpPort)

	log.Println("Ready. Use the web UI to create simulators.")

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	simulatorsMu.RLock()
	for _, sim := range simulators {
		stopSimulatorServer(sim)
	}
	simulatorsMu.RUnlock()
}

// createSimulator creates a new simulator of the given type
func createSimulator(simType string) (*Simulator, error) {
	var protocol, category string
	switch simType {
	case "inverter":
		protocol = "modbus"
		category = "inverter"
	case "v2x_charger":
		protocol = "mqtt"
		category = "v2x_charger"
	case "ocpp_charger":
		protocol = "ocpp"
		category = "ocpp_charger"
	default:
		return nil, fmt.Errorf("unknown simulator type: %s", simType)
	}

	port := portAllocator.Allocate(protocol)
	if port == 0 {
		return nil, fmt.Errorf("no available port for %s", protocol)
	}

	nextIDMu.Lock()
	id := nextSimulatorID
	nextSimulatorID++
	nextIDMu.Unlock()

	pm := profiles.NewManager("profiles")
	pm.InitEmbedded()
	pm.LoadAll()

	// Set default profile for the protocol
	availableProfiles := pm.ListByProtocol(protocol)
	if len(availableProfiles) > 0 {
		pm.SetActive(availableProfiles[0].Name)
	}

	sim := &Simulator{
		ID:              id,
		Protocol:        protocol,
		Category:        category,
		Port:            port,
		Running:         false,
		ProfilesManager: pm,
		Automation: &AutomationState{
			TimeMultiplier: 1.0,
			SimulatedTime:  time.Now(),
			Scenario:       "sunny",
		},
	}

	// Create protocol-specific server
	switch protocol {
	case "modbus":
		serial := fmt.Sprintf("INVSIM%02d", id)
		sim.Serial = serial
		sim.SlaveID = 1
		server := modbus.NewServer(65535)
		server.SetSerialNumber(serial)
		server.InitDefaults()
		sim.Server = server

	case "mqtt":
		mqttServer := mqtt.NewServer(id, port)
		sim.Serial = mqttServer.SerialNumber()
		sim.MQTTServer = mqttServer
		sim.Device = mqttServer
		sim.Automation.Scenario = "idle"

	case "ocpp":
		ocppServer := ocpp.NewServer(id, port, "SimulatorCo", "SimCharger-22", 2, 22.0)
		sim.Serial = ocppServer.SerialNumber()
		sim.OCPPServer = ocppServer
		sim.Device = ocppServer
		sim.Automation.Scenario = "idle"
	}

	simulatorsMu.Lock()
	simulators = append(simulators, sim)
	simulatorsMu.Unlock()

	log.Printf("[%s] Created %s simulator on port %d", sim.Serial, simType, port)
	return sim, nil
}

// deleteSimulator removes a simulator
func deleteSimulator(id int) error {
	simulatorsMu.Lock()
	defer simulatorsMu.Unlock()

	for i, sim := range simulators {
		if sim.ID == id {
			// Stop server if running
			stopSimulatorServer(sim)
			// Release port
			portAllocator.Release(sim.Port)
			// Remove from slice
			simulators = append(simulators[:i], simulators[i+1:]...)
			log.Printf("[%s] Deleted simulator", sim.Serial)
			return nil
		}
	}
	return fmt.Errorf("simulator %d not found", id)
}

// startSimulatorServer starts the protocol server for a simulator
func startSimulatorServer(sim *Simulator) error {
	sim.mu.Lock()
	defer sim.mu.Unlock()

	if sim.Running {
		return nil // Already running
	}

	var err error
	switch sim.Protocol {
	case "modbus":
		addr := fmt.Sprintf(":%d", sim.Port)
		if err = sim.Server.Listen(addr); err != nil {
			return fmt.Errorf("modbus listen error: %v", err)
		}
		go func() {
			if serveErr := sim.Server.Serve(); serveErr != nil {
				log.Printf("[%s] Modbus serve error: %v", sim.Serial, serveErr)
			}
		}()
		log.Printf("[%s] Started Modbus server on %s", sim.Serial, addr)

	case "mqtt":
		if err = sim.MQTTServer.Start(); err != nil {
			return fmt.Errorf("mqtt start error: %v", err)
		}
		log.Printf("[%s] Started MQTT broker on port %d", sim.Serial, sim.Port)

	case "ocpp":
		if err = sim.OCPPServer.Start(); err != nil {
			return fmt.Errorf("ocpp start error: %v", err)
		}
		log.Printf("[%s] Started OCPP server on port %d", sim.Serial, sim.Port)
	}

	sim.Running = true
	return nil
}

// stopSimulatorServer stops the protocol server for a simulator
func stopSimulatorServer(sim *Simulator) {
	sim.mu.Lock()
	defer sim.mu.Unlock()

	if !sim.Running {
		return
	}

	// Stop automation if running
	if sim.Automation.Enabled && sim.Automation.stopChan != nil {
		close(sim.Automation.stopChan)
		sim.Automation.Enabled = false
	}

	switch sim.Protocol {
	case "modbus":
		sim.Server.Close()
		log.Printf("[%s] Stopped Modbus server", sim.Serial)
	case "mqtt":
		sim.MQTTServer.Stop()
		log.Printf("[%s] Stopped MQTT broker", sim.Serial)
	case "ocpp":
		sim.OCPPServer.Stop()
		log.Printf("[%s] Stopped OCPP server", sim.Serial)
	}

	sim.Running = false
}

func startHTTPServer(port int) {
	mux := http.NewServeMux()

	// Dashboard
	mux.HandleFunc("/", handleDashboard)

	// System info
	mux.HandleFunc("/api/system", handleSystemInfo)

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

// getLocalIP returns the local IP address (from HOST_IP env var or auto-detected)
func getLocalIP() string {
	if hostIP := os.Getenv("HOST_IP"); hostIP != "" {
		return hostIP
	}

	// Try to detect the outbound IP by making a UDP "connection" to an external address
	// This doesn't actually send any packets, just determines which local IP would be used
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		if localAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			return localAddr.IP.String()
		}
	}

	// Fallback to detecting local interface IP
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}

func handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Try to get the host from the request (what the browser actually used)
	host := r.Host
	// Remove port if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Use request host if it's an IP, otherwise fall back to getLocalIP()
	ip := host
	if net.ParseIP(host) == nil {
		// Not a valid IP (probably "localhost"), use detected IP
		ip = getLocalIP()
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"localIP": ip,
	})
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
	switch r.Method {
	case "GET":
		handleSimulatorsListGet(w, r)
	case "POST":
		handleSimulatorsCreate(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleSimulatorsCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	sim, err := createSimulator(req.Type)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":       sim.ID,
		"serial":   sim.Serial,
		"protocol": sim.Protocol,
		"category": sim.Category,
		"port":     sim.Port,
		"running":  sim.Running,
	})
}

func handleSimulatorsListGet(w http.ResponseWriter, r *http.Request) {
	// Optional filtering by protocol or category
	protocolFilter := r.URL.Query().Get("protocol")
	categoryFilter := r.URL.Query().Get("category")

	simulatorsMu.RLock()
	var list []map[string]interface{}
	for _, sim := range simulators {
		// Apply filters
		if protocolFilter != "" && sim.Protocol != protocolFilter {
			continue
		}
		if categoryFilter != "" && sim.Category != categoryFilter {
			continue
		}

		sim.mu.RLock()
		entry := map[string]interface{}{
			"id":         sim.ID,
			"serial":     sim.Serial,
			"protocol":   sim.Protocol,
			"category":   sim.Category,
			"port":       sim.Port,
			"running":    sim.Running,
			"automation": sim.Automation.Enabled,
			"profile":    sim.ProfilesManager.GetActiveName(),
		}

		// Add protocol-specific fields
		switch sim.Protocol {
		case "modbus":
			entry["slave_id"] = sim.SlaveID
			entry["pv_power"] = getRegisterValue(sim.Server, 5016)
			entry["soc"] = getRegisterValue(sim.Server, 13022)
			entry["load"] = getRegisterValue(sim.Server, 13007)
			entry["meter"] = getRegisterValue(sim.Server, 13009)
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
		}

		sim.mu.RUnlock()
		list = append(list, entry)
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

	// Handle DELETE before checking if simulator exists (for idempotency)
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	// DELETE /api/simulators/{id}
	if r.Method == "DELETE" && action == "" {
		if err := deleteSimulator(simID); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
		return
	}

	sim := getSimulator(simID)
	if sim == nil {
		http.Error(w, "Simulator not found", http.StatusNotFound)
		return
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
	case action == "state":
		handleState(w, r, sim)
	case action == "clients":
		handleClients(w, r, sim)
	case action == "wakeup":
		handleWakeUp(w, r, sim)
	case action == "power":
		handlePower(w, r, sim)
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
		"id":       sim.ID,
		"serial":   sim.Serial,
		"protocol": sim.Protocol,
		"category": sim.Category,
		"port":     sim.Port,
		"running":  sim.Running,
		"automation": map[string]interface{}{
			"enabled":         sim.Automation.Enabled,
			"time_multiplier": sim.Automation.TimeMultiplier,
			"simulated_time":  sim.Automation.SimulatedTime,
			"scenario":        sim.Automation.Scenario,
		},
	}

	// Add protocol-specific status
	switch sim.Protocol {
	case "modbus":
		status["slave_id"] = sim.SlaveID
		status["pv_power"] = getRegisterValue(sim.Server, 5016)
		status["soc"] = getRegisterValue(sim.Server, 13022)
		status["load"] = getRegisterValue(sim.Server, 13007)
		status["meter"] = getRegisterValue(sim.Server, 13009)
		status["frequency"] = getRegisterValue(sim.Server, 5241)
	case "mqtt":
		status["state"] = sim.MQTTServer.GetState()
	case "ocpp":
		status["state"] = sim.OCPPServer.GetState()
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
	if config.SlaveID != nil && sim.Protocol == "modbus" {
		sim.SlaveID = *config.SlaveID
		log.Printf("[%s] Slave ID changed to %d", sim.Serial, sim.SlaveID)
	}
	if config.Serial != nil {
		oldSerial := sim.Serial
		sim.Serial = *config.Serial
		// Update protocol-specific serial (takes effect immediately for stopped servers,
		// or after restart for running servers)
		switch sim.Protocol {
		case "modbus":
			if sim.Server != nil {
				sim.Server.SetSerialNumber(*config.Serial)
			}
		case "mqtt":
			if sim.MQTTServer != nil {
				sim.MQTTServer.SetSerialNumber(*config.Serial)
			}
		case "ocpp":
			if sim.OCPPServer != nil {
				sim.OCPPServer.SetSerialNumber(*config.Serial)
			}
		}
		log.Printf("[%s] Serial changed from %s", sim.Serial, oldSerial)
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

	var logs interface{}
	switch sim.Protocol {
	case "modbus":
		logs = sim.Server.GetLogs(limit)
	case "mqtt":
		logs = sim.MQTTServer.GetLogs(limit)
	case "ocpp":
		logs = sim.OCPPServer.GetLogs(limit)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"logs": logs})
}

func handleProfiles(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	// Filter profiles by the simulator's protocol
	list := sim.ProfilesManager.ListByProtocol(sim.Protocol)
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

	// Start the server first
	if err := startSimulatorServer(sim); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Then start automation
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

	// Stop automation first
	sim.Automation.mu.Lock()
	if sim.Automation.Enabled && sim.Automation.stopChan != nil {
		close(sim.Automation.stopChan)
		sim.Automation.Enabled = false
	}
	sim.Automation.mu.Unlock()

	// Then stop the server
	stopSimulatorServer(sim)

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

func handleState(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Key   string      `json:"key"`
		Value interface{} `json:"value"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var err error
	switch sim.Protocol {
	case "mqtt":
		err = sim.MQTTServer.SetValue(req.Key, req.Value)
	case "ocpp":
		err = sim.OCPPServer.SetValue(req.Key, req.Value)
	default:
		http.Error(w, "State changes not supported for this protocol", http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func handleClients(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if sim.Protocol != "mqtt" {
		http.Error(w, "Clients endpoint only available for MQTT devices", http.StatusBadRequest)
		return
	}

	clients := sim.MQTTServer.GetClients()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"clients": clients,
		"count":   len(clients),
	})
}

func handleWakeUp(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if sim.Protocol != "mqtt" {
		http.Error(w, "WakeUp endpoint only available for MQTT devices", http.StatusBadRequest)
		return
	}

	var req struct {
		Wake bool `json:"wake"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	sim.MQTTServer.WakeUp(req.Wake)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "ok",
		"session_state": sim.MQTTServer.GetState()["sessionState"],
	})
}

func handlePower(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if sim.Protocol != "mqtt" {
		http.Error(w, "Power endpoint only available for MQTT devices", http.StatusBadRequest)
		return
	}

	var req struct {
		Power float64 `json:"power"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	sim.MQTTServer.SetTargetPower(req.Power)

	state := sim.MQTTServer.GetState()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "ok",
		"power_ac": state["powerAc"],
	})
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
	// Handle different protocols
	switch sim.Protocol {
	case "mqtt":
		if sim.MQTTServer != nil {
			sim.MQTTServer.GenerateValues(simTime)
		}
		return
	case "ocpp":
		if sim.OCPPServer != nil {
			sim.OCPPServer.GenerateValues(simTime)
		}
		return
	}

	// Modbus inverter simulation
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
