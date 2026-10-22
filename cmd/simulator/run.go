package main

// A headless run: one house built from a JSON spec, served on ports from a
// base, driven by a timeline of events and recorded once a second into an
// output directory. An EMS under test talks to the house over Modbus and
// OCPP; its test harness reads the record and can post events while the
// house runs.
//
//	simulator run -spec house.json -port-base 18700 -out run/
//
// Ports: base+0 HTTP API, from base+10 Modbus, from base+30 OCPP, from
// base+50 MQTT. Two runs on one machine need bases 100 apart.
//
// The output directory gets ports.json (where each device listens, written
// once the house is up), plant.jsonl (a sample a second plus every event and
// mark), ocpp-<device>.jsonl (every OCPP frame each charger reads or writes)
// and result.json (written at the end).

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	simmdns "github.com/srcfl/device-simulator/internal/mdns"
	"github.com/srcfl/device-simulator/internal/ocpp"
	"github.com/srcfl/device-simulator/internal/ports"
	"github.com/srcfl/device-simulator/internal/settings"
)

// runSpec is the house of a headless run.
type runSpec struct {
	Name      string          `json:"name"`
	DurationS float64         `json:"duration_s"` // 0 runs until stopped
	Site      runSiteSpec     `json:"site"`
	Devices   []runDeviceSpec `json:"devices"`
	Timeline  []runEvent      `json:"timeline"`
}

type runSiteSpec struct {
	FuseA       int        `json:"fuse_a"`
	FuseProfile string     `json:"fuse_profile,omitempty"` // default iec_gg
	HouseW      [3]float64 `json:"house_w"`                // per phase
}

// runDeviceSpec is one device, by the device-drivers driver it emulates.
// The meter (an SDM630) comes with the site.
type runDeviceSpec struct {
	ID     string `json:"id"`     // the run's name for it, used by events
	Driver string `json:"driver"` // sungrow, pixii... or ocpp for a charger

	// Overrides of the model's parameters.
	CommandResponseS *float64 `json:"command_response_s,omitempty"`

	// A hybrid inverter.
	EMSControl *bool    `json:"ems_control,omitempty"` // default on
	BatteryKWh float64  `json:"battery_kwh,omitempty"`
	SoCPct     *float64 `json:"soc_pct,omitempty"`
	MinSoCPct  *float64 `json:"min_soc_pct,omitempty"`
	MaxSoCPct  *float64 `json:"max_soc_pct,omitempty"`
	PVW        float64  `json:"pv_w,omitempty"`

	// An OCPP 1.6 charger in client mode, and the car on it.
	OCPPURL     string            `json:"ocpp_url,omitempty"` // user:pass@ is sent as Basic auth
	MaxKW       float64           `json:"max_kw,omitempty"`   // on three phases; default 11.04
	Vendor      string            `json:"vendor,omitempty"`
	Model       string            `json:"model,omitempty"`
	PhaseSwitch bool              `json:"phase_switch,omitempty"`
	OCPPConfig  map[string]string `json:"ocpp_config,omitempty"` // configuration keys over the defaults
	CarMaxA     float64           `json:"car_max_a,omitempty"`
	CarPhases   int               `json:"car_phases,omitempty"`
	Plugged     bool              `json:"plugged,omitempty"` // a car plugged in at the start
}

// runEvent changes the house: at at_s seconds into the run on the timeline,
// or at once when posted to /api/run/event.
//
//	mark      label                  a named moment in the record
//	loads     w or a (per phase)     house load
//	pv        pv_w, device           PV power of an inverter (default the first)
//	soc       pct, device            battery state of charge
//	plug      on, device             car plugged in or out
//	car       max_a, phases, device  what the car takes
//	offline   device, for_s          the device stops answering and drops out
//	                                 of the site; back after for_s if given
//	online    device                 back again
//	reconnect device, for_s          a charger loses its connection for for_s;
//	                                 the car keeps charging
//	stop                             end the run
type runEvent struct {
	AtS    float64     `json:"at_s,omitempty"`
	Do     string      `json:"do"`
	Device string      `json:"device,omitempty"`
	Label  string      `json:"label,omitempty"`
	W      *[3]float64 `json:"w,omitempty"`
	A      *[3]float64 `json:"a,omitempty"`
	PVW    *float64    `json:"pv_w,omitempty"`
	Pct    *float64    `json:"pct,omitempty"`
	On     *bool       `json:"on,omitempty"`
	MaxA   *float64    `json:"max_a,omitempty"`
	Phases *int        `json:"phases,omitempty"`
	ForS   float64     `json:"for_s,omitempty"`
}

