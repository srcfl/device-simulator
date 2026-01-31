package ocpp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/srcfl/device-simulator/internal/device"
)

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
	mu              sync.RWMutex
	writeMu         sync.Mutex // Mutex for serializing WebSocket writes
	id              int
	serialNumber    string
	port            int
	vendor          string
	model           string
	numConnectors   int
	maxPowerKW      float64
	connectors      map[int]*Connector
	transactions    map[int]*Transaction
	configuration   map[string]string
	logs            []device.LogEntry
	maxLogs         int
	running         bool
	httpServer      *http.Server
	wsConn          *websocket.Conn
	messageIdSeq    int64
	pendingRequests map[string]chan json.RawMessage
	stopChan        chan struct{}

	// Client mode fields
	ocppURL         string // Central System URL to connect to (client mode)
	clientMode      bool   // true = connect to URL, false = listen for connections
	reconnectDelay  time.Duration

	// 3-phase metering
	voltageL1 float64
	voltageL2 float64
	voltageL3 float64
	currentL1 float64
	currentL2 float64
	currentL3 float64

	// Simulation control
	targetPowerW   float64 // Target charging power in watts
	evConnected    bool    // Whether an EV is plugged in
	nextTxId       int     // Counter for local transaction IDs
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
		id:            id,
		serialNumber:  fmt.Sprintf("OCPPSIM%02d", id),
		port:          port,
		vendor:        vendor,
		model:         model,
		numConnectors: numConnectors,
		maxPowerKW:    maxPowerKW,
		connectors:    connectors,
		transactions:  make(map[int]*Transaction),
		configuration: map[string]string{
			"HeartbeatInterval":        "60",
			"MeterValueSampleInterval": "30",
			"NumberOfConnectors":       fmt.Sprintf("%d", numConnectors),
		},
		logs:            make([]device.LogEntry, 0),
		maxLogs:         500,
		pendingRequests: make(map[string]chan json.RawMessage),
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

// Start starts the OCPP server (or connects as client if URL is set)
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	// Recreate stop channel (may have been closed by previous Stop)
	s.stopChan = make(chan struct{})

	if s.clientMode && s.ocppURL != "" {
		// Client mode: connect to Central System
		go s.clientLoop()
		s.running = true
		log.Printf("[%s] OCPP client connecting to %s", s.serialNumber, s.ocppURL)
		return nil
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
		wsURL := url
		if !strings.HasSuffix(wsURL, "/") {
			wsURL += "/"
		}
		wsURL += serial

		s.addLog("connecting", map[string]interface{}{
			"url": wsURL,
		})

		dialer := websocket.Dialer{
			Subprotocols: []string{"ocpp1.6"},
		}

		conn, _, err := dialer.Dial(wsURL, nil)
		if err != nil {
			s.addLog("connect_error", map[string]interface{}{
				"error": err.Error(),
			})
			log.Printf("[%s] Failed to connect to %s: %v", serial, wsURL, err)

			select {
			case <-s.stopChan:
				return
			case <-time.After(s.reconnectDelay):
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

		// Start heartbeat loop
		go s.heartbeatLoop()

		// Start meter values loop
		go s.meterValuesLoop()

		// Start simulation loop to update values
		go s.simulationLoop()

		// Read messages until disconnected
		s.readLoop(conn)

		s.mu.Lock()
		s.wsConn = nil
		s.mu.Unlock()

		// Check if we should stop or reconnect
		select {
		case <-s.stopChan:
			return
		case <-time.After(s.reconnectDelay):
			log.Printf("[%s] Reconnecting to Central System...", serial)
		}
	}
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
func (s *Server) meterValuesLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
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
	// P = 3 × V_phase × I_phase, so I_phase = P / (3 × V_phase)
	avgVoltage := (s.voltageL1 + s.voltageL2 + s.voltageL3) / 3.0
	offeredCurrent := conn.CurrentPower / (3.0 * avgVoltage)
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

	if s.wsConn != nil {
		s.wsConn.Close()
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

	// Start heartbeat loop
	go s.heartbeatLoop()

	// Start meter values loop
	go s.meterValuesLoop()

	// Start simulation loop to update values
	go s.simulationLoop()

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
	messageType, messageId, action, payload, err := ParseOCPPMessage(data)
	if err != nil {
		log.Printf("[%s] Failed to parse message: %v", s.serialNumber, err)
		return
	}

	s.addLog("receive", map[string]interface{}{
		"type":    messageType,
		"id":      messageId,
		"action":  action,
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
				Readonly: false,
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
				Readonly: false,
				Value:    value,
			})
		} else {
			response.UnknownKey = append(response.UnknownKey, key)
		}
	}

	return response
}

func (s *Server) handleChangeConfiguration(req ChangeConfigurationRequest) *ChangeConfigurationResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if key exists or is allowed
	if _, ok := s.configuration[req.Key]; ok {
		s.configuration[req.Key] = req.Value
		log.Printf("[%s] Configuration changed: %s = %s", s.serialNumber, req.Key, req.Value)
		return &ChangeConfigurationResponse{Status: "Accepted"}
	}

	// Allow setting new keys
	s.configuration[req.Key] = req.Value
	log.Printf("[%s] Configuration set: %s = %s", s.serialNumber, req.Key, req.Value)
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
	if err := json.Unmarshal(resp, &bootResp); err == nil {
		log.Printf("[%s] BootNotification accepted: %s", s.serialNumber, bootResp.Status)
	}
}

func (s *Server) heartbeatLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.sendRequest(ActionHeartbeat, HeartbeatRequest{})
		}
	}
}

