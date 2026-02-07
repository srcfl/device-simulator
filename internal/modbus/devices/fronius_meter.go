package devices

import "github.com/srcfl/device-simulator/internal/modbus"

// FroniusMeterRegisters returns the RegisterSet for Fronius Smart Meter (TS 65A-3).
// Addresses from firmware profile: fronius_smart_meter
// SunSpec Meter model, Holding Registers (FC 0x03).
// The gateway reads with base address 40073, actual Modbus addresses are offset+1.
func FroniusMeterRegisters() *modbus.RegisterSet {
	return modbus.NewRegisterSet("fronius-smart-meter", []modbus.RegisterDef{
		// Identity
		{Address: 40053, Name: "Serial Number", SemanticName: "serial_number", Description: "Meter serial number", Unit: "", Category: "meter", DataType: modbus.STR, Scale: 1.0, Words: 16, Endianness: modbus.Big, UseHolding: true},

		// Current
		{Address: 40074, Name: "L1 Current", SemanticName: "meter_l1_current", Description: "Phase A current", Unit: "A", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 40076, Name: "L2 Current", SemanticName: "meter_l2_current", Description: "Phase B current", Unit: "A", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 40078, Name: "L3 Current", SemanticName: "meter_l3_current", Description: "Phase C current", Unit: "A", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},

		// Voltage
		{Address: 40086, Name: "L1 Voltage", SemanticName: "meter_l1_voltage", Description: "Phase A voltage", Unit: "V", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 40088, Name: "L2 Voltage", SemanticName: "meter_l2_voltage", Description: "Phase B voltage", Unit: "V", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 40090, Name: "L3 Voltage", SemanticName: "meter_l3_voltage", Description: "Phase C voltage", Unit: "V", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},

		// Power
		{Address: 40092, Name: "AC Power", SemanticName: "meter_power", Description: "Total active power (+=import)", Unit: "W", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},

		// Frequency
		{Address: 40094, Name: "Frequency", SemanticName: "grid_frequency", Description: "AC frequency", Unit: "Hz", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},

		// Energy
		{Address: 40098, Name: "Total Import Energy", SemanticName: "total_import", Description: "Total imported energy", Unit: "Wh", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 40100, Name: "Total Export Energy", SemanticName: "total_export", Description: "Total exported energy", Unit: "Wh", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
	}, map[uint16]int64{
		// Defaults set via profile float defaults (F32 encoding)
	})
}
