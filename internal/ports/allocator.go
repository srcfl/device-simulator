package ports

import (
	"fmt"
	"net"
	"sync"
)

// Protocol represents a supported protocol type
type Protocol string

const (
	ProtocolModbus Protocol = "modbus"
	ProtocolMQTT   Protocol = "mqtt"
	ProtocolOCPP   Protocol = "ocpp"
)

// Allocator manages dynamic port allocation for simulators
type Allocator struct {
	mu             sync.Mutex
	modbusBase     int
	mqttBase       int
	ocppBase       int
	allocatedPorts map[int]bool
}

// NewAllocator creates a new port allocator with the given base ports
func NewAllocator(modbusBase, mqttBase, ocppBase int) *Allocator {
	return &Allocator{
		modbusBase:     modbusBase,
		mqttBase:       mqttBase,
		ocppBase:       ocppBase,
		allocatedPorts: make(map[int]bool),
	}
}

// Allocate allocates the next available port for the given protocol
// Returns 0 if no port is available
func (a *Allocator) Allocate(protocol Protocol) int {
	a.mu.Lock()
	defer a.mu.Unlock()

	var base int
	switch protocol {
	case ProtocolModbus:
		base = a.modbusBase
	case ProtocolMQTT:
		base = a.mqttBase
	case ProtocolOCPP:
		base = a.ocppBase
	default:
		return 0
	}

	// Find next available port starting from base
	for port := base; port < base+1000; port++ {
		if !a.allocatedPorts[port] {
			a.allocatedPorts[port] = true
			return port
		}
	}
	return 0 // No available port
}

// Reserve reserves a specific port if available
func (a *Allocator) Reserve(port int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.allocatedPorts[port] {
		return false
	}
	if !isPortAvailable(port) {
		return false
	}
	a.allocatedPorts[port] = true
	return true
}

// IsAvailable returns true if the port is free on the OS
func (a *Allocator) IsAvailable(port int) bool {
	return isPortAvailable(port)
}

// AllocateAvailable allocates the next available port that is free on the OS
// Returns 0 if no port is available
func (a *Allocator) AllocateAvailable(protocol Protocol) int {
	a.mu.Lock()
	defer a.mu.Unlock()

	var base int
	switch protocol {
	case ProtocolModbus:
		base = a.modbusBase
	case ProtocolMQTT:
		base = a.mqttBase
	case ProtocolOCPP:
		base = a.ocppBase
	default:
		return 0
	}

	for port := base; port < base+1000; port++ {
		if a.allocatedPorts[port] {
			continue
		}
		if !isPortAvailable(port) {
			continue
		}
		a.allocatedPorts[port] = true
		return port
	}
	return 0
}

// AllocateString allocates a port using a string protocol name
// This maintains backward compatibility with existing code
func (a *Allocator) AllocateString(protocol string) int {
	return a.Allocate(Protocol(protocol))
}

// AllocateAvailableString allocates a port using a string protocol name and checks OS availability
func (a *Allocator) AllocateAvailableString(protocol string) int {
	return a.AllocateAvailable(Protocol(protocol))
}

// Release releases a previously allocated port
func (a *Allocator) Release(port int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.allocatedPorts, port)
}

// IsAllocated returns whether a port is currently allocated
func (a *Allocator) IsAllocated(port int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allocatedPorts[port]
}

// AllocatedCount returns the number of currently allocated ports
func (a *Allocator) AllocatedCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.allocatedPorts)
}

// GetBasePorts returns the configured base ports for each protocol
func (a *Allocator) GetBasePorts() (modbus, mqtt, ocpp int) {
	return a.modbusBase, a.mqttBase, a.ocppBase
}

func isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
