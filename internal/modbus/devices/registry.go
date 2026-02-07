package devices

import "github.com/srcfl/device-simulator/internal/modbus"

// GetRegisterSet returns the RegisterSet for a given profile/device name.
// Returns nil if no register set is defined for the name.
func GetRegisterSet(name string) *modbus.RegisterSet {
	switch name {
	case "sungrow", "sungrow-hybrid", "sungrow-inverter":
		return SungrowRegisters()
	case "sdm630":
		return SDM630Registers()
	case "solis", "solis-hybrid":
		return SolisRegisters()
	case "huawei", "huawei-sun2000":
		return HuaweiRegisters()
	case "fronius", "fronius-gen24":
		return FroniusRegisters()
	case "fronius-smart-meter":
		return FroniusMeterRegisters()
	case "sma", "sma-tripower":
		return SMARegisters()
	case "deye", "deye-hybrid":
		return DeyeRegisters()
	case "solaredge":
		return SolarEdgeRegisters()
	case "pixii", "pixii-powershaper":
		return PixiiRegisters()
	default:
		return nil
	}
}

// AllDeviceNames returns all registered device names (one per device, canonical name).
func AllDeviceNames() []string {
	return []string{
		"sungrow",
		"sdm630",
		"solis",
		"huawei",
		"fronius",
		"fronius-smart-meter",
		"sma",
		"deye",
		"solaredge",
		"pixii",
	}
}
