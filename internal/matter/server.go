package matter

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/srcfl/device-simulator/internal/device"
)

// Server is the per-device DeviceServer/Automatable adapter. It holds the
// simulated physics state (the Go side is the source of truth) and delegates
// Matter protocol concerns to the shared bridge Manager via nodeId.
type Server struct {
	mu sync.RWMutex

	id            int
	serial        string
	dt            *deviceType
	port          int
	nodeID        string
	passcode      int
	discriminator int
	mgr           *Manager

	// commissioning surface (populated by Start + push events)
	pairingCode  string
	qrCode       string
	commissioned bool
	fabricCount  int
	bridgeUp     bool

	phys   map[string]float64     // physics scalars (bools stored as 0/1)
	extras map[string]interface{} // non-numeric reported values

	logs    []device.LogEntry
	maxLogs int
	running bool
}

// NewServer builds a Matter server for the given device-type key
// ("matter_thermostat", etc. — or the short key "thermostat").
func NewServer(id int, deviceTypeKey string) (*Server, error) {
	dt := lookupDeviceType(deviceTypeKey)
	if dt == nil {
		return nil, fmt.Errorf("unknown matter device type: %s", deviceTypeKey)
	}

	s := &Server{
		id:            id,
		serial:        fmt.Sprintf("MATTER%s%02d", dt.SerialTag, id),
		dt:            dt,
		nodeID:        fmt.Sprintf("%s-%d", dt.Key, id),
		passcode:      derivePasscode(id),
		discriminator: deriveDiscriminator(id),
		mgr:           Shared(),
		phys:          map[string]float64{},
		extras:        map[string]interface{}{},
		logs:          make([]device.LogEntry, 0, 64),
		maxLogs:       500,
	}
	for k, v := range dt.InitPhys {
		s.phys[k] = v
	}
	return s, nil
}

// DeviceTypeKey returns the short bridge key (e.g. "thermostat").
func (s *Server) DeviceTypeKey() string { return s.dt.Key }

// Label returns the human-friendly device-type label.
func (s *Server) Label() string { return s.dt.Label }

func (s *Server) SetPort(port int) {
	s.mu.Lock()
	s.port = port
	s.mu.Unlock()
}

// config builds the DeviceConfig sent to the bridge (also used for replay).
func (s *Server) config() DeviceConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return DeviceConfig{
		NodeID:        s.nodeID,
		Type:          s.dt.Key,
		Serial:        s.serial,
		Port:          s.port,
		Passcode:      s.passcode,
		Discriminator: s.discriminator,
		DeviceName:    s.dt.Label,
		VendorID:      DefaultVendorID,
		ProductID:     DefaultProductID,
		Defaults:      s.dt.MatterDefaults,
	}
}

// Start creates the Matter ServerNode via the bridge and records pairing info.
func (s *Server) Start() error {
	if ok, reason := s.mgr.Available(); !ok {
		return &ErrNodeUnavailable{Reason: reason}
	}
	info, err := s.mgr.createDevice(s.config())
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.pairingCode = info.ManualPairingCode
	s.qrCode = info.QRPairingCode
	s.commissioned = info.Commissioned
	s.fabricCount = info.FabricCount
	s.bridgeUp = true
	s.running = true
	s.mu.Unlock()

	s.mgr.register(s)
	s.addLog("commissioning", map[string]interface{}{
		"pairing_code": info.ManualPairingCode,
		"commissioned": info.Commissioned,
	})
	return nil
}

// Stop tears down the ServerNode (storage is retained so re-pairing isn't
// required on a later Start).
func (s *Server) Stop() error {
	s.mu.Lock()
	running := s.running
	s.running = false
	s.bridgeUp = false
	s.mu.Unlock()
	if !running {
		return nil
	}
	s.mgr.unregister(s.nodeID)
	if err := s.mgr.removeDevice(s.nodeID); err != nil {
		return err
	}
	s.addLog("stopped", nil)
	return nil
}

// GetState returns the current device state for the UI/API.
func (s *Server) GetState() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	state := map[string]interface{}{
		"device_type":  s.dt.Key,
		"label":        s.dt.Label,
		"node_id":      s.nodeID,
		"pairing_code": s.pairingCode,
		"qr":           s.qrCode,
		"commissioned": s.commissioned,
		"fabric_count": s.fabricCount,
		"bridge_up":    s.bridgeUp,
	}
	for k, v := range s.phys {
		state[k] = v
	}
	for k, v := range s.extras {
		state[k] = v
	}
	return state
}

