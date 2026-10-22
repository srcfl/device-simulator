package main

import (
	"math"
	"time"

	"github.com/srcfl/device-simulator/internal/profiles"
)

// EMS control for a Sungrow hybrid's battery.
//
// A real SH applies what an EMS writes to its control block: 13049 is the
// mode, 13050 the forced command (170 charge, 187 discharge, 204 stop) and
// 13051 the forced power in watts. In any mode other than forced it runs
// self-consumption, steering the battery against its own meter. Without
// ems_control the simulator stores those writes and leaves the battery
// where the dashboard or a register write put it. How long the battery takes
// to follow is the model's command_response_s (2 s for sungrow-hybrid).

const (
	emsModeForced   = 2
	emsCmdCharge    = 170
	emsCmdDischarge = 187

	flagCharging    = 0x0002
	flagDischarging = 0x0004
)

// emsBattery is what the battery is doing in answer to its EMS registers.
type emsBattery struct {
	pending  []emsTarget
	appliedW float64 // + charging, - discharging
}

type emsTarget struct {
	at time.Time
	w  float64
}

// emsTargetW is the battery power the inverter heads for. siteW is the
// site meter's latest net power (+ import), which includes appliedW.
func emsTargetW(sim *Simulator, siteW, appliedW float64) float64 {
	var w float64
	if int(getSemanticValue(sim, "grid_mode")) == emsModeForced {
		sp := math.Abs(getSemanticValue(sim, "battery_setpoint"))
		switch int(getSemanticValue(sim, "battery_cmd")) {
		case emsCmdCharge:
			w = sp
		case emsCmdDischarge:
			w = -sp
		}
	} else {
		w = appliedW - siteW
	}
	if limit := getSemanticValue(sim, "max_charge_power") * 1000; limit > 0 && w > limit {
		w = limit
	}
	if limit := getSemanticValue(sim, "max_discharge_power") * 1000; limit > 0 && w < -limit {
		w = -limit
	}
	soc := getSemanticValue(sim, "battery_soc")
	if maxSoC := getSemanticValue(sim, "battery_max_soc"); maxSoC > 0 && soc >= maxSoC && w > 0 {
		w = 0
	}
	if soc <= getSemanticValue(sim, "battery_min_soc") && w < 0 {
		w = 0
	}
	return w
}

// stepEMSBattery moves an EMS-controlled battery to its target once the
// response time has passed, and writes the power and the power-flow flags
// the Lua driver signs it by.
func stepEMSBattery(sim *Simulator, now time.Time, siteW float64) {
	sim.mu.Lock()
	defer sim.mu.Unlock()
	if !sim.EMSControl || !sim.Running || !sim.DeviceOn || sim.Server == nil || sim.Category != "inverter" {
		return
	}
	b := &sim.ems
	b.pending = append(b.pending, emsTarget{at: now.Add(sim.EMSResponse), w: emsTargetW(sim, siteW, b.appliedW)})
	for len(b.pending) > 0 && !b.pending[0].at.After(now) {
		b.appliedW = b.pending[0].w
		b.pending = b.pending[1:]
	}

	setSemanticValue(sim, "battery_power", math.Abs(b.appliedW))
	var flags float64
	switch {
	case b.appliedW > 0:
		flags = flagCharging
	case b.appliedW < 0:
		flags = flagDischarging
	}
	setSemanticValue(sim, "battery_flags", flags)
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// commandResponse is the model's command_response_s.
func commandResponse(p *profiles.Profile) time.Duration {
	return seconds(p.Params().CommandResponseS)
}
