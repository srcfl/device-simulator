package mqtt

import (
	"fmt"
	"log"
	"sync"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"github.com/srcfl/device-simulator/internal/device"
)

// Server represents an MQTT broker with V2X charger simulation
type Server struct {
	mu           sync.RWMutex
	id           int
	serialNumber string
	broker       *mqtt.Server
	state        *V2XState
	logs         []device.LogEntry
	maxLogs      int
	port         int
	running      bool
	stopChan     chan struct{}
}

// V2XState holds the V2X charger state (matches Ambibox MQTT structure)
type V2XState struct {
	// Connection & identity
	Connected    bool   `json:"connected"`
	Version      string `json:"version"`
	SerialNumber string `json:"serialNumber"`

	// Session state
	SessionState   string `json:"sessionState"`
	ControlMode    string `json:"controlMode"`
	EVConnected    bool   `json:"evConnected"`
	ChargeProtocol string `json:"chargeProtocol"`
	ReplugRequired bool   `json:"replugRequired"`
	Sleep          bool   `json:"sleep"`

	// AC measurements
	PowerAc    float64 `json:"powerAc"`
	CurrentAc  float64 `json:"currentAc"`
	CurrentAc1 float64 `json:"currentAc1"`
	CurrentAc2 float64 `json:"currentAc2"`
	CurrentAc3 float64 `json:"currentAc3"`
	VoltageAc  float64 `json:"voltageAc"`
	VoltageAc1 float64 `json:"voltageAc1"`
	VoltageAc2 float64 `json:"voltageAc2"`
	VoltageAc3 float64 `json:"voltageAc3"`
	Frequency  float64 `json:"frequency"`

	// DC measurements
	PowerDc             float64 `json:"powerDc"`
	CurrentDc           float64 `json:"currentDc"`
	VoltageDc           float64 `json:"voltageDc"`
	InverterTemperature float64 `json:"inverterTemperature"`

	// Energy
	EnergyAc              float64 `json:"energyAc"`
	EnergyAcImport        float64 `json:"energyAcImport"`
	EnergyAcExport        float64 `json:"energyAcExport"`
	EnergyAcImportSession float64 `json:"energyAcImportSession"`
	EnergyAcExportSession float64 `json:"energyAcExportSession"`

	// Power limits
	ChargePowerMax    float64 `json:"chargePowerMax"`
	ChargePowerMin    float64 `json:"chargePowerMin"`
	DischargePowerMax float64 `json:"dischargePowerMax"`
	DischargePowerMin float64 `json:"dischargePowerMin"`

	// Vehicle state
	SoC                   float64 `json:"soc"`
	TargetEnergyRequest   float64 `json:"targetEnergyRequest"`
	RequestedTargetPower  float64 `json:"requestedTargetPower"`

	// Control inputs (subscribed topics)
	TargetPower      float64 `json:"targetPower"`
	LimitChargePower float64 `json:"limitChargePower"`
}

// NewServer creates a new MQTT server for V2X charger simulation
func NewServer(id int, port int) *Server {
	return &Server{
		id:           id,
		serialNumber: fmt.Sprintf("V2XSIM%02d", id),
		port:         port,
		maxLogs:      500,
		logs:         make([]device.LogEntry, 0),
		state:        newDefaultState(),
		stopChan:     make(chan struct{}),
	}
}

