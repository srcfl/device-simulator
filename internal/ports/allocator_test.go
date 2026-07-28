package ports

import (
	"sync"
	"testing"
)

func TestNewAllocator(t *testing.T) {
	a := NewAllocator(5000, 1883, 9000, 5540)

	modbus, mqtt, ocpp, matter := a.GetBasePorts()
	if modbus != 5000 {
		t.Errorf("Expected modbus base 5000, got %d", modbus)
	}
	if mqtt != 1883 {
		t.Errorf("Expected mqtt base 1883, got %d", mqtt)
	}
	if ocpp != 9000 {
		t.Errorf("Expected ocpp base 9000, got %d", ocpp)
	}
	if matter != 5540 {
		t.Errorf("Expected matter base 5540, got %d", matter)
	}
}

func TestAllocateModbus(t *testing.T) {
	a := NewAllocator(5000, 1883, 9000, 5540)

	// First allocation should return base port
	port := a.Allocate(ProtocolModbus)
	if port != 5000 {
		t.Errorf("Expected first modbus port 5000, got %d", port)
	}

	// Second allocation should return next port
	port = a.Allocate(ProtocolModbus)
	if port != 5001 {
		t.Errorf("Expected second modbus port 5001, got %d", port)
	}

	// Third allocation
	port = a.Allocate(ProtocolModbus)
	if port != 5002 {
		t.Errorf("Expected third modbus port 5002, got %d", port)
	}
}

func TestAllocateMQTT(t *testing.T) {
	a := NewAllocator(5000, 1883, 9000, 5540)

	port := a.Allocate(ProtocolMQTT)
	if port != 1883 {
		t.Errorf("Expected first mqtt port 1883, got %d", port)
	}

	port = a.Allocate(ProtocolMQTT)
	if port != 1884 {
		t.Errorf("Expected second mqtt port 1884, got %d", port)
	}
}

func TestAllocateOCPP(t *testing.T) {
	a := NewAllocator(5000, 1883, 9000, 5540)

	port := a.Allocate(ProtocolOCPP)
	if port != 9000 {
		t.Errorf("Expected first ocpp port 9000, got %d", port)
	}

	port = a.Allocate(ProtocolOCPP)
	if port != 9001 {
		t.Errorf("Expected second ocpp port 9001, got %d", port)
	}
}

func TestAllocateUnknownProtocol(t *testing.T) {
	a := NewAllocator(5000, 1883, 9000, 5540)

	port := a.Allocate("unknown")
	if port != 0 {
		t.Errorf("Expected 0 for unknown protocol, got %d", port)
	}
}

func TestAllocateString(t *testing.T) {
	a := NewAllocator(5000, 1883, 9000, 5540)

	// Test string-based allocation (backward compatibility)
	port := a.AllocateString("modbus")
	if port != 5000 {
		t.Errorf("Expected modbus port 5000, got %d", port)
	}

	port = a.AllocateString("mqtt")
	if port != 1883 {
		t.Errorf("Expected mqtt port 1883, got %d", port)
	}

	port = a.AllocateString("ocpp")
	if port != 9000 {
		t.Errorf("Expected ocpp port 9000, got %d", port)
	}
}

func TestRelease(t *testing.T) {
	a := NewAllocator(5000, 1883, 9000, 5540)

	// Allocate a port
	port1 := a.Allocate(ProtocolModbus)
	if port1 != 5000 {
		t.Errorf("Expected port 5000, got %d", port1)
	}

	// Allocate another
	port2 := a.Allocate(ProtocolModbus)
	if port2 != 5001 {
		t.Errorf("Expected port 5001, got %d", port2)
	}

	// Release first port
	a.Release(port1)

	// Next allocation should reuse the released port
	port3 := a.Allocate(ProtocolModbus)
	if port3 != 5000 {
		t.Errorf("Expected released port 5000 to be reused, got %d", port3)
	}
}

func TestIsAllocated(t *testing.T) {
	a := NewAllocator(5000, 1883, 9000, 5540)

	// Initially not allocated
	if a.IsAllocated(5000) {
		t.Error("Port 5000 should not be allocated initially")
	}

	// Allocate
	a.Allocate(ProtocolModbus)

	// Now should be allocated
	if !a.IsAllocated(5000) {
		t.Error("Port 5000 should be allocated after Allocate")
	}

	// Release
	a.Release(5000)

	// Should not be allocated anymore
	if a.IsAllocated(5000) {
		t.Error("Port 5000 should not be allocated after Release")
	}
}

