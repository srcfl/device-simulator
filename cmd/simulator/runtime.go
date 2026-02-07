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

	// Use semantic lookup for serial number
	if def := getSemanticDef(sim, "serial_number"); def != nil {
		if def.DataType == modbus.STR {
			// String serial: read N registers and decode as ASCII
			regs := sim.Server.GetHoldingRegisters(def.Address, def.Words)
			if len(regs) == 0 {
				return sim.Serial
			}
			s := modbus.DecodeString(regs)
			if s == "" {
				return sim.Serial
			}
			return s
		}
		// Numeric serial (e.g. SDM630 U32)
		if val, err := sim.Server.ReadRegisterValue(def); err == nil {
			return strconv.FormatUint(uint64(math.Round(val)), 10)
		}
		return sim.Serial
	}

	// Fallback: try Sungrow-style address 4989 (STR, 10 regs)
	regs := sim.Server.GetHoldingRegisters(4989, 10)
	if len(regs) == 0 {
		return sim.Serial
	}
	s := modbus.DecodeString(regs)
	if s == "" {
		return sim.Serial
	}
	return s
}