func newDefaultState() *V2XState {
	return &V2XState{
		// Connection & identity
		Connected:    true,
		Version:      "1.0.0-sim",
		SerialNumber: "",

		// Session state
		SessionState:   "PAUSED",
		ControlMode:    "CONTROLLABLE",
		EVConnected:    true,
		ChargeProtocol: "ISO_15118_2",
		ReplugRequired: false,
		Sleep:          false,

		// AC measurements
		PowerAc:    0,
		CurrentAc:  0,
		CurrentAc1: 0,
		CurrentAc2: 0,
		CurrentAc3: 0,
		VoltageAc:  230,
		VoltageAc1: 230,
		VoltageAc2: 230,
		VoltageAc3: 230,
		Frequency:  50,

		// DC measurements
		PowerDc:             0,
		CurrentDc:           0,
		VoltageDc:           400,
		InverterTemperature: 25,

		// Energy
		EnergyAc:              0,
		EnergyAcImport:        0,
		EnergyAcExport:        0,
		EnergyAcImportSession: 0,
		EnergyAcExportSession: 0,

		// Power limits
		ChargePowerMax:    10100,
		ChargePowerMin:    400,
		DischargePowerMax: 10100,
		DischargePowerMin: 700,

		// Vehicle state
		SoC:                  50,
		TargetEnergyRequest:  0,
		RequestedTargetPower: 0,

		// Control inputs
		TargetPower:      0,
		LimitChargePower: 0,
	}
}

// Start starts the MQTT broker
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	// Recreate stop channel (may have been closed by previous Stop)
	s.stopChan = make(chan struct{})

	// Create MQTT broker
	s.broker = mqtt.New(&mqtt.Options{
		InlineClient: true,
	})

	// Allow all connections (no auth for simulator)
	_ = s.broker.AddHook(new(auth.AllowHook), nil)

	// Add message hook to handle incoming messages
	s.broker.AddHook(&MessageHook{server: s}, nil)

	// Create TCP listener
	tcp := listeners.NewTCP(listeners.Config{
		ID:      fmt.Sprintf("mqtt-%d", s.id),
		Address: fmt.Sprintf(":%d", s.port),
	})

	if err := s.broker.AddListener(tcp); err != nil {
		return fmt.Errorf("failed to add MQTT listener: %w", err)
	}

	// Start broker
	go func() {
		if err := s.broker.Serve(); err != nil {
			log.Printf("[%s] MQTT broker error: %v", s.serialNumber, err)
		}
	}()

	// Start publishing loop
	go s.publishLoop()

	s.running = true
	s.state.SerialNumber = s.serialNumber

	log.Printf("[%s] MQTT broker started on :%d", s.serialNumber, s.port)
	return nil
}

// Stop stops the MQTT broker
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	close(s.stopChan)

	if s.broker != nil {
		s.broker.Close()
	}

	s.running = false
	log.Printf("[%s] MQTT broker stopped", s.serialNumber)
	return nil
}

// publishLoop publishes charger state periodically
func (s *Server) publishLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.publishState()
		}
	}
}

