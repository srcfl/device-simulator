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

func ptr[T any](v T) *T { return &v }
