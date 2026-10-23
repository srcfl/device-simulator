package main

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// runRandom draws a timeline for a headless run from a seed, within the
// house's physical bounds: house load per phase stays under the fuse less
// 2 A, so the house alone never overloads it and anything over the limit
// is the EMS's doing or a step it must answer. The same seed and spec give
// the same timeline; timeline.json in the output holds it for replay.
type runRandom struct {
	Seed      int64    `json:"seed"`
	DurationS float64  `json:"duration_s"`            // events fall in [30 s, duration_s - 30 s]
	GapS      float64  `json:"gap_s,omitempty"`       // mean time between events; default 40
	Kinds     []string `json:"kinds,omitempty"`       // which kinds may happen; default all
	MaxPVW    float64  `json:"max_pv_w,omitempty"`    // PV ceiling; default 8 000 W
	MinSoCPct float64  `json:"min_soc_pct,omitempty"` // the battery's floor; default 5
}

// randomKinds are the events a random timeline draws, with their weights.
var randomKinds = []struct {
	name   string
	weight int
}{
	{"loads_step", 6}, {"loads_drift", 3}, {"loads_spike", 4}, {"pv_cloud", 3}, {"soc_floor", 1},
	{"car_limit", 2}, {"silence", 1}, {"offline", 1}, {"reconnect", 2}, {"meter_late", 2},
	{"meter_glitch", 1}, {"signed_currents", 1}, {"load_balancer", 1},
}

