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
// (as a multiple of In) that just melts it at the reference ambient.
//
// The gG bands keep IEC 60269-1's conventional currents, gates and
// pre-arcing I²t window at 0.01 s, and within them follow a manufacturer's
// published curves (see gg). aM keeps its gates. The other profiles are gG
// with scaled times and are not calibrated. The model is a central
// estimate of one maker's fuse-links, not a worst case, and it is
// independent of any EMS's own fuse protection: it is the judge.
//
// It has no dependencies, so an EMS's own fast simulation can import it.
package fuse

import (
	"fmt"
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
// holder-to-ambient (they add up to 1). Each Tau is that body's heat
// capacity times its own outgoing resistance; they are parameters, not the
// system's modes, which come out of the coupled ladder (at 25 A 0.12 s,
// 9.4 s and 1,308 s).
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
	// Gates are the standard's bounds; I2tMin and I2tMax bound the
	// pre-arcing I²t at 0.01 s (A²s). Reference is the manufacturer's curve
	// the band is fitted to.
	Gates          []Gate
	I2tMin, I2tMax float64
	Reference      []Point

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

// gg are the gG bands, 16 to 63 A.
//
// Gates, I²t windows and conventional currents are IEC 60269-1's, as tabled
// in Schneider Electric, "Complementary technical information" CA908036E
// v3.2 (2018); the 25, 40, 50 and 63 A rows are also in Enel GSCL004 rev. 0
// (2016) Tables 6, 7 and 14, and 25 A in Mersen EduPack TM-104 (2012) slide
// 6. 35 A is not in those tables: its gates and I²t are interpolated on log
// axes between 32 A and 40 A. Conventional currents: Inf = 1.25 In must not
// melt and If = 1.6 In must melt within the conventional time, 1 h for
// 16 ≤ In ≤ 63 A.
//
// The gates leave a wide tube: no gate constrains 1.4-2 ×, where home
// overloads land. Within it each band follows ABB's pre-arcing curves for
// OFAF gG fuse-links, size 000, 500 V (ABB "Fusegear DIN-type HRC-fuse
// links" 1SCC317001C0201, page 14), digitized from the PDF's vector paths
// (48 vertices a curve; read-off error under 2 % in time). All six thermal
// parameters are fitted, least squares on log time from cold at 20 °C, to
// ABB's melting times at 1.5, 1.6, 2, 2.5, 3, 4, 5, 8 and 10 × In, the
// current that melts in 0.01 s and the curve's start at 7,990 s; the fit
// keeps every gate. At those eleven points the residuals are rms
// 0.08-0.095 decades, at most 0.17. Against all 48 vertices of each ABB
// curve they are rms 0.13-0.20, at most 0.22-0.46: the model is too slow
// at 1.6-1.95 × (16 A at 1.82 ×: ABB 150 s, model 447 s) and too fast at
// 2.0-2.5 × (down to -0.27). Where the element's quasi-steady rise over
// the body reaches the melting rise, u = UMin/√(Re + Rb) = 2.02-2.09 × In,
// the body stage takes over from the holder and the curve drops steeply:
// its log-log slope between 1.9 and 2.3 × reaches -18 to -52, ABB's -7 to
// -11. Fitting all 48 vertices reaches only rms 0.12-0.15, so it is the
// three-body ladder's shape. A simulation judging overloads at 1.9-2.1 ×
// In sits on that step: a small change of current there moves the melting
// time a lot.
// Letting the element's resistance rise with temperature (silver, 0.0038
// per K) made the fit worse (rms 0.11-0.14), so it is left out.
var gg = []struct {
	ratingA                      float64
	imin10, imax5, imin01, imax1 float64 // A
	i2tMin, i2tMax               float64 // A²s at 0.01 s
	uMin, re, tauE, rh           float64 // fitted
	tauB, tauH                   float64 // fitted, s
	abb                          [9]float64
	abb001A, abbStartA           float64 // A melting in 0.01 s; A at the curve's 7,990 s start
}{
	{16, 33, 65, 85, 150, 300, 1000, 1.429959, 0.073378, 0.074249, 0.500174, 7.03102, 1604.18,
		[9]float64{3546.8, 1165.2, 53.1, 9.445, 3.29, 0.8381, 0.3328, 0.0554, 0.0246}, 208.1, 22.918},
	{20, 42, 85, 110, 200, 500, 1800, 1.412945, 0.070246, 0.091392, 0.514825, 9.47660, 1251.63,
		[9]float64{2592.9, 819.5, 57.8, 12.3, 4.58, 1.177, 0.4498, 0.0711, 0.0318}, 284.5, 28.272},
	{25, 52, 110, 150, 260, 1000, 3000, 1.396178, 0.073599, 0.128139, 0.527645, 8.81978, 1295.49,
		[9]float64{2341.3, 821.6, 58.7, 11.8, 4.222, 1.098, 0.445, 0.0847, 0.0419}, 406.9, 34.925},
	{32, 75, 150, 200, 350, 1800, 5000, 1.392001, 0.075676, 0.167797, 0.532203, 10.60002, 1500.19,
		[9]float64{2581.2, 970.5, 72.9, 14.1, 5.056, 1.311, 0.5272, 0.1026, 0.0519}, 591.8, 44.605},
	{35, 82.47, 164.94, 222.22, 387.17, 2209.86, 6331.18, 1.390453, 0.075385, 0.169990, 0.533283, 10.92408, 1601.99, // gates interpolated
		[9]float64{2680.3, 1049.8, 76.5, 14.6, 5.206, 1.342, 0.5387, 0.1044, 0.0527}, 650.8, 48.759},
	{40, 95, 190, 260, 450, 3000, 9000, 1.396104, 0.074093, 0.177481, 0.529719, 11.81891, 1595.82,
		[9]float64{2778.0, 1066.7, 80.2, 15.8, 5.714, 1.494, 0.5982, 0.1149, 0.0577}, 761.5, 55.948},
	{50, 125, 250, 350, 610, 5000, 16000, 1.409864, 0.071730, 0.197218, 0.525250, 16.77090, 1948.86,
		[9]float64{3566.2, 1471.4, 121.6, 23.2, 8.242, 2.115, 0.8293, 0.1445, 0.0696}, 1000.8, 70.810},
	{63, 160, 320, 450, 820, 9000, 27000, 1.314326, 0.063439, 0.165090, 0.604208, 19.24866, 1554.48,
		[9]float64{1798.4, 910.0, 134.6, 29.3, 9.987, 2.193, 0.7666, 0.1183, 0.0563}, 1151.8, 82.971},
}

var abbMultiples = [9]float64{1.5, 1.6, 2, 2.5, 3, 4, 5, 8, 10}

func ggBands(timeFactor float64, calibrated bool) []Band {
	var out []Band
	for _, r := range gg {
		th := Thermal{UMin: r.uMin, Re: r.re, TauE: r.tauE, Rb: 1 - r.re - r.rh, TauB: r.tauB,
			Rh: r.rh, TauH: r.tauH, TimeFactor: timeFactor}
		b := Band{RatingA: r.ratingA, Thermal: th}
		if calibrated {
			in := r.ratingA
			b.Gates = []Gate{
				{1.25, 3600, true}, {1.6, 3600, false},
				{r.imin10 / in, 10, true}, {r.imax5 / in, 5, false},
				{r.imin01 / in, 0.1, true}, {r.imax1 / in, 0.1, false},
			}
			b.I2tMin, b.I2tMax = r.i2tMin, r.i2tMax
			b.Reference = append(b.Reference, Point{r.abbStartA / in, 7990})
			for i, m := range abbMultiples {
				b.Reference = append(b.Reference, Point{m, r.abb[i]})
			}
			b.Reference = append(b.Reference, Point{r.abb001A / in, 0.01})
		}
		out = append(out, b)
	}
	return out
}

// amHolderTauS is aM's holder and cabinet, as gG's were before they were
// fitted: a modelling choice.
const amHolderTauS = 1200.0

// amBand is aM at any rating. Gates from Mersen EduPack TM-104 (2012) slide
// 7, "as per IEC 60269", for any current rating: not melting within 60 s at
// 4 In, 0.5 s at 8 In and 0.2 s at 10 In; melting within 60 s at 6.3 In,
// 0.5 s at 12.5 In and 0.1 s at 19 In. Fitted to √(4 × 6.3) In at 60 s,
// 10 In at √(0.2 × 0.5) s and 19 In at 0.05 s, with UMin = 4 (an
// assumption: aM protects against short circuits, not overloads) and the
// holder at a 0.2 share, which the fit needs.
func amBand() Band {
	return Band{RatingA: 0,
		Thermal: Thermal{UMin: 4, Re: 0.2005, TauE: 0.2001, Rb: 0.5995, TauB: 45.9302, Rh: 0.2, TauH: amHolderTauS},
		Gates: []Gate{{4, 60, true}, {6.3, 60, false}, {8, 0.5, true}, {10, 0.2, true},
			{12.5, 0.5, false}, {19, 0.1, false}},
		Reference: []Point{{math.Sqrt(4 * 6.3), 60}, {10, math.Sqrt(0.2 * 0.5)}, {19, 0.05}},
	}
}

var profiles = func() map[string]Profile {
	m := map[string]Profile{
		Manual: {ID: Manual, Label: "Manual", Source: "never blows by itself"},
		IecGg: {ID: IecGg, Label: "IEC 60269 gG/gL (general purpose)", RefRatingA: 25, Bands: ggBands(1, true),
			Source: "IEC 60269-1 gates, I²t and conventional currents; ABB OFAF 000 gG curves within them (see gg)"},
		IecAm: {ID: IecAm, Label: "IEC 60269 aM (motor, short-circuit)", RefRatingA: 25, Bands: []Band{amBand()},
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
		if b, err := p.band(p.RefRatingA); err == nil {
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

// Ratings are the ratings a gG-based profile is calibrated for; between
// them a rating takes the nearest band on log axes.
func (p Profile) Ratings() []float64 {
	var out []float64
	for _, b := range p.Bands {
		if b.RatingA > 0 {
			out = append(out, b.RatingA)
		}
	}
	return out
}

// band is the band for ratingA: the nearest on log axes between the first
// and the last rating, any rating for a rating-independent band (aM), an
// error outside the bands or for a profile without any.
func (p Profile) band(ratingA float64) (*Band, error) {
	if len(p.Bands) == 0 {
		return nil, fmt.Errorf("%s has no fuse model", p.ID)
	}
	if !(ratingA > 0) || math.IsInf(ratingA, 0) {
		return nil, fmt.Errorf("fuse rating %v A is not a rating", ratingA)
	}
	if len(p.Bands) == 1 && p.Bands[0].RatingA == 0 {
		return &p.Bands[0], nil
	}
	lo, hi := p.Bands[0].RatingA, p.Bands[len(p.Bands)-1].RatingA
	if ratingA < lo || ratingA > hi {
		return nil, fmt.Errorf("%s is modelled for %g-%g A, not %g A", p.ID, lo, hi, ratingA)
	}
	best, dist := 0, math.Inf(1)
	for i, b := range p.Bands {
		if d := math.Abs(math.Log(ratingA / b.RatingA)); d < dist {
			best, dist = i, d
		}
	}
	return &p.Bands[best], nil
}

// Band returns the band a fuse rated ratingA uses, and false when the
// profile has none for it.
func (p Profile) Band(ratingA float64) (Band, bool) {
	b, err := p.band(ratingA)
	if err != nil {
		return Band{}, false
	}
	return *b, true
}

// Supports says whether the profile models a fuse rated ratingA.
func (p Profile) Supports(ratingA float64) bool {
	_, err := p.band(ratingA)
	return err == nil
}

// TripSeconds is the time to melt from cold at multiple × the rating, at the
// reference band and ambient; +Inf if it never melts there.
func (p Profile) TripSeconds(multiple float64) float64 {
	return p.MeltSeconds(multiple, p.RefRatingA, RefAmbientC)
}

// MeltSeconds is the time to melt from cold at multiple × a rating of
// ratingA in ambientC; +Inf if it never melts or the rating has no band.
func (p Profile) MeltSeconds(multiple, ratingA, ambientC float64) float64 {
	b, err := p.band(ratingA)
	if err != nil {
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
	// AmbientC is the cabinet's air; nil means DefaultAmbientC. 0 is 0 °C.
	AmbientC *float64
	// Damage is the element's way from ambient to melting, 0 to 1; Step
	// sets it.
	Damage float64
	Blown  bool
}

// Ambient returns a pointer to c, for Phase.AmbientC.
func Ambient(c float64) *float64 { return &c }

func (ph *Phase) ambient() float64 {
	if ph.AmbientC == nil {
		return DefaultAmbientC
	}
	return *ph.AmbientC
}

// Status describes a phase after a step.
type Status struct {
	Ratio            float64 `json:"ratio"`             // current over the rating
	TripSeconds      float64 `json:"trip_s"`            // time to melt at this ratio from cold; 0 if never
	RemainingSeconds float64 `json:"remaining_s"`       // time to melt at this ratio from now; 0 if never
	Damage           float64 `json:"damage"`            // the element's way from ambient to melting
	Active           bool    `json:"active"`            // it will melt if the current stays
	Invalid          string  `json:"invalid,omitempty"` // why the step was refused; the state is unchanged
}

// Step advances one phase by dt seconds at amps through a fuse rated
// ratingA. The sign of amps does not matter, and an infinite current blows
// the fuse at once. The step is exact for a current held over dt, so the
// result does not depend on the step size; dt = 0 changes nothing and asks
// how long the fuse has left at that current.
//
// Step refuses, leaving the phase as it was and saying why in
// Status.Invalid: a NaN current, a negative, NaN or infinite dt, an ambient
// that is not finite or not below the melting point, and a rating the
// profile does not model (the profile without a model, manual, refuses
// nothing and never blows).
func (p Profile) Step(ph *Phase, amps, ratingA, dt float64) Status {
	if len(p.Bands) == 0 {
		return Status{Damage: ph.Damage}
	}
	b, err := p.band(ratingA)
	switch {
	case err != nil:
		return Status{Damage: ph.Damage, Invalid: err.Error()}
	case math.IsNaN(amps):
		return Status{Damage: ph.Damage, Invalid: "current is NaN"}
	case !(dt >= 0) || math.IsInf(dt, 1):
		return Status{Damage: ph.Damage, Invalid: fmt.Sprintf("step of %v s", dt)}
	}
	ambient := ph.ambient()
	if math.IsNaN(ambient) || math.IsInf(ambient, 0) || ambient >= MeltC {
		return Status{Damage: ph.Damage, Invalid: fmt.Sprintf("ambient of %v °C", ambient)}
	}
	if ph.Blown {
		ph.Damage = 1
		return Status{Damage: 1}
	}
	u := math.Abs(amps) / ratingA
	st := Status{Ratio: u}
	if math.IsInf(u*u, 1) {
		// A current whose square overflows (or an infinite one) melts the
		// fuse at once; the ratio stays finite for JSON.
		ph.Blown, ph.Damage = true, 1
		st.Ratio, st.Damage = math.MaxFloat64, 1
		return st
	}
	melt := b.sys.meltRise(ambient)
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

// Preheat puts the phase in the steady state of a current held for ever in
// its ambient, as a fuse that has carried the evening's load: a scenario
// that starts mid-evening starts warm, not cold. It refuses a blown fuse
// (clear Blown and Theta to replace one), a current that would melt the
// fuse before reaching that state, and anything Step refuses, leaving the
// phase as it was.
func (p Profile) Preheat(ph *Phase, amps, ratingA float64) error {
	if len(p.Bands) == 0 {
		return fmt.Errorf("%s has no fuse model", p.ID)
	}
	if ph.Blown {
		return fmt.Errorf("the fuse is blown")
	}
	if st := p.Step(&Phase{AmbientC: ph.AmbientC}, amps, ratingA, 0); st.Invalid != "" {
		return fmt.Errorf("%s", st.Invalid)
	}
	b, _ := p.band(ratingA)
	u := math.Abs(amps) / ratingA
	inf, _ := b.sys.modes([3]float64{}, u)
	if inf[0] >= b.sys.meltRise(ph.ambient()) {
		return fmt.Errorf("%.3g × %g A melts the fuse before it settles", u, ratingA)
	}
	ph.Theta = inf
	ph.Damage = inf[0] / b.sys.meltRise(ph.ambient())
	return nil
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
			for i := 0; i < 200 && hi-lo > 1e-12*hi; i++ {
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
