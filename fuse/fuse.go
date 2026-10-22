// Package fuse models the fuse on each phase of a site's main supply as a
// thermal system: the current heats the fuse element (I², so export heats
// it as much as import), the heat flows through the fuse body and sand to
// the holder and the cabinet, and the element melts when it reaches its
// melting temperature. A fuse that has carried its rating for an hour is
// hot and blows at an overload sooner than a cold one; short overloads add
// up; a fuse cools along the sum of its bodies' exponentials.
//
// Each phase has three thermal bodies in a ladder:
//
//	heat I²  →  element  →  fuse body and sand  →  holder and cabinet  →  ambient
//
// In per-unit terms the heat into the element is u² (u = I/In) and the
// three thermal resistances add up to 1, so at a steady current the element
// sits u² above ambient. It melts at UMin², UMin being the steady current
// (as a multiple of In) that just melts it at the reference ambient. The
// element and the body set the short times (adiabatic I²t below 0.1 s, the
// body's seconds); the holder sets the long ones.
//
// The gG bands are calibrated per rating to IEC 60269-1's conventional
// currents and gates and the pre-arcing I²t at 0.01 s (see gg below); aM to
// its gates. The other profiles are gG with scaled times and are not
// calibrated. The model is independent of any EMS's own fuse protection: it
// is the judge.
//
// It has no dependencies, so an EMS's own fast simulation can import it.
package fuse

import (
	"math"
	"sort"
)

// Profile ids.
const (
	Manual = "manual" // never blows by itself
	IecGg  = "iec_gg"
	IecGm  = "iec_gm"
	IecAm  = "iec_am"
	IecGr  = "iec_gr"
	IecGs  = "iec_gs"
)

const (
	// MeltC is the element's melting temperature: tin, 231.93 °C, the
	// solder spot (M-effect) at which a gG element melts under overload.
	MeltC = 232.0
	// RefAmbientC is the ambient the calibration holds at: IEC 60269 gives
	// time-current characteristics at a reference ambient of 20 °C,
	// approximately valid at 30 °C (Enel GSCL004 rev. 0, 2016, §6.1).
	RefAmbientC = 20.0
	// DefaultAmbientC is a meter cabinet's air when a phase gives none.
	DefaultAmbientC = 25.0
)

// Point is one point of a time-current curve from cold: at Multiple × the
// rating the fuse melts after Seconds.
type Point struct {
	Multiple float64
	Seconds  float64
}

// Gate bounds the melting time from cold at a multiple of the rating: with
// Min the fuse must not melt sooner than Seconds, otherwise it must melt
// within Seconds.
type Gate struct {
	Multiple float64
	Seconds  float64
	Min      bool
}

// Thermal is a band's three bodies. Re, Rb and Rh are the shares of the
// steady temperature rise across element-to-body, body-to-holder and
// holder-to-ambient (they add up to 1); each Tau is that body's heat
// capacity times its resistance onwards.
type Thermal struct {
	UMin       float64 // steady melting multiple at the reference ambient
	Re, TauE   float64 // element
	Rb, TauB   float64 // fuse body and sand
	Rh, TauH   float64 // holder and cabinet
	TimeFactor float64 // scales every time constant; 0 or 1 as calibrated
}

// Band is a fuse type at one rating: its thermal bodies and the points it
// is checked against.
type Band struct {
	RatingA float64
	Thermal Thermal
	// Gates are the sourced bounds; I2tMin and I2tMax bound the pre-arcing
	// I²t at 0.01 s (A²s). Targets are the centre points the band is fitted
	// to.
	Gates          []Gate
	I2tMin, I2tMax float64
	Targets        []Point

	sys system
}

// Profile is a fuse type over its rating bands.
type Profile struct {
	ID     string
	Label  string
	Source string // where its numbers come from
	Bands  []Band
	// Curve is the reference band's cold curve, sampled, for display.
	Curve []Point
	// RefRatingA is the band TripSeconds and Curve use.
	RefRatingA float64
}

