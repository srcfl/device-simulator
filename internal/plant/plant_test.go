package plant

import (
	"math"
	"testing"
	"time"
)

func TestPowerBalanceSelfConsumption(t *testing.T) {
	p := New(DefaultParams())
	p.State.SOC = 50
	start := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	_ = p.Step(Inputs{Time: start, LoadW: 1200, Weather: Weather{GHIWm2: 400}})
	out := p.Step(Inputs{
		Time:    start.Add(time.Second),
		LoadW:   1200,
		Weather: Weather{GHIWm2: 400},
		Control: Control{EMSMode: ModeSelfConsumption, MaxSOC: 100},
	})
	if math.Abs(out.ResidualW) > 1e-6 {
		t.Fatalf("power residual %v, want ~0", out.ResidualW)
	}
	if out.PVW >= 0 {
		t.Fatalf("PV should be negative generation, got %v", out.PVW)
	}
	if math.Abs(out.GridW) > 50 {
		t.Fatalf("self-consumption should nearly zero the grid, got %v (pv=%v batt=%v)", out.GridW, out.PVW, out.BatteryW)
	}
	if out.BatteryW <= 0 {
		t.Fatalf("excess PV should charge the battery, got %v", out.BatteryW)
	}
}

func TestForcedDischargeRespectsSignConvention(t *testing.T) {
	p := New(DefaultParams())
	p.State.SOC = 80
	start := time.Date(2026, 6, 21, 2, 0, 0, 0, time.UTC)
	_ = p.Step(Inputs{Time: start, LoadW: 800, Weather: Weather{}})
	out := p.Step(Inputs{
		Time:  start.Add(time.Second),
		LoadW: 800,
		Control: Control{
			EMSMode:          ModeEMS,
			BatteryCmd:       CmdDischarge,
			BatterySetpointW: 2000,
			MinSOC:           10,
			MaxSOC:           100,
		},
	})
	if out.BatteryW >= 0 {
		t.Fatalf("discharge must be negative battery power, got %v", out.BatteryW)
	}
	if math.Abs(out.BatteryW+2000) > 1 {
		t.Fatalf("battery power %v, want -2000", out.BatteryW)
	}
	// Grid + PV(0) + Batt(-2000) + Load(800) = 0 → grid = 1200 import
	if math.Abs(out.GridW-1200) > 1 {
		t.Fatalf("grid %v, want 1200 import", out.GridW)
	}
}

func TestSOCStopsAtMax(t *testing.T) {
	p := New(DefaultParams())
	p.State.SOC = 99.99
	start := time.Now()
	_ = p.Step(Inputs{Time: start})
	out := p.Step(Inputs{
		Time:    start.Add(time.Second),
		LoadW:   0,
		Weather: Weather{GHIWm2: 1000},
		Control: Control{EMSMode: ModeSelfConsumption, MaxSOC: 100},
	})
	if out.BatteryW != 0 {
		t.Fatalf("full battery must refuse charge, got %v", out.BatteryW)
	}
}

func TestSOCIntegratesWithEfficiency(t *testing.T) {
	pr := DefaultParams()
	pr.ChargeEfficiency = 0.9
	p := New(pr)
	p.State.SOC = 50
	start := time.Now()
	_ = p.Step(Inputs{Time: start, Control: Control{MaxSOC: 100}})
	hours := 1.0
	out := p.Step(Inputs{
		Time:  start.Add(time.Duration(hours * float64(time.Hour))),
		LoadW: 0,
		Control: Control{
			EMSMode:          ModeForced,
			BatteryCmd:       CmdCharge,
			BatterySetpointW: 1000,
			MaxSOC:           100,
		},
	})
	// 1000 W * 1 h * 0.9 / 10 kWh = 9 percentage points
	want := 59.0
	if math.Abs(out.SOC-want) > 0.2 {
		t.Fatalf("SOC %v, want ~%v", out.SOC, want)
	}
}

func TestExportLimitCurtailsPV(t *testing.T) {
	p := New(DefaultParams())
	p.State.SOC = 100
	start := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	_ = p.Step(Inputs{Time: start})
	out := p.Step(Inputs{
		Time:    start.Add(time.Second),
		LoadW:   0,
		Weather: Weather{GHIWm2: 1000},
		Control: Control{
			EMSMode:           ModeSelfConsumption,
			MaxSOC:            100,
			ExportLimitEnable: true,
			PVLimitW:          500,
		},
	})
	if out.GridW < -510 {
		t.Fatalf("export %v W exceeded 500 W cap", -out.GridW)
	}
}

func TestSemanticPVIsPositiveForDeviceRegisters(t *testing.T) {
	out := Outputs{PVW: -3000, GridW: -2000, BatteryW: -1000, LoadW: 0, SOC: 40}
	m := SemanticValues(out)
	if m["pv_power"] != 3000 {
		t.Fatalf("pv_power %v, want 3000 (device magnitude)", m["pv_power"])
	}
	if m["meter_power"] != -2000 {
		t.Fatalf("meter_power %v, want -2000 (export)", m["meter_power"])
	}
	if m["battery_flags"] != 4 {
		t.Fatalf("battery_flags %v, want 4 discharging", m["battery_flags"])
	}
}

func TestGHIZeroAtNight(t *testing.T) {
	night := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	if g := GHIClearSky(night, 1000, 6, 20); g != 0 {
		t.Fatalf("night GHI %v", g)
	}
	noon := time.Date(2026, 6, 21, 13, 0, 0, 0, time.UTC)
	if g := GHIClearSky(noon, 1000, 6, 20); g < 500 {
		t.Fatalf("noon GHI too low: %v", g)
	}
}
