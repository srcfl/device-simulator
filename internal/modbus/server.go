package modbus

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// LogEntry represents a Modbus operation log entry
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Operation string    `json:"operation"`
	Address   uint16    `json:"address"`
	Count     int       `json:"count"`
	Values    []uint16  `json:"values,omitempty"`
	SourceIP  string    `json:"source_ip"`
	UnitID    uint8     `json:"unit_id"`
}

// ClientInfo represents a connected Modbus client
type ClientInfo struct {
	ID         string    `json:"id"`
	RemoteAddr string    `json:"remote_addr"`
	ConnectedAt time.Time `json:"connected_at"`
	LastSeen   time.Time `json:"last_seen"`
}

// Server is a Modbus TCP server
type Server struct {
	mu             sync.RWMutex
	holdingRegs    []uint16
	inputRegs      []uint16
	listener       net.Listener
	logs           []LogEntry
	maxLogs        int
	logMu          sync.RWMutex
	mirrorToInput  bool
	serialNumber   string
	onWrite        func(addr uint16, values []uint16) // callback when registers are written
	clients        map[string]*ClientInfo
	clientsMu      sync.RWMutex
	nextClientID   int
}

// NewServer creates a new Modbus server with the specified register count
func NewServer(regCount int) *Server {
	s := &Server{
		holdingRegs:   make([]uint16, regCount),
		inputRegs:     make([]uint16, regCount),
		logs:          make([]LogEntry, 0, 500),
		maxLogs:       500,
		mirrorToInput: true,
		clients:       make(map[string]*ClientInfo),
	}
	return s
}

// SetSerialNumber sets the device serial number
func (s *Server) SetSerialNumber(serial string) {
	s.serialNumber = serial
	SetSerialNumber(serial, func(addr uint16, values []uint16) {
		s.SetHoldingRegisters(addr, values)
	})
}

// SetOnWrite sets a callback function that is called when registers are written
func (s *Server) SetOnWrite(fn func(addr uint16, values []uint16)) {
	s.onWrite = fn
}

// InitDefaults initializes registers with default values
func (s *Server) InitDefaults() {
	for addr, value := range DefaultValues {
		def := RegisterLookup[addr]
		if def != nil {
			regs, err := EncodeValue(float64(value), def)
			if err != nil {
				log.Printf("Warning: failed to encode default for %d: %v", addr, err)
				continue
			}
			s.SetHoldingRegisters(addr, regs)
		} else {
			// Simple single register
			s.SetHoldingRegisters(addr, []uint16{uint16(value)})
		}
	}
}

// GetHoldingRegisters reads holding registers
func (s *Server) GetHoldingRegisters(addr uint16, count int) []uint16 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	end := int(addr) + count
	if end > len(s.holdingRegs) {
		end = len(s.holdingRegs)
	}
	if int(addr) >= len(s.holdingRegs) {
		return nil
	}

	result := make([]uint16, count)
	copy(result, s.holdingRegs[addr:end])
	return result
}

// SetHoldingRegisters writes holding registers
func (s *Server) SetHoldingRegisters(addr uint16, values []uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, v := range values {
		idx := int(addr) + i
		if idx < len(s.holdingRegs) {
			s.holdingRegs[idx] = v
			// Mirror to input registers
			if s.mirrorToInput && idx < len(s.inputRegs) {
				s.inputRegs[idx] = v
			}
		}
	}

	if s.onWrite != nil {
		s.onWrite(addr, values)
	}
}

// GetInputRegisters reads input registers
func (s *Server) GetInputRegisters(addr uint16, count int) []uint16 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	end := int(addr) + count
	if end > len(s.inputRegs) {
		end = len(s.inputRegs)
	}
	if int(addr) >= len(s.inputRegs) {
		return nil
	}

	result := make([]uint16, count)
	copy(result, s.inputRegs[addr:end])
	return result
}

// SetInputRegisters writes input registers directly
func (s *Server) SetInputRegisters(addr uint16, values []uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, v := range values {
		idx := int(addr) + i
		if idx < len(s.inputRegs) {
			s.inputRegs[idx] = v
		}
	}
}

// AddLog adds a log entry
func (s *Server) AddLog(entry LogEntry) {
	s.logMu.Lock()
	defer s.logMu.Unlock()

	if len(s.logs) >= s.maxLogs {
		// Remove oldest entry
		s.logs = s.logs[1:]
	}
	s.logs = append(s.logs, entry)
}