// The holder and cabinet: a modelling choice, not from a standard. IEC 60269
// fixes a gG fuse only at 1.25 × In (must not melt in 1 h) and 1.6 × In (must
// melt within 1 h) in the long-time region; these two numbers put a cold
// 1.6 × melt at about 13 minutes.
const (
	holderShare = 0.4    // of the steady rise at a steady current
	holderTauS  = 1200.0 // s
)

// gg are the gG bands. Gates, I²t windows and conventional currents from
// IEC 60269-1 as tabled in Schneider Electric, "Complementary technical
// information" CA908036E v3.2 (2018), gate and pre-arcing I²t tables; the
// 25, 40, 50 and 63 A rows also in Enel GSCL004 rev. 0 (2016) Tables 6, 7
// and 14, and 25 A in Mersen EduPack TM-104 (2012) slide 6. 35 A is not in
// those tables: its gates and I²t are interpolated on log axes between 32 A
// and 40 A. Conventional currents: Inf = 1.25 In and If = 1.6 In with a
// conventional time of 1 h for 16 ≤ In ≤ 63 A.
//
// Re, TauE and TauB are fitted (least squares on log time, from cold at the
// reference ambient) to three centre points: the 0.01 s point at the
// geometric mean of the I²t window, the 0.1 s point at the geometric mean
// of the two 0.1 s gate currents, and √50 s at the geometric mean of
// Imin(10 s) and Imax(5 s). UMin = √(1.25 × 1.6) lies between Inf and If.
var gg = []struct {
	ratingA                      float64
	imin10, imax5, imin01, imax1 float64 // A
	i2tMin, i2tMax               float64 // A²s at 0.01 s
	re, tauE, tauB               float64 // fitted
}{
	{16, 33, 65, 85, 150, 300, 1000, 0.042489, 0.040263, 15.5955},
	{20, 42, 85, 110, 200, 500, 1800, 0.038486, 0.040450, 16.8370},
	{25, 52, 110, 150, 260, 1000, 3000, 0.034171, 0.042170, 17.0309},
	{32, 75, 150, 200, 350, 1800, 5000, 0.030930, 0.040115, 21.9215},
	{35, 82.47, 164.94, 222.22, 387.17, 2209.86, 6331.18, 0.030262, 0.041014, 22.1359}, // interpolated
	{40, 95, 190, 260, 450, 3000, 9000, 0.029308, 0.042411, 22.4587},
	{50, 125, 250, 350, 610, 5000, 16000, 0.024424, 0.038492, 25.1411},
	{63, 160, 320, 450, 820, 9000, 27000, 0.022440, 0.038871, 25.8209},
}

func ggBands(timeFactor float64, withGates bool) []Band {
	uMin := math.Sqrt(1.25 * 1.6)
	var out []Band
	for _, r := range gg {
		th := Thermal{UMin: uMin, Re: r.re, TauE: r.tauE, Rb: 1 - r.re - holderShare, TauB: r.tauB,
			Rh: holderShare, TauH: holderTauS, TimeFactor: timeFactor}
		b := Band{RatingA: r.ratingA, Thermal: th}
		if withGates {
			in := r.ratingA
			b.Gates = []Gate{
				{1.25, 3600, true}, {1.6, 3600, false},
				{r.imin10 / in, 10, true}, {r.imax5 / in, 5, false},
				{r.imin01 / in, 0.1, true}, {r.imax1 / in, 0.1, false},
			}
			b.I2tMin, b.I2tMax = r.i2tMin, r.i2tMax
			b.Targets = []Point{
				{math.Sqrt(math.Sqrt(r.i2tMin*r.i2tMax)/0.01) / in, 0.01},
				{math.Sqrt(r.imin01*r.imax1) / in, 0.1},
				{math.Sqrt(r.imin10*r.imax5) / in, math.Sqrt(50)},
			}
		}
		out = append(out, b)
	}
	return out
}