// simulationLoop updates simulation values every second
func (s *Server) simulationLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
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

	resp, err := s.sendRequest(ActionStartTransaction, req)
	if err != nil {
		log.Printf("[%s] StartTransaction failed: %v", s.serialNumber, err)
		return
	}

	var txResp StartTransactionResponse
	if err := json.Unmarshal(resp, &txResp); err == nil {
		log.Printf("[%s] Transaction started, server ID: %d", s.serialNumber, txResp.TransactionId)

		// Update our local transaction with the server's transaction ID
		s.mu.Lock()
		for localId, tx := range s.transactions {
			if tx.ConnectorID == connectorId && tx.IdTag == idTag {
				// Remove old entry and add with server's ID
				delete(s.transactions, localId)
				tx.ID = txResp.TransactionId
				s.transactions[txResp.TransactionId] = tx
				break
			}
		}
		s.mu.Unlock()
	}
}

func (s *Server) sendStopTransaction(transactionId int, idTag string, meterStop int) {
	req := StopTransactionRequest{
		TransactionId: transactionId,
		IdTag:         idTag,
		MeterStop:     meterStop,
		Timestamp:     FormatTimestamp(time.Now()),
	}

	s.sendRequest(ActionStopTransaction, req)
}

func (s *Server) sendRequest(action string, payload interface{}) (json.RawMessage, error) {
	s.mu.Lock()
	conn := s.wsConn
	if conn == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("not connected")
	}

	messageId := fmt.Sprintf("%d", atomic.AddInt64(&s.messageIdSeq, 1))
	responseChan := make(chan json.RawMessage, 1)
	s.pendingRequests[messageId] = responseChan
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
	err = conn.WriteMessage(websocket.TextMessage, msg)
	s.writeMu.Unlock()

	if err != nil {
		s.mu.Lock()
		delete(s.pendingRequests, messageId)
		s.mu.Unlock()
		return nil, err
	}

	select {
	case resp := <-responseChan:
		return resp, nil
	case <-time.After(30 * time.Second):
		s.mu.Lock()
		delete(s.pendingRequests, messageId)
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
	for _, c := range s.connectors {
		connectors = append(connectors, map[string]interface{}{
			"id":           c.ID,
			"status":       c.Status,
			"errorCode":    c.ErrorCode,
			"currentPower": c.CurrentPower,
			"energyWh":     c.EnergyWh,
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
		"voltageL1":   s.voltageL1,
		"voltageL2":   s.voltageL2,
		"voltageL3":   s.voltageL3,
		"currentL1":   s.currentL1,
		"currentL2":   s.currentL2,
		"currentL3":   s.currentL3,
		"totalPower":  totalPower,
		// Control state
		"evConnected":          s.evConnected,
		"targetPowerW":         s.targetPowerW,
		"hasActiveTransaction": hasActiveTransaction,
	}
}

// SetValue sets a charger state value
func (s *Server) SetValue(key string, value interface{}) error {
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
					go s.sendStatusNotification(1, StatusPreparing)
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
		// Set target charging power
		if power, ok := value.(float64); ok {
			s.mu.Lock()
			s.targetPowerW = power
			// If charging, update connector power immediately
			for _, tx := range s.transactions {
				if conn, ok := s.connectors[tx.ConnectorID]; ok && conn.Status == StatusCharging {
					conn.CurrentPower = power
				}
			}
			s.mu.Unlock()
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

// SetSerialNumber sets the serial number
func (s *Server) SetSerialNumber(sn string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serialNumber = sn
}

// GenerateValues generates simulated values based on time
func (s *Server) GenerateValues(simTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Simulate power consumption for active transactions
	var totalPower float64
	for _, tx := range s.transactions {
		conn := s.connectors[tx.ConnectorID]
		if conn.Status == StatusCharging {
			// Use target power if set, otherwise max power
			powerW := s.targetPowerW
			if powerW <= 0 {
				powerW = s.maxPowerKW * 1000
			}
			// Cap at max power
			maxW := s.maxPowerKW * 1000
			if powerW > maxW {
				powerW = maxW
			}
			conn.CurrentPower = powerW
			totalPower += powerW

			// Add energy (Wh) for 1 second at this power
			energyDelta := powerW / 3600.0
			conn.EnergyWh += energyDelta
			tx.MeterNow = int(conn.EnergyWh)
		}
	}

	// Update 3-phase metering based on total power
	// Assume balanced 3-phase load
	if totalPower > 0 {
		// Add small random variations to voltages (typical grid variation)
		s.voltageL1 = 228.0 + (float64(simTime.Second()%10) * 0.2)
		s.voltageL2 = 227.0 + (float64((simTime.Second()+3)%10) * 0.2)
		s.voltageL3 = 228.0 + (float64((simTime.Second()+7)%10) * 0.2)

		// Calculate current per phase (balanced load) using phase voltage (L-N)
		// P = 3 × V_phase × I_phase, so I_phase = P / (3 × V_phase)
		avgVoltage := (s.voltageL1 + s.voltageL2 + s.voltageL3) / 3.0
		totalCurrent := totalPower / (3.0 * avgVoltage)

		// Distribute with small variations
		s.currentL1 = totalCurrent * (0.98 + (float64(simTime.Second()%5) * 0.01))
		s.currentL2 = totalCurrent * (0.99 + (float64((simTime.Second()+2)%5) * 0.01))
		s.currentL3 = totalCurrent * (1.00 + (float64((simTime.Second()+4)%5) * 0.01))
	} else {
		// No charging - idle state
		s.voltageL1 = 230.0
		s.voltageL2 = 230.0
		s.voltageL3 = 230.0
		s.currentL1 = 0
		s.currentL2 = 0
		s.currentL3 = 0
	}
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
