package plant

import (
	"math"
	"testing"
	"time"
)

func TestNativeSiteBalancesTwoHybridsAndHouseLoad(t *testing.T) {
	site := NewNativeSite()
	a := New(DefaultParams())
	b := New(DefaultParams())
	a.State.SOC = 50
	b.State.SOC = 50
	site.Attach("inv-a", a)
	site.Attach("inv-b", b)

	noon := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	out, err := site.StepSite(1, SiteStep{
		Time:    noon,
		Weather: Weather{GHIWm2: 400, AmbientC: 20},
		LoadW:   2000,
		Members: []MemberStep{
			{Key: "inv-a", Control: Control{EMSMode: ModeSelfConsumption, MaxSOC: 100}},
			{Key: "inv-b", Control: Control{EMSMode: ModeSelfConsumption, MaxSOC: 100}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(out.ResidualW) > 1e-6 {
		t.Fatalf("site residual %v", out.ResidualW)
	}
	if len(out.Members) != 2 {
		t.Fatalf("members %d", len(out.Members))
	}
	if math.Abs(out.GridW) > 80 {
		t.Fatalf("two self-consumption hybrids should nearly zero the meter, grid=%v pvA=%v battA=%v pvB=%v battB=%v",
			out.GridW, out.Members[0].Out.PVW, out.Members[0].Out.BatteryW, out.Members[1].Out.PVW, out.Members[1].Out.BatteryW)
	}
}

func TestNativeSiteEMSDoesNotStealSelfConsumptionSlack(t *testing.T) {
	site := NewNativeSite()
	hybrid := New(DefaultParams())
	hybrid.State.SOC = 80
	site.Attach("h", hybrid)

	out, err := site.StepSite(1, SiteStep{
		Time:    time.Now(),
		Weather: Weather{},
		LoadW:   1000,
		Members: []MemberStep{{
			Key: "h",
			Control: Control{
				EMSMode:          ModeEMS,
				BatteryCmd:       CmdDischarge,
				BatterySetpointW: 2500,
				MinSOC:           10,
				MaxSOC:           100,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(out.Members[0].Out.BatteryW+2500) > 1 {
		t.Fatalf("EMS battery %v, want -2500", out.Members[0].Out.BatteryW)
	}
	// P_grid + 0 + (-2500) + 1000 = 0 → import 1500
	if math.Abs(out.GridW-1500) > 1 {
		t.Fatalf("grid %v, want 1500", out.GridW)
	}
}

func TestLoadFMUMissingFile(t *testing.T) {
	_, err := LoadFMU("/no/such/site.fmu")
	if err == nil {
		t.Fatal("expected error")
	}
}
