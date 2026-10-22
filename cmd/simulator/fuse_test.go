package main

import (
	"testing"
	"time"
)

// The site's fuse is package fuse's iec_gg: 2 × a 25 A rating on L1 blows
// it after 600 s, and the meter's other phases stay whole.
func TestSiteFuseBlowsOnTheGGCurve(t *testing.T) {
	site := &Site{FuseSize: 25, FuseProfile: FuseProfileIecGg}
	at := 0
	for s := 1; s <= 700 && at == 0; s++ {
		if updateFuseState(site, PhasePowers{L1: 50 * 230, L2: 1000, L3: -1000}, time.Second).L1 {
			at = s
		}
	}
	if at != 600 || site.FuseBlown.L2 || site.FuseBlown.L3 {
		t.Fatalf("L1 blew after %d s (want 600), L2/L3 %v/%v", at, site.FuseBlown.L2, site.FuseBlown.L3)
	}
	if site.FuseOverload.L1.Damage != 1 || site.FuseOverload.L2.Active {
		t.Fatalf("overload %+v", site.FuseOverload)
	}
}
