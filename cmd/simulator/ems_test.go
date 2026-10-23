package main

import (
	"math"
	"testing"
	"time"

	"github.com/srcfl/device-simulator/internal/modbus/devices"
)

// sungrowInSite is a running Sungrow hybrid in a site with houseW per
// phase, its battery at soc percent with a 5 % floor.
func sungrowInSite(t *testing.T, houseW, soc float64, ems bool) (*Simulator, *Site, *Simulator) {
	t.Helper()
	inv, err := createSimulator("inverter")
	if err != nil {
		t.Fatal(err)
	}
	rs := devices.GetRegisterSet("sungrow")
	inv.RegisterSet = rs
	inv.Server.InitDefaultsFromSet(rs)
	inv.Running = true
	inv.EMSControl = ems
	inv.EMSResponse = 0
	setSemanticValue(inv, "battery_soc", soc)
	setSemanticValue(inv, "battery_min_soc", 5)
	site, meter := testSite(t, houseW, inv)
	return inv, site, meter
}

func writeEMS(sim *Simulator, mode, cmd, watts float64) {
	setSemanticValue(sim, "grid_mode", mode)
	setSemanticValue(sim, "battery_cmd", cmd)
	setSemanticValue(sim, "battery_setpoint", watts)
}

// tick runs the site meter n times and returns its last net power.
func tick(site *Site, meter *Simulator, n int) float64 {
	for i := 0; i < n; i++ {
		calculateAndUpdateMeter(site, 0)
	}
	return getSemanticValue(meter, "meter_power")
}

func TestEMSForcedModeDrivesTheBattery(t *testing.T) {
	inv, site, meter := sungrowInSite(t, 1000, 50, true)

	writeEMS(inv, 2, emsCmdCharge, 3000)
	if got := tick(site, meter, 2); math.Abs(got-6000) > 1 {
		t.Fatalf("charging 3 kW: meter %.0f W, want house 3000 + 3000", got)
	}
	if got := getBatterySignedPower(inv); got != 3000 {
		t.Fatalf("battery %.0f W, want +3000", got)
	}

	writeEMS(inv, 2, emsCmdDischarge, 2000)
	if got := tick(site, meter, 2); math.Abs(got-1000) > 1 {
		t.Fatalf("discharging 2 kW: meter %.0f W, want 3000 - 2000", got)
	}
	if got := getSemanticValue(inv, "battery_flags"); got != flagDischarging {
		t.Fatalf("power-flow flags %v, want discharging (4): the driver signs by them", got)
	}

	writeEMS(inv, 2, 204, 2000)
	if got := tick(site, meter, 2); math.Abs(got-3000) > 1 {
		t.Fatalf("stop: meter %.0f W, want the house alone", got)
	}

	writeEMS(inv, 2, emsCmdCharge, 9000)
	if got := (tick(site, meter, 2) - 3000); math.Abs(got-5000) > 1 {
		t.Fatalf("9 kW asked: battery %.0f W, want the 5 kW limit (33046)", got)
	}
}

// A limit register holding 0 allows no power that way.
func TestEMSKeepsZeroPowerLimits(t *testing.T) {
	inv, site, meter := sungrowInSite(t, 1000, 50, true)
	setSemanticValue(inv, "max_charge_power", 0)
	writeEMS(inv, 2, emsCmdCharge, 3000)
	if got := tick(site, meter, 2) - 3000; math.Abs(got) > 1 {
		t.Fatalf("charge limit 0: battery %.0f W, want 0", got)
	}
	setSemanticValue(inv, "max_discharge_power", 0)
	writeEMS(inv, 0, 204, 0)
	if got := tick(site, meter, 3); math.Abs(got-3000) > 1 {
		t.Fatalf("discharge limit 0: meter %.0f W, want the house alone", got)
	}
}

// Self-consumption (any mode but forced) cancels the meter, as a real SH
// does without an EMS.
func TestEMSSelfConsumptionCancelsTheMeter(t *testing.T) {
	inv, site, meter := sungrowInSite(t, 1000, 50, true)
	writeEMS(inv, 0, 204, 0)
	if got := tick(site, meter, 3); math.Abs(got) > 1 {
		t.Fatalf("meter %.0f W, want 0 with the battery covering the house", got)
	}
	if got := getBatterySignedPower(inv); math.Abs(got+3000) > 1 {
		t.Fatalf("battery %.0f W, want -3000", got)
	}
}

func TestEMSKeepsTheSoCLimits(t *testing.T) {
	inv, site, meter := sungrowInSite(t, 1000, 5, true)
	writeEMS(inv, 2, emsCmdDischarge, 3000)
	if got := tick(site, meter, 2); math.Abs(got-3000) > 1 {
		t.Fatalf("at the 5 %% floor: meter %.0f W, want no discharge", got)
	}
	setSemanticValue(inv, "battery_soc", 100)
	writeEMS(inv, 2, emsCmdCharge, 3000)
	if got := tick(site, meter, 2); math.Abs(got-3000) > 1 {
		t.Fatalf("full: meter %.0f W, want no charge", got)
	}
}

// The battery answers after its response time, not on the write.
func TestEMSResponseTime(t *testing.T) {
	inv, site, meter := sungrowInSite(t, 1000, 50, true)
	inv.EMSResponse = 300 * time.Millisecond
	writeEMS(inv, 2, emsCmdCharge, 3000)
	if got := tick(site, meter, 2); math.Abs(got-3000) > 1 {
		t.Fatalf("right after the write: meter %.0f W, want the house alone", got)
	}
	time.Sleep(350 * time.Millisecond)
	if got := tick(site, meter, 2); math.Abs(got-6000) > 1 {
		t.Fatalf("after 350 ms: meter %.0f W, want house + 3000", got)
	}
}

// Without ems_control the registers stay as written: the dashboard sets the
// battery itself.
func TestEMSWritesIgnoredWithoutEMSControl(t *testing.T) {
	inv, site, meter := sungrowInSite(t, 1000, 50, false)
	writeEMS(inv, 2, emsCmdCharge, 3000)
	if got := tick(site, meter, 2); math.Abs(got-3000) > 1 {
		t.Fatalf("meter %.0f W, want the house alone", got)
	}
}
