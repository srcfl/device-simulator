package plant

import (
	"math"
	"testing"
	"time"
)

func TestASHPHeatsColdHouseAndDrawsElectricity(t *testing.T) {
	site := NewNativeSite()
	f, ok := Preset("house-ashp")
	if !ok {
		t.Fatal("missing house-ashp")
	}
	site.SetFacility(f)
	site.indoorC = 16
	unit := New(DefaultParams())
	site.Attach("inv", unit)
	winter := time.Date(2026, 1, 15, 7, 0, 0, 0, time.UTC)
	out, err := site.StepSite(600, SiteStep{
		Time:    winter,
		Weather: Weather{GHIWm2: 20, AmbientC: -8},
		LoadW:   400,
		Members: []MemberStep{{Key: "inv", Control: Control{EMSMode: ModeEMS, BatteryCmd: CmdStop, MaxSOC: 100}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Thermal.QHpW < 1000 {
		t.Fatalf("expected space heat, Q_hp=%v indoor=%v set=%v", out.Thermal.QHpW, out.Thermal.IndoorC, out.Thermal.SetpointC)
	}
	if out.HPW < 200 {
		t.Fatalf("HP should draw electricity, got %v (COP %v)", out.HPW, out.Thermal.COP)
	}
	if math.Abs(out.ResidualW) > 1 {
		t.Fatalf("residual %v", out.ResidualW)
	}
	if out.GridW+out.Members[0].Out.PVW+out.Members[0].Out.BatteryW+out.HouseW+out.HPW < -1 ||
		math.Abs(out.GridW+out.Members[0].Out.PVW+out.Members[0].Out.BatteryW+out.LoadW) > 2 {
		t.Fatalf("bus grid=%v pv=%v batt=%v load=%v house=%v hp=%v",
			out.GridW, out.Members[0].Out.PVW, out.Members[0].Out.BatteryW, out.LoadW, out.HouseW, out.HPW)
	}
}

func TestGSHPHasHigherWinterCOPThanASHP(t *testing.T) {
	ashp, _ := Preset("house-ashp")
	gshp, _ := Preset("house-gshp")
	a := heatPumpCOP(ashp.HeatPump, -10)
	g := heatPumpCOP(gshp.HeatPump, -10)
	if g <= a {
		t.Fatalf("GSHP COP %v should beat ASHP %v in the cold", g, a)
	}
}

func TestSGReadyBlockStopsHeatPump(t *testing.T) {
	site := NewNativeSite()
	f, _ := Preset("house-ashp")
	site.SetFacility(f)
	site.indoorC = 16
	site.Attach("inv", New(DefaultParams()))
	out, err := site.StepSite(60, SiteStep{
		Time:     time.Date(2026, 1, 15, 7, 0, 0, 0, time.UTC),
		Weather:  Weather{AmbientC: -5},
		HeatPump: HeatPumpCommand{Connected: true, Enable: true, SGReady: 1},
		Members:  []MemberStep{{Key: "inv", Control: Control{EMSMode: ModeEMS, BatteryCmd: CmdStop, MaxSOC: 100}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Thermal.QHpW > 1 {
		t.Fatalf("blocked HP still produced %v W", out.Thermal.QHpW)
	}
}

func TestPresetsAreSelectable(t *testing.T) {
	if len(Presets()) < 4 {
		t.Fatalf("want at least 4 facilities, got %d", len(Presets()))
	}
	for _, id := range []string{"house-ashp", "house-gshp", "apartment-ashp", "pv-battery"} {
		if _, ok := Preset(id); !ok {
			t.Fatalf("missing preset %s", id)
		}
	}
}

func TestUnconnectedHeatPumpFollowsHeatingCurve(t *testing.T) {
	site := NewNativeSite()
	f, _ := Preset("house-ashp")
	site.SetFacility(f)
	site.indoorC = 16
	site.Attach("inv", New(DefaultParams()))
	out, err := site.StepSite(60, SiteStep{
		Time:     time.Date(2026, 1, 15, 7, 0, 0, 0, time.UTC),
		Weather:  Weather{AmbientC: -5},
		HeatPump: HeatPumpCommand{}, // EMS not connected yet
		Members:  []MemberStep{{Key: "inv", Control: Control{EMSMode: ModeEMS, BatteryCmd: CmdStop, MaxSOC: 100}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Thermal.QHpW < 500 {
		t.Fatalf("autonomous HP should heat, Q=%v", out.Thermal.QHpW)
	}
}

func TestConnectedEnableOffStopsHeatPump(t *testing.T) {
	site := NewNativeSite()
	f, _ := Preset("house-ashp")
	site.SetFacility(f)
	site.indoorC = 16
	site.Attach("inv", New(DefaultParams()))
	out, err := site.StepSite(60, SiteStep{
		Time:     time.Date(2026, 1, 15, 7, 0, 0, 0, time.UTC),
		Weather:  Weather{AmbientC: -5},
		HeatPump: HeatPumpCommand{Connected: true, Enable: false},
		Members:  []MemberStep{{Key: "inv", Control: Control{EMSMode: ModeEMS, BatteryCmd: CmdStop, MaxSOC: 100}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Thermal.QHpW > 1 {
		t.Fatalf("disabled HP still produced %v W", out.Thermal.QHpW)
	}
}

func TestFacilityOccupancyAddsHouseLoad(t *testing.T) {
	site := NewNativeSite()
	f, _ := Preset("pv-battery")
	site.SetFacility(f)
	site.Attach("inv", New(DefaultParams()))
	evening := time.Date(2026, 3, 1, 19, 0, 0, 0, time.UTC)
	out, err := site.StepSite(1, SiteStep{
		Time:    evening,
		Weather: Weather{AmbientC: 5},
		LoadW:   0,
		Members: []MemberStep{{Key: "inv", Control: Control{EMSMode: ModeEMS, BatteryCmd: CmdStop, MaxSOC: 100}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.HouseW < 1000 {
		t.Fatalf("occupancy should add evening plug load, house=%v", out.HouseW)
	}
	if out.HPW > 1 {
		t.Fatalf("pv-battery should not run a heat pump, hp=%v", out.HPW)
	}
	if math.Abs(out.ResidualW) > 1 {
		t.Fatalf("residual %v", out.ResidualW)
	}
}

func TestHouseASHPHoldsIndoorOnWinterNight(t *testing.T) {
	site := NewNativeSite()
	f, _ := Preset("house-ashp")
	site.SetFacility(f)
	site.indoorC = 20
	site.dhwC = 48
	site.Attach("inv", New(DefaultParams()))
	t0 := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	var last SiteResult
	for i := 0; i < 24; i++ {
		tm := t0.Add(time.Duration(i) * time.Hour)
		var err error
		last, err = site.StepSite(3600, SiteStep{
			Time:    tm,
			Weather: Weather{AmbientC: OutdoorTemp(tm, "nordic"), GHIWm2: GHIClearSky(tm, 1000, 8, 16)},
			Members: []MemberStep{{Key: "inv", Control: Control{EMSMode: ModeEMS, BatteryCmd: CmdStop, MaxSOC: 100}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(last.ResidualW) > 2 {
			t.Fatalf("hour %d residual %v", i, last.ResidualW)
		}
	}
	if last.Thermal.IndoorC < 16 || last.Thermal.IndoorC > 26 {
		t.Fatalf("indoor drifted out of range: %v (set %v)", last.Thermal.IndoorC, last.Thermal.SetpointC)
	}
	if last.Thermal.DHWC < 35 {
		t.Fatalf("DHW tank too cold: %v", last.Thermal.DHWC)
	}
}

func TestSnapshotFlowsIncludeHeat(t *testing.T) {
	snap := BuildSnapshot("native", "house-ashp", time.Now(), SiteResult{
		GridW:  1500,
		HouseW: 400,
		HPW:    1100,
		LoadW:  1500,
		Thermal: Thermal{
			QSpaceW: 3500,
			QLossW:  2800,
			QSolarW: 200,
		},
		Members: []MemberResult{{Key: "1", Out: Outputs{PVW: 0, BatteryW: 0}}},
	})
	if snap.FacilityName == "" {
		t.Fatal("expected facility name")
	}
	var heat bool
	for _, f := range snap.Flows {
		if f.Kind == "heat" && f.From == "hp" {
			heat = true
		}
	}
	if !heat {
		t.Fatalf("expected hp→building heat flow, got %+v", snap.Flows)
	}
}
