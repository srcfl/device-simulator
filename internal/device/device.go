package device

import "time"

// Protocol represents the communication protocol used by a device
type Protocol string

const (
	ProtocolModbus Protocol = "modbus"
	ProtocolMQTT   Protocol = "mqtt"
	ProtocolOCPP   Protocol = "ocpp"
)

// Category represents the high-level device category
type Category string

const (
	CategoryInverter    Category = "inverter"
	CategoryV2XCharger  Category = "v2x_charger"
	CategoryOCPPCharger Category = "ocpp_charger"
)

// LogEntry represents a protocol operation log entry
type LogEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Operation string                 `json:"operation"`
	Details   map[string]interface{} `json:"details,omitempty"`
	SourceIP  string                 `json:"source_ip,omitempty"`
}

// DeviceServer is the common interface for all protocol servers
type DeviceServer interface {
	// Lifecycle
	Start() error
	Stop() error

	// State management
	GetState() map[string]interface{}
	SetValue(key string, value interface{}) error

	// Logging
	GetLogs(limit int) []LogEntry

	// Metadata
	Protocol() Protocol
	Category() Category
	Address() string
	SerialNumber() string
	SetSerialNumber(sn string)
}

// Automatable extends DeviceServer with time-based value generation
type Automatable interface {
	DeviceServer
	GenerateValues(simTime time.Time)
}
