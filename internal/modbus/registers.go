package modbus

import (
	"encoding/binary"
	"fmt"
	"math"
)

// DataType represents the type of a Modbus register
type DataType int

const (
	U16 DataType = iota
	I16
	U32
	I32
	F32 // IEEE 754 single-precision float
	STR // Multi-word ASCII string
	U64 // Unsigned 64-bit integer (4 words)
)

func (d DataType) String() string {
	return [...]string{"U16", "I16", "U32", "I32", "F32", "STR", "U64"}[d]
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
	Address      uint16
	Name         string
	SemanticName string // Standardized name for device-agnostic access (e.g. "pv_power", "battery_soc")
	Description  string
	Unit         string
	Category     string // pv, battery, meter, grid, control
	DataType     DataType
	Scale        float64
	Words        int
	Endianness   Endianness
	UseHolding   bool
}

// RegisterSet is a collection of register definitions for a specific device type.
// It provides address-based and semantic-name-based lookups.
type RegisterSet struct {
	Name        string
	Definitions []RegisterDef
	Lookup      map[uint16]*RegisterDef // by address
	Semantic    map[string]*RegisterDef // by semantic name
	Defaults    map[uint16]int64        // default register values
}

// NewRegisterSet creates a RegisterSet from a slice of definitions and defaults.
func NewRegisterSet(name string, defs []RegisterDef, defaults map[uint16]int64) *RegisterSet {
	rs := &RegisterSet{
		Name:        name,
		Definitions: defs,
		Lookup:      make(map[uint16]*RegisterDef, len(defs)),
		Semantic:    make(map[string]*RegisterDef, len(defs)),
		Defaults:    defaults,
	}
	for i := range rs.Definitions {
		d := &rs.Definitions[i]
		rs.Lookup[d.Address] = d
		if d.SemanticName != "" {
			rs.Semantic[d.SemanticName] = d
		}
	}
	return rs
}

