package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
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

//go:embed templates/* static/*
var embeddedFS embed.FS

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

// P1UpdateMode represents the update interval mode for P1 port simulation
type P1UpdateMode string

const (
	P1ModeDirect P1UpdateMode = "direct" // Updates every 500ms (default)
	P1Mode1s     P1UpdateMode = "1s"     // Updates every 1 second with averaging
	P1Mode10s    P1UpdateMode = "10s"    // Updates every 10 seconds with averaging
)

// PhasePowers represents power values per phase
type PhasePowers struct {
	L1 float64 `json:"l1"` // Phase 1 power in Watts
	L2 float64 `json:"l2"` // Phase 2 power in Watts
	L3 float64 `json:"l3"` // Phase 3 power in Watts
}

// P1IntervalBuffer holds state for interval-based P1 port updates
type P1IntervalBuffer struct {
	Mode              P1UpdateMode
	LastPower         float64     // Last instant power value
	LastPhasePowers   PhasePowers // Last instant per-phase power values
	ImportEnergyDelta float64     // Accumulated import energy during interval (kWh)
	ExportEnergyDelta float64     // Accumulated export energy during interval (kWh)
	LastUpdateTime    time.Time   // When registers were last written
	IntervalStart     time.Time   // Start of current interval
}

// PhaseFactors represents the load distribution across three phases
type PhaseFactors struct {
	L1 float64 `json:"l1"` // Phase 1 factor (0.0-1.0)
	L2 float64 `json:"l2"` // Phase 2 factor (0.0-1.0)
	L3 float64 `json:"l3"` // Phase 3 factor (0.0-1.0)
}

// PhaseCurrents represents the calculated current per phase
type PhaseCurrents struct {
	L1 float64 `json:"l1"` // Phase 1 current in Amps
	L2 float64 `json:"l2"` // Phase 2 current in Amps
	L3 float64 `json:"l3"` // Phase 3 current in Amps
}

// Site represents a group of simulators with aggregated energy metering
type Site struct {
	ID           int          `json:"id"`
	Name         string       `json:"name"`
	MeterID      int          `json:"meter_id"`      // Simulator ID acting as site meter
	SimulatorIDs []int        `json:"simulator_ids"` // Member simulators
	BaseLoadW    float64      `json:"base_load_w"`   // Configurable 0-10kW
	Enabled      bool         `json:"enabled"`       // Aggregation enabled
	FuseSize     int          `json:"fuse_size"`     // Fuse size in Amps (10, 16, 20, 25, 32)
	PhaseFactors PhaseFactors `json:"phase_factors"` // Per-phase load distribution
	BoundMeter   bool         `json:"bound_meter"`   // True if meter was auto-created with site
	P1Buffer     *P1IntervalBuffer                   // P1 port interval buffer
	mu           sync.RWMutex
	stopChan     chan struct{}
}