// amBand is aM at any rating. Gates from Mersen EduPack TM-104 (2012) slide
// 7, "as per IEC 60269", for any current rating: not melting within 60 s at
// 4 In, 0.5 s at 8 In and 0.2 s at 10 In; melting within 60 s at 6.3 In,
// 0.5 s at 12.5 In and 0.1 s at 19 In. Fitted like gG to √(4 × 6.3) In at
// 60 s, 10 In at √(0.2 × 0.5) s and 19 In at 0.05 s, with UMin = 4 (aM
// protects against short circuits, not overloads) and the holder at a 0.2
// share, which the fit needs.
func amBand() Band {
	return Band{RatingA: 0,
		Thermal: Thermal{UMin: 4, Re: 0.2005, TauE: 0.2001, Rb: 0.5995, TauB: 45.9302, Rh: 0.2, TauH: holderTauS},
		Gates: []Gate{{4, 60, true}, {6.3, 60, false}, {8, 0.5, true}, {10, 0.2, true},
			{12.5, 0.5, false}, {19, 0.1, false}},
		Targets: []Point{{math.Sqrt(4 * 6.3), 60}, {10, math.Sqrt(0.2 * 0.5)}, {19, 0.05}},
	}
}

var profiles = func() map[string]Profile {
	m := map[string]Profile{
		Manual: {ID: Manual, Label: "Manual", Source: "never blows by itself"},
		IecGg: {ID: IecGg, Label: "IEC 60269 gG/gL (general purpose)", RefRatingA: 25, Bands: ggBands(1, true),
			Source: "IEC 60269-1 gates, I²t and conventional currents per rating (see gg)"},
		IecAm: {ID: IecAm, Label: "IEC 60269 aM (motor, short-circuit)", Bands: []Band{amBand()},
			Source: "IEC 60269 aM gates (see amBand)"},
		// Not calibrated: gG with every time scaled, as the simulator's
		// earlier curves had them against gG.
		IecGm: {ID: IecGm, Label: "IEC 60269 gM (motor, dual-rated) - UK common", RefRatingA: 25, Bands: ggBands(2, false),
			Source: "not calibrated: gG with times × 2 (a gM is a gG of its characteristic current, which the site does not give)"},
		IecGr: {ID: IecGr, Label: "IEC 60269 gR (semiconductor, fast)", RefRatingA: 25, Bands: ggBands(0.3, false),
			Source: "not calibrated: gG with times × 0.3"},
		IecGs: {ID: IecGs, Label: "IEC 60269 gS (semiconductor, full-range)", RefRatingA: 25, Bands: ggBands(0.5, false),
			Source: "not calibrated: gG with times × 0.5"},
	}
	for id, p := range m {
		for i := range p.Bands {
			p.Bands[i].sys = newSystem(p.Bands[i].Thermal)
		}
		if len(p.Bands) > 0 {
			b := p.band(p.RefRatingA)
			for _, x := range []float64{1.5, 1.6, 2, 3, 5, 10, 20} {
				if t := b.MeltSeconds(x, RefAmbientC); !math.IsInf(t, 1) {
					p.Curve = append(p.Curve, Point{x, t})
				}
			}
		}
		m[id] = p
	}
	return m
}()

// Lookup returns the profile with this id.
func Lookup(id string) (Profile, bool) {
	p, ok := profiles[id]
	return p, ok
}