func TestAllocatedCount(t *testing.T) {
	a := NewAllocator(5000, 1883, 9000, 5540)

	if a.AllocatedCount() != 0 {
		t.Errorf("Expected 0 allocated ports initially, got %d", a.AllocatedCount())
	}

	a.Allocate(ProtocolModbus)
	if a.AllocatedCount() != 1 {
		t.Errorf("Expected 1 allocated port, got %d", a.AllocatedCount())
	}

	a.Allocate(ProtocolMQTT)
	a.Allocate(ProtocolOCPP)
	if a.AllocatedCount() != 3 {
		t.Errorf("Expected 3 allocated ports, got %d", a.AllocatedCount())
	}

	a.Release(5000)
	if a.AllocatedCount() != 2 {
		t.Errorf("Expected 2 allocated ports after release, got %d", a.AllocatedCount())
	}
}

func TestMixedProtocolAllocation(t *testing.T) {
	a := NewAllocator(5000, 1883, 9000, 5540)

	// Allocate ports from different protocols
	modbus1 := a.Allocate(ProtocolModbus)
	mqtt1 := a.Allocate(ProtocolMQTT)
	ocpp1 := a.Allocate(ProtocolOCPP)
	modbus2 := a.Allocate(ProtocolModbus)
	mqtt2 := a.Allocate(ProtocolMQTT)

	// Verify each protocol has its own range
	if modbus1 != 5000 || modbus2 != 5001 {
		t.Errorf("Modbus ports wrong: %d, %d", modbus1, modbus2)
	}
	if mqtt1 != 1883 || mqtt2 != 1884 {
		t.Errorf("MQTT ports wrong: %d, %d", mqtt1, mqtt2)
	}
	if ocpp1 != 9000 {
		t.Errorf("OCPP port wrong: %d", ocpp1)
	}

	if a.AllocatedCount() != 5 {
		t.Errorf("Expected 5 allocated ports, got %d", a.AllocatedCount())
	}
}

func TestConcurrentAllocation(t *testing.T) {
	a := NewAllocator(5000, 1883, 9000, 5540)

	var wg sync.WaitGroup
	allocatedPorts := make(chan int, 100)

	// Spawn 100 goroutines to allocate ports concurrently
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			port := a.Allocate(ProtocolModbus)
			allocatedPorts <- port
		}()
	}

	wg.Wait()
	close(allocatedPorts)

	// Collect all allocated ports
	seen := make(map[int]bool)
	for port := range allocatedPorts {
		if port == 0 {
			t.Error("Got invalid port 0")
			continue
		}
		if seen[port] {
			t.Errorf("Port %d was allocated twice", port)
		}
		seen[port] = true
	}

	// Should have 100 unique ports
	if len(seen) != 100 {
		t.Errorf("Expected 100 unique ports, got %d", len(seen))
	}

	// All ports should be in range 5000-5099
	for port := range seen {
		if port < 5000 || port >= 5100 {
			t.Errorf("Port %d out of expected range 5000-5099", port)
		}
	}
}

func TestReleaseNonExistentPort(t *testing.T) {
	a := NewAllocator(5000, 1883, 9000, 5540)

	// Should not panic or error
	a.Release(9999)
	a.Release(0)
	a.Release(-1)

	// Allocator should still work
	port := a.Allocate(ProtocolModbus)
	if port != 5000 {
		t.Errorf("Expected port 5000, got %d", port)
	}
}

func TestCustomBasePorts(t *testing.T) {
	// Test with non-standard base ports
	a := NewAllocator(10000, 20000, 30000, 40000)

	modbus := a.Allocate(ProtocolModbus)
	mqtt := a.Allocate(ProtocolMQTT)
	ocpp := a.Allocate(ProtocolOCPP)

	if modbus != 10000 {
		t.Errorf("Expected modbus port 10000, got %d", modbus)
	}
	if mqtt != 20000 {
		t.Errorf("Expected mqtt port 20000, got %d", mqtt)
	}
	if ocpp != 30000 {
		t.Errorf("Expected ocpp port 30000, got %d", ocpp)
	}
}
