package ocpp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/srcfl/device-simulator/internal/device"
)

// storedProfile wraps a ChargingProfile with its assigned connector ID
type storedProfile struct {
	ConnectorID int
	Profile     ChargingProfile
}

// queuedTx is a transaction message waiting for a connection.
type queuedTx struct {
	action   string
	payload  interface{}
	onResult func(json.RawMessage)
}

var errNotConnected = errors.New("not connected")

// delayedOp is an accepted profile change waiting to take effect.
type delayedOp struct {
	due   time.Time
	apply func()
}

// profileKey builds a composite key for the profile map: "connectorId:purpose:stackLevel"
func profileKey(connectorId int, purpose string, stackLevel int) string {
	return fmt.Sprintf("%d:%s:%d", connectorId, purpose, stackLevel)
}

// readonlyConfigKeys defines OCPP configuration keys that cannot be changed via ChangeConfiguration
var readonlyConfigKeys = map[string]bool{
	"SupportedFeatureProfiles": true,
	"NumberOfConnectors":       true,
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for simulator
	},
	Subprotocols: []string{"ocpp1.6"},
}

// Connector represents a charging connector
type Connector struct {
	ID           int     `json:"id"`
	Status       string  `json:"status"`
	ErrorCode    string  `json:"errorCode"`
	CurrentPower float64 `json:"currentPower"`
	EnergyWh     float64 `json:"energyWh"`
	Phases       int     `json:"phases"` // 1 or 3: phases the car draws on
}

// Transaction represents an active charging transaction
type Transaction struct {
	ID          int       `json:"id"`
	ConnectorID int       `json:"connectorId"`
	IdTag       string    `json:"idTag"`
	StartTime   time.Time `json:"startTime"`
	MeterStart  int       `json:"meterStart"`
	MeterNow    int       `json:"meterNow"`
}

// Server represents an OCPP charge point simulator
type Server struct {
	mu               sync.RWMutex
	writeMu          sync.Mutex // Mutex for serializing WebSocket writes
	id               int
	serialNumber     string
	port             int
	vendor           string
	model            string
	numConnectors    int
	maxPowerKW       float64
	connectors       map[int]*Connector
	transactions     map[int]*Transaction
	chargingProfiles map[string]*storedProfile
	configuration    map[string]string
	logs             []device.LogEntry
	maxLogs          int
	running          bool
	httpServer       *http.Server
	wsConn           *websocket.Conn
	messageIdSeq     int64
	pendingRequests  map[string]chan json.RawMessage
	pendingActions   map[string]string // messageId -> action name for logging
	stopChan         chan struct{}

	// Transaction messages that found no connection, oldest first. A real
	// charge point keeps them and delivers them once it is back online.
	txQueueMu sync.Mutex
	txQueue   []queuedTx
	// txAssigned maps a local transaction id to the Central System's, for a
	// session that ended before its StartTransaction was answered: its
	// queued StopTransaction still names the local id. Guarded by mu.
	txAssigned map[int]int

	// profileDelay models a cloud charger: it accepts a charging profile at
	// once and applies it this much later. Zero applies it at once.
	profileDelay time.Duration
	laterMu      sync.Mutex // guards later
	later        []delayedOp
	laterRunMu   sync.Mutex // keeps delayed operations in order

	// Client mode fields
	ocppURL        string // Central System URL to connect to (client mode)
	clientMode     bool   // true = connect to URL, false = listen for connections
	reconnectDelay time.Duration
	redialAt       time.Time // DropConnection keeps the charger away until then
	frameLog       func(dir string, frame []byte)

	// 3-phase metering
	voltageL1 float64
	voltageL2 float64
	voltageL3 float64
	currentL1 float64
	currentL2 float64
	currentL3 float64
	phaseW    [3]float64 // what the chargers draw on L1, L2, L3

	// Simulation control
	carMaxA      float64 // most the car draws per phase; 0 = whatever is offered
	carPhases    int     // phases the car charges on; 0 = as many as offered
	evConnected  bool    // Whether an EV is plugged in
	nextTxId     int     // Counter for local transaction IDs
}

// NewServer creates a new OCPP charge point simulator
func NewServer(id int, port int, vendor, model string, numConnectors int, maxPowerKW float64) *Server {
	connectors := make(map[int]*Connector)
	// Connector 0 is the charge point itself
	connectors[0] = &Connector{ID: 0, Status: StatusAvailable, ErrorCode: "NoError"}
	for i := 1; i <= numConnectors; i++ {
		connectors[i] = &Connector{
			ID:        i,
			Status:    StatusAvailable,
			ErrorCode: "NoError",
		}
	}

	return &Server{
		id:               id,
		serialNumber:     fmt.Sprintf("OCPPSIM%02d", id),
		port:             port,
		vendor:           vendor,
		model:            model,
		numConnectors:    numConnectors,
		maxPowerKW:       maxPowerKW,
		connectors:       connectors,
		transactions:     make(map[int]*Transaction),
		txAssigned:       make(map[int]int),
		chargingProfiles: make(map[string]*storedProfile),
		configuration: map[string]string{
			"HeartbeatInterval":                        "60",
			"MeterValueSampleInterval":                 "30",
			"NumberOfConnectors":                       fmt.Sprintf("%d", numConnectors),
			"SupportedFeatureProfiles":                 "Core,SmartCharging,RemoteTrigger,LocalAuthListManagement",
			"ConnectorSwitch3to1PhaseSupported":        "false",
			"ChargingScheduleAllowedChargingRateUnit":  "Current,Power",
		},
		logs:            make([]device.LogEntry, 0),
		maxLogs:         500,
		pendingRequests: make(map[string]chan json.RawMessage),
		pendingActions:  make(map[string]string),
		stopChan:        make(chan struct{}),
		reconnectDelay:  5 * time.Second,
		voltageL1:       230.0,
		voltageL2:       230.0,
		voltageL3:       230.0,
	}
}

// SetOCPPURL sets the Central System URL to connect to (enables client mode)
func (s *Server) SetOCPPURL(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ocppURL = url
	s.clientMode = url != ""
}

// SetProfileDelay sets how long an accepted SetChargingProfile or
// ClearChargingProfile takes to reach the charger's output.
func (s *Server) SetProfileDelay(d time.Duration) {
	if d < 0 {
		d = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profileDelay = d
}

// ProfileDelay returns the profile delay.
func (s *Server) ProfileDelay() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.profileDelay
}

// SetCar describes the car on the charger: the most it draws per phase and
// on how many phases it charges. A one-phase car draws on L1 whatever the
// charger offers. Zeros mean the car takes what it is offered.
func (s *Server) SetCar(maxA float64, phases int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.carMaxA, s.carPhases = maxA, phases
}

// carDrawLocked is what the car takes of an offer of w watts on phases.
func (s *Server) carDrawLocked(w float64, phases int) (float64, int) {
	if w <= 0 || (s.carMaxA <= 0 && (s.carPhases <= 0 || s.carPhases >= phases)) {
		return w, phases
	}
	v := s.averagePhaseVoltageLocked()
	a := w / (float64(phases) * v)
	if s.carMaxA > 0 {
		a = math.Min(a, s.carMaxA)
	}
	if s.carPhases > 0 && s.carPhases < phases {
		phases = s.carPhases
	}
	return a * float64(phases) * v, phases
}

// GetOCPPURL returns the configured Central System URL
func (s *Server) GetOCPPURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ocppURL
}

// SetVendorModel updates the vendor and model
func (s *Server) SetVendorModel(vendor, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vendor = vendor
	s.model = model
}

// ApplyConfiguration merges configuration values into the OCPP configuration map
func (s *Server) ApplyConfiguration(config map[string]string) {
	if config == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.configuration == nil {
		s.configuration = make(map[string]string)
	}
	for k, v := range config {
		s.configuration[k] = v
	}
}

// Start starts the OCPP server (or connects as client if URL is set)
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	// Recreate stop channel (may have been closed by previous Stop)
	s.stopChan = make(chan struct{})

	// The charger's power follows its profiles whether or not a Central
	// System is connected.
	go s.simulationLoop(s.stopChan)

	if s.clientMode && s.ocppURL != "" {
		// Client mode: connect to Central System
		go s.clientLoop()
		s.running = true
		log.Printf("[%s] OCPP client connecting to %s", s.serialNumber, RedactURL(s.ocppURL))
		return nil
	}

	if s.port == 0 {
		return fmt.Errorf("ocpp port not set")
	}

	// Server mode: wait for connections
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWebSocket)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[%s] OCPP server error: %v", s.serialNumber, err)
		}
	}()

	s.running = true
	log.Printf("[%s] OCPP server started on ws://localhost:%d", s.serialNumber, s.port)
	return nil
}

// clientLoop handles the client connection with automatic reconnection
func (s *Server) clientLoop() {
	for {
		select {
		case <-s.stopChan:
			return
		default:
		}

		s.mu.RLock()
		url := s.ocppURL
		serial := s.serialNumber
		s.mu.RUnlock()

		if url == "" {
			return
		}

		// Build the full WebSocket URL with charge point identity
		// OCPP 1.6 expects: ws://host/path/ChargePointId
		wsURL, header, err := dialTarget(url, serial)
		if err != nil {
			s.addLog("connect_error", map[string]interface{}{
				"error": err.Error(),
			})
			log.Printf("[%s] Bad OCPP URL: %v", serial, err)
			return
		}

		s.addLog("connecting", map[string]interface{}{
			"url": wsURL,
		})

		dialer := websocket.Dialer{
			Subprotocols: []string{"ocpp1.6"},
		}

		conn, _, err := dialer.Dial(wsURL, header)
		if err != nil {
			s.addLog("connect_error", map[string]interface{}{
				"error": err.Error(),
			})
			log.Printf("[%s] Failed to connect to %s: %v", serial, wsURL, err)

			select {
			case <-s.stopChan:
				return
			case <-time.After(s.redialWait()):
				continue
			}
		}

		s.mu.Lock()
		s.wsConn = conn
		s.mu.Unlock()

		s.addLog("connected", map[string]interface{}{
			"url": wsURL,
		})
		log.Printf("[%s] Connected to Central System at %s", serial, wsURL)

		// Send BootNotification
		go s.sendBootNotification()

		// Heartbeats and meter values belong to this connection.
		done := make(chan struct{})
		go s.heartbeatLoop(done)
		go s.meterValuesLoop(done)

		// Read messages until disconnected
		s.readLoop(conn)
		close(done)

		s.mu.Lock()
		s.wsConn = nil
		s.mu.Unlock()

		// Check if we should stop or reconnect
		select {
		case <-s.stopChan:
			return
		case <-time.After(s.redialWait()):
			log.Printf("[%s] Reconnecting to Central System...", serial)
		}
	}
}