type headlessRun struct {
	spec  runSpec
	out   string
	base  int
	start time.Time

	site  *Site
	meter *Simulator
	devs  map[string]*Simulator
	ids   []string // spec order

	recMu   sync.Mutex
	rec     *os.File
	frameMu sync.Mutex
	frames  map[string]*os.File
	events  int
	samples int

	stop     chan struct{}
	stopOnce sync.Once
}

// runHeadless is `simulator run`. It returns the process exit code.
func runHeadless(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	specPath := fs.String("spec", "", "house spec (JSON)")
	base := fs.Int("port-base", 18700, "first of 100 ports on 127.0.0.1")
	out := fs.String("out", "run", "output directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	b, err := os.ReadFile(*specPath)
	if err != nil {
		log.Printf("run: %v", err)
		return 2
	}
	var spec runSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		log.Printf("run: %s: %v", *specPath, err)
		return 2
	}
	r, err := startRun(spec, *base, *out)
	if err != nil {
		log.Printf("run: %v", err)
		return 1
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
	case <-r.stop:
	}
	r.end()
	return 0
}

// startRun builds the house and starts serving, recording and the timeline.
func startRun(spec runSpec, base int, out string) (*headlessRun, error) {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	r := &headlessRun{spec: spec, out: out, base: base, devs: map[string]*Simulator{},
		frames: map[string]*os.File{}, stop: make(chan struct{})}
	// A run is self-contained: its own ports and state file, none of the
	// user's settings or saved simulators.
	os.Setenv("DEVICE_SIMULATOR_STATE_PATH", filepath.Join(out, "state.json"))
	runtimeSettings = &settings.AppSettings{ModbusBasePort: base + 10, MQTTBasePort: base + 50, OCPPBasePort: base + 30, SiteTickMs: 500}
	portAllocator = ports.NewAllocator(base+10, base+50, base+30)
	if mdnsAdvertiser == nil {
		mdnsAdvertiser = simmdns.New()
	}
	f, err := os.Create(filepath.Join(out, "plant.jsonl"))
	if err != nil {
		return nil, err
	}
	r.rec = f
	r.start = time.Now()
	if err := r.build(); err != nil {
		r.end()
		return nil, err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", base))
	if err != nil {
		r.end()
		return nil, err
	}
	go http.Serve(ln, corsMiddleware(r.handler()))
	if err := r.writePorts(); err != nil {
		r.end()
		return nil, err
	}
	go r.record()
	go r.timeline()
	if spec.DurationS > 0 {
		time.AfterFunc(time.Duration(spec.DurationS*float64(time.Second)), r.finish)
	}
	log.Printf("run %q on ports %d+, recording to %s", spec.Name, base, out)
	return r, nil
}

func (r *headlessRun) build() error {
	var members []int
	for _, d := range r.spec.Devices {
		if d.ID == "" || r.devs[d.ID] != nil {
			return fmt.Errorf("device %q: every device needs its own id", d.ID)
		}
		if d.ID == "meter" {
			return fmt.Errorf(`device "meter": that id is the site meter's`)
		}
		var sim *Simulator
		var err error
		if d.Driver == "ocpp" {
			sim, err = r.buildCharger(d)
		} else {
			sim, err = r.buildInverter(d)
		}
		if err != nil {
			return fmt.Errorf("device %s: %w", d.ID, err)
		}
		r.devs[d.ID] = sim
		r.ids = append(r.ids, d.ID)
		members = append(members, sim.ID)
	}
	site, err := createSite(r.spec.Name, 0, members, 0)
	if err != nil {
		return err
	}
	r.site = site
	r.meter = getSimulator(site.MeterID)
	fuseProfile := r.spec.Site.FuseProfile
	if fuseProfile == "" {
		fuseProfile = FuseProfileIecGg
	}
	if _, ok := fuseProfiles[fuseProfile]; !ok {
		return fmt.Errorf("fuse profile %q", fuseProfile)
	}
	site.mu.Lock()
	if r.spec.Site.FuseA > 0 {
		site.FuseSize = r.spec.Site.FuseA
	}
	site.FuseProfile = fuseProfile
	site.PhaseFactors = PhaseFactors{L1: 1.0 / 3, L2: 1.0 / 3, L3: 1.0 / 3}
	site.LoadScenario = "static"
	site.mu.Unlock()
	r.setLoads(r.spec.Site.HouseW)
	for _, d := range r.spec.Devices {
		if d.Driver == "ocpp" && d.Plugged {
			if err := r.devs[d.ID].OCPPServer.SetValue("evConnected", true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *headlessRun) buildInverter(d runDeviceSpec) (*Simulator, error) {
	sim, err := createSimulator("inverter")
	if err != nil {
		return nil, err
	}
	profile := sim.ProfilesManager.ForDriver(d.Driver)
	if profile == nil || profile.Protocol != "modbus" {
		return nil, fmt.Errorf("no Modbus model emulates driver %q", d.Driver)
	}
	applyProfile(sim, profile)
	sim.ProfilesManager.SetActive(profile.Name)
	sim.mu.Lock()
	sim.EMSControl = d.EMSControl == nil || *d.EMSControl
	if d.CommandResponseS != nil {
		sim.EMSResponse = seconds(*d.CommandResponseS)
	}
	if d.BatteryKWh > 0 {
		sim.BatteryCapacityKWh = d.BatteryKWh
	}
	sim.mu.Unlock()
	for name, v := range map[string]*float64{"battery_soc": d.SoCPct, "battery_min_soc": d.MinSoCPct, "battery_max_soc": d.MaxSoCPct} {
		if v != nil {
			setSemanticValue(sim, name, *v)
		}
	}
	setSemanticValue(sim, "pv_power", math.Max(0, d.PVW))
	return sim, startSimulatorServer(sim)
}

func (r *headlessRun) buildCharger(d runDeviceSpec) (*Simulator, error) {
	if d.OCPPURL == "" {
		return nil, fmt.Errorf("a charger needs ocpp_url")
	}
	sim, err := createSimulator("ocpp_charger")
	if err != nil {
		return nil, err
	}
	kw := d.MaxKW
	if kw <= 0 {
		kw = 11.04
	}
	vendor, model := d.Vendor, d.Model
	if vendor == "" {
		vendor = "SimulatorCo"
	}
	if model == "" {
		model = fmt.Sprintf("Sim%.0f", kw)
	}
	srv := ocpp.NewServer(sim.ID, 0, vendor, model, 1, kw)
	cfg := map[string]string{
		"HeartbeatInterval":                       "60",
		"MeterValueSampleInterval":                "10",
		"NumberOfConnectors":                      "1",
		"ConnectorSwitch3to1PhaseSupported":       fmt.Sprint(d.PhaseSwitch),
		"ChargingScheduleAllowedChargingRateUnit": "Current,Power",
		"SupportedFeatureProfiles":                "Core,SmartCharging,RemoteTrigger,LocalAuthListManagement",
	}
	for k, v := range d.OCPPConfig {
		cfg[k] = v
	}
	srv.ApplyConfiguration(cfg)
	srv.SetOCPPURL(d.OCPPURL)
	delay := commandResponse(sim.ProfilesManager.ForDriver("ocpp"))
	if d.CommandResponseS != nil {
		delay = seconds(*d.CommandResponseS)
	}
	srv.SetProfileDelay(delay)
	srv.SetCar(d.CarMaxA, d.CarPhases)
	frames, err := os.Create(filepath.Join(r.out, "ocpp-"+d.ID+".jsonl"))
	if err != nil {
		return nil, err
	}
	r.frames[d.ID] = frames
	srv.SetFrameLog(func(dir string, frame []byte) { r.writeFrame(d.ID, dir, frame) })
	sim.mu.Lock()
	sim.OCPPServer, sim.Device, sim.Serial, sim.EMSResponse = srv, srv, srv.SerialNumber(), delay
	sim.mu.Unlock()
	return sim, startSimulatorServer(sim)
}

// handler is the simulator's API plus /api/run, without self-update.
func (r *headlessRun) handler() http.Handler {
	mux := buildHTTPMux()
	mux.HandleFunc("/api/run", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"name": r.spec.Name, "elapsed_s": time.Since(r.start).Seconds(),
			"events": r.eventCount(), "samples": r.sampleCount()})
	})
	mux.HandleFunc("/api/run/event", func(w http.ResponseWriter, req *http.Request) {
		var ev runEvent
		if req.Method != http.MethodPost || json.NewDecoder(req.Body).Decode(&ev) != nil {
			http.Error(w, "POST a runEvent", http.StatusBadRequest)
			return
		}
		if err := r.apply(ev); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/run/mark", func(w http.ResponseWriter, req *http.Request) {
		var m struct {
			Label string `json:"label"`
		}
		if req.Method != http.MethodPost || json.NewDecoder(req.Body).Decode(&m) != nil || m.Label == "" {
			http.Error(w, `POST {"label": ...}`, http.StatusBadRequest)
			return
		}
		r.apply(runEvent{Do: "mark", Label: m.Label})
		w.WriteHeader(http.StatusNoContent)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/api/update") || req.URL.Path == "/api/restart" {
			http.Error(w, "not in a headless run", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, req)
	})
}

// timeline applies the spec's events at their times.
func (r *headlessRun) timeline() {
	evs := append([]runEvent(nil), r.spec.Timeline...)
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].AtS < evs[j].AtS })
	for _, ev := range evs {
		select {
		case <-r.stop:
			return
		case <-time.After(time.Until(r.start.Add(time.Duration(ev.AtS * float64(time.Second))))):
		}
		if err := r.apply(ev); err != nil {
			log.Printf("run: event at %.1f s: %v", ev.AtS, err)
		}
	}
}