// SetValue applies a control change from the UI/API (e.g. toggle plug, set
// thermostat setpoint, start a wash cycle) and pushes the resulting Matter
// attribute writes to the bridge.
func (s *Server) SetValue(key string, value interface{}) error {
	if s.dt.SetValue == nil {
		return fmt.Errorf("device type %s has no settable values", s.dt.Key)
	}
	changes, handled := s.dt.SetValue(s, key, value)
	if !handled {
		return fmt.Errorf("unknown value key for %s: %s", s.dt.Key, key)
	}
	s.addLog("set_value", map[string]interface{}{"key": key, "value": value})
	return s.pushChanges(changes)
}

// GenerateValues advances the simulated physics once per tick and pushes the
// changed Matter attributes.
func (s *Server) GenerateValues(simTime time.Time) {
	if s.dt.Generate == nil {
		return
	}
	s.mu.RLock()
	running, bridgeUp := s.running, s.bridgeUp
	s.mu.RUnlock()
	if !running || !bridgeUp {
		return
	}
	changes := s.dt.Generate(s, simTime)
	if err := s.pushChanges(changes); err != nil {
		s.addLog("error", map[string]interface{}{"op": "generate", "error": err.Error()})
	}
}

func (s *Server) pushChanges(changes []AttrChange) error {
	if len(changes) == 0 {
		return nil
	}
	s.mu.RLock()
	up := s.bridgeUp && s.running
	s.mu.RUnlock()
	if !up {
		return nil
	}
	return s.mgr.setAttributes(s.nodeID, changes)
}

// handleEvent processes a push event routed from the bridge.
func (s *Server) handleEvent(ev event) {
	switch ev.Name {
	case "commissioned", "decommissioned", "fabricsChanged":
		var e struct {
			FabricCount int `json:"fabricCount"`
		}
		_ = json.Unmarshal(ev.Raw, &e)
		s.mu.Lock()
		s.fabricCount = e.FabricCount
		s.commissioned = e.FabricCount > 0
		s.mu.Unlock()
		s.addLog(ev.Name, map[string]interface{}{"fabric_count": e.FabricCount})
	case "attributeChanged":
		var e struct {
			Cluster   string      `json:"cluster"`
			Attribute string      `json:"attribute"`
			Value     interface{} `json:"value"`
		}
		_ = json.Unmarshal(ev.Raw, &e)
		// Reflect external control into physics so the Go simulation stays in
		// sync (e.g. a Home app toggled the plug off).
		if s.dt.OnExternal != nil {
			s.dt.OnExternal(s, e.Cluster, e.Attribute, e.Value)
		}
		s.addLog("external_change", map[string]interface{}{
			"cluster": e.Cluster, "attribute": e.Attribute, "value": e.Value,
		})
	}
}

func (s *Server) setBridgeUp(up bool) {
	s.mu.Lock()
	s.bridgeUp = up
	s.mu.Unlock()
}

// --- DeviceServer metadata ---

func (s *Server) GetLogs(limit int) []device.LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.logs) {
		limit = len(s.logs)
	}
	out := make([]device.LogEntry, limit)
	copy(out, s.logs[len(s.logs)-limit:])
	return out
}

func (s *Server) Protocol() device.Protocol { return device.ProtocolMatter }
func (s *Server) Category() device.Category { return device.CategoryMatter }

func (s *Server) Address() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("matter://0.0.0.0:%d", s.port)
}

func (s *Server) SerialNumber() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.serial
}

func (s *Server) SetSerialNumber(sn string) {
	s.mu.Lock()
	s.serial = sn
	s.mu.Unlock()
}

// PairingCode / QRCode expose commissioning info to the app layer.
func (s *Server) PairingCode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pairingCode
}

func (s *Server) QRCode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.qrCode
}

func (s *Server) addLog(operation string, details map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, device.LogEntry{
		Timestamp: time.Now(),
		Operation: operation,
		Details:   details,
	})
	if len(s.logs) > s.maxLogs {
		s.logs = s.logs[1:]
	}
}

// --- physics state helpers (used by devicetypes.go) ---

func (s *Server) getF(key string, def float64) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.phys[key]; ok {
		return v
	}
	return def
}

func (s *Server) setF(key string, val float64) {
	s.mu.Lock()
	s.phys[key] = val
	s.mu.Unlock()
}

func (s *Server) addF(key string, delta float64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phys[key] += delta
	return s.phys[key]
}

func (s *Server) getBool(key string, def bool) bool {
	v := s.getF(key, boolToF(def))
	return v != 0
}

func (s *Server) setBool(key string, val bool) {
	s.setF(key, boolToF(val))
}

func boolToF(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// derivePasscode/deriveDiscriminator produce stable, valid commissioning
// credentials from the simulator id so pairing codes are unique per device and
// stable across restarts. Passcode avoids the trivial/invalid values; the test
// passcode 20202021 is the base.
func derivePasscode(id int) int {
	p := 20202021 + id*131
	if p > 99999998 {
		p = 1 + (p % 99999998)
	}
	return p
}

func deriveDiscriminator(id int) int {
	return (0xF00 + id) & 0x0FFF
}