// publishState publishes all V2X charger topics (matches Ambibox structure)
func (s *Server) publishState() {
	s.mu.RLock()
	state := *s.state
	s.mu.RUnlock()

	// device/evCharger/0/* topics
	evChargerTopics := map[string]string{
		// Connection & identity
		"connected":    fmt.Sprintf("%t", state.Connected),
		"version":      state.Version,
		"serialNumber": state.SerialNumber,

		// Session state
		"sessionState":   state.SessionState,
		"controlMode":    state.ControlMode,
		"evConnected":    fmt.Sprintf("%t", state.EVConnected),
		"chargeProtocol": state.ChargeProtocol,
		"replugRequired": fmt.Sprintf("%t", state.ReplugRequired),
		"sleep":          fmt.Sprintf("%t", state.Sleep),

		// AC measurements
		"powerAc":    fmt.Sprintf("%.3f", state.PowerAc),
		"currentAc":  fmt.Sprintf("%.3f", state.CurrentAc),
		"currentAc1": fmt.Sprintf("%.3f", state.CurrentAc1),
		"currentAc2": fmt.Sprintf("%.3f", state.CurrentAc2),
		"currentAc3": fmt.Sprintf("%.3f", state.CurrentAc3),
		"voltageAc":  fmt.Sprintf("%.3f", state.VoltageAc),
		"voltageAc1": fmt.Sprintf("%.3f", state.VoltageAc1),
		"voltageAc2": fmt.Sprintf("%.3f", state.VoltageAc2),
		"voltageAc3": fmt.Sprintf("%.3f", state.VoltageAc3),
		"frequency":  fmt.Sprintf("%.3f", state.Frequency),

		// DC measurements
		"powerDc":             fmt.Sprintf("%.3f", state.PowerDc),
		"currentDc":           fmt.Sprintf("%.3f", state.CurrentDc),
		"voltageDc":           fmt.Sprintf("%.3f", state.VoltageDc),
		"inverterTemperature": fmt.Sprintf("%.3f", state.InverterTemperature),

		// Energy
		"energyAc":              fmt.Sprintf("%.3f", state.EnergyAc),
		"energyAcImport":        fmt.Sprintf("%.3f", state.EnergyAcImport),
		"energyAcExport":        fmt.Sprintf("%.3f", state.EnergyAcExport),
		"energyAcImportSession": fmt.Sprintf("%.3f", state.EnergyAcImportSession),
		"energyAcExportSession": fmt.Sprintf("%.3f", state.EnergyAcExportSession),

		// Power limits
		"chargePowerMax":    fmt.Sprintf("%.3f", state.ChargePowerMax),
		"chargePowerMin":    fmt.Sprintf("%.3f", state.ChargePowerMin),
		"dischargePowerMax": fmt.Sprintf("%.3f", state.DischargePowerMax),
		"dischargePowerMin": fmt.Sprintf("%.3f", state.DischargePowerMin),

		// Vehicle state
		"soc":                  fmt.Sprintf("%.3f", state.SoC),
		"targetEnergyRequest":  fmt.Sprintf("%.3f", state.TargetEnergyRequest),
		"requestedTargetPower": fmt.Sprintf("%.3f", state.RequestedTargetPower),
	}

	// device/ess/0/* topics (mirrors relevant charger state)
	essTopics := map[string]string{
		"chargePowerMin":    fmt.Sprintf("%.3f", state.ChargePowerMin),
		"chargePowerMax":    fmt.Sprintf("%.3f", state.ChargePowerMax),
		"dischargePowerMin": fmt.Sprintf("%.3f", state.DischargePowerMin),
		"dischargePowerMax": fmt.Sprintf("%.3f", state.DischargePowerMax),
		"controlMode":       state.ControlMode,
		"powerAc":           fmt.Sprintf("%.3f", state.PowerAc),
		"soc":               fmt.Sprintf("%.3f", state.SoC),
	}

	// Publish evCharger topics
	for topic, payload := range evChargerTopics {
		s.broker.Publish("device/evCharger/0/"+topic, []byte(payload), false, 0)
	}

	// Publish ess topics
	for topic, payload := range essTopics {
		s.broker.Publish("device/ess/0/"+topic, []byte(payload), false, 0)
	}
}

