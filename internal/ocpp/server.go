package ocpp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
	}
}

// Start starts the OCPP server
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	// Recreate stop channel (may have been closed by previous Stop)
	s.stopChan = make(chan struct{})

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
		log.Printf("[%s] Transaction started: %d", s.serialNumber, txResp.TransactionId)
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

	if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
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

	conn.WriteMessage(websocket.TextMessage, msg)
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

	conn.WriteMessage(websocket.TextMessage, msg)
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

	return map[string]interface{}{
		"serialNumber":  s.serialNumber,
		"vendor":        s.vendor,
		"model":         s.model,
		"numConnectors": s.numConnectors,
		"maxPowerKW":    s.maxPowerKW,
		"connectors":    connectors,
		"transactions":  transactions,
		"connected":     s.wsConn != nil,
	}
}

// SetValue sets a charger state value
func (s *Server) SetValue(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Parse connector operations like "connector.1.status"
	switch key {
	case "evConnected":
		// Simulate EV connection on connector 1
		if connected, ok := value.(bool); ok {
			if connected {
				if s.connectors[1].Status == StatusAvailable {
					s.connectors[1].Status = StatusPreparing
					go s.sendStatusNotification(1, StatusPreparing)
				}
			} else {
				s.connectors[1].Status = StatusAvailable
				go s.sendStatusNotification(1, StatusAvailable)
			}
		}
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
	for _, tx := range s.transactions {
		conn := s.connectors[tx.ConnectorID]
		if conn.Status == StatusCharging {
			// Simulate charging at max power
			powerW := s.maxPowerKW * 1000
			conn.CurrentPower = powerW

			// Add energy (Wh) for 1 second at this power
			energyDelta := powerW / 3600.0
			conn.EnergyWh += energyDelta
			tx.MeterNow = int(conn.EnergyWh)
		}
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
