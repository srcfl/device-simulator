package devices

import "github.com/srcfl/device-simulator/internal/modbus"

// HeatPumpRegisters is a compact Modbus map for a generic heat pump.
// Holding registers are the EMS/SG-Ready command surface. Input registers
// are what the plant writes after each site tick.
func HeatPumpRegisters() *modbus.RegisterSet {
	return modbus.NewRegisterSet("heat-pump", []modbus.RegisterDef{
		{Address: 1000, Name: "Indoor Temperature", SemanticName: "indoor_temp", Description: "Building indoor air", Unit: "°C", Category: "heat", DataType: modbus.I16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 1001, Name: "Outdoor Temperature", SemanticName: "outdoor_temp", Description: "Ambient", Unit: "°C", Category: "heat", DataType: modbus.I16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 1002, Name: "DHW Temperature", SemanticName: "dhw_temp", Description: "Hot water tank", Unit: "°C", Category: "heat", DataType: modbus.I16, Scale: 0.1, Words: 1, Endianness: modbus.Big},
		{Address: 1003, Name: "Electrical Power", SemanticName: "hp_power", Description: "Compressor + backup electrical", Unit: "W", Category: "heat", DataType: modbus.U16, Scale: 1, Words: 1, Endianness: modbus.Big},
		{Address: 1004, Name: "Thermal Power", SemanticName: "hp_thermal", Description: "Heat delivered", Unit: "W", Category: "heat", DataType: modbus.U16, Scale: 1, Words: 1, Endianness: modbus.Big},
		{Address: 1005, Name: "COP", SemanticName: "hp_cop", Description: "Instantaneous COP", Unit: "", Category: "heat", DataType: modbus.U16, Scale: 0.01, Words: 1, Endianness: modbus.Big},
		{Address: 1006, Name: "Status", SemanticName: "hp_status", Description: "0 off, 1 heat, 2 dhw", Unit: "", Category: "heat", DataType: modbus.U16, Scale: 1, Words: 1, Endianness: modbus.Big},
		{Address: 1100, Name: "Enable", SemanticName: "hp_enable", Description: "1 = allowed to run", Unit: "", Category: "heat", DataType: modbus.U16, Scale: 1, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 1101, Name: "Room Setpoint", SemanticName: "hp_setpoint", Description: "0 = heating curve", Unit: "°C", Category: "heat", DataType: modbus.U16, Scale: 0.1, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 1102, Name: "Mode", SemanticName: "hp_mode", Description: "0 auto, 1 heat, 2 off, 3 dhw", Unit: "", Category: "heat", DataType: modbus.U16, Scale: 1, Words: 1, Endianness: modbus.Big, UseHolding: true},
		{Address: 1103, Name: "SG Ready", SemanticName: "hp_sg_ready", Description: "0 normal, 1 block, 2 rec, 3 boost", Unit: "", Category: "heat", DataType: modbus.U16, Scale: 1, Words: 1, Endianness: modbus.Big, UseHolding: true},
	}, map[uint16]int64{
		1000: 19,
		1001: 6,
		1002: 45,
		1005: 3,
		1100: 1,
		1101: 0,
		1102: 0,
		1103: 0,
	})
}
