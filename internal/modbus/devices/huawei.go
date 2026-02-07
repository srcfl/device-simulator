package devices

import "github.com/srcfl/device-simulator/internal/modbus"

// HuaweiRegisters returns the RegisterSet for Huawei SUN2000 hybrid inverters.
// Addresses from firmware profile: huawei
// Huawei uses Holding Registers (FC 0x03) for most reads.
func HuaweiRegisters() *modbus.RegisterSet {
	return modbus.NewRegisterSet("huawei", []modbus.RegisterDef{
		// Identity
		{Address: 30015, Name: "Serial Number", SemanticName: "serial_number", Description: "Inverter serial number", Unit: "", Category: "pv", DataType: modbus.STR, Scale: 1.0, Words: 10, Endianness: modbus.Big, UseHolding: true},

		// PV Registers (Holding)
		{Address: 32064, Name: "PV Power", SemanticName: "pv_power", Description: "Input power", Unit: "W", Category: "pv", DataType: modbus.I32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 32016, Name: "PV1 Voltage", SemanticName: "pv1_voltage", Description: "PV string 1 voltage", Unit: "V", Category: "pv", DataType: modbus.I16, Scale: 0.1, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 32017, Name: "PV1 Current", SemanticName: "pv1_current", Description: "PV string 1 current", Unit: "A", Category: "pv", DataType: modbus.I16, Scale: 0.01, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 32018, Name: "PV2 Voltage", SemanticName: "pv2_voltage", Description: "PV string 2 voltage", Unit: "V", Category: "pv", DataType: modbus.I16, Scale: 0.1, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 32019, Name: "PV2 Current", SemanticName: "pv2_current", Description: "PV string 2 current", Unit: "A", Category: "pv", DataType: modbus.I16, Scale: 0.01, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Battery Registers
		{Address: 37001, Name: "Battery Power", SemanticName: "battery_power", Description: "Battery charge/discharge power (+=charge)", Unit: "W", Category: "battery", DataType: modbus.I32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 37004, Name: "Battery SoC", SemanticName: "battery_soc", Description: "Battery state of charge", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 37003, Name: "Battery Status", SemanticName: "battery_flags", Description: "Battery running status", Unit: "", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 37007, Name: "Total Charge", SemanticName: "total_charge", Description: "Total charge energy", Unit: "kWh", Category: "battery", DataType: modbus.U32, Scale: 0.01, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 37009, Name: "Total Discharge", SemanticName: "total_discharge", Description: "Total discharge energy", Unit: "kWh", Category: "battery", DataType: modbus.U32, Scale: 0.01, Words: 2, Endianness: modbus.Big, UseHolding: true},

		// Battery Control
		{Address: 47086, Name: "Charge/Discharge Cmd", SemanticName: "battery_cmd", Description: "Forced charge/discharge mode", Unit: "", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 47075, Name: "Battery Setpoint", SemanticName: "battery_setpoint", Description: "Active power setpoint", Unit: "W", Category: "battery", DataType: modbus.I32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 47081, Name: "Battery Max SoC", SemanticName: "battery_max_soc", Description: "Charge cutoff SoC", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 47082, Name: "Battery Min SoC", SemanticName: "battery_min_soc", Description: "Discharge cutoff SoC", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Meter / Grid
		{Address: 37113, Name: "Meter Power", SemanticName: "meter_power", Description: "Grid meter active power (+=export)", Unit: "W", Category: "meter", DataType: modbus.I32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 32085, Name: "Grid Frequency", SemanticName: "grid_frequency", Description: "Grid frequency", Unit: "Hz", Category: "grid", DataType: modbus.U16, Scale: 0.01, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Energy Totals
		{Address: 32106, Name: "Total PV Generation", SemanticName: "total_pv_gen", Description: "Cumulative PV energy yield", Unit: "kWh", Category: "pv", DataType: modbus.U32, Scale: 0.01, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 37119, Name: "Total Import", SemanticName: "total_import", Description: "Total grid import", Unit: "kWh", Category: "meter", DataType: modbus.U32, Scale: 0.01, Words: 2, Endianness: modbus.Big, UseHolding: true},
		{Address: 37121, Name: "Total Export", SemanticName: "total_export", Description: "Total grid export", Unit: "kWh", Category: "meter", DataType: modbus.U32, Scale: 0.01, Words: 2, Endianness: modbus.Big, UseHolding: true},

		// Load
		{Address: 32089, Name: "Load Power", SemanticName: "load_power", Description: "Active power consumed by loads", Unit: "W", Category: "control", DataType: modbus.I32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},

		// Nominal
		{Address: 30073, Name: "Nominal Power", SemanticName: "nominal_power", Description: "Rated power", Unit: "kW", Category: "pv", DataType: modbus.U32, Scale: 0.001, Words: 2, Endianness: modbus.Big, UseHolding: true},
	}, map[uint16]int64{
		32064: 0,
		37001: 0,
		37004: 65, // 65.0% (scale 0.1 → raw 650)
		37113: 0,
		32085: 50, // 50.00 Hz (scale 0.01 → raw 5000)
		32089: 700,
	})
}