func (r *headlessRun) device(id string, wantCharger bool) (*Simulator, error) {
	sim := r.devs[id]
	if sim == nil {
		return nil, fmt.Errorf("no device %q", id)
	}
	if wantCharger && sim.OCPPServer == nil {
		return nil, fmt.Errorf("%s is not a charger", id)
	}
	return sim, nil
}

func (r *headlessRun) firstInverter() string {
	for _, id := range r.ids {
		if r.devs[id].Protocol == "modbus" {
			return id
		}
	}
	return ""
}

// apply changes the house now and records the event.
func (r *headlessRun) apply(ev runEvent) error {
	var err error
	switch ev.Do {
	case "mark":
		if ev.Label == "" {
			err = fmt.Errorf("mark needs a label")
		}
	case "loads":
		switch {
		case ev.W != nil:
			r.setLoads(*ev.W)
		case ev.A != nil:
			a := *ev.A
			r.setLoads([3]float64{a[0] * 230, a[1] * 230, a[2] * 230})
		default:
			err = fmt.Errorf("loads needs w or a")
		}
	case "pv", "soc":
		id := ev.Device
		if id == "" {
			id = r.firstInverter()
		}
		var sim *Simulator
		if sim, err = r.device(id, false); err != nil {
			break
		}
		switch {
		case ev.Do == "pv" && ev.PVW != nil:
			setSemanticValue(sim, "pv_power", math.Max(0, *ev.PVW))
		case ev.Do == "soc" && ev.Pct != nil:
			setSemanticValue(sim, "battery_soc", *ev.Pct)
			sim.mu.Lock()
			sim.socInitialized = false
			sim.mu.Unlock()
		default:
			err = fmt.Errorf("%s needs a value", ev.Do)
		}
	case "plug":
		var sim *Simulator
		if sim, err = r.device(ev.Device, true); err == nil {
			err = sim.OCPPServer.SetValue("evConnected", ev.On == nil || *ev.On)
		}
	case "car":
		var sim *Simulator
		if sim, err = r.device(ev.Device, true); err == nil {
			maxA, phases := 0.0, 0
			if ev.MaxA != nil {
				maxA = *ev.MaxA
			}
			if ev.Phases != nil {
				phases = *ev.Phases
			}
			sim.OCPPServer.SetCar(maxA, phases)
		}
	case "offline":
		var sim *Simulator
		if sim, err = r.device(ev.Device, false); err == nil {
			stopSimulatorServer(sim)
			if ev.ForS > 0 {
				id := ev.Device
				time.AfterFunc(time.Duration(ev.ForS*float64(time.Second)), func() { r.apply(runEvent{Do: "online", Device: id}) })
			}
		}
	case "online":
		var sim *Simulator
		if sim, err = r.device(ev.Device, false); err == nil {
			err = startSimulatorServer(sim)
		}
	case "reconnect":
		var sim *Simulator
		if sim, err = r.device(ev.Device, true); err == nil {
			sim.OCPPServer.DropConnection(time.Duration(ev.ForS * float64(time.Second)))
		}
	case "stop":
		defer r.finish()
	default:
		err = fmt.Errorf("unknown event %q", ev.Do)
	}
	if err != nil {
		return err
	}
	ev.AtS = round3(time.Since(r.start).Seconds())
	r.recMu.Lock()
	r.events++
	r.recMu.Unlock()
	line := map[string]any{"t": unixSeconds(time.Now()), "elapsed_s": round3(ev.AtS)}
	if ev.Do == "mark" {
		line["mark"] = ev.Label
	} else {
		line["event"] = ev
	}
	r.writeLine(line)
	return nil
}