// dialTarget appends the charge point identity to the Central System URL and
// moves any user:password in it to a Basic auth header (OCPP 1.6 security
// profile 1). The WebSocket dialer refuses credentials inside the URL, and
// they must not reach the logs either.
func dialTarget(raw, identity string) (string, http.Header, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", nil, err
	}
	var header http.Header
	if u.User != nil {
		pass, _ := u.User.Password()
		req := http.Request{Header: http.Header{}}
		req.SetBasicAuth(u.User.Username(), pass)
		header = req.Header
		u.User = nil
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	u.Path += identity
	return u.String(), header, nil
}

// DropConnection closes the connection to the Central System, as a lost
// network does, and keeps the charger from dialling again for d. A session
// in progress goes on: the car keeps charging.
func (s *Server) DropConnection(d time.Duration) {
	s.mu.Lock()
	s.redialAt = time.Now().Add(d)
	conn := s.wsConn
	s.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// redialWait is how long the client waits before it dials again.
func (s *Server) redialWait() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if until := time.Until(s.redialAt); until > s.reconnectDelay {
		return until
	}
	return s.reconnectDelay
}

// SetFrameLog makes the charger report every OCPP-J frame it reads
// ("cs->cp") or writes ("cp->cs"). Nil turns it off.
func (s *Server) SetFrameLog(fn func(dir string, frame []byte)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frameLog = fn
}

func (s *Server) logFrame(dir string, frame []byte) {
	s.mu.RLock()
	fn := s.frameLog
	s.mu.RUnlock()
	if fn != nil {
		fn(dir, frame)
	}
}

// RedactURL hides the password in a Central System URL for logs.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Redacted()
}

// readLoop reads messages from the WebSocket connection
func (s *Server) readLoop(conn *websocket.Conn) {
	for {
		select {
		case <-s.stopChan:
			return
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			s.addLog("disconnect", map[string]interface{}{
				"error": err.Error(),
			})
			return
		}

		s.handleMessage(message)
	}
}

// meterValuesLoop periodically sends meter values during active transactions
func (s *Server) meterValuesLoop(done <-chan struct{}) {
	for {
		intervalSeconds := s.getMeterValueSampleInterval()
		if intervalSeconds <= 0 {
			intervalSeconds = 30
		}

		select {
		case <-s.stopChan:
			return
		case <-done:
			return
		case <-time.After(time.Duration(intervalSeconds) * time.Second):
			s.sendMeterValuesForActiveTransactions()
		}
	}
}

// sendMeterValuesForActiveTransactions sends meter values for all active transactions
func (s *Server) sendMeterValuesForActiveTransactions() {
	s.mu.RLock()
	transactions := make([]*Transaction, 0, len(s.transactions))
	for _, tx := range s.transactions {
		transactions = append(transactions, tx)
	}
	s.mu.RUnlock()

	for _, tx := range transactions {
		s.sendMeterValues(tx.ConnectorID, tx.ID)
	}
}

// sendMeterValues sends current meter values for a connector
func (s *Server) sendMeterValues(connectorId, transactionId int) {
	s.mu.RLock()
	conn := s.connectors[connectorId]
	if conn == nil {
		s.mu.RUnlock()
		return
	}

	// Build meter value with 3-phase data (exact Ambibox format and ORDER)
	// IMPORTANT: Use "Current.Offered" to trigger CalculateBasedOnCurrent path in CSMS
	// IMPORTANT: Use "L1", "L2", "L3" phases (not "L1-N") - matches Ambibox test data format
	// IMPORTANT: ORDER matters - match Ambibox exactly: Energy, Current.Offered, Current.Import, Voltage
	// Calculate offered current from power using phase voltage (L-N):
	// P = phases × V_phase × I_phase, so I_phase = P / (phases × V_phase)
	avgVoltage := (s.voltageL1 + s.voltageL2 + s.voltageL3) / 3.0
	phases := 3.0
	if conn.Phases == 1 {
		phases = 1
	}
	offeredCurrent := conn.CurrentPower / (phases * avgVoltage)
	if offeredCurrent < 0 {
		offeredCurrent = 0
	}

	sampledValues := []SampledValue{
		{Value: fmt.Sprintf("%.0f", conn.EnergyWh), Measurand: "Energy.Active.Import.Register", Unit: "Wh", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.3f", offeredCurrent), Measurand: "Current.Offered", Unit: "A", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.0f", conn.CurrentPower), Measurand: "Power.Active.Import", Unit: "W", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.3f", s.currentL1), Measurand: "Current.Import", Phase: "L1", Unit: "A", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.3f", s.currentL2), Measurand: "Current.Import", Phase: "L2", Unit: "A", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.3f", s.currentL3), Measurand: "Current.Import", Phase: "L3", Unit: "A", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.2f", s.voltageL1), Measurand: "Voltage", Phase: "L1", Unit: "V", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.2f", s.voltageL2), Measurand: "Voltage", Phase: "L2", Unit: "V", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.2f", s.voltageL3), Measurand: "Voltage", Phase: "L3", Unit: "V", Context: "Sample.Periodic"},
	}
	s.mu.RUnlock()

	req := MeterValuesRequest{
		ConnectorId:   connectorId,
		TransactionId: transactionId,
		MeterValue: []MeterValue{
			{
				Timestamp:    FormatTimestamp(time.Now()),
				SampledValue: sampledValues,
			},
		},
	}

	s.sendRequest(ActionMeterValues, req)
}

// Stop stops the OCPP server
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	close(s.stopChan)

	// A charger that is switched off forgets changes it had not applied.
	s.laterMu.Lock()
	s.later = nil
	s.laterMu.Unlock()

	if s.wsConn != nil {
		s.wsConn.Close()
		s.wsConn = nil
	}

	if s.httpServer != nil {
		s.httpServer.Close()
	}

	s.running = false
	log.Printf("[%s] OCPP server stopped", s.serialNumber)
	return nil
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[%s] WebSocket upgrade error: %v", s.serialNumber, err)
		return
	}
	defer conn.Close()

	s.mu.Lock()
	s.wsConn = conn
	s.mu.Unlock()

	s.addLog("connect", map[string]interface{}{
		"remote": r.RemoteAddr,
	})

	// Send BootNotification
	go s.sendBootNotification()

	// Heartbeats and meter values belong to this connection.
	done := make(chan struct{})
	defer close(done)
	go s.heartbeatLoop(done)
	go s.meterValuesLoop(done)

	// Read messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			s.addLog("disconnect", map[string]interface{}{
				"error": err.Error(),
			})
			break
		}

		s.handleMessage(message)
	}

	s.mu.Lock()
	s.wsConn = nil
	s.mu.Unlock()
}

func (s *Server) handleMessage(data []byte) {
	s.logFrame("cs->cp", data)
	messageType, messageId, action, payload, err := ParseOCPPMessage(data)
	if err != nil {
		log.Printf("[%s] Failed to parse message: %v", s.serialNumber, err)
		return
	}

	// For responses (CallResult/CallError), look up the original action
	logAction := action
	if messageType == MessageTypeCallResult || messageType == MessageTypeCallError {
		s.mu.RLock()
		if a, ok := s.pendingActions[messageId]; ok {
			logAction = a
		}
		s.mu.RUnlock()
	}

	s.addLog("receive", map[string]interface{}{
		"type":    messageType,
		"id":      messageId,
		"action":  logAction,
		"payload": string(payload),
	})

	switch messageType {
	case MessageTypeCall:
		s.handleCall(messageId, action, payload)
	case MessageTypeCallResult:
		s.handleCallResult(messageId, payload)
	case MessageTypeCallError:
		s.handleCallError(messageId, payload)
	}
}

func (s *Server) handleCall(messageId, action string, payload json.RawMessage) {
	var response interface{}
	var err error

	switch action {
	case ActionRemoteStartTransaction:
		var req RemoteStartTransactionRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleRemoteStartTransaction(req)
		}

	case ActionRemoteStopTransaction:
		var req RemoteStopTransactionRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleRemoteStopTransaction(req)
		}

	case ActionSetChargingProfile:
		var req SetChargingProfileRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleSetChargingProfile(req)
		}

	case ActionClearChargingProfile:
		var req ClearChargingProfileRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleClearChargingProfile(req)
		}

	case ActionGetConfiguration:
		var req GetConfigurationRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleGetConfiguration(req)
		}

	case ActionChangeConfiguration:
		var req ChangeConfigurationRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleChangeConfiguration(req)
		}

	case ActionReset:
		var req ResetRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleReset(req)
		}

	case ActionGetLocalListVersion:
		response = s.handleGetLocalListVersion()

	case ActionSendLocalList:
		var req SendLocalListRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleSendLocalList(req)
		}

	case ActionTriggerMessage:
		var req TriggerMessageRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleTriggerMessage(messageId, req)
		}

	case ActionUnlockConnector:
		var req UnlockConnectorRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleUnlockConnector(req)
		}

	case ActionDataTransfer:
		var req DataTransferRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleDataTransfer(req)
		}

	case ActionChangeAvailability:
		var req ChangeAvailabilityRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleChangeAvailability(req)
		}

	case ActionClearCache:
		response = s.handleClearCache()

	case ActionGetCompositeSchedule:
		var req GetCompositeScheduleRequest
		if err = json.Unmarshal(payload, &req); err == nil {
			response = s.handleGetCompositeSchedule(req)
		}

	default:
		log.Printf("[%s] Unknown action: %s", s.serialNumber, action)
		s.sendError(messageId, "NotImplemented", "Action not implemented", nil)
		return
	}

	if err != nil {
		s.sendError(messageId, "FormationViolation", err.Error(), nil)
		return
	}

	s.sendResponse(messageId, response)
}

