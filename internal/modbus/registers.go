package modbus

import (
	"encoding/binary"
	"fmt"
)

// DataType represents the type of a Modbus register
type DataType int

const (
	U16 DataType = iota
	I16
	U32
	I32
)

func (d DataType) String() string {
	return [...]string{"U16", "I16", "U32", "I32"}[d]
}

// Endianness for multi-word registers
type Endianness int

const (
	Big Endianness = iota
	Little
)

func (e Endianness) String() string {
	return [...]string{"BIG", "LITTLE"}[e]
}

// RegisterDef defines a Modbus register
type RegisterDef struct {
	Address     uint16
	Name        string
	Description string
	Unit        string
	Category    string // pv, battery, meter, grid, control
	DataType    DataType
	Scale       float64
	Words       int
	Endianness  Endianness
	UseHolding  bool
}

// RegisterDefinitions contains all known register definitions
// Categories: pv, battery, meter, grid, control
var RegisterDefinitions = []RegisterDef{
	// PV Registers
	{Address: 5016, Name: "PV Power", Description: "Total PV power", Unit: "W", Category: "pv", DataType: U32, Scale: 1.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 13002, Name: "Total PV Generation", Description: "Total PV generation", Unit: "Wh", Category: "pv", DataType: U32, Scale: 1.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 13073, Name: "PV Command", Description: "PV power limit", Unit: "W", Category: "pv", DataType: U16, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: true},

	// Battery Registers
	{Address: 13021, Name: "Battery Power", Description: "Battery power magnitude", Unit: "W", Category: "battery", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 13022, Name: "Battery SoC", Description: "Battery state of charge", Unit: "%", Category: "battery", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 13000, Name: "Battery Flags", Description: "Bit1: charging, Bit2: discharging", Unit: "", Category: "battery", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 13050, Name: "Charge/Discharge Cmd", Description: "170=charge; 187=discharge; 204=stop", Unit: "", Category: "battery", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 13051, Name: "Battery Setpoint", Description: "Battery power setpoint", Unit: "W", Category: "battery", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 33046, Name: "Max Charge Power", Description: "Max charge power", Unit: "kW", Category: "battery", DataType: U16, Scale: 0.01, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 33047, Name: "Max Discharge Power", Description: "Max discharge power", Unit: "kW", Category: "battery", DataType: U16, Scale: 0.01, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 13040, Name: "Total Charge Energy", Description: "Total charge energy", Unit: "Wh", Category: "battery", DataType: U32, Scale: 1.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 13026, Name: "Total Battery Discharge", Description: "Total battery discharge energy", Unit: "Wh", Category: "battery", DataType: U32, Scale: 1.0, Words: 2, Endianness: Little, UseHolding: false},

	// Meter Registers
	{Address: 5600, Name: "Meter Power", Description: "Meter active power (+ = import, - = export)", Unit: "W", Category: "meter", DataType: I32, Scale: 1.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 13036, Name: "Total Import Energy", Description: "Total import energy", Unit: "Wh", Category: "meter", DataType: U32, Scale: 1.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 13045, Name: "Total Export Energy", Description: "Total export energy", Unit: "Wh", Category: "meter", DataType: U32, Scale: 1.0, Words: 2, Endianness: Little, UseHolding: false},

	// Grid Registers
	{Address: 5241, Name: "Grid Frequency HP", Description: "Grid Frequency - High precision", Unit: "Hz", Category: "grid", DataType: U16, Scale: 0.01, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 13049, Name: "Grid Mode", Description: "0: Self-consumption; 2: Forced; 3: External EMS", Unit: "", Category: "grid", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 13086, Name: "Export Limit Enable", Description: "170=enabled; 85=disabled", Unit: "", Category: "grid", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: true},

	// Control/Load Registers
	{Address: 13052, Name: "Load Power", Description: "Load power", Unit: "W", Category: "control", DataType: I16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: true},
}

// RegisterLookup maps address to RegisterDef
var RegisterLookup = make(map[uint16]*RegisterDef)

func init() {
	for i := range RegisterDefinitions {
		RegisterLookup[RegisterDefinitions[i].Address] = &RegisterDefinitions[i]
	}
}

// DefaultValues contains the default register values
var DefaultValues = map[uint16]int64{
	// Device Ratings
	5000:  100,
	5001:  100,
	5241:  50, // Grid Frequency (50.00 Hz, scaled by 100)

	// Power Flow Registers
	5016:  0,  // PV Power
	13021: 0,  // Battery Power
	13022: 78, // Battery SoC (%)
	5600:  0,  // Meter Power
	13052: 0,  // Load Power

	// Control Registers
	13000: 0,     // Battery Flags
	13049: 0,     // Grid Mode
	13050: 204,   // Charge/Discharge Command (204=Stop)
	13051: 0,     // Battery Setpoint
	13073: 10000, // PV Command Power
	13086: 170,   // Export Limit Enable (170=Enabled)
	33046: 500,   // Max Charge Power (5kW)
	33047: 500,   // Max Discharge Power (5kW)

	// Energy totals will be randomized at startup
	13036: 150000, // Total Import Energy (Wh)
	13040: 180000, // Total Charge Energy (Wh)
	13045: 200000, // Total Export Energy (Wh)
	13002: 220000, // Total PV Generation (Wh)
	13026: 160000, // Total Battery Discharge (Wh)
}

