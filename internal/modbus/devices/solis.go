package devices

import "github.com/srcfl/device-simulator/internal/modbus"

// SolisRegisters returns the RegisterSet for Solis Hybrid inverters.
// Addresses from firmware profile: solis
// Solis uses Input Registers (FC 0x04) for reads, Holding (FC 0x03) for control.
func SolisRegisters() *modbus.RegisterSet {
	return modbus.NewRegisterSet("solis", []modbus.RegisterDef{
		// Identity
		{Address: 33004, Name: "Serial Number", SemanticName: "serial_number", Description: "Inverter serial number", Unit: "", Category: "pv", DataType: modbus.STR, Scale: 1.0, Words: 16, Endianness: modbus.Big},

		// PV Registers (Input)
		{Address: 33029, Name: "Total PV Generation", SemanticName: "total_pv_gen", Description: "Total PV generation", Unit: "kWh", Category: "pv", DataType: modbus.U32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 33049, Name: "DC Voltage 1", SemanticName: "pv1_voltage", Description: "PV string 1 voltage", Unit: "V", Category: "pv", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33050, Name: "DC Current 1", SemanticName: "pv1_current", Description: "PV string 1 current", Unit: "A", Category: "pv", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33051, Name: "DC Voltage 2", SemanticName: "pv2_voltage", Description: "PV string 2 voltage", Unit: "V", Category: "pv", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33052, Name: "DC Current 2", SemanticName: "pv2_current", Description: "PV string 2 current", Unit: "A", Category: "pv", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33057, Name: "PV Power", SemanticName: "pv_power", Description: "Total DC input power", Unit: "W", Category: "pv", DataType: modbus.U32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 33093, Name: "Inverter Temperature", SemanticName: "inverter_temperature", Description: "Inverter temperature", Unit: "C", Category: "pv", DataType: modbus.I16, Scale: 0.1, Words: 1, Endianness: modbus.Big},

		// Battery Registers
		{Address: 33096, Name: "Battery Temperature", SemanticName: "battery_temperature", Description: "Battery temperature", Unit: "C", Category: "battery", DataType: modbus.I16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33133, Name: "Battery Voltage", SemanticName: "battery_voltage", Description: "Battery voltage", Unit: "V", Category: "battery", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33134, Name: "Battery Current", SemanticName: "battery_current", Description: "Battery current", Unit: "A", Category: "battery", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33135, Name: "Battery Status", SemanticName: "battery_status", Description: "Battery charge/discharge status", Unit: "", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big},
		{Address: 33139, Name: "Battery SoC", SemanticName: "battery_soc", Description: "Battery state of charge", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big},
		{Address: 33149, Name: "Battery Power", SemanticName: "battery_power", Description: "Battery charge/discharge power", Unit: "W", Category: "battery", DataType: modbus.I32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 33161, Name: "Total Charge", SemanticName: "total_charge", Description: "Total charge energy", Unit: "kWh", Category: "battery", DataType: modbus.U32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 33165, Name: "Total Discharge", SemanticName: "total_discharge", Description: "Total discharge energy", Unit: "kWh", Category: "battery", DataType: modbus.U32, Scale: 1.0, Words: 2, Endianness: modbus.Big},

		// Battery Control (Holding)
		{Address: 43010, Name: "Battery Min SoC", SemanticName: "battery_min_soc", Description: "Min SoC limit", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 43011, Name: "Battery Max SoC", SemanticName: "battery_max_soc", Description: "Max SoC limit", Unit: "%", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 43110, Name: "Charge/Discharge Cmd", SemanticName: "battery_cmd", Description: "Battery charge/discharge command", Unit: "", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 43117, Name: "Battery Setpoint", SemanticName: "battery_setpoint", Description: "Battery power setpoint", Unit: "W", Category: "battery", DataType: modbus.U16, Scale: 1.0, Words: 1, Endianness: modbus.Big, UseHolding: true},

		// Meter / Grid
		{Address: 33130, Name: "Meter Power", SemanticName: "meter_power", Description: "Grid meter total active power", Unit: "W", Category: "meter", DataType: modbus.I32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 33251, Name: "Grid Voltage L1", SemanticName: "meter_l1_voltage", Description: "Grid voltage phase 1", Unit: "V", Category: "meter", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33252, Name: "Grid Current L1", SemanticName: "meter_l1_current", Description: "Grid current phase 1", Unit: "A", Category: "meter", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33253, Name: "Grid Voltage L2", SemanticName: "meter_l2_voltage", Description: "Grid voltage phase 2", Unit: "V", Category: "meter", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33254, Name: "Grid Current L2", SemanticName: "meter_l2_current", Description: "Grid current phase 2", Unit: "A", Category: "meter", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33255, Name: "Grid Voltage L3", SemanticName: "meter_l3_voltage", Description: "Grid voltage phase 3", Unit: "V", Category: "meter", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33256, Name: "Grid Current L3", SemanticName: "meter_l3_current", Description: "Grid current phase 3", Unit: "A", Category: "meter", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 33257, Name: "Grid Power L1", SemanticName: "meter_l1_power", Description: "Grid active power phase 1", Unit: "W", Category: "meter", DataType: modbus.I32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 33259, Name: "Grid Power L2", SemanticName: "meter_l2_power", Description: "Grid active power phase 2", Unit: "W", Category: "meter", DataType: modbus.I32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 33261, Name: "Grid Power L3", SemanticName: "meter_l3_power", Description: "Grid active power phase 3", Unit: "W", Category: "meter", DataType: modbus.I32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 33282, Name: "Grid Frequency", SemanticName: "grid_frequency", Description: "Grid frequency", Unit: "Hz", Category: "meter", DataType: modbus.U16, Scale: 0.01, Words: 1, Endianness: modbus.Big},
		{Address: 33283, Name: "Total Import", SemanticName: "total_import", Description: "Total grid import energy", Unit: "kWh", Category: "meter", DataType: modbus.U32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 33285, Name: "Total Export", SemanticName: "total_export", Description: "Total grid export energy", Unit: "kWh", Category: "meter", DataType: modbus.U32, Scale: 1.0, Words: 2, Endianness: modbus.Big},

		// Load
		{Address: 33147, Name: "Load Power", SemanticName: "load_power", Description: "House load power", Unit: "W", Category: "control", DataType: modbus.I32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
	}, map[uint16]int64{
		33057: 0,
		33149: 0,
		33139: 60,
		33130: 0,
		33282: 50,   // 50.00 Hz (scale 0.01 → raw 5000)
		33147: 400,
		33093: 250,  // 25.0°C (scale 0.1)
		33096: 250,  // 25.0°C (scale 0.1)
		33251: 2300, // 230.0V (scale 0.1)
		33253: 2300, // 230.0V (scale 0.1)
		33255: 2300, // 230.0V (scale 0.1)
	})
}
