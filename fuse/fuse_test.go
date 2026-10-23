package fuse

import (
	"encoding/json"
	"math"
	"testing"
)

func profile(t *testing.T, id string) Profile {
	t.Helper()
	p, ok := Lookup(id)
	if !ok {
		t.Fatalf("no %s profile", id)
	}
	return p
}

func ref() *Phase { return &Phase{AmbientC: Ambient(RefAmbientC)} }

// hold runs a phase at multiple × rating for seconds in steps of dt and
// says how long it took to blow (+Inf if it held).
func hold(p Profile, ph *Phase, multiple, rating, seconds, dt float64) float64 {
	for t := 0.0; t < seconds; t += dt {
		p.Step(ph, multiple*rating, rating, dt)
		if ph.Blown {
			return t + dt
		}
	}
	return math.Inf(1)
}

// Every gG band, from cold at the reference ambient, keeps IEC 60269-1's
// gates and its pre-arcing I²t window at 0.01 s, and sits within 0.2
// decades of the ABB curve it was fitted to.
func TestGGBandsMeetTheGatesAndFollowABB(t *testing.T) {
	p := profile(t, IecGg)
	for _, b := range p.Bands {
		for _, g := range b.Gates {
			got := b.MeltSeconds(g.Multiple, RefAmbientC)
			if g.Min && got < g.Seconds || !g.Min && got > g.Seconds {
				kind := "within"
				if g.Min {
					kind = "not sooner than"
				}
				t.Errorf("%g A at %.2f×: melts after %.3g s, want %s %g s", b.RatingA, g.Multiple, got, kind, g.Seconds)
			}
		}
		u := multipleAt(&b, 0.01)
		if i2t := math.Pow(u*b.RatingA, 2) * 0.01; i2t < b.I2tMin || i2t > b.I2tMax {
			t.Errorf("%g A: pre-arcing I²t at 0.01 s %.0f A²s, want %g-%g", b.RatingA, i2t, b.I2tMin, b.I2tMax)
		}
		if len(b.Reference) != 11 {
			t.Fatalf("%g A: %d reference points", b.RatingA, len(b.Reference))
		}
		for _, pt := range b.Reference {
			got := b.MeltSeconds(pt.Multiple, RefAmbientC)
			if d := math.Abs(math.Log10(got / pt.Seconds)); d > 0.2 {
				t.Errorf("%g A at %.2f×: %.3g s, %.2f decades from ABB's %.3g s", b.RatingA, pt.Multiple, got, d, pt.Seconds)
			}
		}
	}
}

// multipleAt is the multiple at which the band melts after seconds.
func multipleAt(b *Band, seconds float64) float64 {
	lo, hi := 1.0, 1000.0
	for i := 0; i < 100; i++ {
		mid := math.Sqrt(lo * hi)
		if b.MeltSeconds(mid, RefAmbientC) > seconds {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi
}

// Through Step, as a site runs it: 1.25 × In holds for the conventional
// hour, 1.6 × In blows within it.
func TestConventionalCurrents(t *testing.T) {
	p := profile(t, IecGg)
	for _, b := range p.Bands {
		if got := hold(p, ref(), 1.25, b.RatingA, 3600, 1); !math.IsInf(got, 1) {
			t.Errorf("%g A: 1.25× blew after %.0f s, want it to hold 1 h", b.RatingA, got)
		}
		if got := hold(p, ref(), 1.6, b.RatingA, 3600, 1); got > 3600 {
			t.Errorf("%g A: 1.6× held 1 h, want it to blow", b.RatingA)
		}
	}
}

// A fuse that has carried its rating for an hour blows at 2 × in under
// half its cold time; at 1.5 × in under three quarters.
func TestPreloadBlowsSooner(t *testing.T) {
	p := profile(t, IecGg)
	for _, rating := range []float64{16, 25, 63} {
		for _, c := range []struct{ multiple, factor float64 }{{2, 0.5}, {1.5, 0.75}} {
			cold := hold(p, ref(), c.multiple, rating, 7200, 0.5)
			warm := ref()
			hold(p, warm, 1, rating, 3600, 1)
			hot := hold(p, warm, c.multiple, rating, 7200, 0.5)
			t.Logf("%g A, %g×: cold %.1f s, after 1 h at In %.1f s", rating, c.multiple, cold, hot)
			if hot >= c.factor*cold {
				t.Errorf("%g A, %g× after 1 h at In: %.1f s, want under %.0f %% of the cold %.1f s", rating, c.multiple, hot, 100*c.factor, cold)
			}
		}
	}
}

// Preheat sets the steady state of a held current: the same as a long soak,
// and a preheated fuse blows sooner than a cold one. It refuses a current
// that melts the fuse before settling.
func TestPreheat(t *testing.T) {
	p := profile(t, IecGg)
	warm := ref()
	if err := p.Preheat(warm, 25, 25); err != nil {
		t.Fatal(err)
	}
	soaked := ref()
	hold(p, soaked, 1, 25, 6*3600, 10)
	if math.Abs(warm.Damage-soaked.Damage) > 0.01 {
		t.Fatalf("preheated at In damage %.3f, after a 6 h soak %.3f", warm.Damage, soaked.Damage)
	}
	if hot, cold := hold(p, warm, 2, 25, 7200, 0.5), p.MeltSeconds(2, 25, RefAmbientC); hot >= cold/2 {
		t.Errorf("2× from preheated: %.1f s, want under half the cold %.1f s", hot, cold)
	}
	for _, amps := range []float64{1.6 * 25, math.NaN(), math.Inf(1)} {
		ph := ref()
		if err := p.Preheat(ph, amps, 25); err == nil || ph.Theta != ([3]float64{}) {
			t.Errorf("preheat at %v A: err %v, state %v; want refused, cold", amps, err, ph.Theta)
		}
	}
	// A blown fuse stays blown: preheating does not replace it.
	blown := ref()
	hold(p, blown, 3, 25, 600, 1)
	before := *blown
	if err := p.Preheat(blown, 25, 25); err == nil || !blown.Blown || blown.Theta != before.Theta {
		t.Errorf("preheat on a blown fuse: err %v, blown %v; want refused, still blown", err, blown.Blown)
	}
}

// 30 s at 1.5 × every 2 min (0.5 × between) heats a fuse further each
// cycle: the element ends each burst hotter than the last, and after ten
// cycles a held 1.5 × blows sooner than from cold.
func TestShortOverloadsAccumulate(t *testing.T) {
	p := profile(t, IecGg)
	ph := ref()
	var peaks []float64
	for cycle := 0; cycle < 10; cycle++ {
		hold(p, ph, 1.5, 25, 30, 1)
		peaks = append(peaks, ph.Damage)
		hold(p, ph, 0.5, 25, 90, 1)
	}
	for i := 1; i < len(peaks); i++ {
		if peaks[i] <= peaks[i-1] {
			t.Fatalf("burst %d left damage %.3f, not above the last %.3f", i+1, peaks[i], peaks[i-1])
		}
	}
	t.Logf("damage after each burst: %.3f", peaks)
	after := hold(p, ph, 1.5, 25, 7200, 1)
	cold := p.MeltSeconds(1.5, 25, RefAmbientC)
	t.Logf("1.5× after ten bursts blows in %.0f s, from cold %.0f s", after, cold)
	if after >= 0.9*cold {
		t.Errorf("1.5× after ten bursts: %.0f s, want under 90 %% of the cold %.0f s", after, cold)
	}
}

// With no current a hot fuse cools along its slowest mode: after an hour,
// each further 10 minutes take off the same share.
func TestCoolingIsExponential(t *testing.T) {
	p := profile(t, IecGg)
	ph := ref()
	hold(p, ph, 1.3, 25, 7200, 1)
	hold(p, ph, 0, 25, 3600, 1)
	var ratios []float64
	for i := 0; i < 4; i++ {
		before := ph.Theta[0]
		hold(p, ph, 0, 25, 600, 1)
		ratios = append(ratios, ph.Theta[0]/before)
	}
	for _, r := range ratios[1:] {
		if math.Abs(r/ratios[0]-1) > 0.01 {
			t.Fatalf("10-minute cooling ratios %.4f, want one constant share", ratios)
		}
	}
	b, _ := p.Band(25)
	slow := math.Min(b.sys.lambda[0], math.Min(b.sys.lambda[1], b.sys.lambda[2]))
	if want := math.Exp(-600 * slow); math.Abs(ratios[0]-want) > 0.01 {
		t.Errorf("cooling ratio %.4f per 10 min, want e^(-600/τ) = %.4f for the slowest mode", ratios[0], want)
	}
}

func TestExportHeatsAsImport(t *testing.T) {
	p := profile(t, IecGg)
	var in, out Phase
	for s := 0; s < 120; s++ {
		a := p.Step(&in, 40, 25, 1)
		b := p.Step(&out, -40, 25, 1)
		if a != b || in.Theta != out.Theta || in.Damage != out.Damage {
			t.Fatalf("second %d: import %+v, export %+v", s, a, b)
		}
	}
}

// The step is exact: steps of 0.5, 1 and 2 s reach the same state, and the
// time left (a dt = 0 probe) drops by exactly the time stepped.
func TestStepIsExact(t *testing.T) {
	p := profile(t, IecGg)
	for _, m := range []float64{1.7, 2.5, 6} {
		cold := p.MeltSeconds(m, 25, RefAmbientC)
		var states [][3]float64
		for _, dt := range []float64{0.5, 1, 2} {
			ph := ref()
			elapsed := 0.0
			for elapsed+dt < math.Min(cold, 60) {
				p.Step(ph, m*25, 25, dt)
				elapsed += dt
			}
			left := p.Step(ph, m*25, 25, 0).RemainingSeconds
			if math.Abs(left-(cold-elapsed)) > 1e-9*cold {
				t.Errorf("%g× in %g s steps: %.12g s left after %g s, want %.12g", m, dt, left, elapsed, cold-elapsed)
			}
			ph2 := ref()
			for i := 0; i < int(math.Round(4/dt)); i++ {
				p.Step(ph2, m*25, 25, dt)
			}
			states = append(states, ph2.Theta)
		}
		for _, s := range states[1:] {
			for i := range s {
				if math.Abs(s[i]-states[0][i]) > 1e-9*math.Abs(states[0][i]) {
					t.Errorf("%g× after 4 s: state %v, with 0.5 s steps %v", m, s, states[0])
				}
			}
		}
	}
}

// Status: trip_s is the cold time, remaining_s the time from now, damage
// the element's way from ambient to melting.
func TestStatusDescribesThePhase(t *testing.T) {
	p := profile(t, IecGg)
	ph := ref()
	first := p.Step(ph, 50, 25, 1)
	second := p.Step(ph, 50, 25, 1)
	cold := p.MeltSeconds(2, 25, RefAmbientC)
	if !first.Active || math.Abs(first.TripSeconds-cold) > 1e-9*cold || math.Abs(first.RemainingSeconds-(cold-1)) > 1e-9*cold {
		t.Fatalf("first second at 2×: %+v, want active, trip %.2f s, remaining %.2f s", first, cold, cold-1)
	}
	if math.Abs(second.RemainingSeconds-(first.RemainingSeconds-1)) > 1e-9*cold || second.Damage <= first.Damage {
		t.Fatalf("second second: %+v after %+v", second, first)
	}
	idle := p.Step(ref(), 25, 25, 1)
	if idle.Active || idle.TripSeconds != 0 || idle.RemainingSeconds != 0 || idle.Damage <= 0 {
		t.Fatalf("at the rating: %+v, want warming but never melting", idle)
	}
	if hold(p, ph, 2, 25, 7200, 1); !ph.Blown || ph.Damage != 1 {
		t.Fatalf("held at 2×: %+v, want blown", ph)
	}
	if st := p.Step(ph, 0, 25, 1); st.Damage != 1 || !ph.Blown {
		t.Fatalf("a blown fuse: %+v", st)
	}
}

// 0 °C is a temperature: a cold cabinet, not the default.
func TestZeroAmbientIsZero(t *testing.T) {
	p := profile(t, IecGg)
	at0 := p.Step(&Phase{AmbientC: Ambient(0)}, 1.5*25, 25, 0).TripSeconds
	unset := p.Step(&Phase{}, 1.5*25, 25, 0).TripSeconds
	if want := p.MeltSeconds(1.5, 25, 0); at0 != want {
		t.Errorf("Step at 0 °C: %.0f s, MeltSeconds at 0 °C %.0f s", at0, want)
	}
	if want := p.MeltSeconds(1.5, 25, DefaultAmbientC); unset != want || at0 <= unset {
		t.Errorf("unset ambient %.0f s, want the default's %.0f s and slower at 0 °C (%.0f s)", unset, want, at0)
	}
}

// Input that is not a current, a step or a cabinet leaves the phase as it
// was and says so; an infinite current blows the fuse.
func TestInvalidInputLeavesTheState(t *testing.T) {
	p := profile(t, IecGg)
	warm := ref()
	hold(p, warm, 1.2, 25, 600, 1)
	for _, c := range []struct {
		name             string
		amps, rating, dt float64
		ambient          *float64
	}{
		{"NaN current", math.NaN(), 25, 1, nil},
		{"NaN step", 30, 25, math.NaN(), nil},
		{"infinite step", 30, 25, math.Inf(1), nil},
		{"negative step", 30, 25, -1, nil},
		{"NaN ambient", 30, 25, 1, Ambient(math.NaN())},
		{"infinite ambient", 30, 25, 1, Ambient(math.Inf(1))},
		{"ambient at the melting point", 30, 25, 1, Ambient(MeltC)},
		{"no rating", 30, 0, 1, nil},
		{"NaN rating", 30, math.NaN(), 1, nil},
		{"10 A, below the bands", 15, 10, 1, nil},
		{"80 A, above them", 100, 80, 1, nil},
	} {
		ph := *warm
		if c.ambient != nil {
			ph.AmbientC = c.ambient
		}
		before := ph
		st := p.Step(&ph, c.amps, c.rating, c.dt)
		if st.Invalid == "" || ph.Theta != before.Theta || ph.Damage != before.Damage || ph.Blown {
			t.Errorf("%s: %+v, state %v → %v; want refused, unchanged", c.name, st, before.Theta, ph.Theta)
		}
	}
	for _, amps := range []float64{math.Inf(-1), 1e160 * 25} {
		ph := *warm
		st := p.Step(&ph, amps, 25, 1)
		_, err := json.Marshal(st)
		if !ph.Blown || st.Damage != 1 || st.Invalid != "" || math.IsInf(st.Ratio, 0) || err != nil {
			t.Errorf("%g A: %+v (json: %v), want blown with a finite ratio", amps, st, err)
		}
		for _, x := range ph.Theta {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				t.Errorf("%g A left the state %v", amps, ph.Theta)
			}
		}
	}
	if p.Supports(10) || p.Supports(80) || !p.Supports(16) || !p.Supports(30) || !p.Supports(63) {
		t.Error("gG supports 16-63 A only")
	}
}

// A warmer cabinet leaves less margin to the melting point.
func TestWarmCabinetBlowsSooner(t *testing.T) {
	p := profile(t, IecGg)
	at20 := p.MeltSeconds(1.5, 25, 20)
	at40 := p.MeltSeconds(1.5, 25, 40)
	if at40 >= at20 {
		t.Errorf("1.5× at 40 °C %.0f s, at 20 °C %.0f s: want sooner when warmer", at40, at20)
	}
	// IEC 60269's curves hold approximately at 30 °C as at 20 °C.
	if at30 := p.MeltSeconds(5, 25, 30); math.Abs(math.Log10(at30/p.MeltSeconds(5, 25, 20))) > 0.1 {
		t.Errorf("5× at 30 °C %.2f s, more than 0.1 decades from 20 °C", at30)
	}
}

// aM keeps its gates at any rating.
func TestAMGates(t *testing.T) {
	p := profile(t, IecAm)
	for _, rating := range []float64{10, 16, 63, 125} {
		b, ok := p.Band(rating)
		if !ok {
			t.Fatalf("aM has no band for %g A", rating)
		}
		for _, g := range b.Gates {
			got := b.MeltSeconds(g.Multiple, RefAmbientC)
			if g.Min && got < g.Seconds || !g.Min && got > g.Seconds {
				t.Errorf("aM at %g×: %.3g s, gate %g s (min %v)", g.Multiple, got, g.Seconds, g.Min)
			}
		}
	}
}

func TestManualNeverBlows(t *testing.T) {
	p := profile(t, Manual)
	ph := &Phase{}
	if hold(p, ph, 20, 25, 600, 1); ph.Blown {
		t.Fatal("a manual fuse blew")
	}
}

// Every profile but manual has a curve and a source and holds at its rating.
func TestEveryProfileHasACurve(t *testing.T) {
	for _, p := range All() {
		if p.ID == Manual {
			continue
		}
		if len(p.Curve) == 0 || len(p.Bands) == 0 || p.Source == "" {
			t.Errorf("%s: %d curve points, %d bands, source %q", p.ID, len(p.Curve), len(p.Bands), p.Source)
		}
		if !math.IsInf(p.MeltSeconds(1, 25, RefAmbientC), 1) {
			t.Errorf("%s melts at its rating", p.ID)
		}
	}
}
