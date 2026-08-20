//go:build cgo && linux && amd64

package siteplant

import (
	"math"
	"testing"
	"time"

	"github.com/srcfl/device-simulator/internal/plant"
)

func TestFMUSiteStep(t *testing.T) {
	path := testFMU(t)
	m, err := LoadFMU(path)
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := m.(interface{ Close() }); ok {
		defer closer.Close()
	}
	if m.Backend() != "fmu" {
		t.Fatalf("backend %s", m.Backend())
	}
	u := plant.New(plant.DefaultParams())
	m.Attach("inv-a", u)

	noon := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	out, err := m.StepSite(60, plant.SiteStep{
		Time:    noon,
		Weather: plant.Weather{GHIWm2: 0, AmbientC: 20},
		LoadW:   0,
		Members: []plant.MemberStep{{
			Key: "inv-a",
			Control: plant.Control{
				EMSMode:          plant.ModeEMS,
				BatteryCmd:       plant.CmdStop,
				BatterySetpointW: 0,
				MaxSOC:           100,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(out.ResidualW) > 1 {
		t.Fatalf("residual %v members=%+v", out.ResidualW, out.Members)
	}
	if math.Abs(out.GridW) > 1 {
		t.Fatalf("idle grid %v, want ~0", out.GridW)
	}

	out, err = m.StepSite(1, plant.SiteStep{
		Time:    noon,
		Weather: plant.Weather{GHIWm2: 1000, AmbientC: 20},
		Members: []plant.MemberStep{{
			Key: "inv-a",
			Control: plant.Control{
				EMSMode:    plant.ModeEMS,
				BatteryCmd: plant.CmdStop,
				MaxSOC:     100,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Members) != 1 {
		t.Fatalf("members %d", len(out.Members))
	}
	if out.Members[0].Out.PVW >= -100 {
		t.Fatalf("expected PV generation, got %v", out.Members[0].Out.PVW)
	}
	if math.Abs(out.GridW+out.Members[0].Out.PVW) > 5 {
		t.Fatalf("grid %v pv %v should sum to 0", out.GridW, out.Members[0].Out.PVW)
	}
	if math.Abs(out.ResidualW) > 5 {
		t.Fatalf("residual %v", out.ResidualW)
	}
}

func TestOpenMuxFallsBackPastFourUnits(t *testing.T) {
	path := testFMU(t)
	m, err := Open(Options{FMU: path})
	if err != nil {
		t.Fatal(err)
	}
	if m.Backend() != "fmu" {
		t.Fatalf("backend %s", m.Backend())
	}
	members := make([]plant.MemberStep, 5)
	for i := 0; i < 5; i++ {
		key := string(rune('a' + i))
		m.Attach(key, plant.New(plant.DefaultParams()))
		members[i] = plant.MemberStep{
			Key:     key,
			Control: plant.Control{EMSMode: plant.ModeEMS, BatteryCmd: plant.CmdStop, MaxSOC: 100},
		}
	}
	out, err := m.StepSite(1, plant.SiteStep{
		Weather: plant.Weather{AmbientC: 20},
		LoadW:   1000,
		Members: members,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Members) != 5 {
		t.Fatalf("native fallback should keep 5 members, got %d", len(out.Members))
	}
}

func testFMU(t *testing.T) string {
	t.Helper()
	if p := lookupDefaultFMU(); p != "" {
		return p
	}
	return compileTestFMU(t)
}
