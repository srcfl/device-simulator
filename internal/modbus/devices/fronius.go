package devices

import "github.com/srcfl/device-simulator/internal/modbus"

// FroniusRegisters returns the RegisterSet for Fronius GEN24 Plus inverters.
// Addresses from firmware profile: fronius (SunSpec-based)
// Fronius uses Holding Registers (FC 0x03) with SunSpec model layout.
// Note: Fronius firmware uses base address 40069 for SunSpec inverter model.
func FroniusRegisters() *modbus.RegisterSet {
	return modbus.NewRegisterSet("fronius", []modbus.RegisterDef{
		// Identity (SunSpec Common Model at base ~40000)
		{Address: 40052, Name: "Serial Number", SemanticName: "serial_number", Description: "Inverter serial number", Unit: "", Category: "pv", DataType: modbus.STR, Scale: 1.0, Words: 16, Endianness: modbus.Big, UseHolding: true},

		// PV / AC Output (SunSpec Inverter Model)
		{Address: 40107, Name: "PV Power", SemanticName: "pv_power", Description: "AC power output", Unit: "W", Category: "pv", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40108, Name: "PV Power SF", Description: "AC power scale factor", Unit: "", Category: "pv", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40091, Name: "AC Current", Description: "AC total current", Unit: "A", Category: "pv", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40092, Name: "AC Current A", Description: "AC phase A current", Unit: "A", Category: "pv", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40093, Name: "AC Current B", Description: "AC phase B current", Unit: "A", Category: "pv", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40094, Name: "AC Current C", Description: "AC phase C current", Unit: "A", Category: "pv", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40095, Name: "AC Current SF", Description: "AC current scale factor", Unit: "", Category: "pv", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Grid
		{Address: 40109, Name: "Grid Frequency", SemanticName: "grid_frequency", Description: "AC frequency", Unit: "Hz", Category: "grid", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40110, Name: "Grid Frequency SF", Description: "Frequency scale factor", Unit: "", Category: "grid", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Battery (SunSpec Storage Model, Fronius offset ~40321)
		{Address: 40321, Name: "Battery SoC", SemanticName: "battery_soc", Description: "Battery state of charge", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40322, Name: "Battery Status", SemanticName: "battery_flags", Description: "Battery status flags", Unit: "", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40323, Name: "Battery Power", SemanticName: "battery_power", Description: "Battery charge/discharge power", Unit: "W", Category: "battery", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Meter (SunSpec Meter Model at ~40091 area, Fronius uses separate meter model)
		{Address: 40100, Name: "Meter Power", SemanticName: "meter_power", Description: "Total active power (+=export)", Unit: "W", Category: "meter", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40101, Name: "Meter Power SF", Description: "Active power scale factor", Unit: "", Category: "meter", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Energy Totals
		{Address: 40113, Name: "Total PV Generation", SemanticName: "total_pv_gen", Description: "AC lifetime energy production", Unit: "Wh", Category: "pv", DataType: modbus.U32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 40115, Name: "Total PV Gen SF", Description: "Energy scale factor", Unit: "", Category: "pv", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Nominal
		{Address: 40089, Name: "Nominal Power", SemanticName: "nominal_power", Description: "Rated AC power", Unit: "W", Category: "pv", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40090, Name: "Nominal Power SF", Description: "Rated power scale factor", Unit: "", Category: "pv", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
	}, map[uint16]int64{
		40107: 0,
		40321: 45,
		40323: 0,
		40100: 0,
		40109: 5000, // 50.00 Hz (with SF=-2)
	})
}
