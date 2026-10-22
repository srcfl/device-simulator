package fuse

import (
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
// gates and its pre-arcing I²t window at 0.01 s, and sits within 0.05
// decades of the centre points it was fitted to.
func TestGGBandsMeetTheGates(t *testing.T) {
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
		for _, pt := range b.Targets {
			got := b.MeltSeconds(pt.Multiple, RefAmbientC)
			if d := math.Abs(math.Log10(got / pt.Seconds)); d > 0.05 {
				t.Errorf("%g A at %.2f×: %.3g s, %.2f decades from the fitted %.3g s", b.RatingA, pt.Multiple, got, d, pt.Seconds)
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
		cold := func() *Phase { return &Phase{AmbientC: RefAmbientC} }
		if got := hold(p, cold(), 1.25, b.RatingA, 3600, 1); !math.IsInf(got, 1) {
			t.Errorf("%g A: 1.25× blew after %.0f s, want it to hold 1 h", b.RatingA, got)
		}
		if got := hold(p, cold(), 1.6, b.RatingA, 3600, 1); got > 3600 {
			t.Errorf("%g A: 1.6× held 1 h, want it to blow", b.RatingA)
		}
	}
}

// A fuse that has carried its rating for an hour blows at 2 × in under
// half its cold time; at 1.5 × in under two thirds.
func TestPreloadBlowsSooner(t *testing.T) {
	p := profile(t, IecGg)
	for _, rating := range []float64{16, 25, 63} {
		for _, c := range []struct{ multiple, factor float64 }{{2, 0.5}, {1.5, 2.0 / 3}} {
			cold := hold(p, &Phase{AmbientC: RefAmbientC}, c.multiple, rating, 7200, 0.5)
			warm := &Phase{AmbientC: RefAmbientC}
			hold(p, warm, 1, rating, 3600, 1)
			hot := hold(p, warm, c.multiple, rating, 7200, 0.5)
			t.Logf("%g A, %g×: cold %.1f s, after 1 h at In %.1f s", rating, c.multiple, cold, hot)
			if hot >= c.factor*cold {
				t.Errorf("%g A, %g× after 1 h at In: %.1f s, want under %.0f %% of the cold %.1f s", rating, c.multiple, hot, 100*c.factor, cold)
			}
		}
	}
}

// 30 s at 1.5 × every 2 min (0.5 × between) heats a fuse further each
// cycle: the element ends each burst hotter than the last, and after ten
// cycles a held 1.5 × blows sooner than from cold.
func TestShortOverloadsAccumulate(t *testing.T) {
	p := profile(t, IecGg)
	ph := &Phase{AmbientC: RefAmbientC}
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

// With no current a hot fuse cools along its slowest body's exponential:
// after an hour, each further 10 minutes take off the same share.
func TestCoolingIsExponential(t *testing.T) {
	p := profile(t, IecGg)
	ph := &Phase{AmbientC: RefAmbientC}
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
	if want := math.Exp(-600 / holderTauS); math.Abs(ratios[0]-want) > 0.05 {
		t.Errorf("cooling ratio %.3f per 10 min, want about e^(-600/τ) = %.3f", ratios[0], want)
	}
}

func TestExportHeatsAsImport(t *testing.T) {
	p := profile(t, IecGg)
	var in, out Phase
	for s := 0; s < 120; s++ {
		a := p.Step(&in, 40, 25, 1)
		b := p.Step(&out, -40, 25, 1)
		if a != b || in != out {
			t.Fatalf("second %d: import %+v, export %+v", s, a, b)
		}
	}
}

// The step is exact: 0.5 s and 2 s steps blow at the same moment, to
// within the step.
func TestStepSizeDoesNotMatter(t *testing.T) {
	p := profile(t, IecGg)
	for _, m := range []float64{1.7, 2.5, 6} {
		fine := hold(p, &Phase{AmbientC: RefAmbientC}, m, 25, 7200, 0.5)
		coarse := hold(p, &Phase{AmbientC: RefAmbientC}, m, 25, 7200, 2)
		cold := p.MeltSeconds(m, 25, RefAmbientC)
		if math.Abs(fine-cold) > 0.5 || math.Abs(coarse-cold) > 2 {
			t.Errorf("%g×: blew after %.1f s (0.5 s steps) and %.1f s (2 s steps), want %.2f s", m, fine, coarse, cold)
		}
	}
}

// Status: trip_s is the cold time, remaining_s the time from now, damage
// the element's way from ambient to melting.
func TestStatusDescribesThePhase(t *testing.T) {
	p := profile(t, IecGg)
	ph := &Phase{AmbientC: RefAmbientC}
	first := p.Step(ph, 50, 25, 1)
	second := p.Step(ph, 50, 25, 1)
	cold := p.MeltSeconds(2, 25, RefAmbientC)
	if !first.Active || math.Abs(first.TripSeconds-cold) > 1e-6*cold || math.Abs(first.RemainingSeconds-(cold-1)) > 1e-3 {
		t.Fatalf("first second at 2×: %+v, want active, trip %.2f s, remaining %.2f s", first, cold, cold-1)
	}
	if math.Abs(second.RemainingSeconds-(first.RemainingSeconds-1)) > 1e-3 || second.Damage <= first.Damage {
		t.Fatalf("second second: %+v after %+v", second, first)
	}
	idle := p.Step(&Phase{AmbientC: RefAmbientC}, 25, 25, 1)
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
	for _, rating := range []float64{16, 63} {
		b, _ := p.Band(rating)
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

// Every profile but manual melts above its curve's start and holds at its
// rating.
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