// EncodeValue encodes a float64 value to register words according to the register definition
func EncodeValue(value float64, def *RegisterDef) ([]uint16, error) {
	scaled := value
	if def.Scale != 0 {
		scaled = value / def.Scale
	}
	raw := int64(scaled)

	switch def.DataType {
	case U16:
		if raw < 0 || raw > 0xFFFF {
			raw = max(0, min(raw, 0xFFFF))
		}
		return []uint16{uint16(raw)}, nil

	case I16:
		if raw < -0x8000 || raw > 0x7FFF {
			return nil, fmt.Errorf("value %d out of range for I16", raw)
		}
		return []uint16{uint16(raw & 0xFFFF)}, nil

	case U32:
		if raw < 0 || raw > 0xFFFFFFFF {
			return nil, fmt.Errorf("value %d out of range for U32", raw)
		}
		return encodeU32(uint32(raw), def.Endianness), nil

	case I32:
		if raw < -0x80000000 || raw > 0x7FFFFFFF {
			return nil, fmt.Errorf("value %d out of range for I32", raw)
		}
		return encodeU32(uint32(raw), def.Endianness), nil
	}

	return nil, fmt.Errorf("unsupported data type: %v", def.DataType)
}

func encodeU32(value uint32, endian Endianness) []uint16 {
	high := uint16(value >> 16)
	low := uint16(value & 0xFFFF)
	if endian == Big {
		return []uint16{high, low}
	}
	return []uint16{low, high}
}

// DecodeValue decodes register words to a float64 value according to the register definition
func DecodeValue(registers []uint16, def *RegisterDef) (float64, error) {
	if len(registers) < def.Words {
		return 0, fmt.Errorf("expected %d registers, got %d", def.Words, len(registers))
	}

	var raw int64
	switch def.DataType {
	case U16:
		raw = int64(registers[0])

	case I16:
		val := int16(registers[0])
		raw = int64(val)

	case U32:
		raw = int64(decodeU32(registers, def.Endianness))

	case I32:
		uval := decodeU32(registers, def.Endianness)
		raw = int64(int32(uval))
	}

	if def.Scale != 0 {
		return float64(raw) * def.Scale, nil
	}
	return float64(raw), nil
}

func decodeU32(registers []uint16, endian Endianness) uint32 {
	if endian == Big {
		return (uint32(registers[0]) << 16) | uint32(registers[1])
	}
	return (uint32(registers[1]) << 16) | uint32(registers[0])
}

// RegisterPayload is the JSON response format for a register
type RegisterPayload struct {
	Address     uint16   `json:"address"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Unit        string   `json:"unit"`
	Category    string   `json:"category"`
	Value       float64  `json:"value"`
	Scale       float64  `json:"scale"`
	Raw         []uint16 `json:"raw"`
	DataType    string   `json:"data_type"`
	Endianness  string   `json:"endianness"`
	Words       int      `json:"words"`
}

// ToPayload creates a RegisterPayload from a register definition and raw values
func (def *RegisterDef) ToPayload(registers []uint16) (*RegisterPayload, error) {
	value, err := DecodeValue(registers, def)
	if err != nil {
		return nil, err
	}

	return &RegisterPayload{
		Address:     def.Address,
		Name:        def.Name,
		Description: def.Description,
		Unit:        def.Unit,
		Category:    def.Category,
		Value:       value,
		Scale:       def.Scale,
		Raw:         registers,
		DataType:    def.DataType.String(),
		Endianness:  def.Endianness.String(),
		Words:       def.Words,
	}, nil
}

// SetSerialNumber encodes a serial number into registers at address 4990
func SetSerialNumber(serial string, setFunc func(addr uint16, values []uint16)) {
	// Pad or truncate to 20 characters (10 registers * 2 bytes)
	padded := serial
	if len(padded) > 20 {
		padded = padded[:20]
	}
	for len(padded) < 20 {
		padded += "\x00"
	}

	registers := make([]uint16, 10)
	for i := 0; i < 10; i++ {
		registers[i] = uint16(padded[i*2])<<8 | uint16(padded[i*2+1])
	}

	setFunc(4990, registers)
}

// Helper for reading bytes as big-endian uint16
func bytesToUint16BE(b []byte) uint16 {
	return binary.BigEndian.Uint16(b)
}