// handleMessage processes incoming MQTT messages
func (s *Server) handleMessage(topic string, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	payloadStr := string(payload)

	s.addLog("message", map[string]interface{}{
		"topic":   topic,
		"payload": payloadStr,
	})

	switch topic {
	case "device/evCharger/0/wakeUp":
		// Legacy wakeUp topic (true = wake, false = sleep)
		if payloadStr == "true" {
			s.state.Sleep = false
			s.wakeUpCharger()
		} else {
			s.state.Sleep = true
			s.sleepCharger()
		}

	case "device/evCharger/0/sleep":
		// sleep topic (false = wake, true = sleep) - opposite of wakeUp
		if payloadStr == "false" {
			s.state.Sleep = false
			s.wakeUpCharger()
		} else {
			s.state.Sleep = true
			s.sleepCharger()
		}

	case "device/ess/0/targetPower":
		var power float64
		fmt.Sscanf(payloadStr, "%f", &power)
		s.state.TargetPower = power
		s.state.RequestedTargetPower = power

		// Only apply power if EV is connected and not in IDLE
		if s.state.EVConnected && s.state.SessionState != "IDLE" && s.state.ControlMode == "CONTROLLABLE" {
			if power != 0 {
				// Transition to CHARGE_LOOP when power is commanded
				if s.state.SessionState == "PAUSED" {
					s.state.SessionState = "CHARGE_LOOP"
				}
				// Invert sign: positive targetPower = charging (negative powerAc)
				s.state.PowerAc = -power
				s.state.PowerDc = -power * 0.95 // DC power with efficiency loss
				s.updateCurrents()
			} else {
				// Transition back to PAUSED when power is zero
				if s.state.SessionState == "CHARGE_LOOP" {
					s.state.SessionState = "PAUSED"
				}
				s.state.PowerAc = 0
				s.state.PowerDc = 0
				s.updateCurrents()
			}
		}

	case "device/ess/0/limitChargePower":
		var limit float64
		fmt.Sscanf(payloadStr, "%f", &limit)
		s.state.LimitChargePower = limit

		// Only apply limit if EV is connected and not in IDLE
		if s.state.EVConnected && s.state.SessionState != "IDLE" && s.state.ControlMode == "LIMITABLE" {
			if limit != 0 {
				// Transition to CHARGE_LOOP when limit is set
				if s.state.SessionState == "PAUSED" {
					s.state.SessionState = "CHARGE_LOOP"
				}
				// In limitable mode, set power to the limit
				s.state.PowerAc = -limit
				s.state.PowerDc = -limit * 0.95
				s.updateCurrents()
			} else {
				// Transition back to PAUSED when limit is zero
				if s.state.SessionState == "CHARGE_LOOP" {
					s.state.SessionState = "PAUSED"
				}
				s.state.PowerAc = 0
				s.state.PowerDc = 0
				s.updateCurrents()
			}
		}
	}
}

// wakeUpCharger transitions charger to ready state (PAUSED)
// CHARGE_LOOP is only entered when power is actually commanded
func (s *Server) wakeUpCharger() {
	if s.state.SessionState == "IDLE" && s.state.EVConnected {
		s.state.SessionState = "PAUSED"
	}
}

// sleepCharger transitions charger to idle state
func (s *Server) sleepCharger() {
	s.state.SessionState = "IDLE"
	s.state.PowerAc = 0
	s.state.PowerDc = 0
	s.updateCurrents()
}

// updateCurrents calculates phase currents from power
func (s *Server) updateCurrents() {
	// 3-phase balanced load calculation
	perPhaseCurrent := s.state.PowerAc / (3 * s.state.VoltageAc1)
	s.state.CurrentAc1 = perPhaseCurrent
	s.state.CurrentAc2 = perPhaseCurrent
	s.state.CurrentAc3 = perPhaseCurrent
	s.state.CurrentAc = perPhaseCurrent * 3 // Total AC current

	// Average voltage
	s.state.VoltageAc = (s.state.VoltageAc1 + s.state.VoltageAc2 + s.state.VoltageAc3) / 3

	// DC current from DC power
	if s.state.VoltageDc > 0 {
		s.state.CurrentDc = s.state.PowerDc / s.state.VoltageDc
	}
}

// addLog adds a log entry
func (s *Server) addLog(operation string, details map[string]interface{}) {
	entry := device.LogEntry{
		Timestamp: time.Now(),
		Operation: operation,
		Details:   details,
	}

	s.logs = append(s.logs, entry)
	if len(s.logs) > s.maxLogs {
		s.logs = s.logs[1:]
	}
}

