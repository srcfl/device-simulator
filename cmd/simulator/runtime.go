package main

import (
	"math"
	"strconv"

	"github.com/srcfl/device-simulator/internal/modbus"
)

func getRuntimeSerial(sim *Simulator) string {
	if sim == nil {
		return ""
	}

	switch sim.Protocol {
	case "modbus":
		return getModbusRuntimeSerial(sim)
	case "mqtt":
		if sim.MQTTServer != nil {
			if sn, ok := sim.MQTTServer.GetState()["serialNumber"].(string); ok && sn != "" {
				return sn
			}
		}
	case "ocpp":
		if sim.OCPPServer != nil {
			if sn, ok := sim.OCPPServer.GetState()["serialNumber"].(string); ok && sn != "" {
				return sn
			}
		}
	}

	return sim.Serial
}

func getModbusRuntimeSerial(sim *Simulator) string {
	if sim.Server == nil {
		return sim.Serial
	}

	if sim.Category == "energy_meter" {
		if def := modbus.RegisterLookup[64512]; def != nil {
			if val, err := sim.Server.ReadRegisterValue(def); err == nil {
				return strconv.FormatUint(uint64(math.Round(val)), 10)
			}
		}
		return sim.Serial
	}

	regs := sim.Server.GetHoldingRegisters(4990, 10)
	if len(regs) == 0 {
		return sim.Serial
	}

	bytes := make([]byte, 0, len(regs)*2)
	for _, reg := range regs {
		bytes = append(bytes, byte(reg>>8), byte(reg&0xFF))
	}

	for i, b := range bytes {
		if b == 0 {
			bytes = bytes[:i]
			break
		}
	}

	if len(bytes) == 0 {
		return sim.Serial
	}

	return string(bytes)
}