// RegisterDefinitions contains all known register definitions (Sungrow + SDM630 + Fronius meter).
// For device-specific register sets, use the devices package.
// Categories: pv, battery, meter, grid, control
var RegisterDefinitions = []RegisterDef{
	// PV Registers (Sungrow SH Hybrid - Input Registers)
	{Address: 4990, Name: "Serial Number (Lua)", SemanticName: "serial_number_lua", Description: "Sungrow serial as addressed by srcfl/device-drivers (host 4990)", Unit: "", Category: "pv", DataType: STR, Scale: 1.0, Words: 10, Endianness: Big, UseHolding: false},
	{Address: 4999, Name: "Device Type", SemanticName: "device_type", Description: "Sungrow device type (0x0E0E = SH8.0RT-V112)", Unit: "", Category: "pv", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 5000, Name: "Nominal Output Power", SemanticName: "nominal_power", Description: "Nominal output power of the inverter", Unit: "kW", Category: "pv", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 5007, Name: "Inside Temperature", SemanticName: "", Description: "Internal temperature of the inverter", Unit: "°C", Category: "pv", DataType: I16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 5010, Name: "MPPT 1 Voltage", SemanticName: "", Description: "Maximum Power Point Tracker 1 Voltage", Unit: "V", Category: "pv", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 5011, Name: "MPPT 1 Current", SemanticName: "", Description: "Maximum Power Point Tracker 1 Current", Unit: "A", Category: "pv", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 5012, Name: "MPPT 2 Voltage", SemanticName: "", Description: "Maximum Power Point Tracker 2 Voltage", Unit: "V", Category: "pv", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 5013, Name: "MPPT 2 Current", SemanticName: "", Description: "Maximum Power Point Tracker 2 Current", Unit: "A", Category: "pv", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 5016, Name: "PV Power", SemanticName: "pv_power", Description: "Total DC power", Unit: "W", Category: "pv", DataType: U32, Scale: 1.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 13002, Name: "Total PV Generation", SemanticName: "total_pv_gen", Description: "Total photovoltaic generation", Unit: "Wh", Category: "pv", DataType: U32, Scale: 100.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 13073, Name: "PV Command", SemanticName: "pv_cmd", Description: "PV power limit", Unit: "W", Category: "pv", DataType: U16, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: true},
	{Address: 13088, Name: "Active Power Limit Enable", SemanticName: "pv_limit_enable", Description: "170=on; 85=off (Lua host address, documented 13089)", Unit: "", Category: "pv", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 13089, Name: "Active Power Limit Ratio", SemanticName: "pv_limit_ratio", Description: "Active power limit in 0.1% units (1000=100%)", Unit: "%", Category: "pv", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: true},

	// Battery Registers (Sungrow SH Hybrid - Input Registers)
	{Address: 12999, Name: "Running State", SemanticName: "running_state", Description: "Sungrow documented running state (host address 12999)", Unit: "", Category: "battery", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 13000, Name: "System State Flags", SemanticName: "battery_flags", Description: "Bitmask of inverter running states", Unit: "", Category: "battery", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 13019, Name: "Battery Voltage", SemanticName: "", Description: "Battery voltage", Unit: "V", Category: "battery", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 13020, Name: "Battery Current", SemanticName: "", Description: "Battery current", Unit: "A", Category: "battery", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 13021, Name: "Battery Power", SemanticName: "battery_power", Description: "Battery power", Unit: "W", Category: "battery", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 13022, Name: "Battery SoC", SemanticName: "battery_soc", Description: "Battery state of charge", Unit: "%", Category: "battery", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 13024, Name: "Battery Temperature", SemanticName: "", Description: "Battery temperature", Unit: "°C", Category: "battery", DataType: I16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 13026, Name: "Total Battery Discharge", SemanticName: "total_discharge", Description: "Total battery discharge energy", Unit: "Wh", Category: "battery", DataType: U32, Scale: 100.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 13040, Name: "Total Charge Energy", SemanticName: "total_charge", Description: "Total charge energy", Unit: "Wh", Category: "battery", DataType: U32, Scale: 100.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 13057, Name: "Battery Max SoC", SemanticName: "battery_max_soc", Description: "Maximum battery state of charge threshold", Unit: "%", Category: "battery", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 13058, Name: "Battery Min SoC", SemanticName: "battery_min_soc", Description: "Minimum battery state of charge threshold", Unit: "%", Category: "battery", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 13050, Name: "Charge/Discharge Cmd", SemanticName: "battery_cmd", Description: "170=charge; 187=discharge; 204=stop", Unit: "", Category: "battery", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 13051, Name: "Battery Setpoint", SemanticName: "battery_setpoint", Description: "Battery power setpoint", Unit: "W", Category: "battery", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 33046, Name: "Max Charge Power", SemanticName: "max_charge_power", Description: "Max charge power", Unit: "kW", Category: "battery", DataType: U16, Scale: 0.01, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 33047, Name: "Max Discharge Power", SemanticName: "max_discharge_power", Description: "Max discharge power", Unit: "kW", Category: "battery", DataType: U16, Scale: 0.01, Words: 1, Endianness: Big, UseHolding: true},

	// Meter Registers (Sungrow external meter - Input Registers)
	{Address: 5600, Name: "Meter Power", SemanticName: "meter_power", Description: "Meter total active power", Unit: "W", Category: "meter", DataType: I32, Scale: 1.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 5602, Name: "Meter L1 Power", SemanticName: "meter_l1_power", Description: "Meter phase A active power", Unit: "W", Category: "meter", DataType: I32, Scale: 1.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 5604, Name: "Meter L2 Power", SemanticName: "meter_l2_power", Description: "Meter phase B active power", Unit: "W", Category: "meter", DataType: I32, Scale: 1.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 5606, Name: "Meter L3 Power", SemanticName: "meter_l3_power", Description: "Meter phase C active power", Unit: "W", Category: "meter", DataType: I32, Scale: 1.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 5740, Name: "Meter L1 Voltage", SemanticName: "meter_l1_voltage", Description: "Meter phase A voltage", Unit: "V", Category: "meter", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 5741, Name: "Meter L2 Voltage", SemanticName: "meter_l2_voltage", Description: "Meter phase B voltage", Unit: "V", Category: "meter", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 5742, Name: "Meter L3 Voltage", SemanticName: "meter_l3_voltage", Description: "Meter phase C voltage", Unit: "V", Category: "meter", DataType: U16, Scale: 0.1, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 5743, Name: "Meter L1 Current", SemanticName: "meter_l1_current", Description: "Meter phase A current", Unit: "A", Category: "meter", DataType: U16, Scale: 0.01, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 5744, Name: "Meter L2 Current", SemanticName: "meter_l2_current", Description: "Meter phase B current", Unit: "A", Category: "meter", DataType: U16, Scale: 0.01, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 5745, Name: "Meter L3 Current", SemanticName: "meter_l3_current", Description: "Meter phase C current", Unit: "A", Category: "meter", DataType: U16, Scale: 0.01, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 13036, Name: "Total Import Energy", SemanticName: "total_import", Description: "Total import energy", Unit: "Wh", Category: "meter", DataType: U32, Scale: 100.0, Words: 2, Endianness: Little, UseHolding: false},
	{Address: 13045, Name: "Total Export Energy", SemanticName: "total_export", Description: "Total export energy", Unit: "Wh", Category: "meter", DataType: U32, Scale: 100.0, Words: 2, Endianness: Little, UseHolding: false},

	// Grid Registers
	{Address: 5241, Name: "Grid Frequency HP", SemanticName: "grid_frequency", Description: "Grid Frequency - High precision", Unit: "Hz", Category: "grid", DataType: U16, Scale: 0.01, Words: 1, Endianness: Big, UseHolding: false},
	{Address: 13049, Name: "Grid Mode", SemanticName: "grid_mode", Description: "0: Self-consumption; 2: Forced; 3: External EMS", Unit: "", Category: "grid", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: true},
	{Address: 13086, Name: "Export Limit Enable", SemanticName: "export_limit", Description: "170=enabled; 85=disabled", Unit: "", Category: "grid", DataType: U16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: true},

	// Control/Load Registers
	{Address: 13052, Name: "Load Power", SemanticName: "load_power", Description: "Load power", Unit: "W", Category: "control", DataType: I16, Scale: 1.0, Words: 1, Endianness: Big, UseHolding: true},

	// SDM630 Registers (Input Registers - Function Code 0x04)
	// All SDM630 registers use F32 (IEEE 754 float), Big Endian
	{Address: 0, Name: "SDM630 L1 Voltage", SemanticName: "sdm_l1_voltage", Description: "Phase 1 Voltage", Unit: "V", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 2, Name: "SDM630 L2 Voltage", SemanticName: "sdm_l2_voltage", Description: "Phase 2 Voltage", Unit: "V", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 4, Name: "SDM630 L3 Voltage", SemanticName: "sdm_l3_voltage", Description: "Phase 3 Voltage", Unit: "V", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 6, Name: "SDM630 L1 Current", SemanticName: "sdm_l1_current", Description: "Phase 1 Current", Unit: "A", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 8, Name: "SDM630 L2 Current", SemanticName: "sdm_l2_current", Description: "Phase 2 Current", Unit: "A", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 10, Name: "SDM630 L3 Current", SemanticName: "sdm_l3_current", Description: "Phase 3 Current", Unit: "A", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 12, Name: "SDM630 L1 Power", SemanticName: "sdm_l1_power", Description: "Phase 1 Power", Unit: "W", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 14, Name: "SDM630 L2 Power", SemanticName: "sdm_l2_power", Description: "Phase 2 Power", Unit: "W", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 16, Name: "SDM630 L3 Power", SemanticName: "sdm_l3_power", Description: "Phase 3 Power", Unit: "W", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 52, Name: "SDM630 Total Power", SemanticName: "sdm_total_power", Description: "Total System Power", Unit: "W", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 70, Name: "SDM630 Frequency", SemanticName: "sdm_frequency", Description: "Frequency", Unit: "Hz", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 72, Name: "SDM630 Import Energy", SemanticName: "sdm_import_energy", Description: "Total Import Energy", Unit: "kWh", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 74, Name: "SDM630 Export Energy", SemanticName: "sdm_export_energy", Description: "Total Export Energy", Unit: "kWh", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: false},
	{Address: 64512, Name: "SDM630 Serial", SemanticName: "sdm_serial", Description: "SDM630 serial number (0xFC00)", Unit: "", Category: "meter", DataType: U32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: true},

	// Fronius Registers (SunSpec-style)
	// Gateway reads at 40074 (firmware's 40073 + 1), offset calc uses 40073
	// So firmware offset X reads Modbus addr 40074+X, we store at firmware_addr+1
	{Address: 40074, Name: "Fronius L1 Current", SemanticName: "fronius_l1_current", Description: "AC Phase-A Current", Unit: "A", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: true},
	{Address: 40076, Name: "Fronius L2 Current", SemanticName: "fronius_l2_current", Description: "AC Phase-B Current", Unit: "A", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: true},
	{Address: 40078, Name: "Fronius L3 Current", SemanticName: "fronius_l3_current", Description: "AC Phase-C Current", Unit: "A", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: true},
	{Address: 40086, Name: "Fronius L1 Voltage", SemanticName: "fronius_l1_voltage", Description: "AC Voltage Phase-A", Unit: "V", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: true},
	{Address: 40088, Name: "Fronius L2 Voltage", SemanticName: "fronius_l2_voltage", Description: "AC Voltage Phase-B", Unit: "V", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: true},
	{Address: 40090, Name: "Fronius L3 Voltage", SemanticName: "fronius_l3_voltage", Description: "AC Voltage Phase-C", Unit: "V", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: true},
	{Address: 40092, Name: "Fronius AC Power", SemanticName: "fronius_ac_power", Description: "AC Power (+=import, -=export)", Unit: "W", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: true},
	{Address: 40094, Name: "Fronius Frequency", SemanticName: "fronius_frequency", Description: "AC Frequency", Unit: "Hz", Category: "meter", DataType: F32, Scale: 1.0, Words: 2, Endianness: Big, UseHolding: true},
}