// GetState returns the current charger state
func (s *Server) GetState() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return map[string]interface{}{
		// Connection & identity
		"connected":    s.state.Connected,
		"version":      s.state.Version,
		"serialNumber": s.state.SerialNumber,

		// Session state
		"sessionState":   s.state.SessionState,
		"controlMode":    s.state.ControlMode,
		"evConnected":    s.state.EVConnected,
		"chargeProtocol": s.state.ChargeProtocol,
		"replugRequired": s.state.ReplugRequired,
		"sleep":          s.state.Sleep,

		// AC measurements
		"powerAc":    s.state.PowerAc,
		"currentAc":  s.state.CurrentAc,
		"currentAc1": s.state.CurrentAc1,
		"currentAc2": s.state.CurrentAc2,
		"currentAc3": s.state.CurrentAc3,
		"voltageAc":  s.state.VoltageAc,
		"voltageAc1": s.state.VoltageAc1,
		"voltageAc2": s.state.VoltageAc2,
		"voltageAc3": s.state.VoltageAc3,
		"frequency":  s.state.Frequency,

		// DC measurements
		"powerDc":             s.state.PowerDc,
		"currentDc":           s.state.CurrentDc,
		"voltageDc":           s.state.VoltageDc,
		"inverterTemperature": s.state.InverterTemperature,

		// Energy
		"energyAc":              s.state.EnergyAc,
		"energyAcImport":        s.state.EnergyAcImport,
		"energyAcExport":        s.state.EnergyAcExport,
		"energyAcImportSession": s.state.EnergyAcImportSession,
		"energyAcExportSession": s.state.EnergyAcExportSession,

		// Power limits
		"chargePowerMax":    s.state.ChargePowerMax,
		"chargePowerMin":    s.state.ChargePowerMin,
		"dischargePowerMax": s.state.DischargePowerMax,
		"dischargePowerMin": s.state.DischargePowerMin,

		// Vehicle state
		"soc":                  s.state.SoC,
		"targetEnergyRequest":  s.state.TargetEnergyRequest,
		"requestedTargetPower": s.state.RequestedTargetPower,
	}
}

// SetValue sets a charger state value
func (s *Server) SetValue(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch key {
	case "sessionState":
		if v, ok := value.(string); ok {
			s.state.SessionState = v
		}
	case "controlMode":
		if v, ok := value.(string); ok {
			s.state.ControlMode = v
		}
	case "evConnected":
		if v, ok := value.(bool); ok {
			s.state.EVConnected = v
			if v {
				// Auto-transition to PAUSED when EV connects
				if s.state.SessionState == "IDLE" {
					s.state.SessionState = "PAUSED"
				}
			} else {
				// Auto-transition to IDLE when EV disconnects, stop power
				s.state.SessionState = "IDLE"
				s.state.PowerAc = 0
				s.state.PowerDc = 0
				s.updateCurrents()
			}
		}
	case "soc":
		if v, ok := value.(float64); ok {
			s.state.SoC = v
		}
	case "powerAc":
		if v, ok := value.(float64); ok {
			s.state.PowerAc = v
			s.updateCurrents()
		}
	default:
		return fmt.Errorf("unknown key: %s", key)
	}

	return nil
}

// GetLogs returns the operation logs
func (s *Server) GetLogs(limit int) []device.LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > len(s.logs) {
		limit = len(s.logs)
	}

	start := len(s.logs) - limit
	result := make([]device.LogEntry, limit)
	copy(result, s.logs[start:])
	return result
}

// Protocol returns the protocol type
func (s *Server) Protocol() device.Protocol {
	return device.ProtocolMQTT
}

// Category returns the device category
func (s *Server) Category() device.Category {
	return device.CategoryV2XCharger
}

// Address returns the broker address
func (s *Server) Address() string {
	return fmt.Sprintf(":%d", s.port)
}

// SerialNumber returns the serial number
func (s *Server) SerialNumber() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.serialNumber
}

// SetSerialNumber sets the serial number
func (s *Server) SetSerialNumber(sn string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serialNumber = sn
	s.state.SerialNumber = sn
}

