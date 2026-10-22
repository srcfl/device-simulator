package main

import (
	"math"
	"time"

	"github.com/srcfl/device-simulator/fuse"
)

// The fuse model lives in package fuse, which an EMS's own simulation can
// import; these names keep the site code and its JSON as they were.
const (
	FuseProfileManual = fuse.Manual
	FuseProfileIecGg  = fuse.IecGg
	FuseProfileIecGm  = fuse.IecGm
	FuseProfileIecAm  = fuse.IecAm
	FuseProfileIecGr  = fuse.IecGr
	FuseProfileIecGs  = fuse.IecGs
)

type FusePhaseOverload = fuse.Status

type FuseOverload struct {
	L1 FusePhaseOverload `json:"l1"`
	L2 FusePhaseOverload `json:"l2"`
	L3 FusePhaseOverload `json:"l3"`
}

var fuseProfiles = func() map[string]fuse.Profile {
	m := map[string]fuse.Profile{}
	for _, p := range fuse.All() {
		m[p.ID] = p
	}
	return m
}()

func resolveFuseProfile(id string) fuse.Profile {
	if profile, ok := fuseProfiles[id]; ok {
		return profile
	}
	return fuseProfiles[FuseProfileManual]
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
		ph := fuse.Phase{Damage: *damage, Blown: blown}
		status := profile.Step(&ph, math.Abs(power)/230.0, float64(fuseSize), simSeconds)
		*damage = ph.Damage
		return status, ph.Blown && !blown
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