func (s *Server) handleCallResult(messageId string, payload json.RawMessage) {
	s.mu.Lock()
	ch, ok := s.pendingRequests[messageId]
	delete(s.pendingRequests, messageId)
	delete(s.pendingActions, messageId)
	s.mu.Unlock()

	if ok {
		ch <- payload
	}
}

func (s *Server) handleCallError(messageId string, payload json.RawMessage) {
	log.Printf("[%s] Received error for message %s: %s", s.serialNumber, messageId, string(payload))

	s.mu.Lock()
	ch, ok := s.pendingRequests[messageId]
	delete(s.pendingRequests, messageId)
	delete(s.pendingActions, messageId)
	s.mu.Unlock()

	if ok {
		close(ch)
	}
}

func (s *Server) handleRemoteStartTransaction(req RemoteStartTransactionRequest) *RemoteStartTransactionResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	connectorId := req.ConnectorId
	if connectorId == 0 {
		// Find available connector
		for id, conn := range s.connectors {
			if id > 0 && conn.Status == StatusAvailable {
				connectorId = id
				break
			}
		}
	}

	if connectorId == 0 {
		return &RemoteStartTransactionResponse{Status: "Rejected"}
	}

	conn := s.connectors[connectorId]
	if conn.Status != StatusAvailable {
		return &RemoteStartTransactionResponse{Status: "Rejected"}
	}

	// Start transaction
	txId := len(s.transactions) + 1
	s.transactions[txId] = &Transaction{
		ID:          txId,
		ConnectorID: connectorId,
		IdTag:       req.IdTag,
		StartTime:   time.Now(),
		MeterStart:  int(conn.EnergyWh),
		MeterNow:    int(conn.EnergyWh),
	}

	conn.Status = StatusCharging

	// Apply charging profile if provided
	if req.ChargingProfile != nil {
		s.applyChargingProfile(connectorId, req.ChargingProfile)
	}

	// Send StatusNotification and StartTransaction asynchronously
	go func() {
		s.sendStatusNotification(connectorId, StatusPreparing)
		time.Sleep(500 * time.Millisecond)
		s.sendStatusNotification(connectorId, StatusCharging)
		s.sendStartTransaction(connectorId, req.IdTag, int(conn.EnergyWh))
	}()

	return &RemoteStartTransactionResponse{Status: "Accepted"}
}

func (s *Server) handleRemoteStopTransaction(req RemoteStopTransactionRequest) *RemoteStopTransactionResponse {
	s.mu.Lock()
	tx, ok := s.transactions[req.TransactionId]
	if !ok {
		s.mu.Unlock()
		return &RemoteStopTransactionResponse{Status: "Rejected"}
	}

	conn := s.connectors[tx.ConnectorID]
	meterStop := int(conn.EnergyWh)
	s.mu.Unlock()

	// Send StopTransaction asynchronously
	go func() {
		s.sendStopTransaction(req.TransactionId, tx.IdTag, meterStop)

		s.mu.Lock()
		conn.Status = StatusFinishing
		delete(s.transactions, req.TransactionId)
		s.mu.Unlock()

		s.sendStatusNotification(tx.ConnectorID, StatusFinishing)
		time.Sleep(500 * time.Millisecond)

		s.mu.Lock()
		conn.Status = StatusAvailable
		s.mu.Unlock()

		s.sendStatusNotification(tx.ConnectorID, StatusAvailable)
	}()

	return &RemoteStopTransactionResponse{Status: "Accepted"}
}

func (s *Server) handleSetChargingProfile(req SetChargingProfileRequest) *SetChargingProfileResponse {
	if req.ConnectorId < 0 {
		return &SetChargingProfileResponse{Status: "Rejected"}
	}
	// Connector 0 is only valid for ChargePointMaxProfile
	if req.ConnectorId == 0 && req.CsChargingProfiles.ChargingProfilePurpose != "ChargePointMaxProfile" {
		return &SetChargingProfileResponse{Status: "Rejected"}
	}

	profile := req.CsChargingProfiles
	if s.deferProfileChange(func() { s.applyChargingProfile(req.ConnectorId, &profile) }) {
		return &SetChargingProfileResponse{Status: "Accepted"}
	}
	s.applyChargingProfile(req.ConnectorId, &profile)
	return &SetChargingProfileResponse{Status: "Accepted"}
}

func (s *Server) handleClearChargingProfile(req ClearChargingProfileRequest) *ClearChargingProfileResponse {
	if s.deferProfileChange(func() { s.clearChargingProfiles(req) }) {
		return &ClearChargingProfileResponse{Status: "Accepted"}
	}
	cleared := s.clearChargingProfiles(req)
	if cleared {
		return &ClearChargingProfileResponse{Status: "Accepted"}
	}
	return &ClearChargingProfileResponse{Status: "Unknown"}
}

// deferProfileChange schedules apply after the profile delay and reports
// whether it did. Changes take effect in the order they were accepted.
func (s *Server) deferProfileChange(apply func()) bool {
	d := s.ProfileDelay()
	if d <= 0 {
		return false
	}
	s.laterMu.Lock()
	s.later = append(s.later, delayedOp{due: time.Now().Add(d), apply: apply})
	s.laterMu.Unlock()
	time.AfterFunc(d, s.runDueProfileChanges)
	return true
}

func (s *Server) runDueProfileChanges() {
	s.laterRunMu.Lock()
	defer s.laterRunMu.Unlock()
	for {
		s.laterMu.Lock()
		if len(s.later) == 0 || s.later[0].due.After(time.Now()) {
			s.laterMu.Unlock()
			return
		}
		op := s.later[0]
		s.later = s.later[1:]
		s.laterMu.Unlock()
		op.apply()
	}
}

func (s *Server) handleGetConfiguration(req GetConfigurationRequest) *GetConfigurationResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	response := &GetConfigurationResponse{
		ConfigurationKey: make([]ConfigurationKey, 0),
		UnknownKey:       make([]string, 0),
	}

	// If no specific keys requested, return all configuration
	if len(req.Key) == 0 {
		for key, value := range s.configuration {
			response.ConfigurationKey = append(response.ConfigurationKey, ConfigurationKey{
				Key:      key,
				Readonly: readonlyConfigKeys[key],
				Value:    value,
			})
		}
		return response
	}

	// Return requested keys
	for _, key := range req.Key {
		if value, ok := s.configuration[key]; ok {
			response.ConfigurationKey = append(response.ConfigurationKey, ConfigurationKey{
				Key:      key,
				Readonly: readonlyConfigKeys[key],
				Value:    value,
			})
		} else {
			response.UnknownKey = append(response.UnknownKey, key)
		}
	}

	return response
}

