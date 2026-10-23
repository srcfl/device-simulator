package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/srcfl/device-simulator/internal/ocpp/ocpptest"
)

// freeBase finds a base whose next 60 ports are free on 127.0.0.1. It draws
// bases below the ephemeral range, which the kernel hands out in turn and so
// may have used up near its top.
func freeBase(t *testing.T) int {
	t.Helper()
	for try := 0; try < 100; try++ {
		base := 20000 + rand.Intn(300)*100
		ok := true
		for p := base; p < base+60 && ok; p++ {
			l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
			if err != nil {
				ok = false
				continue
			}
			l.Close()
		}
		if ok {
			return base
		}
	}
	t.Fatal("no free port range")
	return 0
}

func itoa(n int) string { return strconv.Itoa(n) }

type planted struct {
	samples []plantSample
	marks   map[string]float64
	events  []runEvent
}

func readPlant(t *testing.T, out string) planted {
	t.Helper()
	f, err := os.Open(filepath.Join(out, "plant.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p := planted{marks: map[string]float64{}}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var line struct {
			plantSample
			Mark  string    `json:"mark"`
			Event *runEvent `json:"event"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("plant.jsonl: %v: %s", err, sc.Bytes())
		}
		switch {
		case line.Mark != "":
			p.marks[line.Mark] = line.Elapsed
		case line.Event != nil:
			p.events = append(p.events, *line.Event)
		default:
			p.samples = append(p.samples, line.plantSample)
		}
	}
	return p
}

func runFor(t *testing.T, spec runSpec) (*headlessRun, string) {
	t.Helper()
	out := t.TempDir()
	r, err := startRun(spec, freeBase(t), out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.end)
	return r, out
}

func waitStop(t *testing.T, r *headlessRun, within time.Duration) {
	t.Helper()
	select {
	case <-r.stop:
		r.end()
	case <-time.After(within):
		t.Fatal("the run did not end")
	}
}

// fuse_preheat_a starts the fuses warm and fuse_ambient_c sets the
// cabinet; a size the fuse profile does not model is refused. One run at a
// time: runs share the simulator's settings.
func TestRunFuseStartsWarm(t *testing.T) {
	t.Run("warm", func(t *testing.T) {
		r, _ := runFor(t, runSpec{Name: "warm", Site: runSiteSpec{FuseA: 25, HouseW: [3]float64{230, 230, 230},
			FuseAmbientC: ptr(30.0), FusePreheatA: &[3]float64{25, 12.5, 0}}})
		r.site.mu.RLock()
		heat, ambient := r.site.FuseHeat, r.site.FuseAmbientC
		r.site.mu.RUnlock()
		if heat[0].Damage < 0.4 || heat[1].Damage >= heat[0].Damage || heat[2].Damage != 0 || ambient == nil || *ambient != 30 {
			t.Fatalf("preheated at 25/12.5/0 A in 30 °C: damage %.2f/%.2f/%.2f, ambient %v", heat[0].Damage, heat[1].Damage, heat[2].Damage, ambient)
		}
	})
	t.Run("cold", func(t *testing.T) {
		r, _ := runFor(t, runSpec{Name: "cold", Site: runSiteSpec{FuseA: 25}})
		r.site.mu.RLock()
		defer r.site.mu.RUnlock()
		if d := r.site.FuseHeat[0].Damage; d != 0 {
			t.Errorf("a cold start has damage %.2f", d)
		}
	})
	t.Run("10 A gG", func(t *testing.T) {
		if r, err := startRun(runSpec{Name: "ten", Site: runSiteSpec{FuseA: 10}}, freeBase(t), t.TempDir()); err == nil {
			r.end()
			t.Error("a 10 A gG fuse was accepted")
		}
	})
}

// wired_phases reaches the charger.
func TestRunWiresAOnePhaseCharger(t *testing.T) {
	spec := runSpec{Name: "t", Site: runSiteSpec{FuseA: 25},
		Devices: []runDeviceSpec{{ID: "cp", Driver: "ocpp", OCPPURL: "ws://127.0.0.1:9/", MaxKW: 3.68, WiredPhases: 1}}}
	r, _ := runFor(t, spec)
	srv := r.devs["cp"].OCPPServer
	if srv == nil || !srv.WiredOnOnePhase() {
		t.Fatal("the charger is not wired on one phase")
	}
}

// "meter" names the site meter in ports.json, so no device may take it.
func TestRunRefusesTheMeterID(t *testing.T) {
	spec := runSpec{Name: "meter-id", Site: runSiteSpec{FuseA: 25},
		Devices: []runDeviceSpec{{ID: "meter", Driver: "sungrow", BatteryKWh: 10}}}
	if r, err := startRun(spec, freeBase(t), t.TempDir()); err == nil {
		r.end()
		t.Fatal("a device called meter was accepted")
	}
}

// A run builds its house from the spec, applies its timeline, records a
// sample a second and says where each device listens.
func TestRunRecordsTheHouse(t *testing.T) {
	spec := runSpec{Name: "t", DurationS: 4,
		Site: runSiteSpec{FuseA: 25, HouseW: [3]float64{690, 690, 690}},
		Devices: []runDeviceSpec{{ID: "inv", Driver: "sungrow", PVW: 1500,
			SoCPct: ptr(50.0), MinSoCPct: ptr(5.0), EMSControl: ptr(false)}},
		Timeline: []runEvent{{AtS: 1.5, Do: "loads", A: &[3]float64{10, 3, 3}}, {AtS: 2, Do: "mark", Label: "after"}}}
	r, out := runFor(t, spec)
	waitStop(t, r, 10*time.Second)

	var ports runPorts
	b, err := os.ReadFile(filepath.Join(out, "ports.json"))
	if err != nil || json.Unmarshal(b, &ports) != nil {
		t.Fatalf("ports.json: %v %s", err, b)
	}
	if ports.Devices["inv"].Modbus == 0 || ports.Devices["meter"].Modbus == 0 || ports.HTTP == 0 {
		t.Fatalf("ports %+v", ports)
	}
	p := readPlant(t, out)
	if len(p.samples) < 3 || len(p.events) != 1 || p.marks["after"] < 1.9 {
		t.Fatalf("%d samples, events %v, marks %v", len(p.samples), p.events, p.marks)
	}
	first, last := p.samples[0], p.samples[len(p.samples)-1]
	// House 690 W per phase less 1 500 W of PV spread evenly.
	if first.PhaseW[0] < 180 || first.PhaseW[0] > 200 {
		t.Errorf("before the step: %v W", first.PhaseW)
	}
	if last.PhaseW[0]-last.PhaseW[1] < 1600 || last.PhaseA[0] < 7 || last.Fuse.RatingA != 25 || last.Fuse.Profile != "iec_gg" {
		t.Errorf("after 10/3/3 A: %+v", last)
	}
	if _, err := os.Stat(filepath.Join(out, "result.json")); err != nil {
		t.Error(err)
	}
	if n := len(simulators); n != 0 {
		t.Errorf("%d simulators left after the run", n)
	}
}

// The harness can change the house while it runs, and stop it.
func TestRunTakesEventsOverHTTP(t *testing.T) {
	spec := runSpec{Name: "t", Site: runSiteSpec{FuseA: 16, HouseW: [3]float64{230, 230, 230}},
		Devices: []runDeviceSpec{{ID: "inv", Driver: "sungrow", EMSControl: ptr(false)}}}
	r, out := runFor(t, spec)
	post := func(path, body string) {
		t.Helper()
		resp, err := http.Post("http://127.0.0.1:"+itoa(r.base)+path, "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s: %s", path, resp.Status)
		}
	}
	time.Sleep(1200 * time.Millisecond)
	post("/api/run/mark", `{"label": "step"}`)
	post("/api/run/event", `{"do": "loads", "w": [2300, 230, 230]}`)
	time.Sleep(1500 * time.Millisecond)
	post("/api/run/event", `{"do": "stop"}`)
	waitStop(t, r, 5*time.Second)
	p := readPlant(t, out)
	if _, ok := p.marks["step"]; !ok || len(p.events) != 2 {
		t.Fatalf("marks %v, events %v", p.marks, p.events)
	}
	if last := p.samples[len(p.samples)-1]; last.PhaseW[0] < 2200 {
		t.Fatalf("after the posted step: %v W", last.PhaseW)
	}
	resp, err := http.Get("http://127.0.0.1:" + itoa(r.base) + "/api/update/check")
	if err == nil {
		resp.Body.Close()
	}
}

// A charger dials the Central System in its spec, logs its frames, and a
// reconnect event drops the connection without ending the session.
func TestRunChargerReconnects(t *testing.T) {
	csms := ocpptest.New(t)
	spec := runSpec{Name: "t", Site: runSiteSpec{FuseA: 25},
		Devices: []runDeviceSpec{{ID: "garage", Driver: "ocpp", OCPPURL: csms.URL("ftw:pw"), Plugged: true, CarPhases: 1}}}
	r, out := runFor(t, spec)
	ocpptest.WaitFor(t, "StartTransaction", 10*time.Second, func() bool { return csms.Sent("StartTransaction") == 1 })
	ocpptest.WaitFor(t, "charging", 5*time.Second, func() bool { return r.devs["garage"].OCPPServer.PhasePowers()[0] > 3000 })
	if p := r.devs["garage"].OCPPServer.PhasePowers(); p[1] != 0 {
		t.Fatalf("a one-phase car: %v", p)
	}
	if err := r.apply(runEvent{Do: "reconnect", Device: "garage", ForS: 0.2}); err != nil {
		t.Fatal(err)
	}
	ocpptest.WaitFor(t, "a second connection", 15*time.Second, func() bool { return csms.Connections() == 2 })
	if p := r.devs["garage"].OCPPServer.PhasePowers(); p[0] < 3000 {
		t.Fatalf("after the reconnect the car draws %v", p)
	}
	r.end()
	b, err := os.ReadFile(filepath.Join(out, "ocpp-garage.jsonl"))
	if err != nil || !bytes.Contains(b, []byte(`"cp->cs"`)) || !bytes.Contains(b, []byte("BootNotification")) {
		t.Fatalf("frame log: %v %.300s", err, b)
	}
}

func randomSpec(seed int64) runSpec {
	return runSpec{Name: "r", Site: runSiteSpec{FuseA: 20, HouseW: [3]float64{690, 690, 690}},
		Devices: []runDeviceSpec{{ID: "inv", Driver: "sungrow", PVW: 3000},
			{ID: "garage", Driver: "ocpp", OCPPURL: "ws://127.0.0.1:1/"}},
		Random: &runRandom{Seed: seed, DurationS: 1800}}
}

// A seed gives the same timeline every time, within the house's bounds.
func TestRandomTimelinesAreSeededAndBounded(t *testing.T) {
	a, b, c := randomSpec(7), randomSpec(7), randomSpec(8)
	ea, eb, ec := randomize(&a), randomize(&b), randomize(&c)
	ja, _ := json.Marshal(ea)
	jb, _ := json.Marshal(eb)
	jc, _ := json.Marshal(ec)
	if !bytes.Equal(ja, jb) || bytes.Equal(ja, jc) {
		t.Fatal("seed 7 twice differs, or seed 8 is the same")
	}
	if *a.Devices[1].CommandResponseS != *b.Devices[1].CommandResponseS || a.Site.SignedPhaseCurrents == nil {
		t.Fatal("the seed did not fix the devices' timing")
	}
	if len(ea) < 20 {
		t.Fatalf("%d events in 30 min", len(ea))
	}
	kinds := map[string]int{}
	for seed := int64(1); seed <= 20; seed++ {
		s := randomSpec(seed)
		for _, ev := range randomize(&s) {
			kinds[ev.Do]++
			if ev.AtS < 30 || ev.AtS > 1800 {
				t.Fatalf("seed %d: event at %.0f s outside the run", seed, ev.AtS)
			}
			if ev.A != nil {
				for _, x := range ev.A {
					if x < 0.3 || x > 18 {
						t.Fatalf("seed %d: house %.1f A on a 20 A fuse", seed, x)
					}
				}
			}
		}
	}
	for _, k := range []string{"loads", "pv", "soc", "car", "silence", "offline", "reconnect", "meter_late", "meter_glitch", "signed_currents", "load_balancer"} {
		if kinds[k] == 0 {
			t.Errorf("20 seeds never drew %s", k)
		}
	}
	only := randomSpec(1)
	only.Random.Kinds = []string{"meter_glitch"}
	for _, ev := range randomize(&only) {
		if ev.Do != "meter_glitch" {
			t.Fatalf("kinds filter let %s through", ev.Do)
		}
	}
}

// The meter's faults reach what an EMS reads: a late meter stands still,
// a glitch puts 0 W in the total once, and signed currents carry the sign.
// A later silence or outage is not ended by an earlier one's timer.
func TestOverlappingFaultsKeepTheLatest(t *testing.T) {
	spec := runSpec{Name: "t", Site: runSiteSpec{FuseA: 25},
		Devices: []runDeviceSpec{{ID: "inv", Driver: "sungrow", EMSControl: ptr(false)}}}
	r, _ := runFor(t, spec)
	inv := r.devs["inv"]
	for _, ev := range []runEvent{{Do: "silence", Device: "inv", ForS: 0.3}, {Do: "silence", Device: "inv"}} {
		if err := r.apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(600 * time.Millisecond)
	if !inv.Server.Silent() {
		t.Fatal("an indefinite silence ended at the earlier silence's deadline")
	}
	for _, ev := range []runEvent{{Do: "online", Device: "inv"}, {Do: "offline", Device: "inv", ForS: 0.3}, {Do: "offline", Device: "inv", ForS: 5}} {
		if err := r.apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(600 * time.Millisecond)
	if isRunning(inv) {
		t.Fatal("a 5 s outage ended at the earlier outage's deadline")
	}
}

// Energy that flows while the meter's readings stand still shows on its
// counters once they move again.
func TestLateMeterKeepsTheEnergy(t *testing.T) {
	spec := runSpec{Name: "t", Site: runSiteSpec{FuseA: 25, HouseW: [3]float64{3000, 3000, 3000}},
		Devices: []runDeviceSpec{{ID: "inv", Driver: "sungrow", EMSControl: ptr(false)}}}
	r, _ := runFor(t, spec)
	kwh := func() float64 { return getSemanticValue(r.meter, "total_import") }
	ocpptest.WaitFor(t, "import on the meter", 5*time.Second, func() bool { return getSemanticValue(r.meter, "meter_power") > 8000 })
	t0, e0 := time.Now(), kwh()
	if err := r.apply(runEvent{Do: "meter_late", ForS: 2}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	w := getSemanticValue(r.meter, "meter_power")
	got, want := kwh()-e0, w/1000*time.Since(t0).Hours()
	if got < 0.8*want || got > 1.2*want {
		t.Fatalf("%.5f kWh counted over %v at %.0f W, want about %.5f", got, time.Since(t0).Round(time.Millisecond), w, want)
	}
}

func TestRunMeterFaults(t *testing.T) {
	spec := runSpec{Name: "t", Site: runSiteSpec{FuseA: 25, HouseW: [3]float64{690, 690, 690}},
		Devices: []runDeviceSpec{{ID: "inv", Driver: "sungrow", PVW: 5000, EMSControl: ptr(false)}}}
	r, _ := runFor(t, spec)
	meterW := func() float64 { return getSemanticValue(r.meter, "meter_power") }
	l1A := func() float64 { return getSemanticValue(r.meter, "meter_l1_current") }
	ocpptest.WaitFor(t, "export on the meter", 5*time.Second, func() bool { return meterW() < -2000 })
	if l1A() < 0 {
		t.Fatalf("an SDM630 reports magnitudes, got %v A", l1A())
	}
	if err := r.apply(runEvent{Do: "signed_currents", On: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	ocpptest.WaitFor(t, "a signed current", 3*time.Second, func() bool { return l1A() < 0 })

	if err := r.apply(runEvent{Do: "meter_late", ForS: 2}); err != nil {
		t.Fatal(err)
	}
	r.setLoads([3]float64{3000, 3000, 3000})
	time.Sleep(1200 * time.Millisecond)
	if w := meterW(); w > -2000 {
		t.Fatalf("a late meter moved to %v W", w)
	}
	ocpptest.WaitFor(t, "the meter again", 5*time.Second, func() bool { return meterW() > 0 })

	if err := r.apply(runEvent{Do: "meter_glitch"}); err != nil {
		t.Fatal(err)
	}
	ocpptest.WaitFor(t, "a 0 W total", 3*time.Second, func() bool { return meterW() == 0 && getSemanticValue(r.meter, "meter_l1_power") > 0 })
	ocpptest.WaitFor(t, "the total again", 3*time.Second, func() bool { return meterW() > 0 })
}
