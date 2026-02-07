package devices

import "github.com/srcfl/device-simulator/internal/modbus"

// SolarEdgeRegisters returns the RegisterSet for SolarEdge inverters with StorEdge.
// Addresses from firmware profile: solaredge
// SolarEdge uses SunSpec models over Holding Registers (FC 0x03).
// Note: SolarEdge uses scale factor (SF) registers adjacent to value registers.
func SolarEdgeRegisters() *modbus.RegisterSet {
	return modbus.NewRegisterSet("solaredge", []modbus.RegisterDef{
		// Identity (SunSpec Common Model)
		{Address: 40052, Name: "Serial Number", SemanticName: "serial_number", Description: "Inverter serial number", Unit: "", Category: "pv", DataType: modbus.STR, Scale: 1.0, Words: 16, Endianness: modbus.Big, UseHolding: true},

		// PV / AC Output (SunSpec Inverter Model 103)
		{Address: 40083, Name: "AC Power", SemanticName: "pv_power", Description: "AC power output", Unit: "W", Category: "pv", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40084, Name: "AC Power SF", Description: "AC power scale factor", Unit: "", Category: "pv", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40071, Name: "AC Current", Description: "AC total current", Unit: "A", Category: "pv", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40072, Name: "AC Current A", Description: "Phase A current", Unit: "A", Category: "pv", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40073, Name: "AC Current B", Description: "Phase B current", Unit: "A", Category: "pv", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40074, Name: "AC Current C", Description: "Phase C current", Unit: "A", Category: "pv", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40075, Name: "AC Current SF", Description: "Current scale factor", Unit: "", Category: "pv", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Grid
		{Address: 40085, Name: "Grid Frequency", SemanticName: "grid_frequency", Description: "AC frequency", Unit: "Hz", Category: "grid", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40086, Name: "Grid Frequency SF", Description: "Frequency scale factor", Unit: "", Category: "grid", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Meter (SolarEdge meter at register block starting ~40190)
		{Address: 40206, Name: "Meter Power", SemanticName: "meter_power", Description: "Total active power (+=export)", Unit: "W", Category: "meter", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40210, Name: "Meter Power SF", Description: "Power scale factor", Unit: "", Category: "meter", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Battery (StorEdge)
		{Address: 62852, Name: "Battery SoC", SemanticName: "battery_soc", Description: "Battery state of charge", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},  // Actually F32 in some models
		{Address: 62836, Name: "Battery Power", SemanticName: "battery_power", Description: "Battery instantaneous power", Unit: "W", Category: "battery", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},  // Actually F32 in some models
		{Address: 62853, Name: "Battery Status", SemanticName: "battery_flags", Description: "Battery status", Unit: "", Category: "battery", DataType: modbus.U32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},

		// Energy Totals (SunSpec)
		{Address: 40093, Name: "Total PV Generation", SemanticName: "total_pv_gen", Description: "AC lifetime energy production", Unit: "Wh", Category: "pv", DataType: modbus.U32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 40095, Name: "Total PV Gen SF", Description: "Energy scale factor", Unit: "", Category: "pv", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Nominal
		{Address: 40069, Name: "Nominal Power", SemanticName: "nominal_power", Description: "Rated AC power", Unit: "W", Category: "pv", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40070, Name: "Nominal Power SF", Description: "Rated power scale factor", Unit: "", Category: "pv", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

	}, map[uint16]int64{
		40083: 0,
		62852: 55,
		62836: 0,
		40206: 0,
		40085: 5000, // 50.00 Hz
	})
}