// SungrowRegisterSet is the RegisterSet for Sungrow inverters built from RegisterDefinitions.
var SungrowRegisterSet *RegisterSet

// RegisterLookup maps address to RegisterDef
var RegisterLookup = make(map[uint16]*RegisterDef)

func init() {
	for i := range RegisterDefinitions {
		RegisterLookup[RegisterDefinitions[i].Address] = &RegisterDefinitions[i]
	}
	// Build SungrowRegisterSet from the Sungrow-specific definitions (non-SDM630, non-Fronius)
	var sungrowDefs []RegisterDef
	for _, def := range RegisterDefinitions {
		// Exclude SDM630 (addresses 0-74, 64512) and Fronius meter (40074-40094)
		if def.Address <= 74 || def.Address == 64512 || (def.Address >= 40053 && def.Address <= 40094) {
			continue
		}
		sungrowDefs = append(sungrowDefs, def)
	}
	SungrowRegisterSet = NewRegisterSet("sungrow", sungrowDefs, DefaultValues)
}

// DefaultValues contains the default register values
var DefaultValues = map[uint16]int64{
	// Device Ratings
	5000: 100,
	5241: 50, // Grid Frequency (50.00 Hz, scaled by 100)

	// Power Flow Registers
	5016:  0,  // PV Power
	13021: 0,  // Battery Power
	13022: 78, // Battery SoC (%)
	5600:  0,  // Meter Power
	13052: 0,  // Load Power

	// Meter Voltages (always present when grid-connected)
	5740: 230, // Meter L1 Voltage (230V)
	5741: 230, // Meter L2 Voltage (230V)
	5742: 230, // Meter L3 Voltage (230V)

	// Battery Measurements
	13019: 48, // Battery Voltage (48V)
	13024: 25, // Battery Temperature (25°C)

	// Inverter Temperature
	5007: 35, // Inside Temperature (35°C)

	// Control Registers
	13000: 0,     // Battery Flags
	13049: 0,     // Grid Mode
	13050: 204,   // Charge/Discharge Command (204=Stop)
	13051: 0,     // Battery Setpoint
	13073: 10000, // PV Command Power
	13088: 85,    // Active power limit off
	13089: 100,   // 100%
	13086: 170,   // Export Limit Enable (170=Enabled)
	4999:  0x0E0E,
	12999: 1,
	33046: 500, // Max Charge Power (5kW)
	33047: 500, // Max Discharge Power (5kW)
	13057: 100, // Battery Max SoC (100.0%)
	13058: 0,   // Battery Min SoC (0.0%)

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

	case F32:
		// IEEE 754 single-precision float (no scaling for floats)
		bits := math.Float32bits(float32(value))
		return encodeU32(bits, def.Endianness), nil

	case U64:
		uval := uint64(raw)
		return encodeU64(uval, def.Endianness), nil

	case STR:
		// STR encoding not supported via numeric value
		return nil, fmt.Errorf("use EncodeString for STR registers")
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

	case F32:
		bits := decodeU32(registers, def.Endianness)
		return float64(math.Float32frombits(bits)), nil

	case U64:
		raw = int64(decodeU64(registers, def.Endianness))

	case STR:
		// STR decoding not supported as numeric value
		return 0, fmt.Errorf("use DecodeString for STR registers")
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

func encodeU64(value uint64, endian Endianness) []uint16 {
	w0 := uint16(value >> 48)
	w1 := uint16(value >> 32)
	w2 := uint16(value >> 16)
	w3 := uint16(value)
	if endian == Big {
		return []uint16{w0, w1, w2, w3}
	}
	return []uint16{w3, w2, w1, w0}
}

func decodeU64(registers []uint16, endian Endianness) uint64 {
	if endian == Big {
		return (uint64(registers[0]) << 48) | (uint64(registers[1]) << 32) | (uint64(registers[2]) << 16) | uint64(registers[3])
	}
	return (uint64(registers[3]) << 48) | (uint64(registers[2]) << 32) | (uint64(registers[1]) << 16) | uint64(registers[0])
}

// EncodeString encodes a string into register words (2 bytes per register, big-endian).
func EncodeString(s string, numWords int) []uint16 {
	padded := s
	maxLen := numWords * 2
	if len(padded) > maxLen {
		padded = padded[:maxLen]
	}
	for len(padded) < maxLen {
		padded += "\x00"
	}
	regs := make([]uint16, numWords)
	for i := 0; i < numWords; i++ {
		regs[i] = uint16(padded[i*2])<<8 | uint16(padded[i*2+1])
	}
	return regs
}

// DecodeString decodes register words into a string (2 bytes per register, big-endian).
func DecodeString(registers []uint16) string {
	bytes := make([]byte, 0, len(registers)*2)
	for _, reg := range registers {
		bytes = append(bytes, byte(reg>>8), byte(reg&0xFF))
	}
	// Trim null bytes
	for i, b := range bytes {
		if b == 0 {
			bytes = bytes[:i]
			break
		}
	}
	return string(bytes)
}

// RegisterPayload is the JSON response format for a register
type RegisterPayload struct {
	Address      uint16   `json:"address"`
	Name         string   `json:"name"`
	SemanticName string   `json:"semantic_name,omitempty"`
	Description  string   `json:"description"`
	Unit         string   `json:"unit"`
	Category     string   `json:"category"`
	Value        float64  `json:"value"`
	Scale        float64  `json:"scale"`
	Raw          []uint16 `json:"raw"`
	DataType     string   `json:"data_type"`
	Endianness   string   `json:"endianness"`
	Words        int      `json:"words"`
}

// ToPayload creates a RegisterPayload from a register definition and raw values
func (def *RegisterDef) ToPayload(registers []uint16) (*RegisterPayload, error) {
	value, err := DecodeValue(registers, def)
	if err != nil {
		return nil, err
	}

	return &RegisterPayload{
		Address:      def.Address,
		Name:         def.Name,
		SemanticName: def.SemanticName,
		Description:  def.Description,
		Unit:         def.Unit,
		Category:     def.Category,
		Value:        value,
		Scale:        def.Scale,
		Raw:          registers,
		DataType:     def.DataType.String(),
		Endianness:   def.Endianness.String(),
		Words:        def.Words,
	}, nil
}

// SetSerialNumber encodes a serial number into registers at address 4989
// Also writes to SDM630 register 64512 (0xFC00) and Fronius register 40053
func SetSerialNumber(serial string, setFunc func(addr uint16, values []uint16)) {
	// Sungrow-style: Pad or truncate to 20 characters (10 registers * 2 bytes)
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

	setFunc(4989, registers)
	// srcfl/device-drivers Sungrow Lua reads serial at host address 4990.
	setFunc(4990, registers)

	// SDM630: Write numeric serial to register 64512/0xFC00 (U32, Big Endian)
	// If serial is numeric, use it directly; otherwise hash the string
	var numericSerial uint32
	if n, err := parseUint32(serial); err == nil {
		numericSerial = n
	} else {
		// Fallback: hash string to numeric value
		numericSerial = 1000000
		for i, c := range serial {
			numericSerial += uint32(c) * uint32(i+1) * 7
		}
	}
	sdm630Regs := []uint16{
		uint16(numericSerial >> 16),
		uint16(numericSerial & 0xFFFF),
	}
	setFunc(64512, sdm630Regs)

	// Fronius: Write string serial to register 40053 (16 registers = 32 bytes)
	// Note: +1 offset due to Modbus addressing convention
	froniusPadded := serial
	if len(froniusPadded) > 32 {
		froniusPadded = froniusPadded[:32]
	}
	for len(froniusPadded) < 32 {
		froniusPadded += "\x00"
	}
	froniusRegs := make([]uint16, 16)
	for i := 0; i < 16; i++ {
		froniusRegs[i] = uint16(froniusPadded[i*2])<<8 | uint16(froniusPadded[i*2+1])
	}
	setFunc(40053, froniusRegs)
}

// parseUint32 parses a string as uint32, returning error if not valid
func parseUint32(s string) (uint32, error) {
	// Try parsing as decimal
	var n uint64
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil {
		return 0, err
	}
	if n > 0xFFFFFFFF {
		return 0, fmt.Errorf("value exceeds uint32 max")
	}
	return uint32(n), nil
}

// Helper for reading bytes as big-endian uint16
func bytesToUint16BE(b []byte) uint16 {
	return binary.BigEndian.Uint16(b)
}