func (r *headlessRun) setLoads(w [3]float64) {
	r.site.mu.Lock()
	defer r.site.mu.Unlock()
	r.site.SitePhaseLoads = PhaseLoads{L1: w[0], L2: w[1], L3: w[2]}
	r.site.BaseLoadW = w[0] + w[1] + w[2]
}

// plantSample is one second of the house. Power is + into the site.
type plantSample struct {
	T       float64        `json:"t"`
	Elapsed float64        `json:"elapsed_s"`
	PhaseW  [3]float64     `json:"phase_w"` // through each phase's fuse
	PhaseA  [3]float64     `json:"phase_a"` // the same, signed amperes
	MeterW  float64        `json:"meter_w"` // what the site meter reads
	Fuse    fuseSample     `json:"fuse"`
	Devices map[string]any `json:"devices"`
}

type fuseSample struct {
	RatingA int        `json:"rating_a"`
	Profile string     `json:"profile"`
	Damage  [3]float64 `json:"damage"`
	Blown   [3]bool    `json:"blown"`
}

func (r *headlessRun) record() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.stop:
			return
		case now := <-tick.C:
			r.writeLine(r.sample(now))
			r.recMu.Lock()
			r.samples++
			r.recMu.Unlock()
		}
	}
}

func (r *headlessRun) sample(now time.Time) plantSample {
	r.site.mu.RLock()
	pw := r.site.lastPhaseW
	fs := fuseSample{RatingA: r.site.FuseSize, Profile: r.site.FuseProfile,
		Damage: [3]float64{r.site.FuseOverload.L1.Damage, r.site.FuseOverload.L2.Damage, r.site.FuseOverload.L3.Damage},
		Blown:  [3]bool{r.site.FuseBlown.L1, r.site.FuseBlown.L2, r.site.FuseBlown.L3}}
	r.site.mu.RUnlock()
	s := plantSample{T: unixSeconds(now), Elapsed: round3(now.Sub(r.start).Seconds()),
		PhaseW: [3]float64{pw.L1, pw.L2, pw.L3}, Fuse: fs, Devices: map[string]any{}}
	for k, w := range s.PhaseW {
		s.PhaseA[k] = round3(w / 230)
	}
	if r.meter != nil {
		s.MeterW = getSemanticValue(r.meter, "meter_power")
	}
	for _, id := range r.ids {
		sim := r.devs[id]
		sim.mu.RLock()
		running := sim.Running
		sim.mu.RUnlock()
		if sim.OCPPServer != nil {
			st := sim.OCPPServer.GetState()
			pp := sim.OCPPServer.PhasePowers()
			status, phases := "", 0
			if cs, ok := st["connectors"].([]map[string]interface{}); ok {
				for _, c := range cs {
					if c["id"] == 1 {
						status, _ = c["status"].(string)
						phases, _ = c["phases"].(int)
					}
				}
			}
			s.Devices[id] = map[string]any{"running": running, "connected": st["connected"], "ev_connected": st["evConnected"],
				"status": status, "w": st["totalPower"], "phase_w": pp, "phases": phases, "offer_w": st["effectivePowerW"],
				"transaction": st["hasActiveTransaction"]}
			continue
		}
		s.Devices[id] = map[string]any{"running": running, "pv_w": getSemanticValue(sim, "pv_power"),
			"battery_w": getBatterySignedPower(sim), "soc_pct": getSemanticValue(sim, "battery_soc"),
			"ems_mode": getSemanticValue(sim, "grid_mode"), "ems_cmd": getSemanticValue(sim, "battery_cmd"),
			"ems_setpoint_w": getSemanticValue(sim, "battery_setpoint")}
	}
	return s
}

