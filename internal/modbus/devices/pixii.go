package devices

import "github.com/srcfl/device-simulator/internal/modbus"

// PixiiRegisters returns the RegisterSet for Pixii PowerShaper 2 (battery-only).
// Addresses from firmware profile: pixii
// Pixii uses SunSpec models over Holding Registers (FC 0x03).
// Note: Pixii is battery-only, no PV. Uses SunSpec Storage + Meter models.
func PixiiRegisters() *modbus.RegisterSet {
	return modbus.NewRegisterSet("pixii", []modbus.RegisterDef{
		// Identity (SunSpec Common Model)
		{Address: 40052, Name: "Serial Number", SemanticName: "serial_number", Description: "Device serial number", Unit: "", Category: "battery", DataType: modbus.STR, Scale: 1.0, Words: 16, Endianness: modbus.Big, UseHolding: true},

		// Battery (SunSpec Storage Model)
		{Address: 40168, Name: "Battery Power", SemanticName: "battery_power", Description: "Battery charge/discharge power", Unit: "W", Category: "battery", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40169, Name: "Battery Power SF", Description: "Battery power scale factor", Unit: "", Category: "battery", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40132, Name: "Battery SoC", SemanticName: "battery_soc", Description: "State of charge", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40133, Name: "Battery SoC SF", Description: "SoC scale factor", Unit: "", Category: "battery", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40134, Name: "Battery Status", SemanticName: "battery_flags", Description: "Battery operating status", Unit: "", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Battery Control
		{Address: 40151, Name: "Storage Control", SemanticName: "battery_cmd", Description: "Storage control mode", Unit: "", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40155, Name: "Charge/Discharge Setpoint", SemanticName: "battery_setpoint", Description: "Active power setpoint", Unit: "W", Category: "battery", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40156, Name: "Setpoint SF", Description: "Setpoint scale factor", Unit: "", Category: "battery", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40149, Name: "Battery Max SoC", SemanticName: "battery_max_soc", Description: "Max SoC threshold", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40150, Name: "Battery Min SoC", SemanticName: "battery_min_soc", Description: "Min SoC threshold", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Meter (SunSpec Meter Model)
		{Address: 40188, Name: "Meter Power", SemanticName: "meter_power", Description: "Total active power", Unit: "W", Category: "meter", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40189, Name: "Meter Power SF", Description: "Power scale factor", Unit: "", Category: "meter", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40187, Name: "Grid Frequency", SemanticName: "grid_frequency", Description: "Grid frequency", Unit: "Hz", Category: "grid", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 40190, Name: "Frequency SF", Description: "Frequency scale factor", Unit: "", Category: "grid", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
	}, map[uint16]int64{
		40168: 0,
		40132: 50,
		40188: 0,
		40187: 5000, // 50.00 Hz
	})
}