var (
	simulators      []*Simulator
	simulatorsMu    sync.RWMutex
	nextSimulatorID int
	nextIDMu        sync.Mutex
	portAllocator   *PortAllocator

	// Site management
	sites      []*Site
	sitesMu    sync.RWMutex
	nextSiteID int = 1
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
	case "energy_meter":
		protocol = "modbus"
		category = "energy_meter"
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

	// Get available profiles for this protocol (used by OCPP for default config)
	availableProfiles := pm.ListByProtocol(protocol)

	// Set default profile based on device type
	if category == "energy_meter" {
		// Energy meters use the sdm630 profile (native gateway support)
		pm.SetActive("sdm630")
	} else if len(availableProfiles) > 0 {
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
		var serial string
		if category == "energy_meter" {
			// Use numeric serial for meters (fits in SDM630's U32 register at 0xFC00)
			serial = fmt.Sprintf("%d", 1000000+id)
		} else {
			serial = fmt.Sprintf("INVSIM%02d", id)
		}
		sim.Serial = serial
		sim.SlaveID = 1
		server := modbus.NewServer(65535)
		server.SetSerialNumber(serial)
		server.InitDefaults()
		sim.Server = server

		// Apply profile defaults for energy meters
		if category == "energy_meter" {
			if profile := pm.GetActive(); profile != nil {
				for addr, value := range profile.Defaults {
					if def := modbus.RegisterLookup[addr]; def != nil {
						server.WriteRegisterValue(def, value)
					}
				}
			}
		}

	case "mqtt":
		mqttServer := mqtt.NewServer(id, port)
		sim.Serial = mqttServer.SerialNumber()
		sim.MQTTServer = mqttServer
		sim.Device = mqttServer
		sim.Automation.Scenario = "idle"

	case "ocpp":
		// Get default values from first available OCPP profile
		vendor := "SimulatorCo"
		model := "SimCharger-22"
		numConnectors := 2
		maxPowerKW := 22.0

		if len(availableProfiles) > 0 {
			if profile := pm.Get(availableProfiles[0].Name); profile != nil && profile.OCPPConfig != nil {
				vendor = profile.OCPPConfig.Vendor
				model = profile.OCPPConfig.Model
				numConnectors = profile.OCPPConfig.NumConnectors
				maxPowerKW = profile.OCPPConfig.MaxPowerKW
			}
		}

		ocppServer := ocpp.NewServer(id, port, vendor, model, numConnectors, maxPowerKW)
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
// isSimulatorBoundMeter checks if a simulator is a bound meter for any site
func isSimulatorBoundMeter(simID int) bool {
	sitesMu.RLock()
	defer sitesMu.RUnlock()

	for _, site := range sites {
		site.mu.RLock()
		isBound := site.BoundMeter && site.MeterID == simID
		site.mu.RUnlock()
		if isBound {
			return true
		}
	}
	return false
}

func deleteSimulator(id int) error {
	// Check if this is a bound meter - cannot delete
	if isSimulatorBoundMeter(id) {
		return fmt.Errorf("cannot delete bound meter - delete the site instead")
	}

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
			// Remove from any sites (do this after releasing simulatorsMu)
			go removeSimulatorFromSites(id)
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

	// Serve static files (CSS, JS)
	staticFS, _ := fs.Sub(embeddedFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Dashboard
	mux.HandleFunc("/", handleDashboard)

	// System info
	mux.HandleFunc("/api/system", handleSystemInfo)

	// Simulator list
	mux.HandleFunc("/api/simulators", handleSimulatorsList)

	// Simulator-specific endpoints
	mux.HandleFunc("/api/simulators/", handleSimulatorAPI)

	// Site endpoints
	mux.HandleFunc("/api/sites", handleSitesList)
	mux.HandleFunc("/api/sites/", handleSiteAPI)

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

	tmpl, err := template.ParseFS(embeddedFS, "templates/index.html")
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
			// Use register 5600 (Meter Power) for energy meters, standard registers for inverters
			if sim.Category == "energy_meter" {
				entry["meter"] = getRegisterValue(sim.Server, 5600)
			} else {
				entry["pv_power"] = getRegisterValue(sim.Server, 5016)
				entry["soc"] = getRegisterValue(sim.Server, 13022)
				entry["load"] = getRegisterValue(sim.Server, 13007)
				entry["meter"] = getRegisterValue(sim.Server, 5600)
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
	case action == "start":
		handleEmulatorStart(w, r, sim)
	case action == "stop":
		handleEmulatorStop(w, r, sim)
	case action == "simulation/start":
		handleSimulationStart(w, r, sim)
	case action == "simulation/stop":
		handleSimulationStop(w, r, sim)
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
	case action == "debug/meter-values":
		handleDebugMeterValues(w, r, sim)
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
		state := sim.OCPPServer.GetState()
		status["state"] = state
		status["ocpp_url"] = sim.OCPPServer.GetOCPPURL()
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
		OCPPURL *string `json:"ocpp_url"`
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
	if config.OCPPURL != nil && sim.Protocol == "ocpp" {
		if sim.OCPPServer != nil {
			sim.OCPPServer.SetOCPPURL(*config.OCPPURL)
			log.Printf("[%s] OCPP URL set to %s", sim.Serial, *config.OCPPURL)
		}
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
	// Filter profiles by the simulator's protocol and category
	var list []profiles.ProfileInfo
	if sim.Category == "energy_meter" {
		// Energy meters only show meter-specific profiles
		list = sim.ProfilesManager.ListByProtocolAndCategory(sim.Protocol, "meter")
	} else {
		list = sim.ProfilesManager.ListByProtocol(sim.Protocol)
	}
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

		// Apply profile based on protocol
		switch sim.Protocol {
		case "modbus":
			for addr, value := range profile.Defaults {
				def := modbus.RegisterLookup[addr]
				if def != nil {
					sim.Server.WriteRegisterValue(def, value)
				}
			}
		case "ocpp":
			if profile.OCPPConfig != nil && sim.OCPPServer != nil {
				sim.OCPPServer.SetVendorModel(profile.OCPPConfig.Vendor, profile.OCPPConfig.Model)
				log.Printf("[%s] Applied OCPP profile: %s (%s)", sim.Serial, profile.OCPPConfig.Model, profile.OCPPConfig.Vendor)
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

// handleEmulatorStart starts only the protocol server (emulator), not the simulation
func handleEmulatorStart(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := startSimulatorServer(sim); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

// handleEmulatorStop stops the protocol server (and simulation if running)
func handleEmulatorStop(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Stop simulation first if running
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

// handleSimulationStart starts data generation (requires server to be running)
func handleSimulationStart(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if server is running
	sim.mu.RLock()
	running := sim.Running
	sim.mu.RUnlock()

	if !running {
		http.Error(w, "Emulator must be online before starting simulation", http.StatusBadRequest)
		return
	}

	// Start simulation
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

// handleSimulationStop stops data generation (server keeps running)
func handleSimulationStop(w http.ResponseWriter, r *http.Request, sim *Simulator) {
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
	var clients interface{}
	var count int

	switch sim.Protocol {
	case "mqtt":
		mqttClients := sim.MQTTServer.GetClients()
		clients = mqttClients
		count = len(mqttClients)
	case "modbus":
		modbusClients := sim.Server.GetClients()
		clients = modbusClients
		count = len(modbusClients)
	default:
		http.Error(w, "Clients endpoint not available for this protocol", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"clients": clients,
		"count":   count,
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

func handleDebugMeterValues(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if sim.Protocol != "ocpp" {
		http.Error(w, "Debug meter-values endpoint only available for OCPP chargers", http.StatusBadRequest)
		return
	}

	debug := sim.OCPPServer.GetMeterValuesDebug()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(debug)
}

// Site API handlers

func handleSitesList(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		handleSitesListGet(w, r)
	case "POST":
		handleSitesCreate(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleSitesListGet(w http.ResponseWriter, r *http.Request) {
	sitesMu.RLock()
	var list []map[string]interface{}
	for _, site := range sites {
		site.mu.RLock()
		p1Mode := P1ModeDirect
		if site.P1Buffer != nil {
			p1Mode = site.P1Buffer.Mode
		}
		entry := map[string]interface{}{
			"id":            site.ID,
			"name":          site.Name,
			"meter_id":      site.MeterID,
			"simulator_ids": site.SimulatorIDs,
			"base_load_w":   site.BaseLoadW,
			"enabled":       site.Enabled,
			"p1_mode":       string(p1Mode),
			"bound_meter":   site.BoundMeter,
		}

		// Add current meter reading if meter is valid
		if meterSim := getSimulator(site.MeterID); meterSim != nil && meterSim.Protocol == "modbus" && meterSim.Server != nil {
			entry["meter_power"] = getRegisterValue(meterSim.Server, 5600)
		}

		site.mu.RUnlock()
		list = append(list, entry)
	}
	sitesMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func handleSitesCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string  `json:"name"`
		MeterID      int     `json:"meter_id"`
		SimulatorIDs []int   `json:"simulator_ids"`
		BaseLoadW    float64 `json:"base_load_w"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		req.Name = fmt.Sprintf("Site %d", nextSiteID)
	}

	site, err := createSite(req.Name, req.MeterID, req.SimulatorIDs, req.BaseLoadW)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":            site.ID,
		"name":          site.Name,
		"meter_id":      site.MeterID,
		"simulator_ids": site.SimulatorIDs,
		"base_load_w":   site.BaseLoadW,
		"enabled":       site.Enabled,
	})
}

func handleSiteAPI(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/sites/{id}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/api/sites/")
	parts := strings.SplitN(path, "/", 2)

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Site ID required", http.StatusBadRequest)
		return
	}

	siteID, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "Invalid site ID", http.StatusBadRequest)
		return
	}

	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	// DELETE /api/sites/{id}
	if r.Method == "DELETE" && action == "" {
		if err := deleteSite(siteID); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
		return
	}

	site := getSite(siteID)
	if site == nil {
		http.Error(w, "Site not found", http.StatusNotFound)
		return
	}

	switch {
	case action == "" || action == "status":
		handleSiteStatus(w, r, site)
	case action == "config":
		handleSiteConfig(w, r, site)
	case action == "meter":
		handleSiteMeter(w, r, site)
	case action == "simulators":
		handleSiteSimulators(w, r, site)
	case strings.HasPrefix(action, "simulators/"):
		simIDStr := strings.TrimPrefix(action, "simulators/")
		handleSiteSimulator(w, r, site, simIDStr)
	case action == "enable":
		handleSiteEnable(w, r, site)
	case action == "disable":
		handleSiteDisable(w, r, site)
	case action == "p1-mode":
		handleSiteP1Mode(w, r, site)
	case action == "virtual-devices":
		handleSiteVirtualDevices(w, r, site)
	case strings.HasPrefix(action, "virtual-devices/"):
		deviceType := strings.TrimPrefix(action, "virtual-devices/")
		handleSiteVirtualDevice(w, r, site, deviceType)
	default:
		http.Error(w, "Unknown action", http.StatusNotFound)
	}
}

func handleSiteStatus(w http.ResponseWriter, r *http.Request, site *Site) {
	site.mu.RLock()
	p1Mode := P1ModeDirect
	if site.P1Buffer != nil {
		p1Mode = site.P1Buffer.Mode
	}
	status := map[string]interface{}{
		"id":            site.ID,
		"name":          site.Name,
		"meter_id":      site.MeterID,
		"simulator_ids": site.SimulatorIDs,
		"base_load_w":   site.BaseLoadW,
		"enabled":       site.Enabled,
		"p1_mode":       string(p1Mode),
		"fuse_size":     site.FuseSize,
		"phase_factors": site.PhaseFactors,
		"bound_meter":   site.BoundMeter,
	}

	// Add current meter reading if meter is valid
	meterSim := getSimulator(site.MeterID)
	if meterSim != nil && meterSim.Protocol == "modbus" && meterSim.Server != nil {
		status["meter_power"] = getRegisterValue(meterSim.Server, 5600)

		// Add per-phase currents from meter registers
		status["phase_currents"] = PhaseCurrents{
			L1: getRegisterValue(meterSim.Server, 6),
			L2: getRegisterValue(meterSim.Server, 8),
			L3: getRegisterValue(meterSim.Server, 10),
		}

		// Include virtual devices if meter is an inverter
		if meterSim.Category == "inverter" && meterSim.Running {
			pvPower := getRegisterValue(meterSim.Server, 5016)
			batteryPower := getRegisterValue(meterSim.Server, 13021)
			batterySoc := getRegisterValue(meterSim.Server, 13022)
			batteryCmd := getRegisterValue(meterSim.Server, 13050)

			batteryDirection := "idle"
			if batteryCmd == 170 {
				batteryDirection = "charging"
			} else if batteryCmd == 187 {
				batteryDirection = "discharging"
			}

			status["virtual_devices"] = []map[string]interface{}{
				{"type": "pv", "power": pvPower},
				{"type": "battery", "power": batteryPower, "soc": batterySoc, "direction": batteryDirection},
			}
		}
	}

	// Add power contributions from each simulator
	powerContributions := make(map[int]float64)
	for _, simID := range site.SimulatorIDs {
		if simID == site.MeterID {
			continue // Skip meter
		}
		sim := getSimulator(simID)
		if sim != nil {
			powerContributions[simID] = getPowerContribution(sim)
		}
	}
	status["power_contributions"] = powerContributions
	status["total_power"] = site.BaseLoadW + sumPowerContributions(powerContributions)

	site.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func sumPowerContributions(contributions map[int]float64) float64 {
	var total float64
	for _, power := range contributions {
		total += power
	}
	return total
}

// newP1IntervalBuffer creates a new P1IntervalBuffer with default direct mode
func newP1IntervalBuffer() *P1IntervalBuffer {
	return &P1IntervalBuffer{
		Mode:           P1ModeDirect,
		LastUpdateTime: time.Now(),
		IntervalStart:  time.Now(),
	}
}

// getP1IntervalDuration returns the interval duration for a given P1 mode
func getP1IntervalDuration(mode P1UpdateMode) time.Duration {
	switch mode {
	case P1Mode1s:
		return time.Second
	case P1Mode10s:
		return 10 * time.Second
	default:
		return 0 // Direct mode has no interval
	}
}

// resetEnergy clears the accumulated energy and resets the interval start time
func (buf *P1IntervalBuffer) resetEnergy() {
	buf.ImportEnergyDelta = 0
	buf.ExportEnergyDelta = 0
	buf.IntervalStart = time.Now()
}

func handleSiteConfig(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var config struct {
		Name         *string       `json:"name"`
		BaseLoadW    *float64      `json:"base_load_w"`
		FuseSize     *int          `json:"fuse_size"`
		PhaseFactors *PhaseFactors `json:"phase_factors"`
	}

	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	site.mu.Lock()
	if config.Name != nil {
		site.Name = *config.Name
	}
	if config.BaseLoadW != nil {
		if *config.BaseLoadW < 0 || *config.BaseLoadW > 10000 {
			site.mu.Unlock()
			http.Error(w, "Base load must be between 0 and 10000 W", http.StatusBadRequest)
			return
		}
		site.BaseLoadW = *config.BaseLoadW
	}
	if config.FuseSize != nil {
		// Validate fuse size (common values: 10, 16, 20, 25, 32 Amps)
		validFuses := map[int]bool{10: true, 16: true, 20: true, 25: true, 32: true}
		if !validFuses[*config.FuseSize] {
			site.mu.Unlock()
			http.Error(w, "Fuse size must be 10, 16, 20, 25, or 32 Amps", http.StatusBadRequest)
			return
		}
		site.FuseSize = *config.FuseSize
	}
	if config.PhaseFactors != nil {
		// Validate phase factors sum to approximately 1.0
		sum := config.PhaseFactors.L1 + config.PhaseFactors.L2 + config.PhaseFactors.L3
		if sum < 0.99 || sum > 1.01 {
			site.mu.Unlock()
			http.Error(w, "Phase factors must sum to 1.0", http.StatusBadRequest)
			return
		}
		// Validate individual factors are in valid range
		if config.PhaseFactors.L1 < 0 || config.PhaseFactors.L1 > 1 ||
			config.PhaseFactors.L2 < 0 || config.PhaseFactors.L2 > 1 ||
			config.PhaseFactors.L3 < 0 || config.PhaseFactors.L3 > 1 {
			site.mu.Unlock()
			http.Error(w, "Each phase factor must be between 0 and 1", http.StatusBadRequest)
			return
		}
		site.PhaseFactors = *config.PhaseFactors
	}
	site.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func handleSiteMeter(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Block changing meter for sites with bound meters
	site.mu.RLock()
	if site.BoundMeter {
		site.mu.RUnlock()
		http.Error(w, "Cannot change meter for site with bound meter", http.StatusForbidden)
		return
	}
	site.mu.RUnlock()

	var req struct {
		MeterID int `json:"meter_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate meter exists and is Modbus
	meterSim := getSimulator(req.MeterID)
	if meterSim == nil {
		http.Error(w, "Meter simulator not found", http.StatusBadRequest)
		return
	}
	if meterSim.Protocol != "modbus" {
		http.Error(w, "Meter must be a Modbus simulator", http.StatusBadRequest)
		return
	}

	site.mu.Lock()
	site.MeterID = req.MeterID
	site.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func handleSiteSimulators(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SimulatorID int `json:"simulator_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate simulator exists
	sim := getSimulator(req.SimulatorID)
	if sim == nil {
		http.Error(w, "Simulator not found", http.StatusBadRequest)
		return
	}

	// Check if already in this site
	site.mu.Lock()
	for _, id := range site.SimulatorIDs {
		if id == req.SimulatorID {
			site.mu.Unlock()
			http.Error(w, "Simulator already in site", http.StatusBadRequest)
			return
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
			if id == req.SimulatorID {
				otherSite.mu.RUnlock()
				sitesMu.RUnlock()
				site.mu.Unlock()
				http.Error(w, fmt.Sprintf("Simulator already in site %d", otherSite.ID), http.StatusBadRequest)
				return
			}
		}
		otherSite.mu.RUnlock()
	}
	sitesMu.RUnlock()

	site.SimulatorIDs = append(site.SimulatorIDs, req.SimulatorID)
	site.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "added"})
}

func handleSiteSimulator(w http.ResponseWriter, r *http.Request, site *Site, simIDStr string) {
	if r.Method != "DELETE" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	simID, err := strconv.Atoi(simIDStr)
	if err != nil {
		http.Error(w, "Invalid simulator ID", http.StatusBadRequest)
		return
	}

	site.mu.Lock()
	found := false
	newSimIDs := make([]int, 0, len(site.SimulatorIDs))
	for _, id := range site.SimulatorIDs {
		if id == simID {
			found = true
		} else {
			newSimIDs = append(newSimIDs, id)
		}
	}

	if !found {
		site.mu.Unlock()
		http.Error(w, "Simulator not in site", http.StatusNotFound)
		return
	}

	site.SimulatorIDs = newSimIDs
	site.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
}

func handleSiteEnable(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate meter is set and running
	site.mu.RLock()
	meterID := site.MeterID
	site.mu.RUnlock()

	if meterID == 0 {
		http.Error(w, "No meter configured for site", http.StatusBadRequest)
		return
	}

	meterSim := getSimulator(meterID)
	if meterSim == nil || meterSim.Protocol != "modbus" {
		http.Error(w, "Invalid meter simulator", http.StatusBadRequest)
		return
	}

	startSiteAggregation(site)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "enabled"})
}

func handleSiteDisable(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stopSiteAggregation(site)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "disabled"})
}

func handleSiteP1Mode(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Mode string `json:"mode"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate mode
	var mode P1UpdateMode
	switch req.Mode {
	case "direct":
		mode = P1ModeDirect
	case "1s":
		mode = P1Mode1s
	case "10s":
		mode = P1Mode10s
	default:
		http.Error(w, "Invalid mode. Must be 'direct', '1s', or '10s'", http.StatusBadRequest)
		return
	}

	site.mu.Lock()
	if site.P1Buffer == nil {
		site.P1Buffer = newP1IntervalBuffer()
	}
	site.P1Buffer.Mode = mode
	site.P1Buffer.resetEnergy() // Reset energy accumulator when mode changes
	site.mu.Unlock()

	log.Printf("[Site %d] P1 mode changed to %s", site.ID, mode)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated", "mode": string(mode)})
}

// handleSiteVirtualDevices returns virtual devices info when meter is an inverter
func handleSiteVirtualDevices(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	site.mu.RLock()
	meterID := site.MeterID
	site.mu.RUnlock()

	meterSim := getSimulator(meterID)
	if meterSim == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"virtual_devices": []interface{}{}})
		return
	}

	meterSim.mu.RLock()
	category := meterSim.Category
	running := meterSim.Running
	server := meterSim.Server
	meterSim.mu.RUnlock()

	if category != "inverter" || !running || server == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"virtual_devices": []interface{}{}})
		return
	}

	// Read values from inverter
	pvPower := getRegisterValue(server, 5016)
	batteryPower := getRegisterValue(server, 13021)
	batterySoc := getRegisterValue(server, 13022)
	batteryCmd := getRegisterValue(server, 13050)

	// Determine battery direction
	batteryDirection := "idle"
	if batteryCmd == 170 {
		batteryDirection = "charging"
	} else if batteryCmd == 187 {
		batteryDirection = "discharging"
	}

	virtualDevices := []map[string]interface{}{
		{
			"type":  "pv",
			"power": pvPower,
		},
		{
			"type":      "battery",
			"power":     batteryPower,
			"soc":       batterySoc,
			"direction": batteryDirection,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"virtual_devices": virtualDevices})
}

// handleSiteVirtualDevice handles writing to a virtual device (pv or battery)
func handleSiteVirtualDevice(w http.ResponseWriter, r *http.Request, site *Site, deviceType string) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	site.mu.RLock()
	meterID := site.MeterID
	site.mu.RUnlock()

	meterSim := getSimulator(meterID)
	if meterSim == nil {
		http.Error(w, "Meter not found", http.StatusNotFound)
		return
	}

	meterSim.mu.RLock()
	category := meterSim.Category
	running := meterSim.Running
	server := meterSim.Server
	meterSim.mu.RUnlock()

	if category != "inverter" {
		http.Error(w, "Meter is not an inverter", http.StatusBadRequest)
		return
	}

	if !running || server == nil {
		http.Error(w, "Inverter is not running", http.StatusBadRequest)
		return
	}

	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch deviceType {
	case "pv":
		if power, ok := req["power"].(float64); ok {
			if pvDef := modbus.RegisterLookup[5016]; pvDef != nil {
				server.WriteRegisterValue(pvDef, power)
			}
		}
	case "battery":
		if power, ok := req["power"].(float64); ok {
			absPower := math.Abs(power)
			if battDef := modbus.RegisterLookup[13021]; battDef != nil {
				server.WriteRegisterValue(battDef, absPower)
			}
			// Set command register: 170=charge, 187=discharge, 204=stop
			var cmd float64 = 204
			if power < 0 {
				cmd = 170 // charge (negative = consuming)
			} else if power > 0 {
				cmd = 187 // discharge (positive = generating)
			}
			if cmdDef := modbus.RegisterLookup[13050]; cmdDef != nil {
				server.WriteRegisterValue(cmdDef, cmd)
			}
		}
		if soc, ok := req["soc"].(float64); ok {
			if socDef := modbus.RegisterLookup[13022]; socDef != nil {
				server.WriteRegisterValue(socDef, soc)
			}
		}
	default:
		http.Error(w, "Unknown device type", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
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

	// Energy meters don't generate their own values - they are controlled by site aggregation
	if sim.Category == "energy_meter" {
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
		sim.Server.WriteRegisterValue(battDef, math.Abs(batteryPower))
	}
	// Set battery command register: 170=charge, 187=discharge, 204=stop
	if cmdDef := modbus.RegisterLookup[13050]; cmdDef != nil {
		var cmd float64 = 204 // stop
		if batteryPower > 0 {
			cmd = 170 // charging (consuming power)
		} else if batteryPower < 0 {
			cmd = 187 // discharging (generating power)
		}
		sim.Server.WriteRegisterValue(cmdDef, cmd)
	}

	// Only update meter register if NOT a site meter (site controls it)
	if !isSimulatorSiteMeter(sim.ID) {
		meterPower := loadPower - solarPower + batteryPower
		if meterDef := modbus.RegisterLookup[5600]; meterDef != nil {
			sim.Server.WriteRegisterValue(meterDef, meterPower)
		}
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

// isSimulatorSiteMeter checks if a simulator is currently acting as a site meter
func isSimulatorSiteMeter(simID int) bool {
	sitesMu.RLock()
	defer sitesMu.RUnlock()
	for _, site := range sites {
		site.mu.RLock()
		isMeter := site.MeterID == simID && site.Enabled
		site.mu.RUnlock()
		if isMeter {
			return true
		}
	}
	return false
}

// Site aggregation functions

// getPowerContribution returns the power contribution from a simulator
// Positive = consuming power, Negative = generating power
func getPowerContribution(sim *Simulator) float64 {
	sim.mu.RLock()
	defer sim.mu.RUnlock()

	switch sim.Protocol {
	case "modbus":
		if sim.Server == nil {
			return 0
		}
		// For inverters: load - pv + battery
		// battery: positive = charging (consuming), negative = discharging (generating)
		load := getRegisterValue(sim.Server, 13052)      // Load power
		pv := getRegisterValue(sim.Server, 5016)         // PV power
		battery := getRegisterValue(sim.Server, 13021)   // Battery power
		return load - pv + battery

	case "mqtt":
		if sim.MQTTServer == nil {
			return 0
		}
		state := sim.MQTTServer.GetState()
		// V2X powerAc: negative = charging (consuming), positive = V2G (generating)
		// We invert the sign so positive = consuming
		powerAc, ok := state["powerAc"].(float64)
		if !ok {
			return 0
		}
		return -powerAc

	case "ocpp":
		if sim.OCPPServer == nil {
			return 0
		}
		state := sim.OCPPServer.GetState()
		// OCPP totalPower is always positive (consumption)
		if connectors, ok := state["connectors"].([]map[string]interface{}); ok {
			var totalPower float64
			for _, conn := range connectors {
				if power, ok := conn["currentPower"].(float64); ok {
					totalPower += power
				}
			}
			return totalPower
		}
		return 0
	}

	return 0
}

// startSiteAggregation starts the aggregation loop for a site
func startSiteAggregation(site *Site) {
	site.mu.Lock()
	if site.Enabled {
		site.mu.Unlock()
		return // Already running
	}
	site.Enabled = true
	site.stopChan = make(chan struct{})
	stopChan := site.stopChan
	site.mu.Unlock()

	log.Printf("[Site %d] Aggregation started", site.ID)

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				log.Printf("[Site %d] Aggregation stopped", site.ID)
				return
			case <-ticker.C:
				calculateAndUpdateMeter(site)
			}
		}
	}()
}

// stopSiteAggregation stops the aggregation loop for a site
func stopSiteAggregation(site *Site) {
	site.mu.Lock()
	defer site.mu.Unlock()

	if !site.Enabled {
		return
	}

	if site.stopChan != nil {
		close(site.stopChan)
	}
	site.Enabled = false
}

// calculateAndUpdateMeter calculates total power and updates the meter
func calculateAndUpdateMeter(site *Site) {
	site.mu.RLock()
	meterID := site.MeterID
	simIDs := make([]int, len(site.SimulatorIDs))
	copy(simIDs, site.SimulatorIDs)
	baseLoad := site.BaseLoadW
	p1Buffer := site.P1Buffer
	phaseFactors := site.PhaseFactors
	site.mu.RUnlock()

	meterSim := getSimulator(meterID)
	if meterSim == nil || meterSim.Protocol != "modbus" {
		return
	}

	meterSim.mu.RLock()
	if !meterSim.Running || meterSim.Server == nil {
		meterSim.mu.RUnlock()
		return
	}
	meterCategory := meterSim.Category
	meterSim.mu.RUnlock()

	// Calculate current total power
	totalPower := baseLoad // Start with base load

	// If meter is an inverter, read its PV/Battery values and include them in the calculation
	if meterCategory == "inverter" {
		pvPower := getRegisterValue(meterSim.Server, 5016)       // PV power
		batteryPower := getRegisterValue(meterSim.Server, 13021) // Battery power (absolute)
		batteryCmd := getRegisterValue(meterSim.Server, 13050)   // Command register

		// Battery sign from command register: 170=charge (importing), 187=discharge (exporting)
		// For site calculation: charging adds to consumption, discharging reduces consumption
		if batteryCmd == 187 {
			batteryPower = -batteryPower // discharge = exporting
		}
		// Note: when cmd=170 (charge), batteryPower stays positive (importing/consuming)

		totalPower = baseLoad - pvPower + batteryPower
	}

	for _, simID := range simIDs {
		// Skip meter simulator to avoid double-counting its own power
		if simID == meterID {
			continue
		}
		sim := getSimulator(simID)
		if sim == nil {
			continue
		}
		totalPower += getPowerContribution(sim)
	}

	// Distribute power across phases using configurable phase factors
	phasePowers := PhasePowers{
		L1: totalPower * phaseFactors.L1,
		L2: totalPower * phaseFactors.L2,
		L3: totalPower * phaseFactors.L3,
	}

	// Calculate energy delta for this 0.5s tick (kWh and Wh)
	energyDeltaKWh := math.Abs(totalPower) / 1000.0 / 7200.0 // kWh per 0.5s
	energyDeltaWh := math.Abs(totalPower) / 7200.0           // Wh per 0.5s

	// Handle P1 interval mode
	if p1Buffer == nil || p1Buffer.Mode == P1ModeDirect {
		// Direct mode: write immediately
		writeMeterRegisters(meterSim, totalPower, phasePowers)
		updateMeterEnergy(meterSim, totalPower, energyDeltaKWh, energyDeltaWh)
	} else {
		// Interval mode: accumulate energy internally, write only when interval elapses
		site.mu.Lock()

		// Store current instant power values
		p1Buffer.LastPower = totalPower
		p1Buffer.LastPhasePowers = phasePowers

		// Accumulate energy deltas continuously
		if totalPower > 0 {
			p1Buffer.ImportEnergyDelta += energyDeltaKWh
		} else if totalPower < 0 {
			p1Buffer.ExportEnergyDelta += energyDeltaKWh
		}

		// Check if interval has elapsed
		intervalDuration := getP1IntervalDuration(p1Buffer.Mode)
		if time.Since(p1Buffer.IntervalStart) >= intervalDuration {
			// Get instant power values (not averaged)
			instantPower := p1Buffer.LastPower
			instantPhasePowers := p1Buffer.LastPhasePowers
			importDelta := p1Buffer.ImportEnergyDelta
			exportDelta := p1Buffer.ExportEnergyDelta

			// Reset energy accumulator and interval timer
			p1Buffer.resetEnergy()
			p1Buffer.LastUpdateTime = time.Now()
			site.mu.Unlock()

			// Write instant power values to registers
			writeMeterRegisters(meterSim, instantPower, instantPhasePowers)

			// Write accumulated energy
			if importDelta > 0 {
				if importDef := modbus.RegisterLookup[72]; importDef != nil {
					currentImport, _ := meterSim.Server.ReadRegisterValue(importDef)
					meterSim.Server.WriteRegisterValue(importDef, currentImport+importDelta)
				}
				if importDefWh := modbus.RegisterLookup[13036]; importDefWh != nil {
					currentImport, _ := meterSim.Server.ReadRegisterValue(importDefWh)
					meterSim.Server.WriteRegisterValue(importDefWh, currentImport+importDelta*1000)
				}
			}
			if exportDelta > 0 {
				if exportDef := modbus.RegisterLookup[74]; exportDef != nil {
					currentExport, _ := meterSim.Server.ReadRegisterValue(exportDef)
					meterSim.Server.WriteRegisterValue(exportDef, currentExport+exportDelta)
				}
				if exportDefWh := modbus.RegisterLookup[13045]; exportDefWh != nil {
					currentExport, _ := meterSim.Server.ReadRegisterValue(exportDefWh)
					meterSim.Server.WriteRegisterValue(exportDefWh, currentExport+exportDelta*1000)
				}
			}
		} else {
			site.mu.Unlock()
		}
	}
}

// writeMeterRegisters writes power values to meter registers
func writeMeterRegisters(meterSim *Simulator, totalPower float64, phasePowers PhasePowers) {
	// Write to meter register 5600 (positive=import, negative=export)
	if meterDef := modbus.RegisterLookup[5600]; meterDef != nil {
		meterSim.Server.WriteRegisterValue(meterDef, totalPower)
	}

	// Also write to SDM630 registers (for gateways that read SDM630 format)
	// Register 52: Total System Power (W)
	if sdm630PowerDef := modbus.RegisterLookup[52]; sdm630PowerDef != nil {
		meterSim.Server.WriteRegisterValue(sdm630PowerDef, totalPower)
	}

	// Write per-phase powers
	if l1PowerDef := modbus.RegisterLookup[12]; l1PowerDef != nil {
		meterSim.Server.WriteRegisterValue(l1PowerDef, phasePowers.L1)
	}
	if l2PowerDef := modbus.RegisterLookup[14]; l2PowerDef != nil {
		meterSim.Server.WriteRegisterValue(l2PowerDef, phasePowers.L2)
	}
	if l3PowerDef := modbus.RegisterLookup[16]; l3PowerDef != nil {
		meterSim.Server.WriteRegisterValue(l3PowerDef, phasePowers.L3)
	}

	// Calculate per-phase currents from power (I = P / V, assuming 230V)
	l1Current := math.Abs(phasePowers.L1) / 230.0
	l2Current := math.Abs(phasePowers.L2) / 230.0
	l3Current := math.Abs(phasePowers.L3) / 230.0

	if l1CurrentDef := modbus.RegisterLookup[6]; l1CurrentDef != nil {
		meterSim.Server.WriteRegisterValue(l1CurrentDef, l1Current)
	}
	if l2CurrentDef := modbus.RegisterLookup[8]; l2CurrentDef != nil {
		meterSim.Server.WriteRegisterValue(l2CurrentDef, l2Current)
	}
	if l3CurrentDef := modbus.RegisterLookup[10]; l3CurrentDef != nil {
		meterSim.Server.WriteRegisterValue(l3CurrentDef, l3Current)
	}

	// Ensure voltage and frequency are always set (SDM630 registers)
	if l1VoltageDef := modbus.RegisterLookup[0]; l1VoltageDef != nil {
		meterSim.Server.WriteRegisterValue(l1VoltageDef, 230.0)
	}
	if l2VoltageDef := modbus.RegisterLookup[2]; l2VoltageDef != nil {
		meterSim.Server.WriteRegisterValue(l2VoltageDef, 230.0)
	}
	if l3VoltageDef := modbus.RegisterLookup[4]; l3VoltageDef != nil {
		meterSim.Server.WriteRegisterValue(l3VoltageDef, 230.0)
	}
	if freqDef := modbus.RegisterLookup[70]; freqDef != nil {
		meterSim.Server.WriteRegisterValue(freqDef, 50.0)
	}
}

// updateMeterEnergy updates energy registers for direct mode
func updateMeterEnergy(meterSim *Simulator, totalPower, energyDeltaKWh, energyDeltaWh float64) {
	if totalPower > 0 {
		// Importing - add to import energy (register 72)
		if importDef := modbus.RegisterLookup[72]; importDef != nil {
			currentImport, _ := meterSim.Server.ReadRegisterValue(importDef)
			meterSim.Server.WriteRegisterValue(importDef, currentImport+energyDeltaKWh)
		}
		if importDef := modbus.RegisterLookup[13036]; importDef != nil {
			currentImport, _ := meterSim.Server.ReadRegisterValue(importDef)
			meterSim.Server.WriteRegisterValue(importDef, currentImport+energyDeltaWh)
		}
	} else if totalPower < 0 {
		// Exporting - add to export energy (register 74)
		if exportDef := modbus.RegisterLookup[74]; exportDef != nil {
			currentExport, _ := meterSim.Server.ReadRegisterValue(exportDef)
			meterSim.Server.WriteRegisterValue(exportDef, currentExport+energyDeltaKWh)
		}
		if exportDef := modbus.RegisterLookup[13045]; exportDef != nil {
			currentExport, _ := meterSim.Server.ReadRegisterValue(exportDef)
			meterSim.Server.WriteRegisterValue(exportDef, currentExport+energyDeltaWh)
		}
	}
}

// getSite returns a site by ID
func getSite(id int) *Site {
	sitesMu.RLock()
	defer sitesMu.RUnlock()
	for _, site := range sites {
		if site.ID == id {
			return site
		}
	}
	return nil
}

// createSite creates a new site
// A site ALWAYS gets a dedicated SDM630 energy meter as its bound main meter.
// The meterID parameter is ignored - bound meters are always auto-created.
func createSite(name string, meterID int, simulatorIDs []int, baseLoadW float64) (*Site, error) {
	// Always create a dedicated SDM630 energy meter as the site's bound meter
	// This meter is the source of truth for site energy balance
	meterSim, err := createSimulator("energy_meter")
	if err != nil {
		return nil, fmt.Errorf("failed to create bound meter: %v", err)
	}

	// Start the meter server
	if err := startSimulatorServer(meterSim); err != nil {
		// Cleanup on failure - use internal delete to bypass bound meter check
		simulatorsMu.Lock()
		for i, sim := range simulators {
			if sim.ID == meterSim.ID {
				simulators = append(simulators[:i], simulators[i+1:]...)
				break
			}
		}
		simulatorsMu.Unlock()
		return nil, fmt.Errorf("failed to start bound meter: %v", err)
	}

	log.Printf("[Site] Created bound SDM630 meter (ID: %d, Serial: %s)", meterSim.ID, meterSim.Serial)

	// Validate simulators exist
	for _, simID := range simulatorIDs {
		if getSimulator(simID) == nil {
			return nil, fmt.Errorf("simulator %d not found", simID)
		}
	}

	// Check simulators aren't already in another site
	sitesMu.RLock()
	for _, existingSite := range sites {
		for _, simID := range simulatorIDs {
			for _, existingSimID := range existingSite.SimulatorIDs {
				if simID == existingSimID {
					sitesMu.RUnlock()
					return nil, fmt.Errorf("simulator %d is already in site %d", simID, existingSite.ID)
				}
			}
		}
	}
	sitesMu.RUnlock()

	sitesMu.Lock()
	id := nextSiteID
	nextSiteID++

	site := &Site{
		ID:           id,
		Name:         name,
		MeterID:      meterSim.ID,
		SimulatorIDs: simulatorIDs,
		BaseLoadW:    baseLoadW,
		Enabled:      false,
		FuseSize:     16, // Default 16A fuse
		PhaseFactors: PhaseFactors{
			L1: 0.333,
			L2: 0.333,
			L3: 0.334,
		},
		BoundMeter: true, // Always true - sites always have a bound meter
		P1Buffer:   newP1IntervalBuffer(),
	}
	sites = append(sites, site)
	sitesMu.Unlock()

	log.Printf("[Site %d] Created site '%s' with bound meter %d", id, name, meterSim.ID)
	return site, nil
}

// deleteSite removes a site
func deleteSite(id int) error {
	sitesMu.Lock()

	for i, site := range sites {
		if site.ID == id {
			stopSiteAggregation(site)

			// If site has a bound meter, delete it too
			boundMeter := site.BoundMeter
			meterID := site.MeterID

			sites = append(sites[:i], sites[i+1:]...)
			sitesMu.Unlock()

			if boundMeter && meterID != 0 {
				if err := deleteSimulator(meterID); err != nil {
					log.Printf("[Site %d] Warning: failed to delete bound meter %d: %v", id, meterID, err)
				} else {
					log.Printf("[Site %d] Deleted bound meter %d", id, meterID)
				}
			}

			log.Printf("[Site %d] Deleted", id)
			return nil
		}
	}
	sitesMu.Unlock()
	return fmt.Errorf("site %d not found", id)
}

// removeSimulatorFromSites removes a simulator from all sites
func removeSimulatorFromSites(simID int) {
	sitesMu.Lock()
	defer sitesMu.Unlock()

	for _, site := range sites {
		site.mu.Lock()
		// Check if this simulator is the meter
		if site.MeterID == simID {
			if site.Enabled && site.stopChan != nil {
				close(site.stopChan)
				site.Enabled = false
			}
			site.MeterID = 0 // Invalidate meter
		}

		// Remove from simulator IDs
		newSimIDs := make([]int, 0, len(site.SimulatorIDs))
		for _, id := range site.SimulatorIDs {
			if id != simID {
				newSimIDs = append(newSimIDs, id)
			}
		}
		site.SimulatorIDs = newSimIDs
		site.mu.Unlock()
	}
}
