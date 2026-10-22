package fuse

import (
	"math"
	"testing"
)

func gg(t *testing.T) Profile {
	t.Helper()
	p, ok := Lookup(IecGg)
	if !ok {
		t.Fatal("no iec_gg profile")
	}
	return p
}

func TestTripSecondsFollowsTheCurve(t *testing.T) {
	p := gg(t)
	for _, pt := range p.Curve {
		if got := p.TripSeconds(pt.Multiple); math.Abs(got-pt.Seconds) > 1e-9*pt.Seconds {
			t.Errorf("%.2f×: %v s, want %v s", pt.Multiple, got, pt.Seconds)
		}
	}
	if got := p.TripSeconds(1); !math.IsInf(got, 1) {
		t.Errorf("1×: %v s, want never", got)
	}
	if got := p.TripSeconds(1.1); got != 3600 {
		t.Errorf("1.1×: %v s, want the first point's 3600 s", got)
	}
	if got := p.TripSeconds(1.4); got <= 1800 || got >= 3600 {
		t.Errorf("1.4×: %.0f s, want between the 1.25× and 1.6× points", got)
	}
	if got := p.TripSeconds(40); got >= 0.5 {
		t.Errorf("40×: %v s, want under the 20× point", got)
	}
}

// A phase held at a multiple of its rating blows after the curve's time,
// in one-second steps; the other phases are untouched. Export heats a fuse
// as much as import.
func TestStepBlowsOnTheCurve(t *testing.T) {
	p := gg(t)
	for _, tc := range []struct{ multiple, wantS float64 }{{2, 600}, {5, 20}, {10, 3}} {
		var phases [3]Phase
		blewAt := 0
		for s := 1; s <= 4000 && blewAt == 0; s++ {
			p.Step(&phases[0], -tc.multiple*25, 25, 1)
			p.Step(&phases[1], 0, 25, 1)
			if phases[0].Blown {
				blewAt = s
			}
		}
		if float64(blewAt) != tc.wantS || phases[1].Damage != 0 || phases[1].Blown {
			t.Errorf("%.0f×: blew after %d s, want %v s", tc.multiple, blewAt, tc.wantS)
		}
	}
	var ph Phase
	for s := 0; s < 4000; s++ {
		p.Step(&ph, 25, 25, 1)
	}
	if ph.Blown || ph.Damage != 0 {
		t.Errorf("at the rating: %+v, want no damage", ph)
	}
}

func TestDamageRecoversBelowTheRating(t *testing.T) {
	p := gg(t)
	ph := Phase{Damage: 0.5}
	st := p.Step(&ph, 10, 25, 75)
	if math.Abs(ph.Damage-0.25) > 1e-12 || st.Active {
		t.Fatalf("after 75 s at 0.4×: damage %v, want 0.25 (cooldown %v s)", ph.Damage, p.CooldownS)
	}
	st = p.Step(&ph, 50, 25, 1)
	if !st.Active || math.Abs(st.TripSeconds-600) > 1e-9 || math.Abs(st.RemainingSeconds-(1-ph.Damage)*st.TripSeconds) > 1e-9 {
		t.Fatalf("at 2×: %+v", st)
	}
}

func TestBlownStaysBlownAndManualNeverBlows(t *testing.T) {
	p := gg(t)
	ph := Phase{Blown: true, Damage: 0.9}
	if st := p.Step(&ph, 0, 25, 1000); !ph.Blown || ph.Damage != 1 || st.Damage != 1 {
		t.Fatalf("blown phase after a cool second: %+v", ph)
	}
	m, _ := Lookup(Manual)
	ph = Phase{}
	m.Step(&ph, 1000, 25, 1000)
	if ph.Blown || ph.Damage != 0 {
		t.Fatalf("manual: %+v, want untouched", ph)
	}
}

func TestAMBuildsOnlyFromItsStartRatio(t *testing.T) {
	p, _ := Lookup(IecAm)
	ph := Phase{}
	p.Step(&ph, 3.9*25, 25, 100)
	if ph.Damage != 0 {
		t.Fatalf("aM at 3.9×: damage %v, want none", ph.Damage)
	}
	p.Step(&ph, 5*25, 25, 1)
	if ph.Damage == 0 {
		t.Fatal("aM at 5×: no damage")
	}
}

func TestAllIsSortedAndComplete(t *testing.T) {
	all := All()
	if len(all) != 6 {
		t.Fatalf("%d profiles", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].ID >= all[i].ID {
			t.Fatalf("not sorted: %s before %s", all[i-1].ID, all[i].ID)
		}
	}
}