// GenerateValues generates simulated values based on time
func (s *Server) GenerateValues(simTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If in CHARGE_LOOP, simulate SoC changes based on power
	if s.state.SessionState == "CHARGE_LOOP" {
		// Calculate energy delta (Wh) for 1 second
		energyDelta := s.state.PowerAc / 3600.0

		if s.state.PowerAc < 0 {
			// Charging (power is negative = energy flowing into car)
			s.state.EnergyAcImport += -energyDelta
			// Assume 50kWh battery, SoC change per second
			s.state.SoC += (-energyDelta / 50000.0) * 100.0
		} else if s.state.PowerAc > 0 {
			// Discharging (power is positive = energy flowing from car)
			s.state.EnergyAcExport += energyDelta
			s.state.SoC -= (energyDelta / 50000.0) * 100.0
		}

		// Clamp SoC
		if s.state.SoC < 0 {
			s.state.SoC = 0
		} else if s.state.SoC > 100 {
			s.state.SoC = 100
		}
	}
}

// MessageHook implements the MQTT hook interface for message handling
type MessageHook struct {
	mqtt.HookBase
	server *Server
}

func (h *MessageHook) ID() string {
	return "message-hook"
}

func (h *MessageHook) Provides(b byte) bool {
	return b == mqtt.OnPublished
}

func (h *MessageHook) OnPublished(cl *mqtt.Client, pk packets.Packet) {
	// Only process messages from external clients (not our own publishes)
	if cl.ID != "inline" {
		h.server.handleMessage(pk.TopicName, pk.Payload)
	}
}

// ClientInfo represents a connected MQTT client
type ClientInfo struct {
	ID                string `json:"id"`
	RemoteAddr        string `json:"remote_addr"`
	SubscriptionCount int    `json:"subscription_count"`
}

// GetClients returns a list of connected MQTT clients
func (s *Server) GetClients() []ClientInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.broker == nil {
		return []ClientInfo{}
	}

	var clients []ClientInfo
	for _, cl := range s.broker.Clients.GetAll() {
		// Skip the inline client (our own publisher)
		if cl.ID == "inline" {
			continue
		}
		clients = append(clients, ClientInfo{
			ID:                cl.ID,
			RemoteAddr:        cl.Net.Remote,
			SubscriptionCount: cl.State.Subscriptions.Len(),
		})
	}
	return clients
}

// WakeUp sends the wakeUp command to transition to CHARGE_LOOP
func (s *Server) WakeUp(wake bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if wake {
		s.state.Sleep = false
		s.wakeUpCharger()
	} else {
		s.state.Sleep = true
		s.sleepCharger()
	}

	s.addLog("command", map[string]interface{}{
		"action": "wakeUp",
		"value":  wake,
		"result": s.state.SessionState,
	})
}

// SetTargetPower sets the charging/discharging power
func (s *Server) SetTargetPower(power float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state.TargetPower = power
	s.state.RequestedTargetPower = power

	// Only apply power if EV is connected and not in IDLE
	if s.state.EVConnected && s.state.SessionState != "IDLE" {
		if power != 0 {
			// Transition to CHARGE_LOOP when power is commanded
			if s.state.SessionState == "PAUSED" {
				s.state.SessionState = "CHARGE_LOOP"
			}
			if s.state.ControlMode == "CONTROLLABLE" {
				// Invert sign: positive targetPower = charging (negative powerAc)
				s.state.PowerAc = -power
				s.state.PowerDc = -power * 0.95 // DC power with efficiency loss
				s.updateCurrents()
			}
		} else {
			// Transition back to PAUSED when power is zero
			if s.state.SessionState == "CHARGE_LOOP" {
				s.state.SessionState = "PAUSED"
			}
			s.state.PowerAc = 0
			s.state.PowerDc = 0
			s.updateCurrents()
		}
	}

	s.addLog("command", map[string]interface{}{
		"action":       "setTargetPower",
		"value":        power,
		"powerAc":      s.state.PowerAc,
		"sessionState": s.state.SessionState,
	})
}
