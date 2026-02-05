package main

import (
	"math"
	"time"
)

const (
	FuseProfileManual = "manual"
	FuseProfileIecGg  = "iec_gg"
	FuseProfileIecGm  = "iec_gm"
	FuseProfileIecAm  = "iec_am"
	FuseProfileIecGr  = "iec_gr"
	FuseProfileIecGs  = "iec_gs"
)

type FuseCurvePoint struct {
	Multiple float64
	Seconds  float64
}

type FuseProfileDefinition struct {
	ID              string
	Label           string
	Curve           []FuseCurvePoint
	CooldownSeconds float64
	MinTripSeconds  float64
	TripStartRatio  float64
}

type FusePhaseOverload struct {
	Ratio            float64 `json:"ratio"`
	TripSeconds      float64 `json:"trip_s"`
	RemainingSeconds float64 `json:"remaining_s"`
	Damage           float64 `json:"damage"`
	Active           bool    `json:"active"`
}

type FuseOverload struct {
	L1 FusePhaseOverload `json:"l1"`
	L2 FusePhaseOverload `json:"l2"`
	L3 FusePhaseOverload `json:"l3"`
}

var fuseProfiles = map[string]FuseProfileDefinition{
	FuseProfileManual: {
		ID:    FuseProfileManual,
		Label: "Manual",
	},
	FuseProfileIecGg: {
		ID:    FuseProfileIecGg,
		Label: "IEC 60269 gG/gL (general purpose)",
		Curve: []FuseCurvePoint{
			{Multiple: 1.25, Seconds: 3600},
			{Multiple: 1.6, Seconds: 1800},
			{Multiple: 2.0, Seconds: 600},
			{Multiple: 3.0, Seconds: 120},
			{Multiple: 5.0, Seconds: 20},
			{Multiple: 10.0, Seconds: 3},
			{Multiple: 20.0, Seconds: 0.5},
		},
		CooldownSeconds: 300,
		MinTripSeconds:  0.05,
		TripStartRatio:  1,
	},
	FuseProfileIecGm: {
		ID:    FuseProfileIecGm,
		Label: "IEC 60269 gM (motor, dual-rated) - UK common",
		Curve: []FuseCurvePoint{
			{Multiple: 1.25, Seconds: 7200},
			{Multiple: 1.6, Seconds: 3600},
			{Multiple: 2.0, Seconds: 1200},
			{Multiple: 3.0, Seconds: 240},
			{Multiple: 5.0, Seconds: 40},
			{Multiple: 10.0, Seconds: 6},
			{Multiple: 20.0, Seconds: 1},
		},
		CooldownSeconds: 420,
		MinTripSeconds:  0.05,
		TripStartRatio:  1,
	},
	FuseProfileIecAm: {
		ID:    FuseProfileIecAm,
		Label: "IEC 60269 aM (motor, short-circuit)",
		// Partial-range curve: trip only at higher multiples.
		Curve: []FuseCurvePoint{
			{Multiple: 4.0, Seconds: 60},
			{Multiple: 6.3, Seconds: 60},
			{Multiple: 8.0, Seconds: 0.5},
			{Multiple: 10.0, Seconds: 0.2},
			{Multiple: 19.0, Seconds: 0.18},
		},
		CooldownSeconds: 600,
		MinTripSeconds:  0.05,
		TripStartRatio:  4,
	},
	FuseProfileIecGr: {
		ID:    FuseProfileIecGr,
		Label: "IEC 60269 gR (semiconductor, fast)",
		Curve: []FuseCurvePoint{
			{Multiple: 1.25, Seconds: 1080},
			{Multiple: 1.6, Seconds: 540},
			{Multiple: 2.0, Seconds: 180},
			{Multiple: 3.0, Seconds: 36},
			{Multiple: 5.0, Seconds: 6},
			{Multiple: 10.0, Seconds: 0.9},
			{Multiple: 20.0, Seconds: 0.15},
		},
		CooldownSeconds: 180,
		MinTripSeconds:  0.02,
		TripStartRatio:  1,
	},
	FuseProfileIecGs: {
		ID:    FuseProfileIecGs,
		Label: "IEC 60269 gS (semiconductor, full-range)",
		Curve: []FuseCurvePoint{
			{Multiple: 1.25, Seconds: 1800},
			{Multiple: 1.6, Seconds: 900},
			{Multiple: 2.0, Seconds: 300},
			{Multiple: 3.0, Seconds: 60},
			{Multiple: 5.0, Seconds: 10},
			{Multiple: 10.0, Seconds: 1.5},
			{Multiple: 20.0, Seconds: 0.25},
		},
		CooldownSeconds: 240,
		MinTripSeconds:  0.03,
		TripStartRatio:  1,
	},
}

func resolveFuseProfile(id string) FuseProfileDefinition {
	if profile, ok := fuseProfiles[id]; ok {
		return profile
	}
	return fuseProfiles[FuseProfileManual]
}