// GetLogs returns recent log entries
func (s *Server) GetLogs(limit int) []LogEntry {
	s.logMu.RLock()
	defer s.logMu.RUnlock()

	if limit <= 0 || limit > len(s.logs) {
		limit = len(s.logs)
	}

	// Return most recent entries
	start := len(s.logs) - limit
	result := make([]LogEntry, limit)
	copy(result, s.logs[start:])
	return result
}

// ClearLogs clears all log entries
func (s *Server) ClearLogs() {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.logs = s.logs[:0]
}

// Listen starts the Modbus TCP server on the specified address
func (s *Server) Listen(addr string) error {
	var err error
	s.listener, err = net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	log.Printf("Modbus server listening on %s", addr)
	return nil
}

// Serve accepts and handles connections
func (s *Server) Serve() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return err
		}
		go s.handleConnection(conn)
	}
}

// Close closes the server
func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// addClient adds a new client and returns its ID
func (s *Server) addClient(remoteAddr string) string {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()

	s.nextClientID++
	clientID := fmt.Sprintf("client-%d", s.nextClientID)
	now := time.Now()
	s.clients[clientID] = &ClientInfo{
		ID:          clientID,
		RemoteAddr:  remoteAddr,
		ConnectedAt: now,
		LastSeen:    now,
	}
	return clientID
}

// removeClient removes a client by ID
func (s *Server) removeClient(clientID string) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	delete(s.clients, clientID)
}

// updateClientLastSeen updates the last seen time for a client
func (s *Server) updateClientLastSeen(clientID string) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if client, ok := s.clients[clientID]; ok {
		client.LastSeen = time.Now()
	}
}

// GetClients returns a list of connected clients
func (s *Server) GetClients() []ClientInfo {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	clients := make([]ClientInfo, 0, len(s.clients))
	for _, client := range s.clients {
		clients = append(clients, *client)
	}
	return clients
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	remoteAddr := conn.RemoteAddr().String()
	log.Printf("New connection from %s", remoteAddr)

	// Track client
	clientID := s.addClient(remoteAddr)
	defer s.removeClient(clientID)

	buf := make([]byte, 256)
	for {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		// Update last seen time
		s.updateClientLastSeen(clientID)

		if n < 8 {
			continue
		}

		// Parse MBAP header
		transactionID := binary.BigEndian.Uint16(buf[0:2])
		protocolID := binary.BigEndian.Uint16(buf[2:4])
		length := binary.BigEndian.Uint16(buf[4:6])
		unitID := buf[6]
		functionCode := buf[7]

		if protocolID != 0 {
			continue // Not Modbus TCP
		}

		_ = length // We trust the actual bytes read

		var response []byte
		switch functionCode {
		case 0x03: // Read Holding Registers
			if n >= 12 {
				startAddr := binary.BigEndian.Uint16(buf[8:10])
				quantity := binary.BigEndian.Uint16(buf[10:12])
				response = s.handleReadHolding(transactionID, unitID, startAddr, quantity, remoteAddr)
			}

		case 0x04: // Read Input Registers
			if n >= 12 {
				startAddr := binary.BigEndian.Uint16(buf[8:10])
				quantity := binary.BigEndian.Uint16(buf[10:12])
				response = s.handleReadInput(transactionID, unitID, startAddr, quantity, remoteAddr)
			}

		case 0x06: // Write Single Register
			if n >= 12 {
				regAddr := binary.BigEndian.Uint16(buf[8:10])
				value := binary.BigEndian.Uint16(buf[10:12])
				response = s.handleWriteSingle(transactionID, unitID, regAddr, value, remoteAddr)
			}

		case 0x10: // Write Multiple Registers
			if n >= 13 {
				startAddr := binary.BigEndian.Uint16(buf[8:10])
				quantity := binary.BigEndian.Uint16(buf[10:12])
				byteCount := buf[12]
				if n >= 13+int(byteCount) {
					values := make([]uint16, quantity)
					for i := 0; i < int(quantity); i++ {
						values[i] = binary.BigEndian.Uint16(buf[13+i*2 : 15+i*2])
					}
					response = s.handleWriteMultiple(transactionID, unitID, startAddr, quantity, values, remoteAddr)
				}
			}

		default:
			// Unsupported function code - send exception
			response = s.makeException(transactionID, unitID, functionCode, 0x01)
		}

		if response != nil {
			conn.Write(response)
		}
	}
}

func (s *Server) handleReadHolding(txID uint16, unitID uint8, addr, count uint16, remoteAddr string) []byte {
	regs := s.GetHoldingRegisters(addr, int(count))

	s.AddLog(LogEntry{
		Timestamp: time.Now(),
		Operation: "read_holding",
		Address:   addr,
		Count:     int(count),
		SourceIP:  remoteAddr,
		UnitID:    unitID,
	})

	return s.makeReadResponse(txID, unitID, 0x03, regs)
}

