package main

import (
	"context"
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
	"github.com/srcfl/device-simulator/internal/mcp"
	simmdns "github.com/srcfl/device-simulator/internal/mdns"
	"github.com/srcfl/device-simulator/internal/modbus"
	"github.com/srcfl/device-simulator/internal/modbus/devices"
	"github.com/srcfl/device-simulator/internal/mqtt"
	"github.com/srcfl/device-simulator/internal/ocpp"
	"github.com/srcfl/device-simulator/internal/plant"
	"github.com/srcfl/device-simulator/internal/ports"
	"github.com/srcfl/device-simulator/internal/profiles"
	"github.com/srcfl/device-simulator/internal/settings"
	"github.com/srcfl/device-simulator/internal/state"
	"github.com/srcfl/device-simulator/siteplant"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed templates/* static/*
var embeddedFS embed.FS

// Simulator represents a single simulator instance
type Simulator struct {
	ID                 int                 `json:"id"`
	Serial             string              `json:"serial"`
	Protocol           string              `json:"protocol"`
	Category           string              `json:"category"`
	SlaveID            uint8               `json:"slave_id,omitempty"`
	Port               int                 `json:"port"`
	Running            bool                `json:"running"`
	SiteID             int                 `json:"site_id"`   // Which site this simulator belongs to (0 = orphan)
	DeviceOn           bool                `json:"device_on"` // Whether device is contributing power (can be off but still on site)
	BatteryCapacityKWh float64             `json:"battery_capacity_kwh"`
	Server             *modbus.Server      `json:"-"`
	MQTTServer         *mqtt.Server        `json:"-"`
	OCPPServer         *ocpp.Server        `json:"-"`
	Device             device.DeviceServer `json:"-"`
	ProfilesManager    *profiles.Manager   `json:"-"`
	RegisterSet        *modbus.RegisterSet `json:"-"` // Device-specific register definitions
	Automation         *AutomationState    `json:"-"` // Legacy - will be removed once migration complete
	internalSOC        float64             // High-precision SOC accumulator (avoids register truncation)
	socInitialized     bool                // Whether internalSOC has been initialized from register
	plant              *plant.Plant        // Physical DAE; nil until first physical step
	mu                 sync.RWMutex
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

// P1UpdateMode represents the update interval mode for P1 port simulation
type P1UpdateMode string

const (
	P1ModeDirect P1UpdateMode = "direct" // Updates every site tick (default 500ms)
	P1Mode1s     P1UpdateMode = "1s"     // Updates every 1 second with averaging
	P1Mode10s    P1UpdateMode = "10s"    // Updates every 10 seconds with averaging
)

const (
	defaultBatteryCapacityKWh = 10.0
	defaultLoadScenarioKey    = "gaming_family"
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

// PhaseFuses represents whether a phase fuse is blown
type PhaseFuses struct {
	L1 bool `json:"l1"`
	L2 bool `json:"l2"`
	L3 bool `json:"l3"`
}

// Site represents a group of simulators with aggregated energy metering
type Site struct {
	ID           int               `json:"id"`
	Name         string            `json:"name"`
	MeterID      int               `json:"meter_id"`      // Primary meter ID (for backward compat)
	MeterIDs     []int             `json:"meter_ids"`     // Multiple meters (all synced with same values)
	SimulatorIDs []int             `json:"simulator_ids"` // Member simulators
	BaseLoadW    float64           `json:"base_load_w"`   // Configurable 0-10kW
	Enabled      bool              `json:"enabled"`       // Aggregation enabled
	FuseSize     int               `json:"fuse_size"`     // Fuse size in Amps (10, 16, 20, 25, 32)
	FuseProfile  string            `json:"fuse_profile"`  // Fuse profile identifier
	PhaseFactors PhaseFactors      `json:"phase_factors"` // Per-phase load distribution
	FuseBlown    PhaseFuses        `json:"fuse_blown"`    // Per-phase blown fuse state
	FuseDamage   PhaseLoads        `json:"-"`
	FuseOverload FuseOverload      `json:"-"`
	BoundMeter   bool              `json:"bound_meter"` // True if meter was auto-created with site
	P1Buffer     *P1IntervalBuffer // P1 port interval buffer

	// Simulation state (moved from per-simulator AutomationState)
	SimulatedTime  time.Time `json:"simulated_time"`
	TimeMultiplier float64   `json:"time_multiplier"`
	SimRunning     bool      `json:"sim_running"` // Simulation running (data generation)
	Scenario       string    `json:"scenario"`    // Weather scenario

	// Site Load (per-phase load control)
	SitePhaseLoads PhaseLoads `json:"phase_loads"`   // Per-phase loads in Watts
	LoadScenario   string     `json:"load_scenario"` // Active load scenario key
	LoadLinked     bool       `json:"load_linked"`   // Whether sliders are linked

	mu          sync.RWMutex
	stopChan    chan struct{}
	simStopChan chan struct{} // Stop channel for simulation loop
	sitePlant   plant.Model
}

var (
	simulators      []*Simulator
	simulatorsMu    sync.RWMutex
	nextSimulatorID int
	nextIDMu        sync.Mutex
	portAllocator   *ports.Allocator
	mdnsAdvertiser  *simmdns.Advertiser
	isDesktopMode   bool
	runtimeSettings *settings.AppSettings
	stateDirty      bool
	stateMu         sync.Mutex
	stateWarningsMu sync.Mutex
	stateWarnings   []string

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
	httpPort := flag.Int("http", 8762, "HTTP server port")
	desktopMode := flag.Bool("desktop", false, "Run in desktop mode (Wails)")
	flag.Parse()

	// Load settings from config file (applies before env var overrides)
	appSettings := settings.Load()

	// Apply settings if no env var is set
	if os.Getenv("MODBUS_BASE_PORT") == "" {
		*modbusBasePort = appSettings.ModbusBasePort
	}
	if os.Getenv("MQTT_BASE_PORT") == "" {
		*mqttBasePort = appSettings.MQTTBasePort
	}
	if os.Getenv("OCPP_BASE_PORT") == "" {
		*ocppBasePort = appSettings.OCPPBasePort
	}

	// Environment variable overrides (takes precedence over settings)
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
	// WAILS_DESKTOP env var overrides flag
	if os.Getenv("WAILS_DESKTOP") == "1" {
		*desktopMode = true
	}

	// Display offsets (used for UI port display)
	modbusDisplayOffset := appSettings.ModbusDisplayOffset
	mqttDisplayOffset := appSettings.MQTTDisplayOffset
	ocppDisplayOffset := appSettings.OCPPDisplayOffset
	siteTickMs := appSettings.SiteTickMs

	if env := os.Getenv("MODBUS_DISPLAY_OFFSET"); env != "" {
		if n, err := strconv.Atoi(env); err == nil {
			modbusDisplayOffset = n
		}
	}
	if env := os.Getenv("MQTT_DISPLAY_OFFSET"); env != "" {
		if n, err := strconv.Atoi(env); err == nil {
			mqttDisplayOffset = n
		}
	}
	if env := os.Getenv("OCPP_DISPLAY_OFFSET"); env != "" {
		if n, err := strconv.Atoi(env); err == nil {
			ocppDisplayOffset = n
		}
	}

	if siteTickMs <= 0 {
		siteTickMs = 500
	}

	// Cache runtime settings for API/UI
	runtimeSettings = &settings.AppSettings{
		ModbusBasePort:      *modbusBasePort,
		MQTTBasePort:        *mqttBasePort,
		OCPPBasePort:        *ocppBasePort,
		ModbusDisplayOffset: modbusDisplayOffset,
		MQTTDisplayOffset:   mqttDisplayOffset,
		OCPPDisplayOffset:   ocppDisplayOffset,
		SiteTickMs:          siteTickMs,
	}

	log.Printf("Starting Device Simulator")
	log.Printf("Port ranges: Modbus %d+, MQTT %d+, OCPP %d+", *modbusBasePort, *mqttBasePort, *ocppBasePort)

	// Initialize port allocator
	portAllocator = ports.NewAllocator(*modbusBasePort, *mqttBasePort, *ocppBasePort)

	// Initialize mDNS advertiser (works in desktop mode; Docker bridge doesn't support multicast)
	isDesktopMode = *desktopMode
	mdnsAdvertiser = simmdns.New()

	// Initialize empty simulator list
	simulators = make([]*Simulator, 0)
	nextSimulatorID = 1
	sites = make([]*Site, 0)
	nextSiteID = 1

	// Restore persisted state before starting servers
	restoreState()
	startStateSaver()

	if *desktopMode {
		// Desktop mode: Run Wails application
		log.Println("Running in desktop mode")
		runWailsApp(*httpPort)
	} else {
		// Server mode: Run HTTP server
		log.Printf("HTTP port: %d", *httpPort)
		runHTTPServer(*httpPort)
	}
}

// runHTTPServer runs the HTTP server mode (for Docker/browser)
func runHTTPServer(httpPort int) {
	// Start HTTP server
	go startHTTPServer(httpPort)

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
	mdnsAdvertiser.Shutdown()
	saveStateIfDirty()
}

// runWailsApp runs the Wails desktop application
func runWailsApp(httpPort int) {
	// Create App instance
	app := NewApp()

	// Create custom asset handler for Go templates
	assetHandler := &templateAssetHandler{
		staticFS: embeddedFS,
	}

	// Run Wails application
	err := wails.Run(&options.App{
		Title:  "Device Simulator",
		Width:  1400,
		Height: 900,
		AssetServer: &assetserver.Options{
			Handler: assetHandler,
		},
		Mac: &mac.Options{
			TitleBar:   mac.TitleBarHiddenInset(),
			Appearance: mac.NSAppearanceNameDarkAqua,
		},
		OnStartup: func(ctx context.Context) {
			// Start HTTP server after Wails is initialized (not during binding generation)
			go startHTTPServer(httpPort)
			app.startup(ctx)
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		log.Fatal("Error running Wails app:", err)
	}

	// Cleanup on exit
	log.Println("Shutting down...")
	simulatorsMu.RLock()
	for _, sim := range simulators {
		stopSimulatorServer(sim)
	}
	simulatorsMu.RUnlock()
	mdnsAdvertiser.Shutdown()
	saveStateIfDirty()
}

// templateAssetHandler serves the embedded frontend assets with template support
type templateAssetHandler struct {
	staticFS embed.FS
}

func (h *templateAssetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" || path == "" {
		path = "/index.html"
	}

	// Handle index.html with template rendering
	if path == "/index.html" {
		tmpl, err := template.ParseFS(h.staticFS, "templates/index.html")
		if err != nil {
			http.Error(w, "Template error", http.StatusInternalServerError)
			log.Printf("Template error: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.Execute(w, nil)
		return
	}

	// Handle docs page
	if path == "/docs" {
		tmpl, err := template.ParseFS(h.staticFS, "templates/docs.html")
		if err != nil {
			http.Error(w, "Template error", http.StatusInternalServerError)
			log.Printf("Docs template error: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.Execute(w, nil)
		return
	}

	// Handle OpenAPI spec
	if path == "/openapi.json" {
		content, err := h.staticFS.ReadFile("static/openapi.json")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(content)
		return
	}

	// Handle static files
	if strings.HasPrefix(path, "/static/") {
		staticPath := strings.TrimPrefix(path, "/")
		content, err := h.staticFS.ReadFile(staticPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		// Set content type based on file extension
		switch {
		case strings.HasSuffix(path, ".css"):
			w.Header().Set("Content-Type", "text/css")
		case strings.HasSuffix(path, ".js"):
			w.Header().Set("Content-Type", "application/javascript")
		case strings.HasSuffix(path, ".svg"):
			w.Header().Set("Content-Type", "image/svg+xml")
		case strings.HasSuffix(path, ".png"):
			w.Header().Set("Content-Type", "image/png")
		case strings.HasSuffix(path, ".ico"):
			w.Header().Set("Content-Type", "image/x-icon")
		case strings.HasSuffix(path, ".woff2"):
			w.Header().Set("Content-Type", "font/woff2")
		}
		w.Write(content)
		return
	}

	http.NotFound(w, r)
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

	port := 0

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
		ID:                 id,
		Protocol:           protocol,
		Category:           category,
		Port:               port,
		Running:            false,
		SiteID:             0,    // Orphan until assigned to a site
		DeviceOn:           true, // Devices are "on" by default when added to site
		BatteryCapacityKWh: defaultBatteryCapacityKWh,
		ProfilesManager:    pm,
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

		// Set up device-specific RegisterSet from the active profile
		if profile := pm.GetActive(); profile != nil && profile.RegisterSetName != "" {
			if rs := devices.GetRegisterSet(profile.RegisterSetName); rs != nil {
				sim.RegisterSet = rs
				server.RegisterSet = rs
				server.InitDefaultsFromSet(rs)
			}
		}
		// Fallback: use SungrowRegisterSet if no device-specific set
		if sim.RegisterSet == nil {
			sim.RegisterSet = modbus.SungrowRegisterSet
			server.RegisterSet = modbus.SungrowRegisterSet
		}

		// Apply profile defaults (overwrites RegisterSet defaults with profile-specific values)
		if profile := pm.GetActive(); profile != nil {
			for addr, value := range profile.Defaults {
				// Look up in the device's RegisterSet first, then fall back to global
				var def *modbus.RegisterDef
				if sim.RegisterSet != nil {
					def = sim.RegisterSet.Lookup[addr]
				}
				if def == nil {
					def = modbus.RegisterLookup[addr]
				}
				if def != nil {
					if err := server.WriteRegisterValue(def, value); err != nil {
						log.Printf("[%d] Failed to write register %d: %v", id, addr, err)
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
		var profileConfig map[string]string

		if len(availableProfiles) > 0 {
			if profile := pm.Get(availableProfiles[0].Name); profile != nil && profile.OCPPConfig != nil {
				vendor = profile.OCPPConfig.Vendor
				model = profile.OCPPConfig.Model
				numConnectors = profile.OCPPConfig.NumConnectors
				maxPowerKW = profile.OCPPConfig.MaxPowerKW
				profileConfig = profile.OCPPConfig.Configuration
			}
		}

		ocppServer := ocpp.NewServer(id, port, vendor, model, numConnectors, maxPowerKW)
		if profileConfig != nil {
			ocppServer.ApplyConfiguration(profileConfig)
		}
		sim.Serial = ocppServer.SerialNumber()
		sim.OCPPServer = ocppServer
		sim.Device = ocppServer
		sim.Automation.Scenario = "idle"
	}

	simulatorsMu.Lock()
	simulators = append(simulators, sim)
	simulatorsMu.Unlock()

	log.Printf("[%s] Created %s simulator on port %d", sim.Serial, simType, port)
	markStateDirty()
	return sim, nil
}

func restoreState() {
	loaded, err := state.Load()
	if err != nil {
		log.Printf("Failed to load state: %v", err)
		return
	}
	if loaded == nil {
		return
	}

	maxSimID := 0
	for _, simState := range loaded.Simulators {
		sim, err := restoreSimulator(simState)
		if err != nil {
			addWarning(fmt.Sprintf("Simulator %s restore failed: %v", simState.Serial, err))
			continue
		}
		if sim.ID > maxSimID {
			maxSimID = sim.ID
		}
	}
	if loaded.NextSimulatorID > maxSimID+1 {
		nextSimulatorID = loaded.NextSimulatorID
	} else {
		nextSimulatorID = maxSimID + 1
	}

	maxSiteID := 0
	for _, siteState := range loaded.Sites {
		site, err := restoreSite(siteState)
		if err != nil {
			addWarning(fmt.Sprintf("Site %s restore failed: %v", siteState.Name, err))
			continue
		}
		if site.ID > maxSiteID {
			maxSiteID = site.ID
		}
	}
	if loaded.NextSiteID > maxSiteID+1 {
		nextSiteID = loaded.NextSiteID
	} else {
		nextSiteID = maxSiteID + 1
	}

	// Start aggregation/simulation for restored sites
	sitesMu.RLock()
	restoredSites := make([]*Site, len(sites))
	copy(restoredSites, sites)
	sitesMu.RUnlock()

	for _, site := range restoredSites {
		site.mu.RLock()
		simRunning := site.SimRunning
		site.mu.RUnlock()

		// Aggregation is always on for sites
		startSiteAggregation(site)

		if simRunning {
			site.mu.Lock()
			site.SimRunning = false
			site.mu.Unlock()
			if err := startSiteSimulationWithServers(site); err != nil {
				addWarning(fmt.Sprintf("Site %s simulation failed to start: %v", site.Name, err))
			}
		}
	}

	if len(loaded.Simulators) > 0 || len(loaded.Sites) > 0 {
		markStateDirty()
	}
}

func restoreSimulator(saved state.SimulatorState) (*Simulator, error) {
	protocol := saved.Protocol
	category := saved.Category
	if protocol == "" || category == "" {
		return nil, fmt.Errorf("invalid simulator protocol/category")
	}

	port := saved.Port

	pm := profiles.NewManager("profiles")
	pm.InitEmbedded()
	pm.LoadAll()

	availableProfiles := pm.ListByProtocol(protocol)
	if saved.Profile != "" && pm.Get(saved.Profile) != nil {
		pm.SetActive(saved.Profile)
	} else if category == "energy_meter" {
		pm.SetActive("sdm630")
	} else if len(availableProfiles) > 0 {
		pm.SetActive(availableProfiles[0].Name)
	}

	sim := &Simulator{
		ID:                 saved.ID,
		Protocol:           protocol,
		Category:           category,
		Port:               port,
		Running:            false,
		SiteID:             saved.SiteID,
		DeviceOn:           saved.DeviceOn,
		BatteryCapacityKWh: saved.BatteryCapacityKWh,
		ProfilesManager:    pm,
		Automation: &AutomationState{
			Enabled:        saved.Automation.Enabled,
			TimeMultiplier: saved.Automation.TimeMultiplier,
			Scenario:       saved.Automation.Scenario,
		},
	}
	if sim.BatteryCapacityKWh <= 0 {
		sim.BatteryCapacityKWh = defaultBatteryCapacityKWh
	}
	if sim.Automation.TimeMultiplier == 0 {
		sim.Automation.TimeMultiplier = 1.0
	}
	if sim.Automation.Scenario == "" {
		sim.Automation.Scenario = "sunny"
	}
	if saved.Automation.SimulatedTime != "" {
		if t, err := time.Parse(time.RFC3339, saved.Automation.SimulatedTime); err == nil {
			sim.Automation.SimulatedTime = t
		} else {
			sim.Automation.SimulatedTime = time.Now()
		}
	} else {
		sim.Automation.SimulatedTime = time.Now()
	}

	switch protocol {
	case "modbus":
		serial := saved.Serial
		if serial == "" {
			if category == "energy_meter" {
				serial = fmt.Sprintf("%d", 1000000+sim.ID)
			} else {
				serial = fmt.Sprintf("INVSIM%02d", sim.ID)
			}
		}
		sim.Serial = serial
		if saved.SlaveID > 0 {
			sim.SlaveID = saved.SlaveID
		} else {
			sim.SlaveID = 1
		}
		server := modbus.NewServer(65535)
		server.SetSerialNumber(serial)
		server.InitDefaults()
		sim.Server = server

		// Set up device-specific RegisterSet from profile
		if profile := pm.GetActive(); profile != nil && profile.RegisterSetName != "" {
			if rs := devices.GetRegisterSet(profile.RegisterSetName); rs != nil {
				sim.RegisterSet = rs
				server.RegisterSet = rs
				server.InitDefaultsFromSet(rs)
			}
		}
		if sim.RegisterSet == nil {
			sim.RegisterSet = modbus.SungrowRegisterSet
			server.RegisterSet = modbus.SungrowRegisterSet
		}

		// Apply profile defaults
		if profile := pm.GetActive(); profile != nil {
			for addr, value := range profile.Defaults {
				var def *modbus.RegisterDef
				if sim.RegisterSet != nil {
					def = sim.RegisterSet.Lookup[addr]
				}
				if def == nil {
					def = modbus.RegisterLookup[addr]
				}
				if def != nil {
					if err := server.WriteRegisterValue(def, value); err != nil {
						log.Printf("[%d] Failed to write register %d: %v", sim.ID, addr, err)
					}
				}
			}
		}
	case "mqtt":
		mqttServer := mqtt.NewServer(sim.ID, port)
		if saved.Serial != "" {
			mqttServer.SetSerialNumber(saved.Serial)
			sim.Serial = saved.Serial
		} else {
			sim.Serial = mqttServer.SerialNumber()
		}
		sim.MQTTServer = mqttServer
		sim.Device = mqttServer
		if sim.Automation.Scenario == "" {
			sim.Automation.Scenario = "idle"
		}
	case "ocpp":
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
		ocppServer := ocpp.NewServer(sim.ID, port, vendor, model, numConnectors, maxPowerKW)
		if saved.Serial != "" {
			ocppServer.SetSerialNumber(saved.Serial)
			sim.Serial = saved.Serial
		} else {
			sim.Serial = ocppServer.SerialNumber()
		}
		if saved.OCPPURL != "" {
			ocppServer.SetOCPPURL(saved.OCPPURL)
		}
		sim.OCPPServer = ocppServer
		sim.Device = ocppServer
		if sim.Automation.Scenario == "" {
			sim.Automation.Scenario = "idle"
		}
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}

	simulatorsMu.Lock()
	simulators = append(simulators, sim)
	simulatorsMu.Unlock()

	if saved.Running {
		if err := startSimulatorServer(sim); err != nil {
			addWarning(fmt.Sprintf("%s: failed to start on port %d: %v", sim.Serial, sim.Port, err))
		} else if saved.Automation.Enabled && sim.SiteID == 0 && !isSimulatorMeter(sim.ID) {
			sim.Automation.mu.Lock()
			sim.Automation.Enabled = true
			sim.Automation.stopChan = make(chan struct{})
			go runAutomationLoop(sim)
			sim.Automation.mu.Unlock()
		}
	}

	return sim, nil
}

func restoreSite(saved state.SiteState) (*Site, error) {
	meterSim := getSimulator(saved.MeterID)
	if meterSim == nil || meterSim.Protocol != "modbus" {
		// Create a new bound meter if missing
		newMeter, err := createSimulator("energy_meter")
		if err != nil {
			return nil, fmt.Errorf("failed to create replacement meter: %v", err)
		}
		meterSim = newMeter
		addWarning(fmt.Sprintf("Site %s: meter missing, created new meter %s on port %d", saved.Name, newMeter.Serial, newMeter.Port))
		saved.MeterID = newMeter.ID
		saved.MeterIDs = []int{newMeter.ID}
		saved.BoundMeter = true
	}

	if len(saved.MeterIDs) == 0 {
		saved.MeterIDs = []int{saved.MeterID}
	}

	phaseFactors := PhaseFactors{
		L1: saved.PhaseFactors.L1,
		L2: saved.PhaseFactors.L2,
		L3: saved.PhaseFactors.L3,
	}
	if phaseFactors.L1 == 0 && phaseFactors.L2 == 0 && phaseFactors.L3 == 0 {
		phaseFactors = PhaseFactors{L1: 0.333, L2: 0.333, L3: 0.334}
	}

	phaseLoads := PhaseLoads{
		L1: saved.PhaseLoads.L1,
		L2: saved.PhaseLoads.L2,
		L3: saved.PhaseLoads.L3,
	}
	if phaseLoads.Total() == 0 && saved.BaseLoadW > 0 {
		phaseLoads = PhaseLoads{
			L1: saved.BaseLoadW * phaseFactors.L1,
			L2: saved.BaseLoadW * phaseFactors.L2,
			L3: saved.BaseLoadW * phaseFactors.L3,
		}
	}

	fuseBlown := PhaseFuses{
		L1: saved.FuseBlown.L1,
		L2: saved.FuseBlown.L2,
		L3: saved.FuseBlown.L3,
	}

	site := &Site{
		ID:             saved.ID,
		Name:           saved.Name,
		MeterID:        saved.MeterID,
		MeterIDs:       append([]int{}, saved.MeterIDs...),
		SimulatorIDs:   append([]int{}, saved.SimulatorIDs...),
		BaseLoadW:      saved.BaseLoadW,
		Enabled:        saved.Enabled,
		FuseSize:       saved.FuseSize,
		FuseProfile:    saved.FuseProfile,
		PhaseFactors:   phaseFactors,
		FuseBlown:      fuseBlown,
		BoundMeter:     saved.BoundMeter,
		SimulatedTime:  time.Now(),
		TimeMultiplier: saved.TimeMultiplier,
		SimRunning:     saved.SimRunning,
		Scenario:       saved.Scenario,
		SitePhaseLoads: phaseLoads,
		LoadScenario:   saved.LoadScenario,
		LoadLinked:     saved.LoadLinked,
		P1Buffer:       newP1IntervalBuffer(),
	}
	if site.FuseSize == 0 {
		site.FuseSize = 16
	}
	if site.FuseProfile == "" {
		site.FuseProfile = FuseProfileManual
	}
	if _, ok := fuseProfiles[site.FuseProfile]; !ok {
		site.FuseProfile = FuseProfileManual
	}
	if site.TimeMultiplier == 0 {
		site.TimeMultiplier = 1.0
	}
	if site.Scenario == "" {
		site.Scenario = "sunny"
	}
	if saved.SimulatedTime != "" {
		if t, err := time.Parse(time.RFC3339, saved.SimulatedTime); err == nil {
			site.SimulatedTime = t
		}
	}
	if site.LoadScenario == "" {
		site.LoadScenario = defaultLoadScenarioKey
		if ls, ok := LoadScenarios[site.LoadScenario]; ok {
			hour := site.SimulatedTime.Hour()
			if site.SimulatedTime.IsZero() {
				hour = time.Now().Hour()
			}
			loads := ls.GetLoadForTime(hour)
			site.SitePhaseLoads = loads
			site.BaseLoadW = loads.Total()
		}
	}

	if site.P1Buffer != nil && saved.P1Mode != "" {
		switch saved.P1Mode {
		case "1s":
			site.P1Buffer.Mode = P1Mode1s
		case "10s":
			site.P1Buffer.Mode = P1Mode10s
		default:
			site.P1Buffer.Mode = P1ModeDirect
		}
	}

	sitesMu.Lock()
	sites = append(sites, site)
	sitesMu.Unlock()

	// Ensure simulator SiteID matches restored site membership
	for _, simID := range site.SimulatorIDs {
		if sim := getSimulator(simID); sim != nil {
			sim.mu.Lock()
			sim.SiteID = site.ID
			sim.mu.Unlock()
		}
	}
	if meterSim != nil {
		meterSim.mu.Lock()
		meterSim.SiteID = site.ID
		meterSim.mu.Unlock()
	}

	return site, nil
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
			markStateDirty()
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

	// Ensure a port is available and reserved for this simulator
	port := sim.Port
	if port != 0 {
		if !portAllocator.Reserve(port) {
			port = 0
		}
	}
	if port == 0 {
		port = portAllocator.AllocateAvailableString(sim.Protocol)
		if port == 0 {
			return fmt.Errorf("no available port for %s", sim.Protocol)
		}
		sim.Port = port
	}
	switch sim.Protocol {
	case "mqtt":
		if sim.MQTTServer != nil {
			sim.MQTTServer.SetPort(sim.Port)
		}
	case "ocpp":
		if sim.OCPPServer != nil {
			sim.OCPPServer.SetPort(sim.Port)
		}
	}

	var err error
	switch sim.Protocol {
	case "modbus":
		addr := fmt.Sprintf(":%d", sim.Port)
		if err = sim.Server.Listen(addr); err != nil {
			portAllocator.Release(sim.Port)
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
			portAllocator.Release(sim.Port)
			return fmt.Errorf("mqtt start error: %v", err)
		}
		log.Printf("[%s] Started MQTT broker on port %d", sim.Serial, sim.Port)

	case "ocpp":
		if err = sim.OCPPServer.Start(); err != nil {
			portAllocator.Release(sim.Port)
			return fmt.Errorf("ocpp start error: %v", err)
		}
		log.Printf("[%s] Started OCPP server on port %d", sim.Serial, sim.Port)
	}

	sim.Running = true

	// Start mDNS advertisement if profile has mDNS config (desktop mode only — Docker bridge doesn't support multicast)
	if isDesktopMode {
		if profile := sim.ProfilesManager.GetActive(); profile != nil && profile.MDNSConfig != nil {
			if err := mdnsAdvertiser.Advertise(sim.ID, profile.MDNSConfig.Hostname, sim.Port, profile.MDNSConfig.ServiceType, getLocalIP(), nil); err != nil {
				log.Printf("[%s] mDNS advertisement failed (non-fatal): %v", sim.Serial, err)
			}
		}
	}

	markStateDirty()
	return nil
}

// stopSimulatorServer stops the protocol server for a simulator
func stopSimulatorServer(sim *Simulator) {
	sim.mu.Lock()
	defer sim.mu.Unlock()

	if !sim.Running {
		return
	}

	stopSimulatorAutomation(sim)
	mdnsAdvertiser.Remove(sim.ID)

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
	if sim.Port != 0 {
		portAllocator.Release(sim.Port)
	}
	markStateDirty()
}

func stopSimulatorAutomation(sim *Simulator) {
	if sim.Automation == nil {
		return
	}
	sim.Automation.mu.Lock()
	defer sim.Automation.mu.Unlock()
	if sim.Automation.Enabled && sim.Automation.stopChan != nil {
		close(sim.Automation.stopChan)
		sim.Automation.stopChan = nil
	}
	sim.Automation.Enabled = false
	markStateDirty()
}

func startHTTPServer(port int) {
	mux := buildHTTPMux()
	addr := fmt.Sprintf(":%d", port)
	log.Printf("HTTP server listening on %s", addr)
	if err := http.ListenAndServe(addr, corsMiddleware(mux)); err != nil {
		log.Printf("HTTP server error: %v", err)
	}
}

func buildHTTPMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Serve static files (CSS, JS)
	staticFS, _ := fs.Sub(embeddedFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Dashboard + docs
	mux.HandleFunc("/", handleDashboard)
	mux.HandleFunc("/docs", handleDocs)
	mux.HandleFunc("/openapi.json", handleOpenAPI)

	// System info
	mux.HandleFunc("/api/system", handleSystemInfo)

	// Update endpoints
	mux.HandleFunc("/api/version", handleVersion)
	mux.HandleFunc("/api/update/check", handleUpdateCheck)
	mux.HandleFunc("/api/update/apply", handleUpdateApply)
	mux.HandleFunc("/api/restart", handleRestart)
	// Settings endpoint
	mux.HandleFunc("/api/settings", handleSettings)
	// Warnings endpoint
	mux.HandleFunc("/api/warnings", handleWarnings)
	mux.HandleFunc("/api/warnings/clear", handleWarningsClear)

	// Simulator list
	mux.HandleFunc("/api/simulators", handleSimulatorsList)

	// Simulator-specific endpoints
	mux.HandleFunc("/api/simulators/", handleSimulatorAPI)

	// Site endpoints
	mux.HandleFunc("/api/sites", handleSitesList)
	mux.HandleFunc("/api/sites/", handleSiteAPI)

	// Load scenarios endpoint
	mux.HandleFunc("/api/load-scenarios", handleLoadScenarios)

	// MCP endpoints
	mcpServer := mcp.NewServer(mux)
	mux.HandleFunc("/mcp", mcpServer.HandleMCP)
	mux.HandleFunc("/.well-known/mcp", mcpServer.HandleDiscovery)

	return mux
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

func handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GetVersionInfo())
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		settingsData := runtimeSettings
		if settingsData == nil {
			settingsData = settings.Load()
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"modbusBasePort":      settingsData.ModbusBasePort,
			"mqttBasePort":        settingsData.MQTTBasePort,
			"ocppBasePort":        settingsData.OCPPBasePort,
			"modbusDisplayOffset": settingsData.ModbusDisplayOffset,
			"mqttDisplayOffset":   settingsData.MQTTDisplayOffset,
			"ocppDisplayOffset":   settingsData.OCPPDisplayOffset,
			"siteTickMs":          settingsData.SiteTickMs,
		})
	case http.MethodPost:
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		current := settings.Load()
		if v, ok := payload["modbusBasePort"].(float64); ok {
			current.ModbusBasePort = int(v)
		}
		if v, ok := payload["mqttBasePort"].(float64); ok {
			current.MQTTBasePort = int(v)
		}
		if v, ok := payload["ocppBasePort"].(float64); ok {
			current.OCPPBasePort = int(v)
		}
		if v, ok := payload["modbusDisplayOffset"].(float64); ok {
			current.ModbusDisplayOffset = int(v)
		}
		if v, ok := payload["mqttDisplayOffset"].(float64); ok {
			current.MQTTDisplayOffset = int(v)
		}
		if v, ok := payload["ocppDisplayOffset"].(float64); ok {
			current.OCPPDisplayOffset = int(v)
		}
		if v, ok := payload["siteTickMs"].(float64); ok {
			if int(v) > 0 {
				current.SiteTickMs = int(v)
			} else {
				current.SiteTickMs = 500
			}
		}
		if err := settings.Save(current); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleWarnings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"warnings": getWarnings(),
	})
}

func handleWarningsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clearWarnings()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "cleared"})
}

func handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	info, err := CheckForUpdate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(info)
}

func handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	err := DoUpdate()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Update applied. Please restart the application.",
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

func handleDocs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/docs" {
		http.NotFound(w, r)
		return
	}

	tmpl, err := template.ParseFS(embeddedFS, "templates/docs.html")
	if err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		log.Printf("Docs template error: %v", err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, nil)
}

func handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/openapi.json" {
		http.NotFound(w, r)
		return
	}

	data, err := embeddedFS.ReadFile("static/openapi.json")
	if err != nil {
		http.Error(w, "OpenAPI spec not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
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
		"id":            sim.ID,
		"serial":        sim.Serial,
		"protocol":      sim.Protocol,
		"category":      sim.Category,
		"port":          sim.Port,
		"running":       sim.Running,
		"mdns_hostname": mdnsAdvertiser.ActiveHostname(sim.ID),
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

		// Add protocol-specific fields
		switch sim.Protocol {
		case "modbus":
			entry["slave_id"] = sim.SlaveID
			// Use meter_power for energy meters, standard registers for inverters
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
		"id":             sim.ID,
		"serial":         sim.Serial,
		"protocol":       sim.Protocol,
		"category":       sim.Category,
		"port":           sim.Port,
		"running":        sim.Running,
		"runtime_serial": getRuntimeSerial(sim),
		"mdns_hostname":  mdnsAdvertiser.ActiveHostname(sim.ID),
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
		status["battery_capacity_kwh"] = sim.BatteryCapacityKWh
		status["pv_power"] = getSemanticValue(sim, "pv_power")
		status["soc"] = getSemanticValue(sim, "battery_soc")
		status["load"] = getSemanticValue(sim, "battery_power")
		status["meter"] = getSemanticValue(sim, "meter_power")
		status["frequency"] = getSemanticValue(sim, "grid_frequency")
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
		SlaveID            *uint8   `json:"slave_id"`
		Serial             *string  `json:"serial"`
		OCPPURL            *string  `json:"ocpp_url"`
		BatteryCapacityKWh *float64 `json:"battery_capacity_kwh"`
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
	if config.BatteryCapacityKWh != nil && *config.BatteryCapacityKWh > 0 {
		sim.BatteryCapacityKWh = *config.BatteryCapacityKWh
	}
	sim.mu.Unlock()

	markStateDirty()
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

	// Use device-specific RegisterSet definitions if available, else global
	defs := modbus.RegisterDefinitions
	if sim.RegisterSet != nil {
		defs = sim.RegisterSet.Definitions
	}

	var payloads []*modbus.RegisterPayload
	for i := range defs {
		def := &defs[i]
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

	// Look up in device-specific RegisterSet first, then fall back to global
	var def *modbus.RegisterDef
	if sim.RegisterSet != nil {
		def = sim.RegisterSet.Lookup[uint16(addr)]
	}
	if def == nil {
		def = modbus.RegisterLookup[uint16(addr)]
	}
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
			// Update RegisterSet if the profile specifies one
			if profile.RegisterSetName != "" {
				if rs := devices.GetRegisterSet(profile.RegisterSetName); rs != nil {
					sim.RegisterSet = rs
					sim.Server.RegisterSet = rs
					sim.Server.InitDefaultsFromSet(rs)
				}
			}
			// Apply profile-specific defaults
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
		case "ocpp":
			if profile.OCPPConfig != nil && sim.OCPPServer != nil {
				sim.OCPPServer.SetVendorModel(profile.OCPPConfig.Vendor, profile.OCPPConfig.Model)
				sim.OCPPServer.ApplyConfiguration(profile.OCPPConfig.Configuration)
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
	if sim.SiteID != 0 || isSimulatorMeter(sim.ID) {
		http.Error(w, "Simulation is controlled by the site for assigned devices", http.StatusBadRequest)
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
	markStateDirty()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func handleAutomationStop(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Stop automation first
	stopSimulatorAutomation(sim)

	// Then stop the server
	stopSimulatorServer(sim)

	markStateDirty()
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

	markStateDirty()
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
	stopSimulatorAutomation(sim)

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
	if sim.SiteID != 0 || isSimulatorMeter(sim.ID) {
		http.Error(w, "Simulation is controlled by the site for assigned devices", http.StatusBadRequest)
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
	markStateDirty()

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
		sim.Automation.stopChan = nil
		sim.Automation.Enabled = false
	}
	sim.Automation.mu.Unlock()

	markStateDirty()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

func handleReset(w http.ResponseWriter, r *http.Request, sim *Simulator) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sim.Server.InitDefaults()
	sim.mu.Lock()
	sim.socInitialized = false
	sim.mu.Unlock()

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
			"p1_mode":         string(p1Mode),
			"bound_meter":     site.BoundMeter,
			"simulated_time":  site.SimulatedTime.Format("2006-01-02 15:04:05"),
			"time_multiplier": site.TimeMultiplier,
			"sim_running":     site.SimRunning,
			"scenario":        site.Scenario,
		}

		// Add current meter reading if meter is valid
		if meterSim := getSimulator(site.MeterID); meterSim != nil && meterSim.Protocol == "modbus" && meterSim.Server != nil {
			entry["meter_power"] = getSemanticValue(meterSim, "meter_power")
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

	sitesMu.RLock()
	siteCount := len(sites)
	sitesMu.RUnlock()
	if siteCount >= 5 {
		http.Error(w, "Maximum of 5 sites allowed", http.StatusBadRequest)
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
	case action == "simulation/start":
		handleSiteSimulationStart(w, r, site)
	case action == "simulation/stop":
		handleSiteSimulationStop(w, r, site)
	case action == "simulation/config":
		handleSiteSimulationConfig(w, r, site)
	case action == "device-power":
		handleSiteDevicePower(w, r, site)
	case action == "meters":
		handleSiteMeters(w, r, site)
	case action == "p1-mode":
		handleSiteP1Mode(w, r, site)
	case action == "virtual-devices":
		handleSiteVirtualDevices(w, r, site)
	case strings.HasPrefix(action, "virtual-devices/"):
		deviceType := strings.TrimPrefix(action, "virtual-devices/")
		handleSiteVirtualDevice(w, r, site, deviceType)
	case action == "phase-loads":
		handleSitePhaseLoads(w, r, site)
	case action == "load-scenario":
		handleSiteLoadScenario(w, r, site)
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
		"id":              site.ID,
		"name":            site.Name,
		"meter_id":        site.MeterID,
		"meter_ids":       site.MeterIDs,
		"simulator_ids":   site.SimulatorIDs,
		"base_load_w":     site.BaseLoadW,
		"enabled":         site.Enabled,
		"p1_mode":         string(p1Mode),
		"fuse_size":       site.FuseSize,
		"fuse_profile":    site.FuseProfile,
		"phase_factors":   site.PhaseFactors,
		"fuse_blown":      site.FuseBlown,
		"fuse_overload":   site.FuseOverload,
		"bound_meter":     site.BoundMeter,
		"simulated_time":  site.SimulatedTime.Format("2006-01-02 15:04:05"),
		"time_multiplier": site.TimeMultiplier,
		"sim_running":     site.SimRunning,
		"scenario":        site.Scenario,
		"phase_loads":     site.SitePhaseLoads,
		"load_scenario":   site.LoadScenario,
		"load_linked":     site.LoadLinked,
	}

	// Add current meter reading if meter is valid
	meterSim := getSimulator(site.MeterID)
	if meterSim != nil && meterSim.Protocol == "modbus" && meterSim.Server != nil {
		status["meter_power"] = getSemanticValue(meterSim, "meter_power")

		// Add per-phase currents from meter registers
		status["phase_currents"] = PhaseCurrents{
			L1: getSemanticValue(meterSim, "meter_l1_current"),
			L2: getSemanticValue(meterSim, "meter_l2_current"),
			L3: getSemanticValue(meterSim, "meter_l3_current"),
		}

		// Add per-phase powers from meter registers
		status["phase_powers"] = PhasePowers{
			L1: getSemanticValue(meterSim, "meter_l1_power"),
			L2: getSemanticValue(meterSim, "meter_l2_power"),
			L3: getSemanticValue(meterSim, "meter_l3_power"),
		}

		// Include virtual devices if meter is an inverter
		if meterSim.Category == "inverter" && meterSim.Running {
			pvPower := getSemanticValue(meterSim, "pv_power")
			batteryPower, batteryDirection := getBatteryPowerAndDirection(meterSim)
			batterySoc := getSemanticValue(meterSim, "battery_soc")

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

func getSiteTickInterval() time.Duration {
	settingsData := runtimeSettings
	if settingsData == nil {
		settingsData = settings.Load()
	}
	tickMs := settingsData.SiteTickMs
	if tickMs <= 0 {
		tickMs = 500
	}
	return time.Duration(tickMs) * time.Millisecond
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
		FuseProfile  *string       `json:"fuse_profile"`
		PhaseFactors *PhaseFactors `json:"phase_factors"`
		FuseBlown    *PhaseFuses   `json:"fuse_blown"`
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
		site.FuseDamage = PhaseLoads{}
		site.FuseOverload = FuseOverload{}
	}
	if config.FuseProfile != nil {
		if _, ok := fuseProfiles[*config.FuseProfile]; !ok {
			site.mu.Unlock()
			http.Error(w, "Unknown fuse profile", http.StatusBadRequest)
			return
		}
		if site.FuseProfile != *config.FuseProfile {
			site.FuseProfile = *config.FuseProfile
			site.FuseDamage = PhaseLoads{}
			site.FuseOverload = FuseOverload{}
		}
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
	if config.FuseBlown != nil {
		if site.FuseBlown.L1 && !config.FuseBlown.L1 {
			site.FuseDamage.L1 = 0
		}
		if site.FuseBlown.L2 && !config.FuseBlown.L2 {
			site.FuseDamage.L2 = 0
		}
		if site.FuseBlown.L3 && !config.FuseBlown.L3 {
			site.FuseDamage.L3 = 0
		}
		site.FuseBlown = *config.FuseBlown
	}
	site.mu.Unlock()

	markStateDirty()
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
	oldMeterID := site.MeterID
	site.MeterID = req.MeterID
	// Keep additional meters, but replace the primary meter in the list.
	newMeterIDs := []int{req.MeterID}
	for _, id := range site.MeterIDs {
		if id == oldMeterID || id == req.MeterID {
			continue
		}
		newMeterIDs = append(newMeterIDs, id)
	}
	site.MeterIDs = newMeterIDs
	site.mu.Unlock()

	startSiteAggregation(site)

	markStateDirty()
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
	siteID := site.ID
	site.mu.Unlock()

	// Update simulator's SiteID
	sim.mu.Lock()
	sim.SiteID = siteID
	sim.mu.Unlock()

	markStateDirty()
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

	// Reset simulator's SiteID
	sim := getSimulator(simID)
	if sim != nil {
		sim.mu.Lock()
		sim.SiteID = 0
		sim.mu.Unlock()
	}

	markStateDirty()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
}

// handleSiteSimulationStart starts data generation for all devices in site
func handleSiteSimulationStart(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := startSiteSimulationWithServers(site); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	markStateDirty()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func startSiteSimulationWithServers(site *Site) error {
	// Aggregation is always on for sites
	startSiteAggregation(site)

	// Start all device servers for simulators in the site
	site.mu.RLock()
	simIDs := make([]int, len(site.SimulatorIDs))
	copy(simIDs, site.SimulatorIDs)
	meterID := site.MeterID
	site.mu.RUnlock()

	// Start meter server
	if meterSim := getSimulator(meterID); meterSim != nil {
		if err := startSimulatorServer(meterSim); err != nil {
			return fmt.Errorf("failed to start meter server: %v", err)
		}
		// Only stop automation if this meter is an inverter controlled by site simulation
		if meterSim.Category == "inverter" {
			stopSimulatorAutomation(meterSim)
		}
	}

	// Start device servers
	for _, simID := range simIDs {
		sim := getSimulator(simID)
		if sim != nil {
			sim.mu.RLock()
			deviceOn := sim.DeviceOn
			sim.mu.RUnlock()

			if deviceOn {
				if err := startSimulatorServer(sim); err != nil {
					log.Printf("[Site %d] Failed to start simulator %d server: %v", site.ID, simID, err)
				}
			}
			// Only stop automation for inverters controlled by site simulation
			if sim.Category == "inverter" {
				stopSimulatorAutomation(sim)
			}
		}
	}

	return startSiteSimulation(site)
}

// handleSiteSimulationStop stops data generation for site
func handleSiteSimulationStop(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stopSiteSimulation(site)

	markStateDirty()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

// handleSiteSimulationConfig updates simulation parameters
func handleSiteSimulationConfig(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var config struct {
		TimeMultiplier *float64 `json:"time_multiplier"`
		Scenario       *string  `json:"scenario"`
		ClockMs        *float64 `json:"clock_ms"`
	}

	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	site.mu.Lock()
	if config.TimeMultiplier != nil && *config.TimeMultiplier > 0 {
		site.TimeMultiplier = *config.TimeMultiplier
	}
	if config.Scenario != nil && *config.Scenario != "" {
		site.Scenario = *config.Scenario
	}
	if config.ClockMs != nil && *config.ClockMs > 0 {
		site.SimulatedTime = time.Unix(0, int64(*config.ClockMs)*int64(time.Millisecond)).In(time.Local)
	}
	site.mu.Unlock()

	markStateDirty()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// handleSiteDevicePower toggles a device's power state within the site
func handleSiteDevicePower(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SimulatorID int  `json:"simulator_id"`
		DeviceOn    bool `json:"device_on"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	sim := getSimulator(req.SimulatorID)
	if sim == nil {
		http.Error(w, "Simulator not found", http.StatusNotFound)
		return
	}

	// Verify simulator belongs to this site
	site.mu.RLock()
	found := false
	for _, id := range site.SimulatorIDs {
		if id == req.SimulatorID {
			found = true
			break
		}
	}
	site.mu.RUnlock()

	if !found {
		http.Error(w, "Simulator not in this site", http.StatusBadRequest)
		return
	}

	sim.mu.Lock()
	sim.DeviceOn = req.DeviceOn
	sim.mu.Unlock()

	// If turning device off, stop its server
	if !req.DeviceOn {
		stopSimulatorServer(sim)
	}

	markStateDirty()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "updated",
		"device_on": req.DeviceOn,
	})
}

// handleSiteMeters manages additional meters for the site
func handleSiteMeters(w http.ResponseWriter, r *http.Request, site *Site) {
	switch r.Method {
	case "GET":
		site.mu.RLock()
		meterIDs := make([]int, len(site.MeterIDs))
		copy(meterIDs, site.MeterIDs)
		site.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"meter_ids": meterIDs})

	case "POST":
		// Add a new meter
		var req struct {
			MeterID int `json:"meter_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		meterSim := getSimulator(req.MeterID)
		if meterSim == nil {
			http.Error(w, "Meter simulator not found", http.StatusNotFound)
			return
		}
		if meterSim.Protocol != "modbus" {
			http.Error(w, "Meter must be a Modbus simulator", http.StatusBadRequest)
			return
		}

		site.mu.Lock()
		// Check if already in list
		for _, id := range site.MeterIDs {
			if id == req.MeterID {
				site.mu.Unlock()
				http.Error(w, "Meter already in site", http.StatusBadRequest)
				return
			}
		}
		site.MeterIDs = append(site.MeterIDs, req.MeterID)
		site.mu.Unlock()

		startSiteAggregation(site)

		markStateDirty()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "added"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
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

	markStateDirty()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated", "mode": string(mode)})
}

// handleSiteVirtualDevices returns virtual devices info when meter is an inverter
func handleSiteVirtualDevices(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Find a running inverter in the site: check meter first, then simulator_ids
	site.mu.RLock()
	meterID := site.MeterID
	simIDs := make([]int, len(site.SimulatorIDs))
	copy(simIDs, site.SimulatorIDs)
	site.mu.RUnlock()

	var inverterSim *Simulator
	meterSim := getSimulator(meterID)
	if meterSim != nil {
		meterSim.mu.RLock()
		if meterSim.Category == "inverter" && meterSim.Running && meterSim.Server != nil {
			inverterSim = meterSim
		}
		meterSim.mu.RUnlock()
	}
	if inverterSim == nil {
		for _, simID := range simIDs {
			sim := getSimulator(simID)
			if sim == nil {
				continue
			}
			sim.mu.RLock()
			if sim.Category == "inverter" && sim.Running && sim.Server != nil {
				inverterSim = sim
				sim.mu.RUnlock()
				break
			}
			sim.mu.RUnlock()
		}
	}
	if inverterSim == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"virtual_devices": []interface{}{}})
		return
	}

	// Read values from inverter
	pvPower := getSemanticValue(inverterSim, "pv_power")
	batteryPower, batteryDirection := getBatteryPowerAndDirection(inverterSim)
	batterySoc := getSemanticValue(inverterSim, "battery_soc")

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

	// Find a running inverter in the site: check meter first, then simulator_ids
	site.mu.RLock()
	meterID := site.MeterID
	simIDs := make([]int, len(site.SimulatorIDs))
	copy(simIDs, site.SimulatorIDs)
	site.mu.RUnlock()

	var inverterSim *Simulator
	meterSim := getSimulator(meterID)
	if meterSim != nil {
		meterSim.mu.RLock()
		if meterSim.Category == "inverter" && meterSim.Running && meterSim.Server != nil {
			inverterSim = meterSim
		}
		meterSim.mu.RUnlock()
	}
	if inverterSim == nil {
		for _, simID := range simIDs {
			sim := getSimulator(simID)
			if sim == nil {
				continue
			}
			sim.mu.RLock()
			if sim.Category == "inverter" && sim.Running && sim.Server != nil {
				inverterSim = sim
				sim.mu.RUnlock()
				break
			}
			sim.mu.RUnlock()
		}
	}
	if inverterSim == nil {
		http.Error(w, "No running inverter found in site", http.StatusBadRequest)
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
			setSemanticValue(inverterSim, "pv_power", power)
		}
	case "battery":
		if power, ok := req["power"].(float64); ok {
			// UI convention: positive = discharge, negative = charge
			// Register convention: positive = charge, negative = discharge
			// Negate the UI value to match register convention.
			regPower := -power
			log.Printf("[DEBUG] virtual-devices/battery: UI power=%.0f, regPower=%.0f, inverter=%s (id=%d)",
				power, regPower, inverterSim.Serial, inverterSim.ID)

			def := getSemanticDef(inverterSim, "battery_power")
			if def != nil && (def.DataType == modbus.I16 || def.DataType == modbus.I32) {
				setSemanticValue(inverterSim, "battery_power", regPower)
			} else {
				setSemanticValue(inverterSim, "battery_power", math.Abs(regPower))
			}
			// Set command register: 170=charge, 187=discharge, 204=stop
			var cmd float64 = 204
			if regPower > 0 {
				cmd = 170 // charge
			} else if regPower < 0 {
				cmd = 187 // discharge
			}
			setSemanticValue(inverterSim, "battery_cmd", cmd)
			flags := 0.0
			if cmd == 187 {
				flags = 4 // discharging
			} else if cmd == 170 {
				flags = 2 // charging
			}
			setSemanticValue(inverterSim, "battery_flags", flags)
		}
		if soc, ok := req["soc"].(float64); ok {
			setSemanticValue(inverterSim, "battery_soc", soc)
			inverterSim.mu.Lock()
			inverterSim.internalSOC = soc
			inverterSim.socInitialized = true
			inverterSim.mu.Unlock()
		}
	default:
		http.Error(w, "Unknown device type", http.StatusBadRequest)
		return
	}

	markStateDirty()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// handleLoadScenarios returns all available load scenarios
func handleLoadScenarios(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GetLoadScenariosSlice())
}

// handleSitePhaseLoads handles setting per-phase loads for a site
func handleSitePhaseLoads(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		L1     *float64 `json:"l1"`
		L2     *float64 `json:"l2"`
		L3     *float64 `json:"l3"`
		Linked *bool    `json:"linked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	site.mu.Lock()
	if req.L1 != nil {
		site.SitePhaseLoads.L1 = *req.L1
	}
	if req.L2 != nil {
		site.SitePhaseLoads.L2 = *req.L2
	}
	if req.L3 != nil {
		site.SitePhaseLoads.L3 = *req.L3
	}
	if req.Linked != nil {
		site.LoadLinked = *req.Linked
	}

	// Update BaseLoadW for backward compat
	site.BaseLoadW = site.SitePhaseLoads.Total()
	loads := site.SitePhaseLoads
	linked := site.LoadLinked
	site.mu.Unlock()

	markStateDirty()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"phase_loads": loads,
		"load_linked": linked,
		"base_load_w": loads.Total(),
	})
}

// handleSiteLoadScenario handles setting the active load scenario for a site
func handleSiteLoadScenario(w http.ResponseWriter, r *http.Request, site *Site) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Scenario string `json:"scenario"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Allow empty scenario for backward compatibility
	if req.Scenario != "" {
		if _, ok := LoadScenarios[req.Scenario]; !ok {
			http.Error(w, "Unknown load scenario: "+req.Scenario, http.StatusBadRequest)
			return
		}
	}

	site.mu.Lock()
	site.LoadScenario = req.Scenario

	// If switching to a scenario, initialize phase loads to current time slot
	// For "static", keep current phase load values
	if req.Scenario != "" && req.Scenario != "static" {
		if ls, ok := LoadScenarios[req.Scenario]; ok {
			hour := site.SimulatedTime.Hour()
			if site.SimulatedTime.IsZero() {
				hour = time.Now().Hour()
			}
			loads := ls.GetLoadForTime(hour)
			site.SitePhaseLoads = loads
			site.BaseLoadW = loads.Total()
		}
	}

	scenario := site.LoadScenario
	loads := site.SitePhaseLoads
	site.mu.Unlock()

	markStateDirty()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"load_scenario": scenario,
		"phase_loads":   loads,
		"base_load_w":   loads.Total(),
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

func simulatorUsesPlant(sim *Simulator) bool {
	if sim == nil || sim.Automation == nil {
		return false
	}
	sim.Automation.mu.RLock()
	defer sim.Automation.mu.RUnlock()
	return sim.Automation.Scenario == "physical"
}

func ensurePlant(sim *Simulator) *plant.Plant {
	sim.mu.Lock()
	defer sim.mu.Unlock()
	if sim.plant != nil {
		return sim.plant
	}
	pr := plant.DefaultParams()
	if sim.BatteryCapacityKWh > 0 {
		pr.BatteryCapacityWh = sim.BatteryCapacityKWh * 1000
	}
	sim.plant = plant.New(pr)
	return sim.plant
}

func controlFromRegisters(sim *Simulator) plant.Control {
	ratioPct := getSemanticValue(sim, "pv_limit_ratio")
	ratio := 1.0
	if ratioPct > 0 {
		ratio = ratioPct / 100.0
	}
	maxChgKW := getSemanticValue(sim, "max_charge_power")
	maxDisKW := getSemanticValue(sim, "max_discharge_power")
	return plant.Control{
		EMSMode:           int(getSemanticValue(sim, "grid_mode")),
		BatteryCmd:        int(getSemanticValue(sim, "battery_cmd")),
		BatterySetpointW:  getSemanticValue(sim, "battery_setpoint"),
		PVLimitW:          getSemanticValue(sim, "pv_cmd"),
		PVLimitEnable:     int(getSemanticValue(sim, "export_limit")) == plant.EnableOn,
		ExportLimitEnable: int(getSemanticValue(sim, "export_limit")) == plant.EnableOn,
		ActiveLimitEnable: int(getSemanticValue(sim, "pv_limit_enable")) == plant.EnableOn,
		ActiveLimitRatio:  ratio,
		MaxChargeW:        maxChgKW * 1000,
		MaxDischargeW:     maxDisKW * 1000,
		MinSOC:            getSemanticValue(sim, "battery_min_soc"),
		MaxSOC:            getSemanticValue(sim, "battery_max_soc"),
	}
}

func stepPhysicalPlant(sim *Simulator, simTime time.Time, scenario string, loadW float64) {
	if sim == nil || sim.Server == nil {
		return
	}
	p := ensurePlant(sim)
	if rated := getSemanticValue(sim, "nominal_power"); rated > 0 {
		p.Params.RatedACW = rated * 1000
		p.Params.RatedPVW = rated * 1000
	}
	if sim.BatteryCapacityKWh > 0 {
		p.Params.BatteryCapacityWh = sim.BatteryCapacityKWh * 1000
	}
	if soc := getSemanticValue(sim, "battery_soc"); soc > 0 {
		p.SeedSOC(soc)
	}
	if loadW <= 0 {
		loadW = plant.LoadForScenario(simTime, scenario)
	}
	out := p.Step(plant.Inputs{
		Time:    simTime,
		LoadW:   loadW,
		Weather: plant.WeatherForScenario(simTime, scenario),
		Control: controlFromRegisters(sim),
	})
	applyPlantOutputs(sim, out)
}

func applyPlantOutputs(sim *Simulator, out plant.Outputs) {
	for name, value := range plant.SemanticValues(out) {
		if name == "meter_power" || name == "meter_l1_power" || name == "meter_l2_power" || name == "meter_l3_power" {
			if isSimulatorSiteMeter(sim.ID) {
				continue
			}
		}
		setSemanticValue(sim, name, value)
	}
	sim.mu.Lock()
	sim.internalSOC = out.SOC
	sim.socInitialized = true
	sim.mu.Unlock()
}

func siteUsesPhysicalPlant(site *Site) bool {
	if site == nil {
		return false
	}
	site.mu.RLock()
	defer site.mu.RUnlock()
	return site.Scenario == "physical"
}

func ensureSitePlant(site *Site) plant.Model {
	site.mu.Lock()
	defer site.mu.Unlock()
	if site.sitePlant == nil {
		m, err := siteplant.Open(siteplant.Options{})
		if err != nil {
			log.Printf("[Site %d] siteplant: %v; using native Go plant", site.ID, err)
			m = plant.NewNativeSite()
		}
		site.sitePlant = m
		log.Printf("[Site %d] plant backend %s", site.ID, m.Backend())
	}
	return site.sitePlant
}

func collectSiteInverters(site *Site) []*Simulator {
	site.mu.RLock()
	meterID := site.MeterID
	ids := append([]int{}, site.SimulatorIDs...)
	site.mu.RUnlock()

	seen := map[int]struct{}{}
	var out []*Simulator
	add := func(id int) {
		if id == 0 {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		sim := getSimulator(id)
		if sim == nil {
			return
		}
		sim.mu.RLock()
		ok := sim.Running && sim.DeviceOn && sim.Category == "inverter" && sim.Protocol == "modbus" && sim.Server != nil
		sim.mu.RUnlock()
		if ok {
			out = append(out, sim)
		}
	}
	add(meterID)
	for _, id := range ids {
		add(id)
	}
	return out
}

// runSitePlantAndApply steps the site DAE on the site clock and writes
// member registers. The meter is written by calculateAndUpdateMeter.
func runSitePlantAndApply(site *Site, simDelta time.Duration) (gridW float64, ok bool) {
	if !siteUsesPhysicalPlant(site) {
		return 0, false
	}
	inverters := collectSiteInverters(site)
	if len(inverters) == 0 {
		return 0, false
	}
	site.mu.RLock()
	simTime := site.SimulatedTime
	loadW := site.SitePhaseLoads.Total()
	if loadW == 0 {
		loadW = site.BaseLoadW
	}
	scenario := site.Scenario
	site.mu.RUnlock()
	if simTime.IsZero() {
		simTime = time.Now()
	}

	model := ensureSitePlant(site)
	members := make([]plant.MemberStep, 0, len(inverters))
	exportLimit := false
	exportW := 0.0
	for _, sim := range inverters {
		key := strconv.Itoa(sim.ID)
		u := ensurePlant(sim)
		if rated := getSemanticValue(sim, "nominal_power"); rated > 0 {
			u.Params.RatedACW = rated * 1000
			u.Params.RatedPVW = rated * 1000
		}
		if sim.BatteryCapacityKWh > 0 {
			u.Params.BatteryCapacityWh = sim.BatteryCapacityKWh * 1000
		}
		if soc := getSemanticValue(sim, "battery_soc"); soc > 0 {
			u.SeedSOC(soc)
		}
		model.Attach(key, u)
		ctrl := controlFromRegisters(sim)
		members = append(members, plant.MemberStep{Key: key, Control: ctrl})
		if ctrl.ExportLimitEnable && ctrl.PVLimitW > 0 {
			exportLimit = true
			exportW = ctrl.PVLimitW
		}
	}

	res, err := model.StepSite(simDelta.Seconds(), plant.SiteStep{
		Time:              simTime,
		Weather:           plant.WeatherForScenario(simTime, scenario),
		LoadW:             loadW,
		ExportLimitEnable: exportLimit,
		ExportLimitW:      exportW,
		Members:           members,
	})
	if err != nil {
		log.Printf("[Site %d] plant step: %v", site.ID, err)
		return 0, false
	}
	for _, m := range res.Members {
		id, convErr := strconv.Atoi(m.Key)
		if convErr != nil {
			continue
		}
		sim := getSimulator(id)
		if sim == nil {
			continue
		}
		applyPlantOutputs(sim, m.Out)
	}
	return res.GridW, true
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
	sim.Automation.mu.RLock()
	scenario := sim.Automation.Scenario
	sim.Automation.mu.RUnlock()

	if scenario == "physical" {
		stepPhysicalPlant(sim, simTime, scenario, 0)
		return
	}

	hour := float64(simTime.Hour()) + float64(simTime.Minute())/60.0

	var solarPower, loadPower float64

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

	case "cloudy":
		// Cloudy - reduced solar with variations
		if hour >= 7 && hour <= 19 {
			progress := (hour - 7) / 12 * math.Pi
			solarPower = 2500 * math.Sin(progress) * (0.5 + 0.5*math.Sin(hour*3))
		}
		loadPower = 1000

	case "overcast":
		// Very cloudy - minimal solar
		if hour >= 8 && hour <= 18 {
			solarPower = 500 + 300*math.Sin(hour)
		}
		loadPower = 1200

	case "rainy":
		// Rainy - almost no solar, higher load
		solarPower = 100
		loadPower = 1500

	case "night":
		// Night mode - no solar
		solarPower = 0
		loadPower = 600

	case "peak_load":
		// High demand scenario
		if hour >= 6 && hour <= 20 {
			progress := (hour - 6) / 14 * math.Pi
			solarPower = 4000 * math.Sin(progress)
		}
		loadPower = 4500

	case "battery_test":
		// Battery cycling test (PV-only per inverter simulator rules)
		solarPower = 3000
		loadPower = 1500

	default:
		// Default sunny behavior
		if hour >= 6 && hour <= 20 {
			progress := (hour - 6) / 14 * math.Pi
			solarPower = 5000 * math.Sin(progress)
		}
		loadPower = 1000
	}

	// Update registers
	setSemanticValue(sim, "pv_power", solarPower)
	setSemanticValue(sim, "load_power", loadPower)

	// Grid frequency (50Hz with small variation)
	freq := 50.0 + 0.05*math.Sin(float64(simTime.Second())*0.1)
	setSemanticValue(sim, "grid_frequency", freq)

	// Derive PV string voltages and currents from total PV power
	if solarPower > 0 {
		stringPower := solarPower / 2.0
		stringVoltage := 300.0 + 50.0*math.Sin(float64(simTime.Hour())*0.5)
		stringCurrent := stringPower / stringVoltage
		setSemanticValue(sim, "pv1_voltage", stringVoltage)
		setSemanticValue(sim, "pv1_current", stringCurrent)
		setSemanticValue(sim, "pv2_voltage", stringVoltage)
		setSemanticValue(sim, "pv2_current", stringCurrent)
	} else {
		setSemanticValue(sim, "pv1_voltage", 0)
		setSemanticValue(sim, "pv1_current", 0)
		setSemanticValue(sim, "pv2_voltage", 0)
		setSemanticValue(sim, "pv2_current", 0)
	}

	// Derive battery voltage and current from battery power
	batteryPower := 0.0
	if sim.Server != nil {
		batteryPower = getBatterySignedPower(sim)
	}
	batteryVoltage := 48.0
	if batteryPower != 0 {
		batteryCurrent := math.Abs(batteryPower) / batteryVoltage
		setSemanticValue(sim, "battery_voltage", batteryVoltage)
		setSemanticValue(sim, "battery_current", batteryCurrent)
	} else {
		setSemanticValue(sim, "battery_voltage", batteryVoltage)
		setSemanticValue(sim, "battery_current", 0)
	}

	// Inverter temperature (scales with load)
	invTemp := 30.0 + 15.0*(solarPower/5000.0)
	setSemanticValue(sim, "inverter_temperature", invTemp)

	// Battery temperature (relatively stable)
	setSemanticValue(sim, "battery_temperature", 25.0+3.0*math.Sin(float64(simTime.Hour())*0.3))

	// Only update meter register if NOT a site meter (site controls it)
	if !isSimulatorSiteMeter(sim.ID) {
		meterPower := loadPower - solarPower + batteryPower
		setSemanticValue(sim, "meter_power", meterPower)
	}
}

// getSemanticValue reads a register by semantic name using the simulator's RegisterSet.
func getSemanticValue(sim *Simulator, name string) float64 {
	if sim == nil || sim.RegisterSet == nil || sim.Server == nil {
		return 0
	}
	def := sim.RegisterSet.Semantic[name]
	if def == nil {
		return 0
	}
	val, _ := sim.Server.ReadRegisterValue(def)
	return val
}

// setSemanticValue writes a register by semantic name using the simulator's RegisterSet.
func setSemanticValue(sim *Simulator, name string, value float64) {
	if sim == nil || sim.RegisterSet == nil || sim.Server == nil {
		return
	}
	def := sim.RegisterSet.Semantic[name]
	if def == nil {
		return
	}
	sim.Server.WriteRegisterValue(def, value)
}

// getSemanticDef returns the register definition for a semantic name, or nil.
func getSemanticDef(sim *Simulator, name string) *modbus.RegisterDef {
	if sim == nil || sim.RegisterSet == nil {
		return nil
	}
	return sim.RegisterSet.Semantic[name]
}

// accumulateSemanticValue adds delta to the current value of a semantic register.
func accumulateSemanticValue(sim *Simulator, name string, delta float64) {
	if sim == nil || sim.RegisterSet == nil || sim.Server == nil {
		return
	}
	def := sim.RegisterSet.Semantic[name]
	if def == nil {
		return
	}
	current, _ := sim.Server.ReadRegisterValue(def)
	sim.Server.WriteRegisterValue(def, current+delta)
}

// accumulateMeterEnergy writes import/export energy deltas to a meter using semantic names.
// It also writes to global-lookup addresses for backward compatibility (dual register sets).
func accumulateMeterEnergy(mSim *Simulator, importDelta, exportDelta float64) {
	if importDelta > 0 {
		accumulateSemanticValue(mSim, "total_import", importDelta)
		// Also accumulate on global-lookup addresses for backward compat
		if importDef := modbus.RegisterLookup[72]; importDef != nil {
			if mSim.RegisterSet == nil || mSim.RegisterSet.Lookup[72] == nil {
				current, _ := mSim.Server.ReadRegisterValue(importDef)
				mSim.Server.WriteRegisterValue(importDef, current+importDelta)
			}
		}
		if importDefWh := modbus.RegisterLookup[13036]; importDefWh != nil {
			if mSim.RegisterSet == nil || mSim.RegisterSet.Lookup[13036] == nil {
				current, _ := mSim.Server.ReadRegisterValue(importDefWh)
				mSim.Server.WriteRegisterValue(importDefWh, current+importDelta*1000)
			}
		}
	}
	if exportDelta > 0 {
		accumulateSemanticValue(mSim, "total_export", exportDelta)
		if exportDef := modbus.RegisterLookup[74]; exportDef != nil {
			if mSim.RegisterSet == nil || mSim.RegisterSet.Lookup[74] == nil {
				current, _ := mSim.Server.ReadRegisterValue(exportDef)
				mSim.Server.WriteRegisterValue(exportDef, current+exportDelta)
			}
		}
		if exportDefWh := modbus.RegisterLookup[13045]; exportDefWh != nil {
			if mSim.RegisterSet == nil || mSim.RegisterSet.Lookup[13045] == nil {
				current, _ := mSim.Server.ReadRegisterValue(exportDefWh)
				mSim.Server.WriteRegisterValue(exportDefWh, current+exportDelta*1000)
			}
		}
	}
}

func markStateDirty() {
	stateMu.Lock()
	stateDirty = true
	stateMu.Unlock()
}

func startStateSaver() {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			saveStateIfDirty()
		}
	}()
}

func saveStateIfDirty() {
	stateMu.Lock()
	if !stateDirty {
		stateMu.Unlock()
		return
	}
	stateDirty = false
	stateMu.Unlock()

	appState := buildStateSnapshot()
	if err := state.Save(appState); err != nil {
		log.Printf("Failed to save state: %v", err)
	}
}

func buildStateSnapshot() *state.AppState {
	appState := state.DefaultState()

	simulatorsMu.RLock()
	sims := make([]*Simulator, len(simulators))
	copy(sims, simulators)
	simulatorsMu.RUnlock()

	sitesMu.RLock()
	siteList := make([]*Site, len(sites))
	copy(siteList, sites)
	sitesMu.RUnlock()

	maxSimID := 0
	for _, sim := range sims {
		sim.mu.RLock()
		sState := state.SimulatorState{
			ID:                 sim.ID,
			Serial:             sim.Serial,
			Protocol:           sim.Protocol,
			Category:           sim.Category,
			SlaveID:            sim.SlaveID,
			Port:               sim.Port,
			Running:            sim.Running,
			SiteID:             sim.SiteID,
			DeviceOn:           sim.DeviceOn,
			BatteryCapacityKWh: sim.BatteryCapacityKWh,
			Profile:            sim.ProfilesManager.GetActiveName(),
			Automation: state.AutomationState{
				Enabled:        sim.Automation.Enabled,
				TimeMultiplier: sim.Automation.TimeMultiplier,
				Scenario:       sim.Automation.Scenario,
				SimulatedTime:  sim.Automation.SimulatedTime.Format(time.RFC3339),
			},
		}
		if sim.OCPPServer != nil {
			sState.OCPPURL = sim.OCPPServer.GetOCPPURL()
		}
		sim.mu.RUnlock()

		appState.Simulators = append(appState.Simulators, sState)
		if sim.ID > maxSimID {
			maxSimID = sim.ID
		}
	}

	maxSiteID := 0
	for _, site := range siteList {
		site.mu.RLock()
		p1Mode := P1ModeDirect
		if site.P1Buffer != nil {
			p1Mode = site.P1Buffer.Mode
		}
		appState.Sites = append(appState.Sites, state.SiteState{
			ID:             site.ID,
			Name:           site.Name,
			MeterID:        site.MeterID,
			MeterIDs:       append([]int{}, site.MeterIDs...),
			SimulatorIDs:   append([]int{}, site.SimulatorIDs...),
			BaseLoadW:      site.BaseLoadW,
			Enabled:        site.Enabled,
			FuseSize:       site.FuseSize,
			FuseProfile:    site.FuseProfile,
			PhaseFactors:   state.PhaseFactors{L1: site.PhaseFactors.L1, L2: site.PhaseFactors.L2, L3: site.PhaseFactors.L3},
			FuseBlown:      state.PhaseFuses{L1: site.FuseBlown.L1, L2: site.FuseBlown.L2, L3: site.FuseBlown.L3},
			BoundMeter:     site.BoundMeter,
			SimulatedTime:  site.SimulatedTime.Format(time.RFC3339),
			TimeMultiplier: site.TimeMultiplier,
			SimRunning:     site.SimRunning,
			Scenario:       site.Scenario,
			PhaseLoads:     state.PhaseLoads{L1: site.SitePhaseLoads.L1, L2: site.SitePhaseLoads.L2, L3: site.SitePhaseLoads.L3},
			LoadScenario:   site.LoadScenario,
			LoadLinked:     site.LoadLinked,
			P1Mode:         string(p1Mode),
		})
		site.mu.RUnlock()

		if site.ID > maxSiteID {
			maxSiteID = site.ID
		}
	}

	appState.NextSimulatorID = maxSimID + 1
	appState.NextSiteID = maxSiteID + 1
	return appState
}

func addWarning(message string) {
	stateWarningsMu.Lock()
	stateWarnings = append(stateWarnings, message)
	stateWarningsMu.Unlock()
}

func getWarnings() []string {
	stateWarningsMu.Lock()
	defer stateWarningsMu.Unlock()
	if len(stateWarnings) == 0 {
		return []string{}
	}
	copyWarnings := make([]string, len(stateWarnings))
	copy(copyWarnings, stateWarnings)
	return copyWarnings
}

func clearWarnings() {
	stateWarningsMu.Lock()
	stateWarnings = nil
	stateWarningsMu.Unlock()
}

func getBatterySignedPower(sim *Simulator) float64 {
	if sim == nil || sim.Server == nil {
		return 0
	}
	power := getSemanticValue(sim, "battery_power")

	// Check flags/cmd first — these are explicitly set by the UI and always correct.
	flags := getSemanticValue(sim, "battery_flags")
	if int(flags)&0x0004 != 0 {
		return -math.Abs(power)
	}
	if int(flags)&0x0002 != 0 {
		return math.Abs(power)
	}
	cmd := int(getSemanticValue(sim, "battery_cmd"))
	if cmd == 187 { // discharge
		return -math.Abs(power)
	}
	if cmd == 170 { // charge
		return math.Abs(power)
	}

	// Fallback: for signed register types (I16/I32), trust the register sign.
	// This handles external Modbus writes that set the register directly without cmd.
	if def := getSemanticDef(sim, "battery_power"); def != nil {
		if def.DataType == modbus.I16 || def.DataType == modbus.I32 {
			return power
		}
	}

	return math.Abs(power)
}

func getBatteryPowerAndDirection(sim *Simulator) (float64, string) {
	signed := getBatterySignedPower(sim)
	if signed > 0 {
		return math.Abs(signed), "charging"
	}
	if signed < 0 {
		return math.Abs(signed), "discharging"
	}
	return 0, "idle"
}

func clampBatterySOC(value, minSOC, maxSOC float64) float64 {
	if maxSOC <= 0 || maxSOC > 100 {
		maxSOC = 100
	}
	if minSOC < 0 || minSOC >= maxSOC {
		minSOC = 0
	}
	if value > maxSOC {
		return maxSOC
	}
	if value < minSOC {
		return minSOC
	}
	return value
}

func updateBatterySOC(sim *Simulator, simHours, capacityKWh float64) {
	if sim == nil || sim.Server == nil || simHours <= 0 {
		return
	}

	power := getBatterySignedPower(sim)
	if power == 0 {
		return
	}

	if capacityKWh <= 0 {
		capacityKWh = defaultBatteryCapacityKWh
	}
	capacityWh := capacityKWh * 1000.0
	if capacityWh <= 0 {
		return
	}

	// Use high-precision internal accumulator to avoid register truncation.
	// Many devices use U16 Scale 1.0 for SOC which can only store integers,
	// causing tiny per-tick deltas to be lost or amplified by truncation.
	sim.mu.Lock()
	if !sim.socInitialized {
		sim.internalSOC = getSemanticValue(sim, "battery_soc")
		sim.socInitialized = true
	}
	currentSOC := sim.internalSOC
	sim.mu.Unlock()

	maxSOC := getSemanticValue(sim, "battery_max_soc")
	minSOC := getSemanticValue(sim, "battery_min_soc")

	deltaSOC := (power * simHours / capacityWh) * 100.0
	nextSOC := clampBatterySOC(currentSOC+deltaSOC, minSOC, maxSOC)

	sim.mu.Lock()
	sim.internalSOC = nextSOC
	sim.mu.Unlock()
	setSemanticValue(sim, "battery_soc", nextSOC)

	energyWh := math.Abs(power) * simHours
	if energyWh == 0 {
		return
	}
	if power > 0 {
		if chargeDef := getSemanticDef(sim, "total_charge"); chargeDef != nil {
			currentCharge, _ := sim.Server.ReadRegisterValue(chargeDef)
			sim.Server.WriteRegisterValue(chargeDef, currentCharge+energyWh)
		}
	} else if power < 0 {
		if dischargeDef := getSemanticDef(sim, "total_discharge"); dischargeDef != nil {
			currentDischarge, _ := sim.Server.ReadRegisterValue(dischargeDef)
			sim.Server.WriteRegisterValue(dischargeDef, currentDischarge+energyWh)
		}
	}
}

// accumulatePVEnergy adds PV generation energy over the given time period.
func accumulatePVEnergy(sim *Simulator, simHours float64) {
	if sim == nil || sim.Server == nil || simHours <= 0 {
		return
	}
	pvPower := getSemanticValue(sim, "pv_power")
	if pvPower <= 0 {
		return
	}
	energyWh := pvPower * simHours
	accumulateSemanticValue(sim, "total_pv_gen", energyWh)
}

// updateBatteryFlags sets the system state flags register based on current battery power direction.
// Bit 0x0002 = charging, bit 0x0004 = discharging, 0 = idle.
func updateBatteryFlags(sim *Simulator) {
	if sim == nil || sim.Server == nil {
		return
	}
	power := getSemanticValue(sim, "battery_power")
	if power == 0 {
		setSemanticValue(sim, "battery_flags", 0)
		return
	}

	// Derive direction from battery_cmd (user intent), not from flags (avoids feedback loop).
	cmd := int(getSemanticValue(sim, "battery_cmd"))
	var flags float64
	switch cmd {
	case 170: // charge
		flags = 2
	case 187: // discharge
		flags = 4
	default:
		// No cmd set — use register sign for signed types, else assume charging
		if def := getSemanticDef(sim, "battery_power"); def != nil {
			if def.DataType == modbus.I16 || def.DataType == modbus.I32 {
				if power > 0 {
					flags = 2
				} else {
					flags = 4
				}
			}
		}
	}
	setSemanticValue(sim, "battery_flags", flags)
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

func isSimulatorMeter(simID int) bool {
	sitesMu.RLock()
	defer sitesMu.RUnlock()
	for _, site := range sites {
		site.mu.RLock()
		isMeter := site.MeterID == simID
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

	// Non-running simulators don't contribute power
	if !sim.Running || !sim.DeviceOn {
		return 0
	}

	switch sim.Protocol {
	case "modbus":
		if sim.Server == nil {
			return 0
		}
		// For inverters: contribution is -pv + battery (generation/consumption only).
		// The house load comes from the site's phase loads, not the inverter register,
		// to avoid double-counting.
		if sim.Category == "inverter" {
			pv := getSemanticValue(sim, "pv_power")
			batteryPower := getBatterySignedPower(sim)
			return -pv + batteryPower
		}
		// For other devices (meters, etc.): load - pv + battery
		load := getSemanticValue(sim, "load_power")
		pv := getSemanticValue(sim, "pv_power")
		batteryPower := getBatterySignedPower(sim)
		return load - pv + batteryPower

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
		// OCPP totalPower is already calculated by GetState() - always positive (consumption)
		if totalPower, ok := state["totalPower"].(float64); ok {
			return totalPower
		}
		return 0
	}

	return 0
}

// startSiteAggregation starts the aggregation loop for a site
func startSiteAggregation(site *Site) {
	ensureSiteMetersRunning(site)

	site.mu.Lock()
	if site.stopChan != nil {
		site.mu.Unlock()
		return // Already running
	}
	site.Enabled = true
	site.stopChan = make(chan struct{})
	stopChan := site.stopChan
	site.mu.Unlock()

	log.Printf("[Site %d] Aggregation started", site.ID)

	go func() {
		ticker := time.NewTicker(getSiteTickInterval())
		defer ticker.Stop()
		lastTick := time.Now()

		for {
			select {
			case <-stopChan:
				log.Printf("[Site %d] Aggregation stopped", site.ID)
				return
			case <-ticker.C:
				now := time.Now()
				realDelta := now.Sub(lastTick)
				lastTick = now

				var simDelta time.Duration
				site.mu.Lock()
				if site.SimulatedTime.IsZero() {
					site.SimulatedTime = now
				}
				multiplier := site.TimeMultiplier
				if multiplier < 0 {
					multiplier = 0
				}
				simDelta = time.Duration(float64(realDelta) * multiplier)
				site.SimulatedTime = site.SimulatedTime.Add(simDelta)
				site.mu.Unlock()

				calculateAndUpdateMeter(site, simDelta)
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
	site.stopChan = nil
}

func ensureSiteMetersRunning(site *Site) {
	site.mu.RLock()
	primaryID := site.MeterID
	meterIDs := make([]int, len(site.MeterIDs))
	copy(meterIDs, site.MeterIDs)
	site.mu.RUnlock()

	seen := map[int]struct{}{}
	if primaryID != 0 {
		seen[primaryID] = struct{}{}
	}
	for _, id := range meterIDs {
		seen[id] = struct{}{}
	}

	for id := range seen {
		meterSim := getSimulator(id)
		if meterSim == nil || meterSim.Protocol != "modbus" {
			continue
		}
		if err := startSimulatorServer(meterSim); err != nil {
			log.Printf("[Site %d] Failed to start meter %d server: %v", site.ID, id, err)
		}
	}
}

// startSiteSimulation starts the data generation loop for all devices in a site
func startSiteSimulation(site *Site) error {
	site.mu.Lock()
	if site.SimRunning {
		site.mu.Unlock()
		return nil // Already running
	}

	// Check if aggregation is enabled (required for simulation)
	if !site.Enabled {
		site.mu.Unlock()
		return fmt.Errorf("aggregation must be enabled to start simulation")
	}

	site.SimRunning = true
	site.simStopChan = make(chan struct{})
	simStopChan := site.simStopChan
	siteID := site.ID
	site.mu.Unlock()

	log.Printf("[Site %d] Simulation started", siteID)

	go runSiteSimulationLoop(site, simStopChan)
	return nil
}

// stopSiteSimulation stops the data generation loop for a site
func stopSiteSimulation(site *Site) {
	site.mu.Lock()
	defer site.mu.Unlock()

	if !site.SimRunning {
		return
	}

	if site.simStopChan != nil {
		close(site.simStopChan)
	}
	site.SimRunning = false
	log.Printf("[Site %d] Simulation stopped", site.ID)
}

// runSiteSimulationLoop is the main simulation loop that generates values for all devices
func runSiteSimulationLoop(site *Site, stopChan chan struct{}) {
	ticker := time.NewTicker(getSiteTickInterval())
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			site.mu.Lock()
			if !site.SimRunning {
				site.mu.Unlock()
				return
			}

			simTime := site.SimulatedTime
			if simTime.IsZero() {
				simTime = time.Now()
			}
			scenario := site.Scenario
			loadScenario := site.LoadScenario
			simIDs := make([]int, len(site.SimulatorIDs))
			copy(simIDs, site.SimulatorIDs)
			meterID := site.MeterID

			// Update load based on scenario if one is active (skip for static)
			if loadScenario != "" && loadScenario != "static" {
				if ls, ok := LoadScenarios[loadScenario]; ok {
					loads := ls.GetLoadForTime(simTime.Hour())
					site.SitePhaseLoads = loads
					// Also update BaseLoadW for backward compat
					site.BaseLoadW = loads.Total()
				}
			}
			site.mu.Unlock()

			if scenario != "physical" {
				// Generate values for each device in the site
				for _, simID := range simIDs {
					sim := getSimulator(simID)
					if sim == nil {
						continue
					}

					sim.mu.RLock()
					running := sim.Running
					deviceOn := sim.DeviceOn
					category := sim.Category
					sim.mu.RUnlock()

					if category != "inverter" {
						continue
					}
					if running && deviceOn {
						sim.Automation.mu.RLock()
						simScenario := sim.Automation.Scenario
						sim.Automation.mu.RUnlock()
						if simScenario == "" {
							simScenario = scenario
						}
						if simScenario == "physical" {
							stepPhysicalPlant(sim, simTime, simScenario, 0)
						} else {
							generateValuesForSite(sim, simTime, simScenario)
						}
					}
				}

				// Also generate for the meter if it's an inverter
				meterSim := getSimulator(meterID)
				if meterSim != nil {
					meterSim.mu.RLock()
					running := meterSim.Running
					category := meterSim.Category
					meterSim.mu.RUnlock()

					if running && category == "inverter" {
						meterSim.Automation.mu.RLock()
						meterScenario := meterSim.Automation.Scenario
						meterSim.Automation.mu.RUnlock()
						if meterScenario == "" {
							meterScenario = scenario
						}
						if meterScenario == "physical" {
							stepPhysicalPlant(meterSim, simTime, meterScenario, 0)
						} else {
							generateValuesForSite(meterSim, simTime, meterScenario)
						}
					}
				}
			}
		}
	}
}

// generateValuesForSite generates values for a simulator using site's simulation time
func generateValuesForSite(sim *Simulator, simTime time.Time, scenario string) {
	if sim.Category != "inverter" {
		return
	}
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

	if scenario == "physical" {
		stepPhysicalPlant(sim, simTime, scenario, 0)
		return
	}

	// Modbus inverter simulation
	hour := float64(simTime.Hour()) + float64(simTime.Minute())/60.0

	var solarPower, loadPower float64

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

	case "cloudy":
		// Cloudy - reduced solar with variations
		if hour >= 7 && hour <= 19 {
			progress := (hour - 7) / 12 * math.Pi
			solarPower = 2500 * math.Sin(progress) * (0.5 + 0.5*math.Sin(hour*3))
		}
		loadPower = 1000

	case "overcast":
		// Very cloudy - minimal solar
		if hour >= 8 && hour <= 18 {
			solarPower = 500 + 300*math.Sin(hour)
		}
		loadPower = 1200

	case "rainy":
		// Rainy - almost no solar, higher load
		solarPower = 100
		loadPower = 1500

	case "night":
		// Night mode - no solar
		solarPower = 0
		loadPower = 600

	case "peak_load":
		// High demand scenario
		if hour >= 6 && hour <= 20 {
			progress := (hour - 6) / 14 * math.Pi
			solarPower = 4000 * math.Sin(progress)
		}
		loadPower = 4000

	default: // "sunny" as default
		if hour >= 6 && hour <= 20 {
			progress := (hour - 6) / 14 * math.Pi
			solarPower = 5000 * math.Sin(progress)
		}
		loadPower = 800
	}

	// Update registers
	if sim.Server != nil {
		setSemanticValue(sim, "pv_power", solarPower)
		setSemanticValue(sim, "load_power", loadPower)

		// Grid frequency (50Hz with small variation)
		freq := 50.0 + 0.05*math.Sin(float64(simTime.Second())*0.1)
		setSemanticValue(sim, "grid_frequency", freq)

		// Derive PV string voltages and currents from total PV power
		// Assume 2 MPPT strings sharing power equally, typical string voltage ~350V
		if solarPower > 0 {
			stringPower := solarPower / 2.0
			stringVoltage := 300.0 + 50.0*math.Sin(float64(simTime.Hour())*0.5) // 300-350V range
			stringCurrent := stringPower / stringVoltage
			setSemanticValue(sim, "pv1_voltage", stringVoltage)
			setSemanticValue(sim, "pv1_current", stringCurrent)
			setSemanticValue(sim, "pv2_voltage", stringVoltage)
			setSemanticValue(sim, "pv2_current", stringCurrent)
		} else {
			setSemanticValue(sim, "pv1_voltage", 0)
			setSemanticValue(sim, "pv1_current", 0)
			setSemanticValue(sim, "pv2_voltage", 0)
			setSemanticValue(sim, "pv2_current", 0)
		}

		// Derive battery voltage and current from battery power
		batteryPower := getSemanticValue(sim, "battery_power")
		batteryVoltage := 48.0 // Nominal battery voltage
		if batteryPower != 0 {
			batteryCurrent := math.Abs(batteryPower) / batteryVoltage
			setSemanticValue(sim, "battery_voltage", batteryVoltage)
			setSemanticValue(sim, "battery_current", batteryCurrent)
		} else {
			setSemanticValue(sim, "battery_voltage", batteryVoltage)
			setSemanticValue(sim, "battery_current", 0)
		}

		// Inverter temperature (scales with load)
		invTemp := 30.0 + 15.0*(solarPower/5000.0)
		setSemanticValue(sim, "inverter_temperature", invTemp)

		// Battery temperature (relatively stable)
		setSemanticValue(sim, "battery_temperature", 25.0+3.0*math.Sin(float64(simTime.Hour())*0.3))
	}
}

// deriveInverterValues computes PV string V/I, battery V/I, and temperatures
// from the current register values. Called from the aggregation loop so derived
// values are always populated when power is flowing.
func deriveInverterValues(sim *Simulator, simTime time.Time) {
	pvPower := getSemanticValue(sim, "pv_power")
	if pvPower > 0 {
		stringPower := pvPower / 2.0
		stringVoltage := 300.0 + 50.0*math.Sin(float64(simTime.Hour())*0.5)
		stringCurrent := stringPower / stringVoltage
		setSemanticValue(sim, "pv1_voltage", stringVoltage)
		setSemanticValue(sim, "pv1_current", stringCurrent)
		setSemanticValue(sim, "pv2_voltage", stringVoltage)
		setSemanticValue(sim, "pv2_current", stringCurrent)
	} else {
		setSemanticValue(sim, "pv1_voltage", 0)
		setSemanticValue(sim, "pv1_current", 0)
		setSemanticValue(sim, "pv2_voltage", 0)
		setSemanticValue(sim, "pv2_current", 0)
	}

	batteryPower := getBatterySignedPower(sim)
	batteryVoltage := 48.0
	if batteryPower != 0 {
		setSemanticValue(sim, "battery_voltage", batteryVoltage)
		setSemanticValue(sim, "battery_current", math.Abs(batteryPower)/batteryVoltage)
	} else {
		setSemanticValue(sim, "battery_voltage", batteryVoltage)
		setSemanticValue(sim, "battery_current", 0)
	}

	invTemp := 30.0 + 15.0*(pvPower/5000.0)
	setSemanticValue(sim, "inverter_temperature", invTemp)
	setSemanticValue(sim, "battery_temperature", 25.0+3.0*math.Sin(float64(simTime.Hour())*0.3))
}

// calculateAndUpdateMeter calculates total power and updates the meter
func calculateAndUpdateMeter(site *Site, simDelta time.Duration) {
	site.mu.RLock()
	meterID := site.MeterID
	meterIDs := make([]int, len(site.MeterIDs))
	copy(meterIDs, site.MeterIDs)
	simIDs := make([]int, len(site.SimulatorIDs))
	copy(simIDs, site.SimulatorIDs)
	baseLoad := site.BaseLoadW
	p1Buffer := site.P1Buffer
	phaseFactors := site.PhaseFactors
	sitePhaseLoads := site.SitePhaseLoads
	site.mu.RUnlock()

	// Get primary meter
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
	meterCapacityKWh := meterSim.BatteryCapacityKWh
	meterSim.mu.RUnlock()

	// Start with per-phase loads from SitePhaseLoads (fallback to base load if empty)
	phasePowers := PhasePowers{
		L1: sitePhaseLoads.L1,
		L2: sitePhaseLoads.L2,
		L3: sitePhaseLoads.L3,
	}
	if sitePhaseLoads.Total() == 0 && baseLoad > 0 {
		phasePowers = PhasePowers{
			L1: baseLoad * phaseFactors.L1,
			L2: baseLoad * phaseFactors.L2,
			L3: baseLoad * phaseFactors.L3,
		}
	}

	plantGrid, usedPlant := runSitePlantAndApply(site, simDelta)
	if usedPlant {
		phasePowers = PhasePowers{
			L1: plantGrid * phaseFactors.L1,
			L2: plantGrid * phaseFactors.L2,
			L3: plantGrid * phaseFactors.L3,
		}
	} else if meterCategory == "inverter" {
		pvPower := getSemanticValue(meterSim, "pv_power") // PV power
		batteryPower := getBatterySignedPower(meterSim)   // Signed battery power

		// Distribute PV generation (reduces load) and battery across phases
		pvBatteryNet := -pvPower + batteryPower
		phasePowers.L1 += pvBatteryNet * phaseFactors.L1
		phasePowers.L2 += pvBatteryNet * phaseFactors.L2
		phasePowers.L3 += pvBatteryNet * phaseFactors.L3
	}

	if !usedPlant {
		var otherPower float64
		for _, simID := range simIDs {
			// Skip meter simulator to avoid double-counting its own power
			if simID == meterID {
				continue
			}
			sim := getSimulator(simID)
			if sim == nil {
				continue
			}
			otherPower += getPowerContribution(sim)
		}

		phasePowers.L1 += otherPower * phaseFactors.L1
		phasePowers.L2 += otherPower * phaseFactors.L2
		phasePowers.L3 += otherPower * phaseFactors.L3
	}

	// Update fuse state based on overload profile (before applying blown phases)
	fuseBlown := updateFuseState(site, phasePowers, simDelta)

	// Apply fuse state: blown phases measure zero power
	if fuseBlown.L1 {
		phasePowers.L1 = 0
	}
	if fuseBlown.L2 {
		phasePowers.L2 = 0
	}
	if fuseBlown.L3 {
		phasePowers.L3 = 0
	}

	// Calculate total power from phase powers
	totalPower := phasePowers.L1 + phasePowers.L2 + phasePowers.L3

	// Update BaseLoadW for backward compatibility (used for display)
	// This is the sum of SitePhaseLoads
	_ = baseLoad // Kept for backward compat in API responses

	// Calculate energy delta based on simulated time
	simHours := simDelta.Hours()
	if simHours < 0 {
		simHours = 0
	}
	energyDeltaKWh := math.Abs(totalPower) / 1000.0 * simHours
	energyDeltaWh := math.Abs(totalPower) * simHours

	if simHours > 0 && !usedPlant {
		// Update battery SOC and PV energy for all inverters using the central clock
		if meterCategory == "inverter" && meterSim.Server != nil && !simulatorUsesPlant(meterSim) {
			updateBatterySOC(meterSim, simHours, meterCapacityKWh)
			accumulatePVEnergy(meterSim, simHours)
			updateBatteryFlags(meterSim)
		}
		for _, simID := range simIDs {
			if simID == meterID {
				continue
			}
			sim := getSimulator(simID)
			if sim == nil {
				continue
			}
			sim.mu.RLock()
			running := sim.Running
			deviceOn := sim.DeviceOn
			category := sim.Category
			protocol := sim.Protocol
			server := sim.Server
			capacityKWh := sim.BatteryCapacityKWh
			sim.mu.RUnlock()

			if !running || !deviceOn || category != "inverter" || protocol != "modbus" || server == nil {
				continue
			}
			if simulatorUsesPlant(sim) {
				continue
			}
			updateBatterySOC(sim, simHours, capacityKWh)
			accumulatePVEnergy(sim, simHours)
			updateBatteryFlags(sim)
		}
	}

	// Sync site load to inverter load_power registers so the inverter dashboard
	// reflects the site-defined load (the site is the source of truth for load).
	siteLoad := sitePhaseLoads.Total()
	if siteLoad == 0 {
		siteLoad = baseLoad
	}
	if meterCategory == "inverter" && meterSim.Server != nil {
		setSemanticValue(meterSim, "load_power", siteLoad)
	}
	for _, simID := range simIDs {
		if simID == meterID {
			continue
		}
		sim := getSimulator(simID)
		if sim == nil {
			continue
		}
		sim.mu.RLock()
		isInverter := sim.Running && sim.Category == "inverter" && sim.Protocol == "modbus" && sim.Server != nil
		sim.mu.RUnlock()
		if isInverter {
			setSemanticValue(sim, "load_power", siteLoad)
		}
	}

	// Derive PV string, battery, and temperature values for all inverters in the site.
	// This runs in the aggregation loop (always-on) so derived values are populated
	// even when site simulation is not explicitly started.
	site.mu.RLock()
	simTime := site.SimulatedTime
	site.mu.RUnlock()
	if simTime.IsZero() {
		simTime = time.Now()
	}

	if meterCategory == "inverter" && meterSim.Server != nil {
		deriveInverterValues(meterSim, simTime)
	}
	for _, simID := range simIDs {
		if simID == meterID {
			continue
		}
		sim := getSimulator(simID)
		if sim == nil {
			continue
		}
		sim.mu.RLock()
		running := sim.Running
		category := sim.Category
		protocol := sim.Protocol
		server := sim.Server
		sim.mu.RUnlock()
		if running && category == "inverter" && protocol == "modbus" && server != nil {
			deriveInverterValues(sim, simTime)
		}
	}

	// Handle P1 interval mode
	if p1Buffer == nil || p1Buffer.Mode == P1ModeDirect {
		// Direct mode: write immediately to all meters
		for _, mID := range meterIDs {
			mSim := getSimulator(mID)
			if mSim != nil && mSim.Protocol == "modbus" && mSim.Server != nil && mSim.Running {
				writeMeterRegisters(mSim, totalPower, phasePowers, fuseBlown)
				updateMeterEnergy(mSim, totalPower, energyDeltaKWh, energyDeltaWh)
			}
		}
		// Also write to primary meter if not in meterIDs (backward compat)
		if len(meterIDs) == 0 {
			writeMeterRegisters(meterSim, totalPower, phasePowers, fuseBlown)
			updateMeterEnergy(meterSim, totalPower, energyDeltaKWh, energyDeltaWh)
		}
		// Propagate meter values to inverter sims that have built-in meter registers
		for _, simID := range simIDs {
			sim := getSimulator(simID)
			if sim == nil || sim.Protocol != "modbus" || sim.Server == nil || !sim.Running {
				continue
			}
			if sim.Category == "inverter" && sim.RegisterSet != nil && sim.RegisterSet.Semantic["meter_power"] != nil {
				writeMeterRegisters(sim, totalPower, phasePowers, fuseBlown)
			}
		}
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

			// Write instant power values to all meters
			for _, mID := range meterIDs {
				mSim := getSimulator(mID)
				if mSim == nil || mSim.Protocol != "modbus" || mSim.Server == nil || !mSim.Running {
					continue
				}
				writeMeterRegisters(mSim, instantPower, instantPhasePowers, fuseBlown)
				accumulateMeterEnergy(mSim, importDelta, exportDelta)
			}
			// Also write to primary meter if not in meterIDs (backward compat)
			if len(meterIDs) == 0 {
				writeMeterRegisters(meterSim, instantPower, instantPhasePowers, fuseBlown)
				accumulateMeterEnergy(meterSim, importDelta, exportDelta)
			}
			// Propagate meter values to inverter sims that have built-in meter registers
			for _, simID := range simIDs {
				sim := getSimulator(simID)
				if sim == nil || sim.Protocol != "modbus" || sim.Server == nil || !sim.Running {
					continue
				}
				if sim.Category == "inverter" && sim.RegisterSet != nil && sim.RegisterSet.Semantic["meter_power"] != nil {
					writeMeterRegisters(sim, instantPower, instantPhasePowers, fuseBlown)
				}
			}
		} else {
			site.mu.Unlock()
		}
	}
}

// writeMeterRegisters writes power values to meter registers.
// Writes to semantic names on the meter's RegisterSet, and also to global-lookup
// addresses for dual-format support (SDM630 + Sungrow on same meter).
func writeMeterRegisters(meterSim *Simulator, totalPower float64, phasePowers PhasePowers, fuseBlown PhaseFuses) {
	if fuseBlown.L1 {
		phasePowers.L1 = 0
	}
	if fuseBlown.L2 {
		phasePowers.L2 = 0
	}
	if fuseBlown.L3 {
		phasePowers.L3 = 0
	}
	totalPower = phasePowers.L1 + phasePowers.L2 + phasePowers.L3

	// Write total power via semantic name (hits the correct address for any device)
	setSemanticValue(meterSim, "meter_power", totalPower)

	// Write per-phase powers via semantic names
	setSemanticValue(meterSim, "meter_l1_power", phasePowers.L1)
	setSemanticValue(meterSim, "meter_l2_power", phasePowers.L2)
	setSemanticValue(meterSim, "meter_l3_power", phasePowers.L3)

	// Calculate per-phase currents from power (I = P / V, assuming 230V)
	l1Current := math.Abs(phasePowers.L1) / 230.0
	l2Current := math.Abs(phasePowers.L2) / 230.0
	l3Current := math.Abs(phasePowers.L3) / 230.0

	setSemanticValue(meterSim, "meter_l1_current", l1Current)
	setSemanticValue(meterSim, "meter_l2_current", l2Current)
	setSemanticValue(meterSim, "meter_l3_current", l3Current)

	// Voltages
	l1Voltage := 230.0
	l2Voltage := 230.0
	l3Voltage := 230.0
	if fuseBlown.L1 {
		l1Voltage = 0
	}
	if fuseBlown.L2 {
		l2Voltage = 0
	}
	if fuseBlown.L3 {
		l3Voltage = 0
	}

	setSemanticValue(meterSim, "meter_l1_voltage", l1Voltage)
	setSemanticValue(meterSim, "meter_l2_voltage", l2Voltage)
	setSemanticValue(meterSim, "meter_l3_voltage", l3Voltage)
	setSemanticValue(meterSim, "grid_frequency", 50.0)

	// Dual-format: also write to global-lookup addresses not covered by the meter's RegisterSet.
	// This ensures both SDM630 and Sungrow addresses are populated on the server.
	writeMeterGlobalFallback(meterSim, totalPower, phasePowers, l1Current, l2Current, l3Current, l1Voltage, l2Voltage, l3Voltage)
}

// writeMeterGlobalFallback writes meter values to global-lookup addresses not in the meter's RegisterSet.
func writeMeterGlobalFallback(meterSim *Simulator, totalPower float64, phasePowers PhasePowers, l1Current, l2Current, l3Current, l1Voltage, l2Voltage, l3Voltage float64) {
	writeIfNotInSet := func(addr uint16, val float64) {
		if meterSim.RegisterSet != nil && meterSim.RegisterSet.Lookup[addr] != nil {
			return // Already written via semantic
		}
		if def := modbus.RegisterLookup[addr]; def != nil {
			meterSim.Server.WriteRegisterValue(def, val)
		}
	}

	// Total power: SDM630 addr 52, Sungrow addr 5600
	writeIfNotInSet(52, totalPower)
	writeIfNotInSet(5600, totalPower)

	// Per-phase powers: SDM630 addrs 12/14/16, Sungrow addrs 5602/5604/5606
	writeIfNotInSet(12, phasePowers.L1)
	writeIfNotInSet(14, phasePowers.L2)
	writeIfNotInSet(16, phasePowers.L3)
	writeIfNotInSet(5602, phasePowers.L1)
	writeIfNotInSet(5604, phasePowers.L2)
	writeIfNotInSet(5606, phasePowers.L3)

	// Per-phase currents: SDM630 addrs 6/8/10, Sungrow addrs 5743/5744/5745
	writeIfNotInSet(6, l1Current)
	writeIfNotInSet(8, l2Current)
	writeIfNotInSet(10, l3Current)
	writeIfNotInSet(5743, l1Current)
	writeIfNotInSet(5744, l2Current)
	writeIfNotInSet(5745, l3Current)

	// Per-phase voltages: SDM630 addrs 0/2/4, Sungrow addrs 5740/5741/5742
	writeIfNotInSet(0, l1Voltage)
	writeIfNotInSet(2, l2Voltage)
	writeIfNotInSet(4, l3Voltage)
	writeIfNotInSet(5740, l1Voltage)
	writeIfNotInSet(5741, l2Voltage)
	writeIfNotInSet(5742, l3Voltage)

	// Frequency: SDM630 addr 70
	writeIfNotInSet(70, 50.0)
}

// updateMeterEnergy updates energy registers for direct mode
func updateMeterEnergy(meterSim *Simulator, totalPower, energyDeltaKWh, energyDeltaWh float64) {
	if totalPower > 0 {
		accumulateMeterEnergy(meterSim, energyDeltaKWh, 0)
	} else if totalPower < 0 {
		accumulateMeterEnergy(meterSim, 0, energyDeltaKWh)
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
// If meterID is 0, the site gets a dedicated bound SDM630 energy meter.
// If meterID is provided, that existing simulator will be used as the meter.
func createSite(name string, meterID int, simulatorIDs []int, baseLoadW float64) (*Site, error) {
	var meterSim *Simulator
	boundMeter := false

	if meterID == 0 {
		// Create a dedicated SDM630 energy meter as the site's bound meter
		var err error
		meterSim, err = createSimulator("energy_meter")
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

		boundMeter = true
		log.Printf("[Site] Created bound SDM630 meter (ID: %d, Serial: %s)", meterSim.ID, meterSim.Serial)
	} else {
		// Use existing simulator as meter
		meterSim = getSimulator(meterID)
		if meterSim == nil {
			return nil, fmt.Errorf("meter simulator %d not found", meterID)
		}
		if meterSim.Protocol != "modbus" {
			return nil, fmt.Errorf("meter simulator %d must be Modbus", meterID)
		}
		// Ensure meter isn't already assigned to another site
		sitesMu.RLock()
		for _, existingSite := range sites {
			if existingSite.MeterID == meterID {
				sitesMu.RUnlock()
				return nil, fmt.Errorf("meter simulator %d is already in site %d", meterID, existingSite.ID)
			}
			for _, existingMeterID := range existingSite.MeterIDs {
				if existingMeterID == meterID {
					sitesMu.RUnlock()
					return nil, fmt.Errorf("meter simulator %d is already in site %d", meterID, existingSite.ID)
				}
			}
		}
		sitesMu.RUnlock()
	}

	// Validate simulators exist
	for _, simID := range simulatorIDs {
		if getSimulator(simID) == nil {
			return nil, fmt.Errorf("simulator %d not found", simID)
		}
	}

	// Remove meter from simulator list if included
	filteredSimIDs := make([]int, 0, len(simulatorIDs))
	for _, simID := range simulatorIDs {
		if meterSim != nil && simID == meterSim.ID {
			continue
		}
		filteredSimIDs = append(filteredSimIDs, simID)
	}

	// Check simulators aren't already in another site
	sitesMu.RLock()
	for _, existingSite := range sites {
		for _, simID := range filteredSimIDs {
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

	simulatedTime := time.Now()
	loadScenario := defaultLoadScenarioKey
	loads := PhaseLoads{L1: 333, L2: 333, L3: 334}
	if ls, ok := LoadScenarios[loadScenario]; ok {
		loads = ls.GetLoadForTime(simulatedTime.Hour())
	}

	site := &Site{
		ID:           id,
		Name:         name,
		MeterID:      meterSim.ID,
		MeterIDs:     []int{meterSim.ID}, // Initialize with primary meter
		SimulatorIDs: filteredSimIDs,
		BaseLoadW:    loads.Total(),
		Enabled:      false,
		FuseSize:     16, // Default 16A fuse
		FuseProfile:  FuseProfileManual,
		PhaseFactors: PhaseFactors{
			L1: 0.333,
			L2: 0.333,
			L3: 0.334,
		},
		FuseBlown: PhaseFuses{
			L1: false,
			L2: false,
			L3: false,
		},
		BoundMeter: boundMeter,
		P1Buffer:   newP1IntervalBuffer(),
		// Simulation state initialization
		SimulatedTime:  simulatedTime,
		TimeMultiplier: 1.0,
		SimRunning:     false,
		Scenario:       "sunny",
		// Site Load initialization (based on default profile)
		SitePhaseLoads: loads,
		LoadScenario:   loadScenario,
		LoadLinked:     true,
	}
	sites = append(sites, site)
	sitesMu.Unlock()

	log.Printf("[Site %d] Created site '%s' with bound meter %d", id, name, meterSim.ID)
	startSiteAggregation(site)
	markStateDirty()
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
			markStateDirty()
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
	markStateDirty()
}
