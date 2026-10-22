package main

import (
	"math"
	"testing"
	"time"

	"github.com/srcfl/device-simulator/internal/ocpp/ocpptest"
)

// testSite is a site whose meter is never served over Modbus: the test
// calls calculateAndUpdateMeter itself.
func testSite(t *testing.T, houseW float64, members ...*Simulator) (*Site, *Simulator) {
	t.Helper()
	meter, err := createSimulator("energy_meter")
	if err != nil {
		t.Fatal(err)
	}
	meter.Running = true
	ids := make([]int, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	t.Cleanup(func() {
		// Not deleteSimulator: it expects the servers main() starts.
		simulatorsMu.Lock()
		defer simulatorsMu.Unlock()
		kept := simulators[:0]
		for _, sim := range simulators {
			if sim != meter && !containsInt(ids, sim.ID) {
				kept = append(kept, sim)
			}
		}
		simulators = kept
	})
	site := &Site{
		ID:             -1,
		MeterID:        meter.ID,
		MeterIDs:       []int{meter.ID},
		SimulatorIDs:   ids,
		FuseSize:       25,
		FuseProfile:    FuseProfileManual,
		PhaseFactors:   PhaseFactors{L1: 1.0 / 3, L2: 1.0 / 3, L3: 1.0 / 3},
		SitePhaseLoads: PhaseLoads{L1: houseW, L2: houseW, L3: houseW},
	}
	return site, meter
}

// A car on a 1-phase profile from a charger that can switch phases loads L1
// on the site meter, not a third of it on each phase. The default charger
// is rated 7.7 kW, so 11 A on one phase.
func TestSiteMeterPutsOnePhaseChargerOnL1(t *testing.T) {
	csms := ocpptest.New(t)
	charger, err := createSimulator("ocpp_charger")
	if err != nil {
		t.Fatal(err)
	}
	cp := charger.OCPPServer
	cp.ApplyConfiguration(map[string]string{"ConnectorSwitch3to1PhaseSupported": "true"})
	cp.SetOCPPURL(csms.URL(""))
	if err := cp.Start(); err != nil {
		t.Fatal(err)
	}
	defer cp.Stop()
	charger.Running = true
	site, meter := testSite(t, 230, charger)

	ocpptest.WaitFor(t, "BootNotification", 5*time.Second, func() bool { return csms.Sent("BootNotification") > 0 })
	if err := cp.SetValue("evConnected", true); err != nil {
		t.Fatal(err)
	}
	ocpptest.WaitFor(t, "StartTransaction", 5*time.Second, func() bool { return csms.Sent("StartTransaction") > 0 })
	csms.Call("SetChargingProfile", map[string]interface{}{
		"connectorId": 1,
		"csChargingProfiles": map[string]interface{}{
			"chargingProfileId": 1, "stackLevel": 0,
			"chargingProfilePurpose": "TxProfile", "chargingProfileKind": "Relative",
			"chargingSchedule": map[string]interface{}{
				"chargingRateUnit":       "A",
				"chargingSchedulePeriod": []map[string]interface{}{{"startPeriod": 0, "limit": 10, "numberPhases": 1}},
			},
		},
	})
	ocpptest.WaitFor(t, "a 1-phase draw", 5*time.Second, func() bool {
		p := cp.PhasePowers()
		return p[0] > 2000 && p[1] == 0
	})

	calculateAndUpdateMeter(site, 0)
	l1 := getSemanticValue(meter, "meter_l1_power")
	l2 := getSemanticValue(meter, "meter_l2_power")
	l3 := getSemanticValue(meter, "meter_l3_power")
	// 10 A at the charger's 227-230 V.
	if math.Abs(l2-230) > 1 || math.Abs(l3-230) > 1 || l1 < 230+2270 || l1 > 230+2300 {
		t.Fatalf("meter L1/L2/L3 = %.0f/%.0f/%.0f W, want house 230 W each plus ~2290 W on L1", l1, l2, l3)
	}
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
