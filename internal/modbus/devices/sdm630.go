package devices

import "github.com/srcfl/device-simulator/internal/modbus"

// SDM630Registers returns the RegisterSet for Eastron SDM630 energy meters.
// All registers use F32 (IEEE 754 float), Big Endian, Input Registers (FC 0x04).
func SDM630Registers() *modbus.RegisterSet {
	return modbus.NewRegisterSet("sdm630", []modbus.RegisterDef{
		{Address: 0, Name: "L1 Voltage", SemanticName: "meter_l1_voltage", Description: "Phase 1 Voltage", Unit: "V", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 2, Name: "L2 Voltage", SemanticName: "meter_l2_voltage", Description: "Phase 2 Voltage", Unit: "V", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 4, Name: "L3 Voltage", SemanticName: "meter_l3_voltage", Description: "Phase 3 Voltage", Unit: "V", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 6, Name: "L1 Current", SemanticName: "meter_l1_current", Description: "Phase 1 Current", Unit: "A", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 8, Name: "L2 Current", SemanticName: "meter_l2_current", Description: "Phase 2 Current", Unit: "A", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 10, Name: "L3 Current", SemanticName: "meter_l3_current", Description: "Phase 3 Current", Unit: "A", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 12, Name: "L1 Power", SemanticName: "meter_l1_power", Description: "Phase 1 Power", Unit: "W", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 14, Name: "L2 Power", SemanticName: "meter_l2_power", Description: "Phase 2 Power", Unit: "W", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 16, Name: "L3 Power", SemanticName: "meter_l3_power", Description: "Phase 3 Power", Unit: "W", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 52, Name: "Total Power", SemanticName: "meter_power", Description: "Total System Power", Unit: "W", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 70, Name: "Frequency", SemanticName: "grid_frequency", Description: "Grid Frequency", Unit: "Hz", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 72, Name: "Import Energy", SemanticName: "total_import", Description: "Total Import Energy", Unit: "kWh", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 74, Name: "Export Energy", SemanticName: "total_export", Description: "Total Export Energy", Unit: "kWh", Category: "meter", DataType: modbus.F32, Scale: 1.0, Words: 2, Endianness: modbus.Big},
		{Address: 64512, Name: "Serial Number", SemanticName: "serial_number", Description: "Serial number (0xFC00)", Unit: "", Category: "meter", DataType: modbus.U32, Scale: 1.0, Words: 2, Endianness: modbus.Big, UseHolding: true},
	}, map[uint16]int64{
		// Defaults are set via profile float defaults (F32 encoding handled separately)
	})
}
