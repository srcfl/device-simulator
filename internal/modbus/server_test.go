package modbus

import (
	"testing"
	"time"
)

func TestServerBasicOperations(t *testing.T) {
	server := NewServer(60001)

	// Test SetHoldingRegisters and GetHoldingRegisters
	server.SetHoldingRegisters(100, []uint16{1234, 5678})
	regs := server.GetHoldingRegisters(100, 2)
	if len(regs) != 2 || regs[0] != 1234 || regs[1] != 5678 {
		t.Errorf("GetHoldingRegisters = %v, want [1234, 5678]", regs)
	}

	// Test mirroring to input registers
	inputRegs := server.GetInputRegisters(100, 2)
	if len(inputRegs) != 2 || inputRegs[0] != 1234 || inputRegs[1] != 5678 {
		t.Errorf("GetInputRegisters = %v, want [1234, 5678] (mirrored)", inputRegs)
	}
}

func TestServerInitDefaults(t *testing.T) {
	server := NewServer(60001)
	server.InitDefaults()

	// Check that Battery SoC default is set (78%)
	def := RegisterLookup[13022]
	if def == nil {
		t.Fatal("Battery SoC register definition not found")
	}

	value, err := server.ReadRegisterValue(def)
	if err != nil {
		t.Fatalf("ReadRegisterValue error: %v", err)
	}
	if value != 78.0 {
		t.Errorf("Battery SoC = %v, want 78.0", value)
	}

	// Check Grid Frequency (default is 50 Hz, stored as 5000 with 0.01 scale)
	freqDef := RegisterLookup[5241]
	if freqDef == nil {
		t.Fatal("Grid Frequency register definition not found")
	}

	freq, err := server.ReadRegisterValue(freqDef)
	if err != nil {
		t.Fatalf("ReadRegisterValue error: %v", err)
	}
	if freq != 50.0 { // Default is 50 Hz
		t.Errorf("Grid Frequency = %v, want 50.0 Hz", freq)
	}
}

func TestServerWriteRegisterValue(t *testing.T) {
	server := NewServer(60001)

	def := RegisterLookup[13022] // Battery SoC
	if def == nil {
		t.Fatal("Battery SoC register definition not found")
	}

	// Write 85.5%
	err := server.WriteRegisterValue(def, 85.5)
	if err != nil {
		t.Fatalf("WriteRegisterValue error: %v", err)
	}

	// Read back
	value, err := server.ReadRegisterValue(def)
	if err != nil {
		t.Fatalf("ReadRegisterValue error: %v", err)
	}
	// Note: Due to scale 0.1, 85.5 becomes 855, which decodes back to 85.5
	if value != 85.5 {
		t.Errorf("Battery SoC = %v, want 85.5", value)
	}
}

func TestServerLogging(t *testing.T) {
	server := NewServer(60001)

	// Add some logs
	for i := 0; i < 10; i++ {
		server.AddLog(LogEntry{
			Timestamp: time.Now(),
			Operation: "read",
			Address:   uint16(i * 100),
			Count:     1,
		})
	}

	// Get all logs
	logs := server.GetLogs(0)
	if len(logs) != 10 {
		t.Errorf("GetLogs(0) = %d entries, want 10", len(logs))
	}

	// Get limited logs
	logs = server.GetLogs(5)
	if len(logs) != 5 {
		t.Errorf("GetLogs(5) = %d entries, want 5", len(logs))
	}

	// Verify most recent are returned
	if logs[4].Address != 900 {
		t.Errorf("Most recent log address = %d, want 900", logs[4].Address)
	}

	// Clear logs
	server.ClearLogs()
	logs = server.GetLogs(0)
	if len(logs) != 0 {
		t.Errorf("After ClearLogs, got %d entries, want 0", len(logs))
	}
}

func TestServerLogRingBuffer(t *testing.T) {
	server := NewServer(60001)
	server.maxLogs = 5

	// Add more logs than max
	for i := 0; i < 10; i++ {
		server.AddLog(LogEntry{
			Timestamp: time.Now(),
			Operation: "read",
			Address:   uint16(i),
		})
	}

	logs := server.GetLogs(0)
	if len(logs) != 5 {
		t.Errorf("Ring buffer: got %d entries, want 5", len(logs))
	}

	// Should have entries 5-9 (oldest ones dropped)
	if logs[0].Address != 5 {
		t.Errorf("Oldest log address = %d, want 5", logs[0].Address)
	}
	if logs[4].Address != 9 {
		t.Errorf("Newest log address = %d, want 9", logs[4].Address)
	}
}

func TestServerSetSerialNumber(t *testing.T) {
	server := NewServer(60001)
	server.SetSerialNumber("INVSIM00")

	// Read serial number registers (4989-4998)
	regs := server.GetHoldingRegisters(4989, 10)
	if len(regs) != 10 {
		t.Fatalf("Got %d serial registers, want 10", len(regs))
	}

	// First two bytes should be 'I' and 'N'
	firstChar := byte(regs[0] >> 8)
	secondChar := byte(regs[0] & 0xFF)
	if firstChar != 'I' || secondChar != 'N' {
		t.Errorf("First register = 0x%04x, expected 'IN' (0x%02x%02x)",
			regs[0], 'I', 'N')
	}
}

func TestServerOnWriteCallback(t *testing.T) {
	server := NewServer(60001)

	var callbackAddr uint16
	var callbackValues []uint16

	server.SetOnWrite(func(addr uint16, values []uint16) {
		callbackAddr = addr
		callbackValues = values
	})

	server.SetHoldingRegisters(5000, []uint16{100, 200})

	if callbackAddr != 5000 {
		t.Errorf("Callback address = %d, want 5000", callbackAddr)
	}
	if len(callbackValues) != 2 || callbackValues[0] != 100 || callbackValues[1] != 200 {
		t.Errorf("Callback values = %v, want [100, 200]", callbackValues)
	}
}

func TestServerBoundaryConditions(t *testing.T) {
	server := NewServer(100) // Small register space

	// Write at boundary
	server.SetHoldingRegisters(98, []uint16{1, 2, 3, 4})

	// Should only write what fits
	regs := server.GetHoldingRegisters(98, 4)
	if regs[0] != 1 || regs[1] != 2 {
		t.Errorf("Boundary write: got %v", regs)
	}

	// Read beyond boundary
	regs = server.GetHoldingRegisters(95, 10)
	if len(regs) != 10 {
		t.Errorf("Boundary read: got length %d", len(regs))
	}
}