func fuseTripTimeSeconds(profile FuseProfileDefinition, multiple float64) float64 {
	if multiple <= 1 {
		return math.Inf(1)
	}
	if len(profile.Curve) == 0 {
		return math.Inf(1)
	}

	points := profile.Curve

	if multiple <= points[0].Multiple {
		return points[0].Seconds
	}

	for i := 1; i < len(points); i++ {
		if multiple <= points[i].Multiple {
			return logLogInterpolate(multiple, points[i-1], points[i])
		}
	}

	// Extrapolate using the last segment slope for very high currents.
	last := points[len(points)-1]
	prev := points[len(points)-2]
	return logLogInterpolate(multiple, prev, last)
}

func logLogInterpolate(x float64, a, b FuseCurvePoint) float64 {
	if x <= 0 || a.Multiple <= 0 || b.Multiple <= 0 || a.Seconds <= 0 || b.Seconds <= 0 {
		return b.Seconds
	}
	lx := math.Log10(x)
	lx1 := math.Log10(a.Multiple)
	lx2 := math.Log10(b.Multiple)
	if lx2 == lx1 {
		return b.Seconds
	}
	ly1 := math.Log10(a.Seconds)
	ly2 := math.Log10(b.Seconds)
	t := (lx - lx1) / (lx2 - lx1)
	return math.Pow(10, ly1+(ly2-ly1)*t)
}

func updateFuseState(site *Site, phasePowers PhasePowers, simDelta time.Duration) PhaseFuses {
	simSeconds := simDelta.Seconds()
	if simSeconds < 0 {
		simSeconds = 0
	}

	site.mu.Lock()
	defer site.mu.Unlock()

	changed := false
	profile := resolveFuseProfile(site.FuseProfile)
	if _, ok := fuseProfiles[site.FuseProfile]; !ok {
		site.FuseProfile = profile.ID
		changed = true
	}

	if profile.ID == FuseProfileManual {
		site.FuseOverload = FuseOverload{}
		if changed {
			markStateDirty()
		}
		return site.FuseBlown
	}

	fuseSize := site.FuseSize
	if fuseSize <= 0 {
		fuseSize = 16
	}

	damage := site.FuseDamage
	fuseBlown := site.FuseBlown
	overload := FuseOverload{}

	updatePhase := func(power float64, blown bool, damage *float64) (FusePhaseOverload, bool) {
		if blown {
			if *damage < 1 {
				*damage = 1
			}
			return FusePhaseOverload{Damage: *damage}, false
		}

		current := math.Abs(power) / 230.0
		ratio := 0.0
		if fuseSize > 0 {
			ratio = current / float64(fuseSize)
		}

		startRatio := profile.TripStartRatio
		if startRatio <= 0 {
			startRatio = 1
		}

		if ratio <= startRatio {
			if profile.CooldownSeconds > 0 && simSeconds > 0 {
				*damage -= simSeconds / profile.CooldownSeconds
				if *damage < 0 {
					*damage = 0
				}
			}
			return FusePhaseOverload{Ratio: ratio, Damage: *damage, Active: false}, false
		}

		tripSeconds := fuseTripTimeSeconds(profile, ratio)
		if tripSeconds <= 0 || math.IsInf(tripSeconds, 1) {
			return FusePhaseOverload{Ratio: ratio, Damage: *damage, Active: false}, false
		}
		if profile.MinTripSeconds > 0 && tripSeconds < profile.MinTripSeconds {
			tripSeconds = profile.MinTripSeconds
		}

		if simSeconds > 0 {
			*damage += simSeconds / tripSeconds
			if *damage > 1 {
				*damage = 1
			}
		}

		if *damage >= 1 {
			return FusePhaseOverload{
				Ratio:            ratio,
				TripSeconds:      tripSeconds,
				RemainingSeconds: 0,
				Damage:           *damage,
				Active:           false,
			}, true
		}

		remaining := (1 - *damage) * tripSeconds
		return FusePhaseOverload{
			Ratio:            ratio,
			TripSeconds:      tripSeconds,
			RemainingSeconds: remaining,
			Damage:           *damage,
			Active:           true,
		}, false
	}

	status, blew := updatePhase(phasePowers.L1, fuseBlown.L1, &damage.L1)
	overload.L1 = status
	if blew {
		fuseBlown.L1 = true
		changed = true
	}

	status, blew = updatePhase(phasePowers.L2, fuseBlown.L2, &damage.L2)
	overload.L2 = status
	if blew {
		fuseBlown.L2 = true
		changed = true
	}

	status, blew = updatePhase(phasePowers.L3, fuseBlown.L3, &damage.L3)
	overload.L3 = status
	if blew {
		fuseBlown.L3 = true
		changed = true
	}

	site.FuseDamage = damage
	site.FuseOverload = overload

	if fuseBlown != site.FuseBlown {
		site.FuseBlown = fuseBlown
		changed = true
	}

	if changed {
		markStateDirty()
	}

	return site.FuseBlown
}