func (r *headlessRun) writeLine(v any) {
	b, err := marshalLine(v)
	if err != nil {
		log.Printf("run: record: %v", err)
		return
	}
	r.recMu.Lock()
	defer r.recMu.Unlock()
	if r.rec != nil {
		r.rec.Write(b)
	}
}

// marshalLine is one JSON line, with "->" and "<" left as they are.
func marshalLine(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(v)
	return buf.Bytes(), err
}

func (r *headlessRun) writeFrame(id, dir string, frame []byte) {
	rec := map[string]any{"t": unixSeconds(time.Now()), "dir": dir, "frame": json.RawMessage(frame)}
	if !json.Valid(frame) {
		rec["frame"] = string(frame)
	}
	b, _ := marshalLine(rec)
	r.frameMu.Lock()
	defer r.frameMu.Unlock()
	if f := r.frames[id]; f != nil {
		f.Write(b)
	}
}

// runPorts is ports.json: where the EMS finds each device.
type runPorts struct {
	HTTP    int                       `json:"http"`
	Devices map[string]runDevicePorts `json:"devices"`
}

type runDevicePorts struct {
	Driver string `json:"driver"`
	Modbus int    `json:"modbus_port,omitempty"`
	UnitID uint8  `json:"unit_id,omitempty"`
	Serial string `json:"serial,omitempty"` // an OCPP charger's identity
}

