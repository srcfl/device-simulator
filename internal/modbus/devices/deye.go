package devices

import "github.com/srcfl/device-simulator/internal/modbus"

// DeyeRegisters returns the RegisterSet for Deye hybrid inverters.
// Addresses from firmware profile: deye
// Deye uses Holding Registers (FC 0x03).
// Notable: Deye uses low register addresses (0-700 range).
func DeyeRegisters() *modbus.RegisterSet {
	return modbus.NewRegisterSet("deye", []modbus.RegisterDef{
		// Identity
		{Address: 3, Name: "Serial Number", SemanticName: "serial_number", Description: "Device serial number", Unit: "", Category: "pv", DataType: modbus.STR, Scale: 1.0, Words: 5, Endianness: modbus.Big, UseHolding: true},

		// PV Registers
		{Address: 672, Name: "PV Power", SemanticName: "pv_power", Description: "Total PV power", Unit: "W", Category: "pv", DataType: modbus.U32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 109, Name: "PV1 Voltage", SemanticName: "pv1_voltage", Description: "PV1 voltage", Unit: "V", Category: "pv", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 110, Name: "PV1 Current", SemanticName: "pv1_current", Description: "PV1 current", Unit: "A", Category: "pv", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 111, Name: "PV2 Voltage", SemanticName: "pv2_voltage", Description: "PV2 voltage", Unit: "V", Category: "pv", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 112, Name: "PV2 Current", SemanticName: "pv2_current", Description: "PV2 current", Unit: "A", Category: "pv", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Battery Registers
		{Address: 590, Name: "Battery Power", SemanticName: "battery_power", Description: "Battery power (+=charge, -=discharge)", Unit: "W", Category: "battery", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 588, Name: "Battery SoC", SemanticName: "battery_soc", Description: "Battery state of charge", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 587, Name: "Battery Voltage", SemanticName: "battery_voltage", Description: "Battery voltage", Unit: "V", Category: "battery", DataType: modbus.U16, Scale: 0.01, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Battery Control
		{Address: 98, Name: "Work Mode", SemanticName: "battery_cmd", Description: "Work mode (0=selling first, 1=zero export, 2=battery first)", Unit: "", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 108, Name: "Battery Max SoC", SemanticName: "battery_max_soc", Description: "Max SoC for charging", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 105, Name: "Battery Min SoC", SemanticName: "battery_min_soc", Description: "Min SoC for discharging", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Meter / Grid
		{Address: 619, Name: "Meter Power", SemanticName: "meter_power", Description: "Grid power (+=export, -=import)", Unit: "W", Category: "meter", DataType: modbus.I16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 79, Name: "Grid Frequency", SemanticName: "grid_frequency", Description: "Grid frequency", Unit: "Hz", Category: "grid", DataType: modbus.U16, Scale: 0.01, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Energy Totals
		{Address: 534, Name: "Total PV Generation", SemanticName: "total_pv_gen", Description: "Total PV energy", Unit: "kWh", Category: "pv", DataType: modbus.U32, Scale: 0.1, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 520, Name: "Total Charge", SemanticName: "total_charge", Description: "Total battery charge energy", Unit: "kWh", Category: "battery", DataType: modbus.U32, Scale: 0.1, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 522, Name: "Total Discharge", SemanticName: "total_discharge", Description: "Total battery discharge energy", Unit: "kWh", Category: "battery", DataType: modbus.U32, Scale: 0.1, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 524, Name: "Total Import", SemanticName: "total_import", Description: "Total grid import", Unit: "kWh", Category: "meter", DataType: modbus.U32, Scale: 0.1, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 526, Name: "Total Export", SemanticName: "total_export", Description: "Total grid export", Unit: "kWh", Category: "meter", DataType: modbus.U32, Scale: 0.1, Words: 2, Endianness: modbus.Big, UseHolding: true},

		// Load
		{Address: 653, Name: "Load Power", SemanticName: "load_power", Description: "Total load power", Unit: "W", Category: "control", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
	}, map[uint16]int64{
		672: 0,
		590: 0,
		588: 80,
		619: 0,
		79:  50, // 50.00 Hz (scale 0.01 → raw 5000)
		653: 450,
	})
}
