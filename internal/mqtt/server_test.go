package mqtt

import (
	"net"
	"testing"
	"time"
)

func TestServerStartStop(t *testing.T) {
	// Use a high port to avoid conflicts
	port := 51883

	server := NewServer(1, port)

	// Step 1: Verify port is not accepting connections before start
	conn, err := net.DialTimeout("tcp", "localhost:51883", 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("Port should not be accepting connections before server starts")
	}

	// Step 2: Start the server
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	// Give the server a moment to start listening
	time.Sleep(100 * time.Millisecond)

	// Step 3: Verify port accepts connections after start
	conn, err = net.DialTimeout("tcp", "localhost:51883", 500*time.Millisecond)
	if err != nil {
		t.Fatalf("Server should accept connections after start: %v", err)
	}
	conn.Close()

	// Step 4: Stop the server
	if err := server.Stop(); err != nil {
		t.Fatalf("Failed to stop server: %v", err)
	}

	// Give the server a moment to stop
	time.Sleep(100 * time.Millisecond)

	// Step 5: Verify port no longer accepts connections
	conn, err = net.DialTimeout("tcp", "localhost:51883", 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("Port should not accept connections after server stops")
	}
}

func TestServerMQTTProtocol(t *testing.T) {
	port := 51884
	server := NewServer(2, port)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect and send MQTT CONNECT packet
	conn, err := net.DialTimeout("tcp", "localhost:51884", 500*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Set read/write deadline
	conn.SetDeadline(time.Now().Add(2 * time.Second))

	// Send MQTT 3.1.1 CONNECT packet
	// Fixed header: CONNECT (0x10), remaining length
	// Variable header: Protocol name "MQTT", Protocol level 4, Connect flags, Keep alive
	// Payload: Client ID
	clientID := "test-client-001"
	connectPacket := buildMQTTConnectPacket(clientID)

	_, err = conn.Write(connectPacket)
	if err != nil {
		t.Fatalf("Failed to send CONNECT: %v", err)
	}

	// Read CONNACK response
	response := make([]byte, 4)
	n, err := conn.Read(response)
	if err != nil {
		t.Fatalf("Failed to read CONNACK: %v", err)
	}

	// Verify CONNACK packet
	// Fixed header: 0x20 (CONNACK), 0x02 (remaining length)
	// Variable header: 0x00 (connect acknowledge flags), 0x00 (return code - success)
	if n < 4 {
		t.Fatalf("CONNACK too short: got %d bytes", n)
	}
	if response[0] != 0x20 {
		t.Errorf("Expected CONNACK packet type 0x20, got 0x%02x", response[0])
	}
	if response[3] != 0x00 {
		t.Errorf("Expected CONNACK return code 0x00 (success), got 0x%02x", response[3])
	}
}

func TestServerDefaultState(t *testing.T) {
	server := NewServer(3, 51885)

	state := server.GetState()

	// Verify default state values
	if state["sessionState"] != "PAUSED" {
		t.Errorf("Expected default sessionState 'paused', got '%v'", state["sessionState"])
	}
	if state["controlMode"] != "CONTROLLABLE" {
		t.Errorf("Expected default controlMode 'CONTROLLABLE', got '%v'", state["controlMode"])
	}
	if state["evConnected"] != false {
		t.Errorf("Expected default evConnected false, got %v", state["evConnected"])
	}
	if state["soc"] != 50.0 {
		t.Errorf("Expected default SoC 50.0, got %v", state["soc"])
	}
	if state["voltageAc"] != 230.0 {
		t.Errorf("Expected default voltageAc 230.0, got %v", state["voltageAc"])
	}
}

func TestServerSetValue(t *testing.T) {
	server := NewServer(4, 51886)

	// Test setting SoC
	err := server.SetValue("soc", 75.0)
	if err != nil {
		t.Fatalf("SetValue failed: %v", err)
	}

	state := server.GetState()
	if state["soc"] != 75.0 {
		t.Errorf("Expected SoC 75.0, got %v", state["soc"])
	}

	// Test setting sessionState
	err = server.SetValue("sessionState", "CHARGE_LOOP")
	if err != nil {
		t.Fatalf("SetValue failed: %v", err)
	}

	state = server.GetState()
	if state["sessionState"] != "CHARGE_LOOP" {
		t.Errorf("Expected sessionState 'charge_loop', got '%v'", state["sessionState"])
	}

	// Test setting invalid key
	err = server.SetValue("invalidKey", "value")
	if err == nil {
		t.Error("Expected error for invalid key, got nil")
	}
}

func TestServerSerialNumber(t *testing.T) {
	server := NewServer(5, 51887)

	// Check default serial number format
	sn := server.SerialNumber()
	if sn != "V2XSIM05" {
		t.Errorf("Expected serial 'V2XSIM05', got '%s'", sn)
	}

	// Set custom serial number
	server.SetSerialNumber("CUSTOM123")
	sn = server.SerialNumber()
	if sn != "CUSTOM123" {
		t.Errorf("Expected serial 'CUSTOM123', got '%s'", sn)
	}

	// Verify state also updated
	state := server.GetState()
	if state["serialNumber"] != "CUSTOM123" {
		t.Errorf("Expected state serialNumber 'CUSTOM123', got '%v'", state["serialNumber"])
	}
}

func TestServerDoubleStartStop(t *testing.T) {
	server := NewServer(6, 51888)

	// Start twice should not error
	if err := server.Start(); err != nil {
		t.Fatalf("First start failed: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("Second start should not error: %v", err)
	}

	// Stop twice should not error
	if err := server.Stop(); err != nil {
		t.Fatalf("First stop failed: %v", err)
	}
	if err := server.Stop(); err != nil {
		t.Fatalf("Second stop should not error: %v", err)
	}
}

func TestServerSetTargetPower(t *testing.T) {
	server := NewServer(7, 51889)

	// Plug in EV first (defaults to disconnected)
	server.SetValue("evConnected", true)

	// Set target power for charging (negative = charging)
	server.SetTargetPower(-5000)

	state := server.GetState()

	// Should transition to charge_loop
	if state["sessionState"] != "CHARGE_LOOP" {
		t.Errorf("Expected sessionState 'charge_loop', got '%v'", state["sessionState"])
	}

	// Power should be applied (EV connected and CONTROLLABLE mode by default)
	if state["powerAc"] != -5000.0 {
		t.Errorf("Expected powerAc -5000, got %v", state["powerAc"])
	}

	// DC power should be 95% of AC
	expectedDC := -5000.0 * 0.95
	if state["powerDc"] != expectedDC {
		t.Errorf("Expected powerDc %v, got %v", expectedDC, state["powerDc"])
	}
}

func TestServerWakeUp(t *testing.T) {
	server := NewServer(8, 51890)

	// Plug in EV first (defaults to disconnected)
	server.SetValue("evConnected", true)

	// Wake up the charger
	server.WakeUp(true)

	state := server.GetState()

	// Should transition to charge_loop
	if state["sessionState"] != "CHARGE_LOOP" {
		t.Errorf("Expected sessionState 'charge_loop', got '%v'", state["sessionState"])
	}

	// Should start with 200W charging (negative)
	if state["powerAc"] != -200.0 {
		t.Errorf("Expected powerAc -200, got %v", state["powerAc"])
	}

	// Sleep the charger
	server.WakeUp(false)

	state = server.GetState()

	// Should transition to paused (not IDLE — IDLE no longer exists)
	if state["sessionState"] != "PAUSED" {
		t.Errorf("Expected sessionState 'paused', got '%v'", state["sessionState"])
	}

	// Power should be zero
	if state["powerAc"] != 0.0 {
		t.Errorf("Expected powerAc 0, got %v", state["powerAc"])
	}
}

// buildMQTTConnectPacket creates an MQTT 3.1.1 CONNECT packet
func buildMQTTConnectPacket(clientID string) []byte {
	// Protocol name "MQTT"
	protocolName := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T'}
	// Protocol level 4 (MQTT 3.1.1)
	protocolLevel := byte(0x04)
	// Connect flags: Clean session
	connectFlags := byte(0x02)
	// Keep alive: 60 seconds
	keepAlive := []byte{0x00, 0x3C}

	// Client ID with length prefix
	clientIDBytes := []byte(clientID)
	clientIDLen := []byte{byte(len(clientIDBytes) >> 8), byte(len(clientIDBytes) & 0xFF)}

	// Variable header + payload
	variableHeader := append(protocolName, protocolLevel, connectFlags)
	variableHeader = append(variableHeader, keepAlive...)
	payload := append(clientIDLen, clientIDBytes...)

	// Calculate remaining length
	remainingLen := len(variableHeader) + len(payload)

	// Fixed header: CONNECT (0x10) + remaining length
	fixedHeader := []byte{0x10, byte(remainingLen)}

	// Full packet
	packet := append(fixedHeader, variableHeader...)
	packet = append(packet, payload...)

	return packet
}