func (r *headlessRun) writePorts() error {
	p := runPorts{HTTP: r.base, Devices: map[string]runDevicePorts{}}
	add := func(id, driver string, sim *Simulator) {
		sim.mu.RLock()
		defer sim.mu.RUnlock()
		dp := runDevicePorts{Driver: driver}
		if sim.Protocol == "modbus" {
			dp.Modbus, dp.UnitID = sim.Port, sim.SlaveID
		} else {
			dp.Serial = sim.Serial
		}
		p.Devices[id] = dp
	}
	for i, id := range r.ids {
		add(id, r.spec.Devices[i].Driver, r.devs[id])
	}
	if r.meter != nil {
		add("meter", "sdm630", r.meter)
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	return os.WriteFile(filepath.Join(r.out, "ports.json"), b, 0o644)
}

func (r *headlessRun) eventCount() int  { r.recMu.Lock(); defer r.recMu.Unlock(); return r.events }
func (r *headlessRun) sampleCount() int { r.recMu.Lock(); defer r.recMu.Unlock(); return r.samples }

// finish ends the run; runHeadless then calls end.
func (r *headlessRun) finish() { r.stopOnce.Do(func() { close(r.stop) }) }

// end stops the house, writes result.json and closes the record.
func (r *headlessRun) end() {
	r.finish()
	res := map[string]any{"name": r.spec.Name, "duration_s": round3(time.Since(r.start).Seconds()),
		"samples": r.sampleCount(), "events": r.eventCount()}
	if r.site != nil {
		r.site.mu.RLock()
		res["fuse"] = map[string]any{"rating_a": r.site.FuseSize, "profile": r.site.FuseProfile,
			"blown": [3]bool{r.site.FuseBlown.L1, r.site.FuseBlown.L2, r.site.FuseBlown.L3}}
		r.site.mu.RUnlock()
		stopSiteSimulation(r.site)
		stopSiteAggregation(r.site)
	}
	for _, id := range r.ids {
		stopSimulatorServer(r.devs[id])
	}
	if r.meter != nil {
		stopSimulatorServer(r.meter)
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	os.WriteFile(filepath.Join(r.out, "result.json"), b, 0o644)
	r.recMu.Lock()
	if r.rec != nil {
		r.rec.Close()
		r.rec = nil
	}
	r.recMu.Unlock()
	r.frameMu.Lock()
	for id, f := range r.frames {
		f.Close()
		delete(r.frames, id)
	}
	r.frameMu.Unlock()
	// Leave the process's simulator lists as they were before the run.
	var ids []int
	for _, sim := range r.devs {
		ids = append(ids, sim.ID)
	}
	if r.meter != nil {
		ids = append(ids, r.meter.ID)
	}
	simulatorsMu.Lock()
	kept := simulators[:0]
	for _, sim := range simulators {
		drop := false
		for _, id := range ids {
			drop = drop || sim.ID == id
		}
		if !drop {
			kept = append(kept, sim)
		}
	}
	simulators = kept
	simulatorsMu.Unlock()
	if r.site != nil {
		sitesMu.Lock()
		keptSites := sites[:0]
		for _, s := range sites {
			if s != r.site {
				keptSites = append(keptSites, s)
			}
		}
		sites = keptSites
		sitesMu.Unlock()
	}
}

func unixSeconds(t time.Time) float64 { return float64(t.UnixMicro()) / 1e6 }

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }
