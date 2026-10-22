// Package fuse models a fuse on each phase of a site's main supply: the time
// it takes to blow at a multiple of its rating (IEC 60269 time-current
// curves) and the damage a phase builds up and sheds as the current moves.
//
// Every second above the rating spends 1/t of the fuse's life, with t the
// curve's time to blow at that multiple; below it the damage recovers over
// the profile's cooldown. A phase blows when its damage reaches 1. The curve
// blows slowly from just above the rating, so it is stricter than a real gG
// fuse below 1.25 × its rating.
//
// It has no dependencies, so an EMS's own fast simulation can import the
// same curves the device simulator uses.
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

// Point is one point of a time-current curve: at Multiple × the rating the
// fuse blows after Seconds.
type Point struct {
	Multiple float64
	Seconds  float64
}

// Profile is a fuse type.
type Profile struct {
	ID    string
	Label string
	Curve []Point
	// CooldownS is how long damage 1 takes to recover at or below the start ratio.
	CooldownS float64
	// MinTripS is the shortest time to blow.
	MinTripS float64
	// TripStartRatio is the multiple of the rating above which damage builds.
	TripStartRatio float64
}

var profiles = map[string]Profile{
	Manual: {ID: Manual, Label: "Manual"},
	IecGg: {
		ID:    IecGg,
		Label: "IEC 60269 gG/gL (general purpose)",
		Curve: []Point{
			{1.25, 3600}, {1.6, 1800}, {2.0, 600}, {3.0, 120}, {5.0, 20}, {10.0, 3}, {20.0, 0.5},
		},
		CooldownS: 300, MinTripS: 0.05, TripStartRatio: 1,
	},
	IecGm: {
		ID:    IecGm,
		Label: "IEC 60269 gM (motor, dual-rated) - UK common",
		Curve: []Point{
			{1.25, 7200}, {1.6, 3600}, {2.0, 1200}, {3.0, 240}, {5.0, 40}, {10.0, 6}, {20.0, 1},
		},
		CooldownS: 420, MinTripS: 0.05, TripStartRatio: 1,
	},
	IecAm: {
		ID:    IecAm,
		Label: "IEC 60269 aM (motor, short-circuit)",
		// Partial-range curve: trips only at higher multiples.
		Curve: []Point{
			{4.0, 60}, {6.3, 60}, {8.0, 0.5}, {10.0, 0.2}, {19.0, 0.18},
		},
		CooldownS: 600, MinTripS: 0.05, TripStartRatio: 4,
	},
	IecGr: {
		ID:    IecGr,
		Label: "IEC 60269 gR (semiconductor, fast)",
		Curve: []Point{
			{1.25, 1080}, {1.6, 540}, {2.0, 180}, {3.0, 36}, {5.0, 6}, {10.0, 0.9}, {20.0, 0.15},
		},
		CooldownS: 180, MinTripS: 0.02, TripStartRatio: 1,
	},
	IecGs: {
		ID:    IecGs,
		Label: "IEC 60269 gS (semiconductor, full-range)",
		Curve: []Point{
			{1.25, 1800}, {1.6, 900}, {2.0, 300}, {3.0, 60}, {5.0, 10}, {10.0, 1.5}, {20.0, 0.25},
		},
		CooldownS: 240, MinTripS: 0.03, TripStartRatio: 1,
	},
}

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

// TripSeconds is the time to blow at multiple × the rating, interpolated on
// log-log axes, with the last segment extended above its end. It is +Inf at
// or below 1 ×, and for a profile without a curve.
func (p Profile) TripSeconds(multiple float64) float64 {
	if multiple <= 1 || len(p.Curve) == 0 {
		return math.Inf(1)
	}
	pts := p.Curve
	if multiple <= pts[0].Multiple {
		return pts[0].Seconds
	}
	for i := 1; i < len(pts); i++ {
		if multiple <= pts[i].Multiple {
			return logLog(multiple, pts[i-1], pts[i])
		}
	}
	return logLog(multiple, pts[len(pts)-2], pts[len(pts)-1])
}

func logLog(x float64, a, b Point) float64 {
	if x <= 0 || a.Multiple <= 0 || b.Multiple <= 0 || a.Seconds <= 0 || b.Seconds <= 0 {
		return b.Seconds
	}
	lx, lx1, lx2 := math.Log10(x), math.Log10(a.Multiple), math.Log10(b.Multiple)
	if lx2 == lx1 {
		return b.Seconds
	}
	ly1, ly2 := math.Log10(a.Seconds), math.Log10(b.Seconds)
	return math.Pow(10, ly1+(ly2-ly1)*(lx-lx1)/(lx2-lx1))
}

// Phase is the state of one phase's fuse.
type Phase struct {
	Damage float64 // 0 new, 1 blown
	Blown  bool
}

// Status describes a phase after a step.
type Status struct {
	Ratio            float64 `json:"ratio"`  // current over the rating
	TripSeconds      float64 `json:"trip_s"` // time to blow at this ratio from new
	RemainingSeconds float64 `json:"remaining_s"`
	Damage           float64 `json:"damage"`
	Active           bool    `json:"active"` // building damage
}

// Step advances one phase by dt seconds at amps through a fuse rated
// ratingA. The sign of amps does not matter: export heats a fuse too. A
// profile without a curve leaves the phase as it is.
func (p Profile) Step(ph *Phase, amps, ratingA, dt float64) Status {
	if len(p.Curve) == 0 {
		return Status{Damage: ph.Damage}
	}
	if dt < 0 {
		dt = 0
	}
	if ph.Blown {
		ph.Damage = math.Max(ph.Damage, 1)
		return Status{Damage: ph.Damage}
	}
	ratio := 0.0
	if ratingA > 0 {
		ratio = math.Abs(amps) / ratingA
	}
	start := p.TripStartRatio
	if start <= 0 {
		start = 1
	}
	if ratio <= start {
		if p.CooldownS > 0 {
			ph.Damage = math.Max(0, ph.Damage-dt/p.CooldownS)
		}
		return Status{Ratio: ratio, Damage: ph.Damage}
	}
	trip := p.TripSeconds(ratio)
	if trip <= 0 || math.IsInf(trip, 1) {
		return Status{Ratio: ratio, Damage: ph.Damage}
	}
	trip = math.Max(trip, p.MinTripS)
	ph.Damage = math.Min(1, ph.Damage+dt/trip)
	if ph.Damage >= 1 {
		ph.Blown = true
		return Status{Ratio: ratio, TripSeconds: trip, Damage: ph.Damage}
	}
	return Status{Ratio: ratio, TripSeconds: trip, RemainingSeconds: (1 - ph.Damage) * trip, Damage: ph.Damage, Active: true}
}
