package main

import (
	"math"
	"testing"
	"time"

	"github.com/srcfl/device-simulator/fuse"
)

// The site's fuse is package fuse's iec_gg: 2 × a 25 A rating on L1 blows
// it after the band's cold time in the cabinet's 25 °C, and the meter's
// other phases stay whole.
func TestSiteFuseBlowsOnTheGGCurve(t *testing.T) {
	site := &Site{FuseSize: 25, FuseProfile: FuseProfileIecGg}
	gg, _ := fuse.Lookup(fuse.IecGg)
	want := gg.MeltSeconds(2, 25, fuse.DefaultAmbientC)
	at := 0
	for s := 1; s <= 700 && at == 0; s++ {
		if updateFuseState(site, PhasePowers{L1: 50 * 230, L2: 1000, L3: -1000}, time.Second).L1 {
			at = s
		}
	}
	if math.Abs(float64(at)-want) > 1 || site.FuseBlown.L2 || site.FuseBlown.L3 {
		t.Fatalf("L1 blew after %d s (want %.1f), L2/L3 %v/%v", at, want, site.FuseBlown.L2, site.FuseBlown.L3)
	}
	if site.FuseOverload.L1.Damage != 1 || site.FuseOverload.L2.Active {
		t.Fatalf("overload %+v", site.FuseOverload)
	}
}

// The site keeps each phase's heat between ticks: a phase that has carried
// its rating for an hour blows at 2 × sooner than a cold one, and clearing
// a blown fuse starts it cold.
func TestSiteFuseRemembersItsHeat(t *testing.T) {
	blowAt := func(site *Site) int {
		for s := 1; s <= 700; s++ {
			if updateFuseState(site, PhasePowers{L1: 50 * 230}, time.Second).L1 {
				return s
			}
		}
		return 0
	}
	cold := blowAt(&Site{FuseSize: 25, FuseProfile: FuseProfileIecGg})
	warm := &Site{FuseSize: 25, FuseProfile: FuseProfileIecGg}
	for s := 0; s < 3600; s++ {
		updateFuseState(warm, PhasePowers{L1: 25 * 230}, time.Second)
	}
	if d := warm.FuseDamage.L1; d < 0.4 || d > 0.6 {
		t.Fatalf("after 1 h at the rating damage %.2f, want about half way to melting", d)
	}
	if hot := blowAt(warm); hot == 0 || hot >= cold*2/3 {
		t.Fatalf("2× after 1 h at the rating blew after %d s, cold after %d s", hot, cold)
	}
	warm.FuseBlown.L1, warm.FuseDamage.L1, warm.FuseHeat[0] = false, 0, fuse.Phase{}
	if again := blowAt(warm); again != cold {
		t.Fatalf("a cleared fuse blew after %d s, want the cold %d s", again, cold)
	}
}

// A site saved with a size the model does not cover says so on every phase
// and never blows; new sizes outside the bands are refused.
func TestUnsupportedFuseSize(t *testing.T) {
	site := &Site{FuseSize: 10, FuseProfile: FuseProfileIecGg}
	for s := 0; s < 60; s++ {
		updateFuseState(site, PhasePowers{L1: 40 * 230}, time.Second)
	}
	if site.FuseBlown.L1 || site.FuseOverload.L1.Invalid == "" {
		t.Fatalf("10 A at 40 A: blown %v, status %+v; want refused", site.FuseBlown.L1, site.FuseOverload.L1)
	}
	for _, a := range []int{16, 20, 25, 32, 35, 40, 50, 63} {
		if err := checkFuseSize(a); err != nil {
			t.Errorf("%d A refused: %v", a, err)
		}
	}
	for _, a := range []int{0, 10, 30, 80} {
		if checkFuseSize(a) == nil {
			t.Errorf("%d A accepted", a)
		}
	}
}