func (s *Server) handleReadInput(txID uint16, unitID uint8, addr, count uint16, remoteAddr string) []byte {
	regs := s.GetInputRegisters(addr, int(count))

	s.AddLog(LogEntry{
		Timestamp: time.Now(),
		Operation: "read_input",
		Address:   addr,
		Count:     int(count),
		SourceIP:  remoteAddr,
		UnitID:    unitID,
	})

	return s.makeReadResponse(txID, unitID, 0x04, regs)
}

func (s *Server) handleWriteSingle(txID uint16, unitID uint8, addr, value uint16, remoteAddr string) []byte {
	s.SetHoldingRegisters(addr, []uint16{value})

	s.AddLog(LogEntry{
		Timestamp: time.Now(),
		Operation: "write_single",
		Address:   addr,
		Count:     1,
		Values:    []uint16{value},
		SourceIP:  remoteAddr,
		UnitID:    unitID,
	})

	// Echo request as response
	resp := make([]byte, 12)
	binary.BigEndian.PutUint16(resp[0:2], txID)
	binary.BigEndian.PutUint16(resp[2:4], 0) // Protocol ID
	binary.BigEndian.PutUint16(resp[4:6], 6) // Length
	resp[6] = unitID
	resp[7] = 0x06
	binary.BigEndian.PutUint16(resp[8:10], addr)
	binary.BigEndian.PutUint16(resp[10:12], value)
	return resp
}

func (s *Server) handleWriteMultiple(txID uint16, unitID uint8, addr, count uint16, values []uint16, remoteAddr string) []byte {
	s.SetHoldingRegisters(addr, values)

	s.AddLog(LogEntry{
		Timestamp: time.Now(),
		Operation: "write_multiple",
		Address:   addr,
		Count:     int(count),
		Values:    values,
		SourceIP:  remoteAddr,
		UnitID:    unitID,
	})

	// Response
	resp := make([]byte, 12)
	binary.BigEndian.PutUint16(resp[0:2], txID)
	binary.BigEndian.PutUint16(resp[2:4], 0) // Protocol ID
	binary.BigEndian.PutUint16(resp[4:6], 6) // Length
	resp[6] = unitID
	resp[7] = 0x10
	binary.BigEndian.PutUint16(resp[8:10], addr)
	binary.BigEndian.PutUint16(resp[10:12], count)
	return resp
}

func (s *Server) makeReadResponse(txID uint16, unitID uint8, funcCode byte, regs []uint16) []byte {
	byteCount := len(regs) * 2
	length := 3 + byteCount

	resp := make([]byte, 9+byteCount)
	binary.BigEndian.PutUint16(resp[0:2], txID)
	binary.BigEndian.PutUint16(resp[2:4], 0)              // Protocol ID
	binary.BigEndian.PutUint16(resp[4:6], uint16(length)) // Length
	resp[6] = unitID
	resp[7] = funcCode
	resp[8] = byte(byteCount)

	for i, reg := range regs {
		binary.BigEndian.PutUint16(resp[9+i*2:11+i*2], reg)
	}

	return resp
}

func (s *Server) makeException(txID uint16, unitID uint8, funcCode, exceptionCode byte) []byte {
	resp := make([]byte, 9)
	binary.BigEndian.PutUint16(resp[0:2], txID)
	binary.BigEndian.PutUint16(resp[2:4], 0) // Protocol ID
	binary.BigEndian.PutUint16(resp[4:6], 3) // Length
	resp[6] = unitID
	resp[7] = funcCode | 0x80 // Error flag
	resp[8] = exceptionCode
	return resp
}

// ReadRegisterValue reads a register value using its definition
func (s *Server) ReadRegisterValue(def *RegisterDef) (float64, error) {
	var regs []uint16
	if def.UseHolding {
		regs = s.GetHoldingRegisters(def.Address, def.Words)
	} else {
		regs = s.GetInputRegisters(def.Address, def.Words)
	}
	if regs == nil {
		return 0, fmt.Errorf("register %d not found", def.Address)
	}
	return DecodeValue(regs, def)
}

// WriteRegisterValue writes a register value using its definition
func (s *Server) WriteRegisterValue(def *RegisterDef, value float64) error {
	regs, err := EncodeValue(value, def)
	if err != nil {
		return err
	}
	if def.UseHolding {
		s.SetHoldingRegisters(def.Address, regs)
	} else {
		// Write directly to input registers for devices that use FC 0x04
		s.SetInputRegisters(def.Address, regs)
	}
	return nil
}