// All returns every profile, ordered by id.
func All() []Profile {
	out := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// band is the band nearest ratingA on log axes; a rating outside the bands
// takes the nearest end.
func (p Profile) band(ratingA float64) *Band {
	if len(p.Bands) == 0 {
		return nil
	}
	best, dist := 0, math.Inf(1)
	for i, b := range p.Bands {
		if b.RatingA <= 0 || ratingA <= 0 {
			return &p.Bands[i]
		}
		if d := math.Abs(math.Log(ratingA / b.RatingA)); d < dist {
			best, dist = i, d
		}
	}
	return &p.Bands[best]
}

// Band returns the band a fuse rated ratingA uses, and false for a profile
// without bands.
func (p Profile) Band(ratingA float64) (Band, bool) {
	b := p.band(ratingA)
	if b == nil {
		return Band{}, false
	}
	return *b, true
}

// TripSeconds is the time to melt from cold at multiple × the rating, at the
// reference band and ambient; +Inf if it never melts there.
func (p Profile) TripSeconds(multiple float64) float64 {
	return p.MeltSeconds(multiple, p.RefRatingA, RefAmbientC)
}

// MeltSeconds is the time to melt from cold at multiple × a rating of
// ratingA in ambientC; +Inf if it never melts.
func (p Profile) MeltSeconds(multiple, ratingA, ambientC float64) float64 {
	b := p.band(ratingA)
	if b == nil {
		return math.Inf(1)
	}
	return b.MeltSeconds(multiple, ambientC)
}

// MeltSeconds is the band's time to melt from cold.
func (b *Band) MeltSeconds(multiple, ambientC float64) float64 {
	return b.sys.timeToMelt([3]float64{}, multiple, b.sys.meltRise(ambientC))
}

// Phase is the state of one phase's fuse.
type Phase struct {
	// Theta is each body's rise over ambient, in per-unit (element, body,
	// holder); zero is cold.
	Theta [3]float64
	// AmbientC is the cabinet's air; 0 means DefaultAmbientC.
	AmbientC float64
	// Damage is the element's way from ambient to melting, 0 to 1; Step
	// sets it.
	Damage float64
	Blown  bool
}

// Status describes a phase after a step.
type Status struct {
	Ratio            float64 `json:"ratio"`       // current over the rating
	TripSeconds      float64 `json:"trip_s"`      // time to melt at this ratio from cold; 0 if never
	RemainingSeconds float64 `json:"remaining_s"` // time to melt at this ratio from now; 0 if never
	Damage           float64 `json:"damage"`      // the element's way from ambient to melting
	Active           bool    `json:"active"`      // it will melt if the current stays
}

// Step advances one phase by dt seconds at amps through a fuse rated
// ratingA. The sign of amps does not matter. The step is exact for a
// current held over dt, so the result does not depend on the step size. A
// profile without bands leaves the phase as it is.
func (p Profile) Step(ph *Phase, amps, ratingA, dt float64) Status {
	b := p.band(ratingA)
	if b == nil {
		return Status{Damage: ph.Damage}
	}
	if dt < 0 {
		dt = 0
	}
	if ph.Blown {
		ph.Damage = 1
		return Status{Damage: 1}
	}
	u := 0.0
	if ratingA > 0 {
		u = math.Abs(amps) / ratingA
	}
	ambient := ph.AmbientC
	if ambient == 0 {
		ambient = DefaultAmbientC
	}
	melt := b.sys.meltRise(ambient)
	st := Status{Ratio: u}
	if cold := b.sys.timeToMelt([3]float64{}, u, melt); !math.IsInf(cold, 1) {
		st.TripSeconds = cold
	}
	left := b.sys.timeToMelt(ph.Theta, u, melt)
	if left <= dt {
		ph.Theta = b.sys.advance(ph.Theta, u, left)
		ph.Blown, ph.Damage = true, 1
		st.Damage = 1
		return st
	}
	ph.Theta = b.sys.advance(ph.Theta, u, dt)
	ph.Damage = math.Max(0, math.Min(1, ph.Theta[0]/melt))
	st.Damage = ph.Damage
	if !math.IsInf(left, 1) {
		st.RemainingSeconds, st.Active = left-dt, true
	}
	return st
}

// system is a band's ladder solved once: C dθ/dt = -G θ + (u², 0, 0).
// With y = θ/s (s = 1/√C), dy/dt = -M y + ..., M = s G s symmetric, so
// θ(t) = θ∞ + P e^{-Λt} Q (θ0 - θ∞), P = diag(s) V, Q = Vᵀ diag(1/s).
type system struct {
	uMin2  float64
	inf    [3]float64 // θ∞ at u = 1
	lambda [3]float64
	p, q   [3][3]float64
}

func newSystem(t Thermal) system {
	f := t.TimeFactor
	if f <= 0 {
		f = 1
	}
	c := [3]float64{f * t.TauE / t.Re, f * t.TauB / t.Rb, f * t.TauH / t.Rh}
	g1, g2, g3 := 1/t.Re, 1/t.Rb, 1/t.Rh
	g := [3][3]float64{{g1, -g1, 0}, {-g1, g1 + g2, -g2}, {0, -g2, g2 + g3}}
	var s [3]float64
	for i := range s {
		s[i] = 1 / math.Sqrt(c[i])
	}
	var m [3][3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			m[i][j] = s[i] * g[i][j] * s[j]
		}
	}
	lam, v := eigen3(m)
	sys := system{uMin2: t.UMin * t.UMin, lambda: lam,
		inf: [3]float64{t.Re + t.Rb + t.Rh, t.Rb + t.Rh, t.Rh}}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			sys.p[i][j] = s[i] * v[i][j]
			sys.q[i][j] = v[j][i] / s[j]
		}
	}
	return sys
}