// randomize fills the unset timing and quirks of the spec's devices from the
// seed and returns the events to add to its timeline.
func randomize(spec *runSpec) []runEvent {
	rr := spec.Random
	rng := rand.New(rand.NewSource(rr.Seed))
	gap := rr.GapS
	if gap <= 0 {
		gap = 40
	}
	maxPV := rr.MaxPVW
	if maxPV <= 0 {
		maxPV = 8000
	}
	minSoC := rr.MinSoCPct
	if minSoC <= 0 {
		minSoC = 5
	}
	fuse := float64(spec.Site.FuseA)
	if fuse <= 0 {
		fuse = 16
	}
	maxA := fuse - 2
	allowed := map[string]bool{}
	for _, k := range rr.Kinds {
		allowed[k] = true
	}
	var kinds []string
	total := 0
	for _, k := range randomKinds {
		if len(allowed) == 0 || allowed[k.name] {
			for i := 0; i < k.weight; i++ {
				kinds = append(kinds, k.name)
			}
			total += k.weight
		}
	}

	// The devices' timing: whatever the spec leaves open.
	var chargers, inverters []string
	for i := range spec.Devices {
		d := &spec.Devices[i]
		if d.Driver == "ocpp" {
			chargers = append(chargers, d.ID)
			if d.CommandResponseS == nil {
				d.CommandResponseS = ptr([]float64{0, 5, 40, 60}[rng.Intn(4)])
			}
			if d.StartupDelayS == nil {
				d.StartupDelayS = ptr([]float64{0, 0, 5, 30}[rng.Intn(4)])
			}
			if d.CarPhases == 0 && rng.Intn(5) == 0 {
				d.CarPhases = 1
			}
		} else {
			inverters = append(inverters, d.ID)
		}
	}
	if spec.Site.SignedPhaseCurrents == nil {
		spec.Site.SignedPhaseCurrents = ptr(rng.Intn(2) == 0)
	}
	if len(kinds) == 0 {
		return nil
	}

	// House load per phase, in amperes, as the timeline moves it.
	var level [3]float64
	for k, w := range spec.Site.HouseW {
		level[k] = math.Min(maxA, math.Max(0.3, w/230))
	}
	pick := func(xs []string) string {
		if len(xs) == 0 {
			return ""
		}
		return xs[rng.Intn(len(xs))]
	}
	between := func(lo, hi float64) float64 { return math.Round((lo+rng.Float64()*(hi-lo))*10) / 10 }
	// later is when a follow-up lands: never after the run.
	later := func(at float64) float64 { return math.Min(at, rr.DurationS) }
	loads := func(at float64) runEvent {
		a := level
		return runEvent{AtS: at, Do: "loads", A: &a}
	}

	var evs []runEvent
	end := rr.DurationS - 30
	for t := 30 + rng.ExpFloat64()*gap; t < end; t += math.Min(3*gap, math.Max(5, rng.ExpFloat64()*gap)) {
		t = math.Round(t*10) / 10
		switch kinds[rng.Intn(total)] {
		case "loads_step":
			if rng.Intn(2) == 0 {
				level[rng.Intn(3)] = between(0.3, maxA)
			} else {
				for k := range level {
					level[k] = between(0.3, maxA)
				}
			}
			evs = append(evs, loads(t))
		case "loads_drift":
			for i := 0; i < 3+rng.Intn(4); i++ {
				for k := range level {
					level[k] = math.Min(maxA, math.Max(0.3, level[k]+between(-1.5, 1.5)))
				}
				evs = append(evs, loads(later(t+float64(10*i))))
			}
		case "loads_spike":
			k := rng.Intn(3)
			if room := maxA - level[k]; room > 1 {
				before := level[k]
				level[k] += between(1, room)
				evs = append(evs, loads(t))
				level[k] = before
				evs = append(evs, loads(later(t+between(5, 30))))
			}
		case "pv_cloud":
			if inv := pick(inverters); inv != "" {
				sun := between(0.4, 1) * maxPV
				evs = append(evs, runEvent{AtS: t, Do: "pv", Device: inv, PVW: ptr(math.Round(sun * between(0.1, 0.6)))},
					runEvent{AtS: later(t + between(10, 90)), Do: "pv", Device: inv, PVW: ptr(math.Round(sun))})
			}
		case "soc_floor":
			if inv := pick(inverters); inv != "" {
				evs = append(evs, runEvent{AtS: t, Do: "soc", Device: inv, Pct: ptr(minSoC + between(0.5, 3))})
			}
		case "car_limit":
			if cp := pick(chargers); cp != "" {
				a := 0.0
				if rng.Intn(3) > 0 {
					a = between(6, 16)
				}
				evs = append(evs, runEvent{AtS: t, Do: "car", Device: cp, MaxA: ptr(a)})
			}
		case "silence":
			if inv := pick(inverters); inv != "" {
				evs = append(evs, runEvent{AtS: t, Do: "silence", Device: inv, ForS: between(5, 60)})
			}
		case "offline":
			if inv := pick(inverters); inv != "" {
				evs = append(evs, runEvent{AtS: t, Do: "offline", Device: inv, ForS: between(20, 90)})
			}
		case "reconnect":
			if cp := pick(chargers); cp != "" {
				evs = append(evs, runEvent{AtS: t, Do: "reconnect", Device: cp, ForS: between(5, 30)})
			}
		case "meter_late":
			evs = append(evs, runEvent{AtS: t, Do: "meter_late", ForS: between(5, 40)})
		case "meter_glitch":
			evs = append(evs, runEvent{AtS: t, Do: "meter_glitch"})
		case "signed_currents":
			evs = append(evs, runEvent{AtS: t, Do: "signed_currents", On: ptr(rng.Intn(2) == 0)})
		case "load_balancer":
			if cp := pick(chargers); cp != "" {
				evs = append(evs, runEvent{AtS: t, Do: "load_balancer", Device: cp, MaxA: ptr(between(6, 16))},
					runEvent{AtS: later(t + between(30, 180)), Do: "load_balancer", Device: cp, MaxA: ptr(0.0)})
			}
		}
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].AtS < evs[j].AtS })
	return evs
}

func describeRandom(rr *runRandom, n int) string {
	return fmt.Sprintf("random seed %d: %d events over %.0f s", rr.Seed, n, rr.DurationS)
}

func ptr[T any](v T) *T { return &v }