func (s *Server) handleChangeConfiguration(req ChangeConfigurationRequest) *ChangeConfigurationResponse {
	// Reject changes to readonly keys
	if readonlyConfigKeys[req.Key] {
		log.Printf("[%s] Configuration change rejected (readonly): %s", s.serialNumber, req.Key)
		return &ChangeConfigurationResponse{Status: "Rejected"}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.configuration[req.Key] = req.Value
	log.Printf("[%s] Configuration changed: %s = %s", s.serialNumber, req.Key, req.Value)
	return &ChangeConfigurationResponse{Status: "Accepted"}
}

func (s *Server) handleReset(req ResetRequest) *ResetResponse {
	log.Printf("[%s] Reset requested: %s", s.serialNumber, req.Type)

	// For soft reset, just accept and continue
	// For hard reset, we could restart the connection
	if req.Type == "Hard" {
		// Disconnect and reconnect
		go func() {
			time.Sleep(1 * time.Second)
			s.mu.Lock()
			if s.wsConn != nil {
				s.wsConn.Close()
			}
			s.mu.Unlock()
		}()
	}

	return &ResetResponse{Status: "Accepted"}
}

func (s *Server) handleGetLocalListVersion() *GetLocalListVersionResponse {
	log.Printf("[%s] GetLocalListVersion requested", s.serialNumber)
	return &GetLocalListVersionResponse{ListVersion: 0}
}

func (s *Server) handleSendLocalList(req SendLocalListRequest) *SendLocalListResponse {
	log.Printf("[%s] SendLocalList received: version=%d, updateType=%s, entries=%d",
		s.serialNumber, req.ListVersion, req.UpdateType, len(req.LocalAuthorizationList))
	return &SendLocalListResponse{Status: "Accepted"}
}

func (s *Server) handleTriggerMessage(messageId string, req TriggerMessageRequest) *TriggerMessageResponse {
	log.Printf("[%s] TriggerMessage: %s", s.serialNumber, req.RequestedMessage)

	switch req.RequestedMessage {
	case "StatusNotification":
		go func() {
			s.mu.RLock()
			connectorIds := make([]int, 0)
			if req.ConnectorId != nil {
				connectorIds = append(connectorIds, *req.ConnectorId)
			} else {
				for id := range s.connectors {
					connectorIds = append(connectorIds, id)
				}
			}
			statuses := make(map[int]string)
			for _, id := range connectorIds {
				if c, ok := s.connectors[id]; ok {
					statuses[id] = c.Status
				}
			}
			s.mu.RUnlock()

			sort.Ints(connectorIds)
			for _, id := range connectorIds {
				if status, ok := statuses[id]; ok {
					s.sendStatusNotification(id, status)
				}
			}
		}()
		return &TriggerMessageResponse{Status: "Accepted"}

	case "MeterValues":
		go s.sendMeterValuesForActiveTransactions()
		return &TriggerMessageResponse{Status: "Accepted"}

	case "Heartbeat":
		go s.sendRequest(ActionHeartbeat, HeartbeatRequest{})
		return &TriggerMessageResponse{Status: "Accepted"}

	case "BootNotification":
		go s.sendBootNotification()
		return &TriggerMessageResponse{Status: "Accepted"}

	default:
		return &TriggerMessageResponse{Status: "NotImplemented"}
	}
}

func (s *Server) handleUnlockConnector(req UnlockConnectorRequest) *UnlockConnectorResponse {
	s.mu.Lock()
	conn, ok := s.connectors[req.ConnectorId]
	if !ok || req.ConnectorId <= 0 {
		s.mu.Unlock()
		return &UnlockConnectorResponse{Status: "NotSupported"}
	}

	// Stop active transaction on this connector if any
	for txId, tx := range s.transactions {
		if tx.ConnectorID == req.ConnectorId {
			meterStop := int(conn.EnergyWh)
			idTag := tx.IdTag
			delete(s.transactions, txId)
			conn.Status = StatusAvailable
			conn.CurrentPower = 0
			s.mu.Unlock()
			go func() {
				s.sendStopTransaction(txId, idTag, meterStop)
				s.sendStatusNotification(req.ConnectorId, StatusAvailable)
			}()
			return &UnlockConnectorResponse{Status: "Unlocked"}
		}
	}

	conn.Status = StatusAvailable
	conn.CurrentPower = 0
	s.mu.Unlock()
	go s.sendStatusNotification(req.ConnectorId, StatusAvailable)
	return &UnlockConnectorResponse{Status: "Unlocked"}
}

func (s *Server) handleDataTransfer(req DataTransferRequest) *DataTransferResponse {
	log.Printf("[%s] DataTransfer from vendor=%s messageId=%s", s.serialNumber, req.VendorId, req.MessageId)
	return &DataTransferResponse{Status: "Accepted"}
}

func (s *Server) handleChangeAvailability(req ChangeAvailabilityRequest) *ChangeAvailabilityResponse {
	s.mu.Lock()

	// ConnectorId 0 means entire charge point
	if req.ConnectorId == 0 {
		// Check if any connector has an active transaction
		hasActiveTx := false
		for _, tx := range s.transactions {
			if tx.ConnectorID > 0 {
				hasActiveTx = true
				break
			}
		}

		if req.Type == "Inoperative" {
			// Set all idle connectors to Unavailable
			for id, conn := range s.connectors {
				if id == 0 {
					continue
				}
				if conn.Status == StatusAvailable || conn.Status == StatusReserved {
					conn.Status = StatusUnavailable
					go s.sendStatusNotification(id, StatusUnavailable)
				}
			}
			s.mu.Unlock()
			if hasActiveTx {
				return &ChangeAvailabilityResponse{Status: "Scheduled"}
			}
			return &ChangeAvailabilityResponse{Status: "Accepted"}
		}

		// Operative — set all non-charging connectors to Available
		for id, conn := range s.connectors {
			if id == 0 {
				continue
			}
			if conn.Status == StatusUnavailable {
				conn.Status = StatusAvailable
				go s.sendStatusNotification(id, StatusAvailable)
			}
		}
		s.mu.Unlock()
		return &ChangeAvailabilityResponse{Status: "Accepted"}
	}

	// Specific connector
	conn, ok := s.connectors[req.ConnectorId]
	if !ok {
		s.mu.Unlock()
		return &ChangeAvailabilityResponse{Status: "Rejected"}
	}

	// Check if connector has an active transaction
	hasActiveTx := false
	for _, tx := range s.transactions {
		if tx.ConnectorID == req.ConnectorId {
			hasActiveTx = true
			break
		}
	}

	if req.Type == "Inoperative" {
		if hasActiveTx {
			s.mu.Unlock()
			return &ChangeAvailabilityResponse{Status: "Scheduled"}
		}
		conn.Status = StatusUnavailable
		conn.CurrentPower = 0
		s.mu.Unlock()
		go s.sendStatusNotification(req.ConnectorId, StatusUnavailable)
		return &ChangeAvailabilityResponse{Status: "Accepted"}
	}

	// Operative
	if conn.Status == StatusUnavailable {
		conn.Status = StatusAvailable
		s.mu.Unlock()
		go s.sendStatusNotification(req.ConnectorId, StatusAvailable)
		return &ChangeAvailabilityResponse{Status: "Accepted"}
	}
	s.mu.Unlock()
	return &ChangeAvailabilityResponse{Status: "Accepted"}
}

func (s *Server) handleClearCache() *ClearCacheResponse {
	log.Printf("[%s] ClearCache requested", s.serialNumber)
	return &ClearCacheResponse{Status: "Accepted"}
}

func (s *Server) handleGetCompositeSchedule(req GetCompositeScheduleRequest) *GetCompositeScheduleResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	connectorId := req.ConnectorId
	if connectorId < 0 || (connectorId > 0 && s.connectors[connectorId] == nil) {
		return &GetCompositeScheduleResponse{Status: "Rejected"}
	}

	now := time.Now()
	unit := "W"
	if req.ChargingRateUnit == "A" {
		unit = "A"
	}

	// Sample the effective power at 1-second resolution, but emit periods only on changes.
	// To keep it reasonable, sample at 60-second intervals for schedules > 600s.
	step := 1
	if req.Duration > 600 {
		step = 60
	}

	var periods []ChargingSchedulePeriod
	lastLimit, lastPhases := -1.0, 0

	for offset := 0; offset <= req.Duration; offset += step {
		t := now.Add(time.Duration(offset) * time.Second)
		powerW, phases := s.computeEffectiveLocked(connectorId, t)

		var limit float64
		if unit == "A" {
			avgV := s.averagePhaseVoltageLocked()
			if avgV > 0 {
				limit = math.Round(powerW/(float64(phases)*avgV)*10) / 10 // round to 0.1A
			}
		} else {
			limit = math.Round(powerW)
		}

		// A change of phases is a new period even at the same limit.
		if limit != lastLimit || phases != lastPhases {
			n := phases
			periods = append(periods, ChargingSchedulePeriod{
				StartPeriod:  int64(offset),
				Limit:        limit,
				NumberPhases: &n,
			})
			lastLimit, lastPhases = limit, phases
		}
	}

	if len(periods) == 0 {
		return &GetCompositeScheduleResponse{Status: "Rejected"}
	}

	dur := int64(req.Duration)
	schedule := &ChargingSchedule{
		Duration:               &dur,
		StartSchedule:          FormatTimestamp(now),
		ChargingRateUnit:       unit,
		ChargingSchedulePeriod: periods,
	}

	return &GetCompositeScheduleResponse{
		Status:           "Accepted",
		ConnectorId:      &connectorId,
		ScheduleStart:    FormatTimestamp(now),
		ChargingSchedule: schedule,
	}
}

func (s *Server) applyChargingProfile(connectorId int, profile *ChargingProfile) {
	if profile == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.chargingProfiles == nil {
		s.chargingProfiles = make(map[string]*storedProfile)
	}
	key := profileKey(connectorId, profile.ChargingProfilePurpose, profile.StackLevel)
	s.chargingProfiles[key] = &storedProfile{
		ConnectorID: connectorId,
		Profile:     *profile,
	}
}

func (s *Server) clearChargingProfiles(req ClearChargingProfileRequest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	cleared := false
	for key, sp := range s.chargingProfiles {
		if sp == nil {
			continue
		}
		if req.ConnectorId != nil && sp.ConnectorID != *req.ConnectorId {
			continue
		}
		if req.Id != nil && sp.Profile.ChargingProfileId != *req.Id {
			continue
		}
		if req.ChargingProfilePurpose != "" && sp.Profile.ChargingProfilePurpose != req.ChargingProfilePurpose {
			continue
		}
		if req.StackLevel != nil && sp.Profile.StackLevel != *req.StackLevel {
			continue
		}
		delete(s.chargingProfiles, key)
		cleared = true
	}

	return cleared
}

// evaluateProfileLimit determines the current power limit (in watts) from a single
// charging profile evaluated at the given time. txStartTime is used for Relative profiles.
// Returns (limitW, active) — active=false if the profile has no applicable period at this time.
func (s *Server) evaluateProfileLimit(profile *ChargingProfile, now time.Time, txStartTime time.Time) (float64, bool) {
	w, _, active := s.evaluateProfile(profile, now, txStartTime)
	return w, active
}

// evaluateProfile is evaluateProfileLimit plus the active period's
// numberPhases (3 when it names none).
func (s *Server) evaluateProfile(profile *ChargingProfile, now time.Time, txStartTime time.Time) (float64, int, bool) {
	if profile == nil {
		return 0, 0, false
	}

	schedule := profile.ChargingSchedule
	if len(schedule.ChargingSchedulePeriod) == 0 {
		return 0, 0, false
	}

	var elapsed float64 // seconds since schedule reference point

	switch profile.ChargingProfileKind {
	case "Recurring":
		startSchedule, err := time.Parse(time.RFC3339, schedule.StartSchedule)
		if err != nil {
			return 0, 0, false
		}
		if profile.RecurrencyKind == "Daily" {
			// Compute seconds since today's occurrence of startSchedule
			todayStart := time.Date(now.Year(), now.Month(), now.Day(),
				startSchedule.Hour(), startSchedule.Minute(), startSchedule.Second(), 0, now.Location())
			if now.Before(todayStart) {
				// Before today's occurrence — wrap to yesterday's
				todayStart = todayStart.Add(-24 * time.Hour)
			}
			elapsed = now.Sub(todayStart).Seconds()
		} else if profile.RecurrencyKind == "Weekly" {
			// Compute seconds since this week's occurrence
			dayDiff := (int(now.Weekday()) - int(startSchedule.Weekday()) + 7) % 7
			weekStart := time.Date(now.Year(), now.Month(), now.Day()-dayDiff,
				startSchedule.Hour(), startSchedule.Minute(), startSchedule.Second(), 0, now.Location())
			if now.Before(weekStart) {
				weekStart = weekStart.Add(-7 * 24 * time.Hour)
			}
			elapsed = now.Sub(weekStart).Seconds()
		} else {
			return 0, 0, false
		}

	case "Relative":
		elapsed = now.Sub(txStartTime).Seconds()
		if elapsed < 0 {
			return 0, 0, false
		}

	case "Absolute":
		startSchedule, err := time.Parse(time.RFC3339, schedule.StartSchedule)
		if err != nil {
			return 0, 0, false
		}
		elapsed = now.Sub(startSchedule).Seconds()
		if elapsed < 0 {
			return 0, 0, false
		}

	default:
		// Unknown kind — try absolute-style with startSchedule
		if schedule.StartSchedule != "" {
			startSchedule, err := time.Parse(time.RFC3339, schedule.StartSchedule)
			if err != nil {
				return 0, 0, false
			}
			elapsed = now.Sub(startSchedule).Seconds()
			if elapsed < 0 {
				return 0, 0, false
			}
		} else {
			elapsed = 0
		}
	}

	// Check schedule duration
	if schedule.Duration != nil && *schedule.Duration > 0 && elapsed >= float64(*schedule.Duration) {
		return 0, 0, false
	}

	// Find active period: last period where startPeriod <= elapsed
	var activePeriod *ChargingSchedulePeriod
	for i := range schedule.ChargingSchedulePeriod {
		p := &schedule.ChargingSchedulePeriod[i]
		if float64(p.StartPeriod) <= elapsed {
			activePeriod = p
		} else {
			break // periods are ordered by startPeriod
		}
	}

	if activePeriod == nil {
		return 0, 0, false
	}

	limit := activePeriod.Limit
	phases := 3
	if activePeriod.NumberPhases != nil && *activePeriod.NumberPhases > 0 {
		phases = *activePeriod.NumberPhases
	}
	// Convert to watts
	unit := strings.ToUpper(strings.TrimSpace(schedule.ChargingRateUnit))
	switch unit {
	case "A", "AMPERE", "CURRENT":
		avgVoltage := s.averagePhaseVoltageLocked()
		return limit * float64(phases) * avgVoltage, phases, true
	case "W", "WATT", "POWER":
		return limit, phases, true
	default:
		return limit, phases, true
	}
}

// computeEffectivePowerLocked determines the current effective power limit for a connector.
// Must be called with s.mu held (at least read lock).
// Priority: TxProfile > TxDefaultProfile, capped by ChargePointMaxProfile on connector 0.
func (s *Server) computeEffectivePowerLocked(connectorId int, now time.Time) float64 {
	w, _ := s.computeEffectiveLocked(connectorId, now)
	return w
}

// computeEffectiveLocked is computeEffectivePowerLocked plus the phases the
// car draws on. A charger that can switch phases follows the active Tx or
// TxDefault profile's numberPhases; on one phase it gives at most its rated
// current, a third of its three-phase power. Any other charger draws on
// three phases.
func (s *Server) computeEffectiveLocked(connectorId int, now time.Time) (float64, int) {
	maxW := s.maxPowerKW * 1000
	phases := 3

	// Find the transaction start time for this connector (needed for Relative profiles)
	var txStartTime time.Time
	for _, tx := range s.transactions {
		if tx.ConnectorID == connectorId {
			txStartTime = tx.StartTime
			break
		}
	}

	// Find highest-stackLevel TxProfile for this connector
	bestLimit := -1.0
	bestStack := -1
	bestPhases := 3
	for _, sp := range s.chargingProfiles {
		if sp.ConnectorID != connectorId {
			continue
		}
		if sp.Profile.ChargingProfilePurpose != "TxProfile" {
			continue
		}
		if sp.Profile.StackLevel <= bestStack {
			continue
		}
		if limitW, n, active := s.evaluateProfile(&sp.Profile, now, txStartTime); active {
			bestLimit = limitW
			bestStack = sp.Profile.StackLevel
			bestPhases = n
		}
	}

	// If no active TxProfile, try TxDefaultProfile
	if bestLimit < 0 {
		bestStack = -1
		for _, sp := range s.chargingProfiles {
			if sp.ConnectorID != connectorId {
				continue
			}
			if sp.Profile.ChargingProfilePurpose != "TxDefaultProfile" {
				continue
			}
			if sp.Profile.StackLevel <= bestStack {
				continue
			}
			if limitW, n, active := s.evaluateProfile(&sp.Profile, now, txStartTime); active {
				bestLimit = limitW
				bestStack = sp.Profile.StackLevel
				bestPhases = n
			}
		}
	}

	// No profiles active → use max power
	effectiveW := maxW
	if bestLimit >= 0 {
		effectiveW = bestLimit
	}

	// Cap against ChargePointMaxProfile (connector 0)
	capStack := -1
	capLimit := -1.0
	for _, sp := range s.chargingProfiles {
		if sp.ConnectorID != 0 {
			continue
		}
		if sp.Profile.ChargingProfilePurpose != "ChargePointMaxProfile" {
			continue
		}
		if sp.Profile.StackLevel <= capStack {
			continue
		}
		if limitW, active := s.evaluateProfileLimit(&sp.Profile, now, txStartTime); active {
			capLimit = limitW
			capStack = sp.Profile.StackLevel
		}
	}
	if capLimit >= 0 {
		effectiveW = math.Min(effectiveW, capLimit)
	}

	if bestLimit >= 0 && bestPhases == 1 && s.phaseSwitchingLocked() {
		phases = 1
		maxW /= 3
	}

	// Cap at hardware max
	effectiveW = math.Min(effectiveW, maxW)
	if effectiveW < 0 {
		effectiveW = 0
	}

	return effectiveW, phases
}

// phaseSwitchingLocked reports ConnectorSwitch3to1PhaseSupported.
func (s *Server) phaseSwitchingLocked() bool {
	ok, _ := strconv.ParseBool(s.configuration["ConnectorSwitch3to1PhaseSupported"])
	return ok
}

func (s *Server) averagePhaseVoltageLocked() float64 {
	v1, v2, v3 := s.voltageL1, s.voltageL2, s.voltageL3
	if v1 <= 0 && v2 <= 0 && v3 <= 0 {
		return 230.0
	}
	return (v1 + v2 + v3) / 3.0
}

func (s *Server) getMeterValueSampleInterval() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.configuration == nil {
		return 30
	}
	value, ok := s.configuration["MeterValueSampleInterval"]
	if !ok {
		return 30
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 30
	}
	return parsed
}

func (s *Server) sendBootNotification() {
	req := BootNotificationRequest{
		ChargePointVendor:       s.vendor,
		ChargePointModel:        s.model,
		ChargePointSerialNumber: s.serialNumber,
		FirmwareVersion:         "1.0.0",
	}

	resp, err := s.sendRequest(ActionBootNotification, req)
	if err != nil {
		log.Printf("[%s] BootNotification failed: %v", s.serialNumber, err)
		return
	}

	var bootResp BootNotificationResponse
	if err := json.Unmarshal(resp, &bootResp); err != nil {
		log.Printf("[%s] Failed to parse BootNotification response: %v", s.serialNumber, err)
		return
	}

	log.Printf("[%s] BootNotification status: %s", s.serialNumber, bootResp.Status)

	if bootResp.Status != "Accepted" {
		return
	}

	if bootResp.Interval > 0 {
		s.mu.Lock()
		s.configuration["HeartbeatInterval"] = fmt.Sprintf("%d", bootResp.Interval)
		s.mu.Unlock()
	}

	// Send StatusNotification for connector 0 (charge point itself)
	s.sendStatusNotification(0, StatusAvailable)

	// Send StatusNotification for each physical connector
	s.mu.RLock()
	connectorIds := make([]int, 0)
	for id := range s.connectors {
		if id > 0 {
			connectorIds = append(connectorIds, id)
		}
	}
	s.mu.RUnlock()

	sort.Ints(connectorIds)
	for _, id := range connectorIds {
		s.mu.RLock()
		status := s.connectors[id].Status
		s.mu.RUnlock()
		s.sendStatusNotification(id, status)
	}

	s.flushTxQueue()
}

func (s *Server) getHeartbeatInterval() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if val, ok := s.configuration["HeartbeatInterval"]; ok {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 60
}

func (s *Server) heartbeatLoop(done <-chan struct{}) {
	interval := s.getHeartbeatInterval()
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-done:
			return
		case <-ticker.C:
			s.sendRequest(ActionHeartbeat, HeartbeatRequest{})
			// Check if interval changed (e.g. from BootNotification response)
			newInterval := s.getHeartbeatInterval()
			if newInterval != interval {
				interval = newInterval
				ticker.Reset(time.Duration(interval) * time.Second)
			}
		}
	}
}

// simulationLoop updates simulation values every second for as long as the
// charger runs, connected or not: a car charges through a network outage.
func (s *Server) simulationLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.GenerateValues(time.Now())
		}
	}
}

func (s *Server) sendStatusNotification(connectorId int, status string) {
	s.mu.RLock()
	conn := s.connectors[connectorId]
	s.mu.RUnlock()

	req := StatusNotificationRequest{
		ConnectorId: connectorId,
		ErrorCode:   conn.ErrorCode,
		Status:      status,
		Timestamp:   FormatTimestamp(time.Now()),
	}

	s.sendRequest(ActionStatusNotification, req)
}

func (s *Server) sendStartTransaction(connectorId int, idTag string, meterStart int) {
	req := StartTransactionRequest{
		ConnectorId: connectorId,
		IdTag:       idTag,
		MeterStart:  meterStart,
		Timestamp:   FormatTimestamp(time.Now()),
	}
	localID := 0
	s.mu.RLock()
	for id, tx := range s.transactions {
		if tx.ConnectorID == connectorId && tx.IdTag == idTag {
			localID = id
			break
		}
	}
	s.mu.RUnlock()

	s.sendTxRequest(ActionStartTransaction, req, func(resp json.RawMessage) {
		var txResp StartTransactionResponse
		if err := json.Unmarshal(resp, &txResp); err != nil {
			return
		}
		log.Printf("[%s] Transaction started, server ID: %d", s.serialNumber, txResp.TransactionId)

		// Rename the local transaction to the server's ID, or, if it has
		// already ended, remember the ID for its queued StopTransaction.
		s.mu.Lock()
		if tx := s.transactions[localID]; tx != nil {
			delete(s.transactions, localID)
			tx.ID = txResp.TransactionId
			s.transactions[txResp.TransactionId] = tx
		} else if localID != 0 {
			s.txAssigned[localID] = txResp.TransactionId
		}
		s.mu.Unlock()
	})
}

func (s *Server) sendStopTransaction(transactionId int, idTag string, meterStop int) {
	req := StopTransactionRequest{
		TransactionId: transactionId,
		IdTag:         idTag,
		MeterStop:     meterStop,
		Timestamp:     FormatTimestamp(time.Now()),
	}

	s.sendTxRequest(ActionStopTransaction, req, nil)
}

// sendTxRequest sends a transaction message, or queues it while there is no
// connection. Without the queue a car plugged in before the charger reached
// its Central System charged in a session the Central System never heard of.
func (s *Server) sendTxRequest(action string, payload interface{}, onResult func(json.RawMessage)) {
	resp, err := s.sendRequest(action, payload)
	if errors.Is(err, errNotConnected) {
		s.txQueueMu.Lock()
		s.txQueue = append(s.txQueue, queuedTx{action: action, payload: payload, onResult: onResult})
		s.txQueueMu.Unlock()
		log.Printf("[%s] %s queued until the charger is online", s.serialNumber, action)
		return
	}
	if err != nil {
		log.Printf("[%s] %s failed: %v", s.serialNumber, action, err)
		return
	}
	if onResult != nil {
		onResult(resp)
	}
}

// flushTxQueue delivers queued transaction messages in order. It runs after
// a BootNotification is accepted.
func (s *Server) flushTxQueue() {
	s.txQueueMu.Lock()
	queue := s.txQueue
	s.txQueue = nil
	s.txQueueMu.Unlock()

	for i := range queue {
		m := &queue[i]
		if stop, ok := m.payload.(StopTransactionRequest); ok {
			s.mu.Lock()
			if id, ok := s.txAssigned[stop.TransactionId]; ok {
				delete(s.txAssigned, stop.TransactionId)
				stop.TransactionId = id
				m.payload = stop
			}
			s.mu.Unlock()
		}
		resp, err := s.sendRequest(m.action, m.payload)
		if errors.Is(err, errNotConnected) {
			// Offline again: keep the rest, in front of anything newer.
			s.txQueueMu.Lock()
			s.txQueue = append(append([]queuedTx{}, queue[i:]...), s.txQueue...)
			s.txQueueMu.Unlock()
			return
		}
		if err != nil {
			log.Printf("[%s] queued %s failed: %v", s.serialNumber, m.action, err)
			continue
		}
		if m.onResult != nil {
			m.onResult(resp)
		}
	}
}

func (s *Server) sendRequest(action string, payload interface{}) (json.RawMessage, error) {
	s.mu.Lock()
	conn := s.wsConn
	if conn == nil {
		s.mu.Unlock()
		return nil, errNotConnected
	}

	messageId := fmt.Sprintf("%d", atomic.AddInt64(&s.messageIdSeq, 1))
	responseChan := make(chan json.RawMessage, 1)
	s.pendingRequests[messageId] = responseChan
	s.pendingActions[messageId] = action
	s.mu.Unlock()

	msg, err := CreateCallMessage(messageId, action, payload)
	if err != nil {
		return nil, err
	}

	s.addLog("send", map[string]interface{}{
		"action":  action,
		"id":      messageId,
		"payload": payload,
	})

	// Serialize WebSocket writes to prevent concurrent write panic
	s.writeMu.Lock()
	s.logFrame("cp->cs", msg)
	err = conn.WriteMessage(websocket.TextMessage, msg)
	s.writeMu.Unlock()

	if err != nil {
		s.mu.Lock()
		delete(s.pendingRequests, messageId)
		delete(s.pendingActions, messageId)
		s.mu.Unlock()
		return nil, err
	}

	select {
	case resp := <-responseChan:
		return resp, nil
	case <-time.After(30 * time.Second):
		s.mu.Lock()
		delete(s.pendingRequests, messageId)
		delete(s.pendingActions, messageId)
		s.mu.Unlock()
		return nil, fmt.Errorf("timeout waiting for response")
	}
}

func (s *Server) sendResponse(messageId string, payload interface{}) {
	s.mu.RLock()
	conn := s.wsConn
	s.mu.RUnlock()

	if conn == nil {
		return
	}

	msg, err := CreateCallResultMessage(messageId, payload)
	if err != nil {
		log.Printf("[%s] Failed to create response: %v", s.serialNumber, err)
		return
	}

	s.addLog("send", map[string]interface{}{
		"type":    "response",
		"id":      messageId,
		"payload": payload,
	})

	// Serialize WebSocket writes
	s.writeMu.Lock()
	s.logFrame("cp->cs", msg)
	if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
		log.Printf("[%s] Failed to send response: %v", s.serialNumber, err)
	}
	s.writeMu.Unlock()
}

func (s *Server) sendError(messageId, errorCode, errorDescription string, errorDetails interface{}) {
	s.mu.RLock()
	conn := s.wsConn
	s.mu.RUnlock()

	if conn == nil {
		return
	}

	msg, err := CreateCallErrorMessage(messageId, errorCode, errorDescription, errorDetails)
	if err != nil {
		return
	}

	// Serialize WebSocket writes
	s.writeMu.Lock()
	s.logFrame("cp->cs", msg)
	if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
		log.Printf("[%s] Failed to send error response: %v", s.serialNumber, err)
	}
	s.writeMu.Unlock()
}

func (s *Server) addLog(operation string, details map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

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

// DeviceServer interface implementation

// GetState returns the current charger state
func (s *Server) GetState() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	connectors := make([]map[string]interface{}, 0)
	ids := make([]int, 0, len(s.connectors))
	for id := range s.connectors {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		c := s.connectors[id]
		if c == nil {
			continue
		}
		connectors = append(connectors, map[string]interface{}{
			"id":           c.ID,
			"status":       c.Status,
			"errorCode":    c.ErrorCode,
			"currentPower": c.CurrentPower,
			"energyWh":     c.EnergyWh,
			"phases":       c.Phases,
		})
	}

	transactions := make([]map[string]interface{}, 0)
	for _, tx := range s.transactions {
		transactions = append(transactions, map[string]interface{}{
			"id":          tx.ID,
			"connectorId": tx.ConnectorID,
			"idTag":       tx.IdTag,
			"startTime":   tx.StartTime,
			"meterStart":  tx.MeterStart,
			"meterNow":    tx.MeterNow,
		})
	}

	// Calculate total power from all connectors
	var totalPower float64
	for _, c := range s.connectors {
		totalPower += c.CurrentPower
	}

	// Check if there's an active transaction
	hasActiveTransaction := len(s.transactions) > 0

	// Copy configuration for safe external use
	configCopy := make(map[string]string, len(s.configuration))
	for k, v := range s.configuration {
		configCopy[k] = v
	}

	phaseMode := "unknown"
	phaseSwitchingSupported := false
	if val, ok := configCopy["ConnectorSwitch3to1PhaseSupported"]; ok {
		if parsed, err := strconv.ParseBool(val); err == nil {
			phaseSwitchingSupported = parsed
			if parsed {
				phaseMode = "1/3-phase"
			} else {
				phaseMode = "3-phase only"
			}
		}
	}

	rateUnitAllowed := configCopy["ChargingScheduleAllowedChargingRateUnit"]
	if strings.EqualFold(rateUnitAllowed, "Current") {
		rateUnitAllowed = "Current (A)"
	} else if strings.EqualFold(rateUnitAllowed, "Power") {
		rateUnitAllowed = "Power (W)"
	}

	meterInterval := 0
	if val, ok := configCopy["MeterValueSampleInterval"]; ok {
		if parsed, err := strconv.Atoi(val); err == nil {
			meterInterval = parsed
		}
	}

	return map[string]interface{}{
		"serialNumber":  s.serialNumber,
		"vendor":        s.vendor,
		"model":         s.model,
		"numConnectors": s.numConnectors,
		"maxPowerKW":    s.maxPowerKW,
		"connectors":    connectors,
		"transactions":  transactions,
		"connected":     s.wsConn != nil,
		"ocppURL":       s.ocppURL,
		"clientMode":    s.clientMode,
		// 3-phase metering
		"voltageL1":  s.voltageL1,
		"voltageL2":  s.voltageL2,
		"voltageL3":  s.voltageL3,
		"currentL1":  s.currentL1,
		"currentL2":  s.currentL2,
		"currentL3":  s.currentL3,
		"totalPower": totalPower,
		"phasePowers": map[string]float64{"l1": s.phaseW[0], "l2": s.phaseW[1], "l3": s.phaseW[2]},
		// Control state
		"evConnected":              s.evConnected,
		"effectivePowerW":          s.computeEffectivePowerLocked(1, time.Now()),
		"hasActiveTransaction":     hasActiveTransaction,
		"configuration":            configCopy,
		"phaseMode":                phaseMode,
		"phaseSwitchingSupported":  phaseSwitchingSupported,
		"chargingRateUnitAllowed":  rateUnitAllowed,
		"meterValueSampleInterval": meterInterval,
	}
}

// SetValue sets a charger state value
func (s *Server) SetValue(key string, value interface{}) error {
	s.mu.RLock()
	running := s.running
	s.mu.RUnlock()
	if !running {
		return fmt.Errorf("emulator not started")
	}

	switch key {
	case "evConnected":
		// Simulate EV connection on connector 1
		if connected, ok := value.(bool); ok {
			s.mu.Lock()
			s.evConnected = connected
			conn := s.connectors[1]
			if conn == nil {
				s.mu.Unlock()
				log.Printf("[%s] evConnected: connector 1 is nil", s.serialNumber)
				return fmt.Errorf("connector 1 not found")
			}
			log.Printf("[%s] evConnected: setting to %v, current status is %s", s.serialNumber, connected, conn.Status)
			if connected {
				if conn.Status == StatusAvailable {
					conn.Status = StatusPreparing
					s.mu.Unlock()
					log.Printf("[%s] evConnected: changed status to Preparing", s.serialNumber)
					go func() {
						s.sendStatusNotification(1, StatusPreparing)
						time.Sleep(2 * time.Second)
						// Auto-start transaction (simulates local authorization)
						s.mu.Lock()
						c := s.connectors[1]
						if c != nil && c.Status == StatusPreparing {
							s.nextTxId++
							txId := s.nextTxId
							s.transactions[txId] = &Transaction{
								ID:          txId,
								ConnectorID: 1,
								IdTag:       "LOCAL",
								StartTime:   time.Now(),
								MeterStart:  int(c.EnergyWh),
								MeterNow:    int(c.EnergyWh),
							}
							c.Status = StatusCharging
							s.mu.Unlock()
							s.sendStatusNotification(1, StatusCharging)
							s.sendStartTransaction(1, "LOCAL", int(c.EnergyWh))
						} else {
							s.mu.Unlock()
						}
					}()
					return nil
				}
				log.Printf("[%s] evConnected: status already %s, not changing", s.serialNumber, conn.Status)
			} else {
				// Disconnect - stop any active transaction first
				for txId, tx := range s.transactions {
					if tx.ConnectorID == 1 {
						meterStop := int(conn.EnergyWh)
						s.mu.Unlock()
						s.sendStopTransaction(txId, tx.IdTag, meterStop)
						s.mu.Lock()
						delete(s.transactions, txId)
						break
					}
				}
				conn.Status = StatusAvailable
				conn.CurrentPower = 0
				s.mu.Unlock()
				go s.sendStatusNotification(1, StatusAvailable)
				return nil
			}
			s.mu.Unlock()
		}

	case "startCharging":
		// Start a local charging session
		s.mu.Lock()
		conn := s.connectors[1]
		if conn == nil {
			s.mu.Unlock()
			log.Printf("[%s] startCharging: connector 1 is nil", s.serialNumber)
			return fmt.Errorf("connector 1 not found")
		}
		log.Printf("[%s] startCharging: connector status is %s", s.serialNumber, conn.Status)
		if conn.Status == StatusPreparing || conn.Status == StatusAvailable {
			s.nextTxId++
			txId := s.nextTxId
			s.transactions[txId] = &Transaction{
				ID:          txId,
				ConnectorID: 1,
				IdTag:       "LOCAL",
				StartTime:   time.Now(),
				MeterStart:  int(conn.EnergyWh),
				MeterNow:    int(conn.EnergyWh),
			}
			conn.Status = StatusCharging
			s.mu.Unlock()
			log.Printf("[%s] startCharging: starting transaction %d", s.serialNumber, txId)
			go s.sendStatusNotification(1, StatusCharging)
			go s.sendStartTransaction(1, "LOCAL", int(conn.EnergyWh))
			return nil
		}
		log.Printf("[%s] startCharging: cannot start, status is %s", s.serialNumber, conn.Status)
		s.mu.Unlock()

	case "stopCharging":
		// Stop the active charging session
		s.mu.Lock()
		conn := s.connectors[1]
		for txId, tx := range s.transactions {
			if tx.ConnectorID == 1 {
				meterStop := int(conn.EnergyWh)
				delete(s.transactions, txId)
				conn.Status = StatusFinishing
				s.mu.Unlock()
				go func() {
					s.sendStopTransaction(txId, tx.IdTag, meterStop)
					s.sendStatusNotification(1, StatusFinishing)
					time.Sleep(500 * time.Millisecond)
					s.mu.Lock()
					if s.evConnected {
						conn.Status = StatusPreparing
						s.mu.Unlock()
						s.sendStatusNotification(1, StatusPreparing)
					} else {
						conn.Status = StatusAvailable
						s.mu.Unlock()
						s.sendStatusNotification(1, StatusAvailable)
					}
				}()
				return nil
			}
		}
		s.mu.Unlock()

	case "chargingPower":
		// Set target charging power via a synthetic TxProfile on connector 1
		if power, ok := value.(float64); ok {
			syntheticProfile := &ChargingProfile{
				ChargingProfileId:      -1, // synthetic
				StackLevel:             99, // high priority
				ChargingProfilePurpose: "TxProfile",
				ChargingProfileKind:    "Absolute",
				ChargingSchedule: ChargingSchedule{
					ChargingRateUnit: "W",
					StartSchedule:    FormatTimestamp(time.Now().Add(-1 * time.Minute)),
					ChargingSchedulePeriod: []ChargingSchedulePeriod{
						{StartPeriod: 0, Limit: power},
					},
				},
			}
			s.applyChargingProfile(1, syntheticProfile)
		}

	case "suspendEVSE":
		// Charger suspends power delivery (power management, no auth, etc.)
		s.mu.Lock()
		conn := s.connectors[1]
		if conn == nil {
			s.mu.Unlock()
			return fmt.Errorf("connector 1 not found")
		}
		if conn.Status == StatusCharging {
			conn.Status = StatusSuspendedEVSE
			conn.CurrentPower = 0
			s.mu.Unlock()
			go s.sendStatusNotification(1, StatusSuspendedEVSE)
			return nil
		}
		s.mu.Unlock()

	case "suspendEV":
		// Vehicle stops accepting power (battery full, user limit, etc.)
		s.mu.Lock()
		conn := s.connectors[1]
		if conn == nil {
			s.mu.Unlock()
			return fmt.Errorf("connector 1 not found")
		}
		if conn.Status == StatusCharging {
			conn.Status = StatusSuspendedEV
			conn.CurrentPower = 0
			s.mu.Unlock()
			go s.sendStatusNotification(1, StatusSuspendedEV)
			return nil
		}
		s.mu.Unlock()

	case "resumeCharging":
		// Resume charging from suspended state
		s.mu.Lock()
		conn := s.connectors[1]
		if conn == nil {
			s.mu.Unlock()
			return fmt.Errorf("connector 1 not found")
		}
		if conn.Status == StatusSuspendedEVSE || conn.Status == StatusSuspendedEV {
			conn.Status = StatusCharging
			s.mu.Unlock()
			go s.sendStatusNotification(1, StatusCharging)
			return nil
		}
		s.mu.Unlock()

	case "reserve":
		// Reserve connector for specific user
		s.mu.Lock()
		conn := s.connectors[1]
		if conn == nil {
			s.mu.Unlock()
			return fmt.Errorf("connector 1 not found")
		}
		if conn.Status == StatusAvailable {
			conn.Status = StatusReserved
			s.mu.Unlock()
			go s.sendStatusNotification(1, StatusReserved)
			return nil
		}
		s.mu.Unlock()

	case "cancelReservation":
		// Cancel reservation
		s.mu.Lock()
		conn := s.connectors[1]
		if conn == nil {
			s.mu.Unlock()
			return fmt.Errorf("connector 1 not found")
		}
		if conn.Status == StatusReserved {
			conn.Status = StatusAvailable
			s.mu.Unlock()
			go s.sendStatusNotification(1, StatusAvailable)
			return nil
		}
		s.mu.Unlock()

	case "setUnavailable":
		// Put connector out of service (maintenance, restart pending)
		s.mu.Lock()
		conn := s.connectors[1]
		if conn == nil {
			s.mu.Unlock()
			return fmt.Errorf("connector 1 not found")
		}
		// Can set unavailable from most states except active charging
		if conn.Status != StatusCharging {
			conn.Status = StatusUnavailable
			conn.CurrentPower = 0
			s.mu.Unlock()
			go s.sendStatusNotification(1, StatusUnavailable)
			return nil
		}
		s.mu.Unlock()

	case "setAvailable":
		// Return connector to available state
		s.mu.Lock()
		conn := s.connectors[1]
		if conn == nil {
			s.mu.Unlock()
			return fmt.Errorf("connector 1 not found")
		}
		if conn.Status == StatusUnavailable || conn.Status == StatusReserved || conn.Status == StatusFaulted {
			conn.Status = StatusAvailable
			conn.ErrorCode = "NoError"
			s.mu.Unlock()
			go s.sendStatusNotification(1, StatusAvailable)
			return nil
		}
		s.mu.Unlock()

	case "setFaulted":
		// Simulate a fault condition
		s.mu.Lock()
		conn := s.connectors[1]
		if conn == nil {
			s.mu.Unlock()
			return fmt.Errorf("connector 1 not found")
		}
		// Stop any active charging
		for txId, tx := range s.transactions {
			if tx.ConnectorID == 1 {
				meterStop := int(conn.EnergyWh)
				delete(s.transactions, txId)
				go s.sendStopTransaction(txId, tx.IdTag, meterStop)
				break
			}
		}
		conn.Status = StatusFaulted
		conn.CurrentPower = 0
		// Set error code from value if provided
		if errorCode, ok := value.(string); ok && errorCode != "" {
			conn.ErrorCode = errorCode
		} else {
			conn.ErrorCode = "OtherError"
		}
		s.mu.Unlock()
		go s.sendStatusNotification(1, StatusFaulted)
		return nil

	case "clearFault":
		// Clear fault and return to available
		s.mu.Lock()
		conn := s.connectors[1]
		if conn == nil {
			s.mu.Unlock()
			return fmt.Errorf("connector 1 not found")
		}
		if conn.Status == StatusFaulted {
			conn.Status = StatusAvailable
			conn.ErrorCode = "NoError"
			s.mu.Unlock()
			go s.sendStatusNotification(1, StatusAvailable)
			return nil
		}
		s.mu.Unlock()
	}

	return nil
}

// GetLogs returns operation logs
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
	return device.ProtocolOCPP
}

// Category returns the device category
func (s *Server) Category() device.Category {
	return device.CategoryOCPPCharger
}

// Address returns the server address
func (s *Server) Address() string {
	return fmt.Sprintf("ws://localhost:%d", s.port)
}

// SerialNumber returns the serial number
func (s *Server) SerialNumber() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.serialNumber
}

// SetPort updates the server port (should be called while stopped)
func (s *Server) SetPort(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.port = port
}

// SetSerialNumber sets the serial number
func (s *Server) SetSerialNumber(sn string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serialNumber = sn
}

// GenerateValues generates simulated values based on time
func (s *Server) GenerateValues(simTime time.Time) {
	s.mu.Lock()

	// Collect status transitions to send after releasing the lock
	type statusChange struct {
		connectorId int
		status      string
	}
	var statusChanges []statusChange

	// Simulate power consumption for active transactions
	var totalPower float64
	var phaseW [3]float64
	for _, tx := range s.transactions {
		conn := s.connectors[tx.ConnectorID]
		if conn.Status == StatusCharging || conn.Status == StatusSuspendedEVSE {
			powerW, phases := s.carDrawLocked(s.computeEffectiveLocked(tx.ConnectorID, simTime))
			conn.Phases = phases

			if powerW <= 0 && conn.Status == StatusCharging {
				// Power dropped to 0 — transition to SuspendedEVSE
				conn.Status = StatusSuspendedEVSE
				conn.CurrentPower = 0
				statusChanges = append(statusChanges, statusChange{tx.ConnectorID, StatusSuspendedEVSE})
			} else if powerW > 0 && conn.Status == StatusSuspendedEVSE {
				// Power restored — transition back to Charging
				conn.Status = StatusCharging
				conn.CurrentPower = powerW
				statusChanges = append(statusChanges, statusChange{tx.ConnectorID, StatusCharging})
			} else {
				conn.CurrentPower = powerW
			}

			totalPower += conn.CurrentPower
			if phases == 1 {
				phaseW[0] += conn.CurrentPower
			} else {
				for k := range phaseW {
					phaseW[k] += conn.CurrentPower / 3
				}
			}

			// Add energy (Wh) for 1 second at this power
			energyDelta := conn.CurrentPower / 3600.0
			conn.EnergyWh += energyDelta
			tx.MeterNow = int(conn.EnergyWh)
		}
	}

	// Update 3-phase metering from the power on each phase
	s.phaseW = phaseW
	if totalPower > 0 {
		// Add small random variations to voltages (typical grid variation)
		s.voltageL1 = 228.0 + (float64(simTime.Second()%10) * 0.2)
		s.voltageL2 = 227.0 + (float64((simTime.Second()+3)%10) * 0.2)
		s.voltageL3 = 228.0 + (float64((simTime.Second()+7)%10) * 0.2)

		// Current per phase using phase voltage (L-N): I = P_phase / V_phase,
		// with small variations
		avgVoltage := (s.voltageL1 + s.voltageL2 + s.voltageL3) / 3.0
		s.currentL1 = phaseW[0] / avgVoltage * (0.98 + (float64(simTime.Second()%5) * 0.01))
		s.currentL2 = phaseW[1] / avgVoltage * (0.99 + (float64((simTime.Second()+2)%5) * 0.01))
		s.currentL3 = phaseW[2] / avgVoltage * (1.00 + (float64((simTime.Second()+4)%5) * 0.01))
	} else {
		// No charging - idle state
		s.voltageL1 = 230.0
		s.voltageL2 = 230.0
		s.voltageL3 = 230.0
		s.currentL1 = 0
		s.currentL2 = 0
		s.currentL3 = 0
	}

	s.mu.Unlock()

	// Send status notifications outside the lock
	for _, sc := range statusChanges {
		s.sendStatusNotification(sc.connectorId, sc.status)
	}
}

// PhasePowers returns what the chargers draw on L1, L2 and L3, in watts.
// A site meter adds these to its phases.
func (s *Server) PhasePowers() [3]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.phaseW
}

// GetConnectors returns all connectors
func (s *Server) GetConnectors() map[int]*Connector {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[int]*Connector)
	for k, v := range s.connectors {
		result[k] = v
	}
	return result
}

// GetTransactions returns all active transactions
func (s *Server) GetTransactions() map[int]*Transaction {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[int]*Transaction)
	for k, v := range s.transactions {
		result[k] = v
	}
	return result
}

// MeterValuesDebug contains debug information about current meter values
type MeterValuesDebug struct {
	VoltageL1    float64                `json:"voltage_L1"`
	VoltageL2    float64                `json:"voltage_L2"`
	VoltageL3    float64                `json:"voltage_L3"`
	CurrentL1    float64                `json:"current_L1"`
	CurrentL2    float64                `json:"current_L2"`
	CurrentL3    float64                `json:"current_L3"`
	PowerW       float64                `json:"power_W"`
	EnergyWh     float64                `json:"energy_Wh"`
	SampleJSON   string                 `json:"sample_meter_values_json"`
	ExpectedCSMS map[string]interface{} `json:"expected_csms_output"`
}

// GetMeterValuesDebug returns debug information about current meter values
func (s *Server) GetMeterValuesDebug() MeterValuesDebug {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Get connector 1 for power/energy values
	var powerW, energyWh float64
	if conn := s.connectors[1]; conn != nil {
		powerW = conn.CurrentPower
		energyWh = conn.EnergyWh
	}

	// Calculate offered current (same formula as sendMeterValues)
	// P = 3 × V_phase × I_phase, so I_phase = P / (3 × V_phase)
	avgVoltage := (s.voltageL1 + s.voltageL2 + s.voltageL3) / 3.0
	offeredCurrent := powerW / (3.0 * avgVoltage)
	if offeredCurrent < 0 {
		offeredCurrent = 0
	}

	// Build the same sampled values as would be sent
	sampledValues := []SampledValue{
		{Value: fmt.Sprintf("%.0f", energyWh), Measurand: "Energy.Active.Import.Register", Unit: "Wh", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.3f", offeredCurrent), Measurand: "Current.Offered", Unit: "A", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.0f", powerW), Measurand: "Power.Active.Import", Unit: "W", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.3f", s.currentL1), Measurand: "Current.Import", Phase: "L1", Unit: "A", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.3f", s.currentL2), Measurand: "Current.Import", Phase: "L2", Unit: "A", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.3f", s.currentL3), Measurand: "Current.Import", Phase: "L3", Unit: "A", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.2f", s.voltageL1), Measurand: "Voltage", Phase: "L1", Unit: "V", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.2f", s.voltageL2), Measurand: "Voltage", Phase: "L2", Unit: "V", Context: "Sample.Periodic"},
		{Value: fmt.Sprintf("%.2f", s.voltageL3), Measurand: "Voltage", Phase: "L3", Unit: "V", Context: "Sample.Periodic"},
	}

	// Create sample MeterValues request
	req := MeterValuesRequest{
		ConnectorId:   1,
		TransactionId: 1,
		MeterValue: []MeterValue{
			{
				Timestamp:    FormatTimestamp(time.Now()),
				SampledValue: sampledValues,
			},
		},
	}

	jsonBytes, _ := json.MarshalIndent(req, "", "  ")

	// Expected CSMS output based on field mappings
	expectedCSMS := map[string]interface{}{
		"W":               powerW,
		"total_import_Wh": energyWh,
		"L1_V":            s.voltageL1,
		"L2_V":            s.voltageL2,
		"L3_V":            s.voltageL3,
		"L1_A":            s.currentL1,
		"L2_A":            s.currentL2,
		"L3_A":            s.currentL3,
		"_note":           "If these values are null in your CSMS, the bug is in the data-handler or query layer",
	}

	return MeterValuesDebug{
		VoltageL1:    s.voltageL1,
		VoltageL2:    s.voltageL2,
		VoltageL3:    s.voltageL3,
		CurrentL1:    s.currentL1,
		CurrentL2:    s.currentL2,
		CurrentL3:    s.currentL3,
		PowerW:       powerW,
		EnergyWh:     energyWh,
		SampleJSON:   string(jsonBytes),
		ExpectedCSMS: expectedCSMS,
	}
}