// meltRise is the element's melting rise over ambient, in per-unit: UMin²
// at the reference ambient, less in a warmer cabinet.
func (s *system) meltRise(ambientC float64) float64 {
	ambientC = math.Min(ambientC, MeltC-1)
	return s.uMin2 * (MeltC - ambientC) / (MeltC - RefAmbientC)
}

// modes returns θ∞ at u and the modal amplitudes of θ0 - θ∞.
func (s *system) modes(theta [3]float64, u float64) (inf, amp [3]float64) {
	u2 := u * u
	var d [3]float64
	for i := range inf {
		inf[i] = s.inf[i] * u2
		d[i] = theta[i] - inf[i]
	}
	for k := 0; k < 3; k++ {
		for j := 0; j < 3; j++ {
			amp[k] += s.q[k][j] * d[j]
		}
	}
	return inf, amp
}

func (s *system) advance(theta [3]float64, u, dt float64) [3]float64 {
	inf, amp := s.modes(theta, u)
	var out [3]float64
	for i := 0; i < 3; i++ {
		out[i] = inf[i]
		for k := 0; k < 3; k++ {
			out[i] += s.p[i][k] * math.Exp(-s.lambda[k]*dt) * amp[k]
		}
	}
	return out
}

// timeToMelt is when the element first reaches melt at u from theta; +Inf
// if it never does (within ten million seconds).
func (s *system) timeToMelt(theta [3]float64, u, melt float64) float64 {
	inf, amp := s.modes(theta, u)
	f := func(t float64) float64 {
		x := inf[0] - melt
		for k := 0; k < 3; k++ {
			x += s.p[0][k] * math.Exp(-s.lambda[k]*t) * amp[k]
		}
		return x
	}
	if f(0) >= 0 {
		return 0
	}
	prev := 0.0
	for t := 1e-6; t <= 1e7; t *= 1.2 {
		if f(t) >= 0 {
			lo, hi := prev, t
			for i := 0; i < 60 && hi-lo > 1e-9*hi; i++ {
				mid := (lo + hi) / 2
				if f(mid) >= 0 {
					hi = mid
				} else {
					lo = mid
				}
			}
			return hi
		}
		prev = t
	}
	return math.Inf(1)
}

// eigen3 diagonalises a symmetric 3×3 matrix by Jacobi rotations: the
// eigenvalues and the eigenvectors as columns.
func eigen3(a [3][3]float64) ([3]float64, [3][3]float64) {
	v := [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	for sweep := 0; sweep < 50; sweep++ {
		off := a[0][1]*a[0][1] + a[0][2]*a[0][2] + a[1][2]*a[1][2]
		if off < 1e-30 {
			break
		}
		for _, pq := range [][2]int{{0, 1}, {0, 2}, {1, 2}} {
			p, q := pq[0], pq[1]
			if math.Abs(a[p][q]) < 1e-300 {
				continue
			}
			theta := (a[q][q] - a[p][p]) / (2 * a[p][q])
			t := math.Copysign(1, theta) / (math.Abs(theta) + math.Sqrt(theta*theta+1))
			c := 1 / math.Sqrt(t*t+1)
			sn := t * c
			for k := 0; k < 3; k++ {
				akp, akq := a[k][p], a[k][q]
				a[k][p], a[k][q] = c*akp-sn*akq, sn*akp+c*akq
			}
			for k := 0; k < 3; k++ {
				apk, aqk := a[p][k], a[q][k]
				a[p][k], a[q][k] = c*apk-sn*aqk, sn*apk+c*aqk
			}
			for k := 0; k < 3; k++ {
				vkp, vkq := v[k][p], v[k][q]
				v[k][p], v[k][q] = c*vkp-sn*vkq, sn*vkp+c*vkq
			}
		}
	}
	return [3]float64{a[0][0], a[1][1], a[2][2]}, v
}
