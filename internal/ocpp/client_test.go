package ocpp

import (
	"encoding/json"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/srcfl/device-simulator/internal/ocpp/ocpptest"
)

var waitFor = ocpptest.WaitFor

func startClient(t *testing.T, url string) *Server {
	t.Helper()
	s := newTestServer()
	s.SetOCPPURL(url)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop() })
	return s
}

// A Central System that asks for Basic auth gets it from the URL, and the
// charge point identity is still the last path segment.
func TestClientSendsBasicAuthFromURL(t *testing.T) {
	csms := ocpptest.New(t)
	startClient(t, csms.URL("ftw:s3cret"))
	waitFor(t, "BootNotification", 5*time.Second, func() bool { return csms.Sent(ActionBootNotification) > 0 })

	if got := csms.Paths()[0]; got != "/ocpp/OCPPSIM01" {
		t.Errorf("path = %q, want /ocpp/OCPPSIM01", got)
	}
	// "ftw:s3cret" in base64.
	if got := csms.Auths()[0]; got != "Basic ZnR3OnMzY3JldA==" {
		t.Errorf("Authorization = %q, want Basic auth for ftw:s3cret", got)
	}
}

func TestClientWithoutCredentialsSendsNoAuth(t *testing.T) {
	csms := ocpptest.New(t)
	startClient(t, csms.URL(""))
	waitFor(t, "BootNotification", 5*time.Second, func() bool { return csms.Sent(ActionBootNotification) > 0 })

	if got := csms.Auths()[0]; got != "" {
		t.Errorf("Authorization = %q, want none", got)
	}
}

// A car plugged in while the charger cannot reach its Central System still
// starts a session the Central System hears about once the charger is back.
func TestStartTransactionWaitsForTheConnection(t *testing.T) {
	csms := ocpptest.New(t)
	csms.SetClosed(true)
	s := newTestServer()
	s.reconnectDelay = 50 * time.Millisecond
	s.SetOCPPURL(csms.URL(""))
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop() })

	if err := s.SetValue("evConnected", true); err != nil {
		t.Fatal(err)
	}
	// The charger starts the session locally 2 s after plug-in.
	waitFor(t, "local transaction", 5*time.Second, func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return len(s.transactions) == 1
	})
	s.txQueueMu.Lock()
	queued := len(s.txQueue)
	s.txQueueMu.Unlock()
	if queued != 1 {
		t.Fatalf("%d messages queued while offline, want the StartTransaction", queued)
	}

	csms.SetClosed(false)
	waitFor(t, "StartTransaction", 5*time.Second, func() bool { return csms.Sent(ActionStartTransaction) == 1 })
	waitFor(t, "server transaction id", 5*time.Second, func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.transactions[42] != nil
	})

	var boot, start int
	actions := csms.Actions()
	for i, a := range actions {
		switch a {
		case ActionBootNotification:
			boot = i
		case ActionStartTransaction:
			start = i
		}
	}
	if start < boot {
		t.Errorf("StartTransaction sent before BootNotification: %v", actions)
	}
}

// A session that starts and ends offline is stopped under the transaction
// id the Central System gives its start, not the charger's local one.
func TestQueuedStopNamesTheAssignedTransaction(t *testing.T) {
	csms := ocpptest.New(t)
	csms.SetClosed(true)
	s := newTestServer()
	s.reconnectDelay = 50 * time.Millisecond
	s.SetOCPPURL(csms.URL(""))
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop() })

	if err := s.SetValue("evConnected", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "local transaction", 5*time.Second, func() bool {
		s.txQueueMu.Lock()
		defer s.txQueueMu.Unlock()
		return len(s.txQueue) == 1
	})
	if err := s.SetValue("evConnected", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "queued stop", 5*time.Second, func() bool {
		s.txQueueMu.Lock()
		defer s.txQueueMu.Unlock()
		return len(s.txQueue) == 2
	})

	csms.SetClosed(false)
	waitFor(t, "StopTransaction", 5*time.Second, func() bool { return csms.Sent(ActionStopTransaction) == 1 })
	var stop StopTransactionRequest
	if err := json.Unmarshal(csms.Payloads(ActionStopTransaction)[0], &stop); err != nil {
		t.Fatal(err)
	}
	if stop.TransactionId != 42 {
		t.Fatalf("StopTransaction names transaction %d, want 42 from the start's answer", stop.TransactionId)
	}
}

func effectiveW(s *Server) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.computeEffectivePowerLocked(1, time.Now())
}

func txProfileW(id int64, watts float64) ChargingProfile {
	return ChargingProfile{
		ChargingProfileId:      id,
		ChargingProfilePurpose: "TxProfile",
		ChargingProfileKind:    "Relative",
		ChargingSchedule: ChargingSchedule{
			ChargingRateUnit:       "W",
			ChargingSchedulePeriod: []ChargingSchedulePeriod{{StartPeriod: 0, Limit: watts}},
		},
	}
}

// A slow cloud charger answers Accepted at once and changes its output only
// after the delay, in the order the changes were accepted.
func TestProfileDelayAcceptsAtOnceAndAppliesLater(t *testing.T) {
	s := newTestServer() // 22 kW
	s.transactions[1] = &Transaction{ID: 1, ConnectorID: 1, StartTime: time.Now()}
	s.SetProfileDelay(300 * time.Millisecond)

	if got := s.handleSetChargingProfile(SetChargingProfileRequest{ConnectorId: 1, CsChargingProfiles: txProfileW(1, 5000)}); got.Status != "Accepted" {
		t.Fatalf("status %q, want Accepted", got.Status)
	}
	time.Sleep(100 * time.Millisecond)
	s.handleSetChargingProfile(SetChargingProfileRequest{ConnectorId: 1, CsChargingProfiles: txProfileW(1, 7000)})
	if got := effectiveW(s); got != 22000 {
		t.Fatalf("right after Accepted: %v W, want the old 22000 W", got)
	}
	waitFor(t, "first profile", time.Second, func() bool { return effectiveW(s) == 5000 })
	waitFor(t, "second profile", time.Second, func() bool { return effectiveW(s) == 7000 })

	// A connector-0 TxProfile is refused at once, as without a delay.
	if got := s.handleSetChargingProfile(SetChargingProfileRequest{ConnectorId: 0, CsChargingProfiles: txProfileW(2, 1000)}); got.Status != "Rejected" {
		t.Errorf("connector 0 TxProfile: %q, want Rejected", got.Status)
	}

	if got := s.handleClearChargingProfile(ClearChargingProfileRequest{}); got.Status != "Accepted" {
		t.Fatalf("clear: %q, want Accepted", got.Status)
	}
	if got := effectiveW(s); got != 7000 {
		t.Fatalf("right after clear: %v W, want 7000 W until the delay passes", got)
	}
	waitFor(t, "clear", time.Second, func() bool { return effectiveW(s) == 22000 })
}

func TestNoProfileDelayAppliesAtOnce(t *testing.T) {
	s := newTestServer()
	s.transactions[1] = &Transaction{ID: 1, ConnectorID: 1, StartTime: time.Now()}
	s.handleSetChargingProfile(SetChargingProfileRequest{ConnectorId: 1, CsChargingProfiles: txProfileW(1, 5000)})
	if got := effectiveW(s); got != 5000 {
		t.Fatalf("%v W, want 5000 W at once", got)
	}
}

func txProfileA(amps float64, phases int) ChargingProfile {
	p := txProfileW(1, 0)
	p.ChargingSchedule.ChargingRateUnit = "A"
	p.ChargingSchedule.ChargingSchedulePeriod = []ChargingSchedulePeriod{{StartPeriod: 0, Limit: amps, NumberPhases: &phases}}
	return p
}

func charging(t *testing.T, kw float64, phaseSwitch bool, profile ChargingProfile) *Server {
	t.Helper()
	s := NewServer(1, 0, "TestVendor", "TestModel", 1, kw)
	s.configuration["ConnectorSwitch3to1PhaseSupported"] = map[bool]string{true: "true", false: "false"}[phaseSwitch]
	s.transactions[1] = &Transaction{ID: 1, ConnectorID: 1, StartTime: time.Now()}
	s.connectors[1].Status = StatusCharging
	s.applyChargingProfile(1, &profile)
	s.GenerateValues(time.Now())
	return s
}

// A charger that can switch phases puts a 1-phase profile on L1; one that
// cannot keeps drawing on three.
func TestOnePhaseProfileDrawsOnL1(t *testing.T) {
	s := charging(t, 22.08, true, txProfileA(16, 1))
	if got := s.PhasePowers(); got != [3]float64{3680, 0, 0} {
		t.Fatalf("phase powers %v, want 3680 W on L1", got)
	}
	if s.currentL2 != 0 || s.currentL3 != 0 || s.currentL1 < 15 {
		t.Fatalf("currents %.1f/%.1f/%.1f A, want ~16 A on L1 only", s.currentL1, s.currentL2, s.currentL3)
	}
	if s.connectors[1].Phases != 1 {
		t.Errorf("connector phases %d, want 1", s.connectors[1].Phases)
	}

	s = charging(t, 22.08, false, txProfileA(16, 1))
	if got := s.PhasePowers(); math.Abs(got[0]-3680.0/3) > 1e-6 || got[0] != got[1] || got[1] != got[2] {
		t.Fatalf("no phase switching: %v, want 3680 W spread on three phases", got)
	}

	s = charging(t, 22.08, true, txProfileA(16, 3))
	if got := s.PhasePowers(); got != [3]float64{3680, 3680, 3680} {
		t.Fatalf("3-phase profile: %v, want 3680 W per phase", got)
	}
}

// The composite schedule reports what the charger draws: 16 A on one phase,
// not a third of it on three.
func TestCompositeScheduleKeepsThePhases(t *testing.T) {
	s := charging(t, 22.08, true, txProfileA(16, 1))
	r := s.handleGetCompositeSchedule(GetCompositeScheduleRequest{ConnectorId: 1, Duration: 60, ChargingRateUnit: "A"})
	if r.Status != "Accepted" {
		t.Fatalf("status %s", r.Status)
	}
	p := r.ChargingSchedule.ChargingSchedulePeriod
	if len(p) != 1 || math.Abs(p[0].Limit-16) > 0.5 || p[0].NumberPhases == nil || *p[0].NumberPhases != 1 {
		t.Fatalf("periods %+v, want one of 16 A on 1 phase", p)
	}
	r = s.handleGetCompositeSchedule(GetCompositeScheduleRequest{ConnectorId: 1, Duration: 60, ChargingRateUnit: "W"})
	if p := r.ChargingSchedule.ChargingSchedulePeriod; len(p) != 1 || math.Abs(p[0].Limit-3680) > 60 || *p[0].NumberPhases != 1 {
		t.Fatalf("periods %+v, want one of 3680 W on 1 phase", p)
	}
}

// A charger wired on one phase gives an order's current on L1 whatever its
// numberPhases, and no more than its rating.
func TestOnePhaseWiring(t *testing.T) {
	wired := func(p ChargingProfile) *Server {
		s := NewServer(1, 0, "TestVendor", "TestModel", 1, 3.68)
		s.SetWiredPhases(1)
		s.transactions[1] = &Transaction{ID: 1, ConnectorID: 1, StartTime: time.Now()}
		s.connectors[1].Status = StatusCharging
		s.applyChargingProfile(1, &p)
		s.GenerateValues(time.Now())
		return s
	}
	for _, c := range []struct {
		name string
		p    ChargingProfile
		want float64
	}{
		{"16 A on three phases", txProfileA(16, 3), 3680},
		{"10 A on one phase", txProfileA(10, 1), 2300},
		{"32 A", txProfileA(32, 3), 3680},
		{"3 kW", txProfileW(1, 3000), 3000},
	} {
		s := wired(c.p)
		if got := s.PhasePowers(); math.Abs(got[0]-c.want) > 1 || got[1] != 0 || got[2] != 0 {
			t.Errorf("%s: phase powers %v, want %.0f W on L1", c.name, got, c.want)
		}
	}
}

// On one phase a charger gives at most its rated current per phase.
func TestOnePhaseDrawIsCappedAtRatedCurrent(t *testing.T) {
	s := charging(t, 11.04, true, txProfileA(24, 1)) // a 16 A charger
	if got := s.PhasePowers(); got != [3]float64{3680, 0, 0} {
		t.Fatalf("phase powers %v, want 16 A (3680 W) on L1", got)
	}
}

// A dropped connection keeps the charger away for the time asked, the car
// keeps charging through it, and after it comes back the charger sends one
// heartbeat per interval, not one per connection it ever had.
// An outage that starts while the charger is already retrying lasts its
// full length.
func TestDropConnectionWhileRetrying(t *testing.T) {
	csms := ocpptest.New(t)
	csms.SetClosed(true)
	s := newTestServer()
	s.reconnectDelay = 50 * time.Millisecond
	s.SetOCPPURL(csms.URL(""))
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop() })
	time.Sleep(20 * time.Millisecond) // the first dial fails; a retry waits
	before := time.Now()
	s.DropConnection(600 * time.Millisecond)
	csms.SetClosed(false)
	waitFor(t, "BootNotification", 5*time.Second, func() bool { return csms.Sent(ActionBootNotification) == 1 })
	if away := time.Since(before); away < 600*time.Millisecond {
		t.Fatalf("back after %v, want at least 600 ms", away)
	}
}

func TestDropConnectionKeepsTheSessionAndOneLoopSet(t *testing.T) {
	csms := ocpptest.New(t)
	csms.SetHeartbeatInterval(1)
	s := newTestServer()
	s.reconnectDelay = 50 * time.Millisecond
	var mu sync.Mutex
	frames := map[string]int{}
	s.SetFrameLog(func(dir string, _ []byte) { mu.Lock(); frames[dir]++; mu.Unlock() })
	s.SetOCPPURL(csms.URL(""))
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop() })
	waitFor(t, "BootNotification", 5*time.Second, func() bool { return csms.Sent(ActionBootNotification) == 1 })
	if err := s.SetValue("evConnected", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "StartTransaction", 5*time.Second, func() bool { return csms.Sent(ActionStartTransaction) == 1 })

	for i := 0; i < 2; i++ {
		before := time.Now()
		s.DropConnection(300 * time.Millisecond)
		waitFor(t, "reconnect", 5*time.Second, func() bool { return csms.Connections() == i+2 })
		if away := time.Since(before); away < 300*time.Millisecond {
			t.Fatalf("back after %v, want at least 300 ms", away)
		}
	}
	waitFor(t, "BootNotification after the drops", 5*time.Second, func() bool { return csms.Sent(ActionBootNotification) == 3 })

	beats := csms.Sent(ActionHeartbeat)
	s.mu.RLock()
	energy0 := s.connectors[1].EnergyWh
	s.mu.RUnlock()
	time.Sleep(3500 * time.Millisecond)
	if n := csms.Sent(ActionHeartbeat) - beats; n < 2 || n > 4 {
		t.Errorf("%d heartbeats in 3.5 s at a 1 s interval after two drops, want 3", n)
	}
	s.mu.RLock()
	gained, txs, power := s.connectors[1].EnergyWh-energy0, len(s.transactions), s.connectors[1].CurrentPower
	s.mu.RUnlock()
	if txs != 1 || power <= 0 {
		t.Fatalf("after the drops: %d transactions, %v W", txs, power)
	}
	// About 3.5 s at the charger's power, not a multiple of it.
	if want := power * 3.5 / 3600; gained > 1.5*want {
		t.Errorf("energy grew %.2f Wh in 3.5 s at %.0f W, want about %.2f Wh", gained, power, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if frames["cs->cp"] == 0 || frames["cp->cs"] == 0 {
		t.Errorf("frame log %v, want both directions", frames)
	}
}

// The car decides too: a one-phase car under a three-phase order draws on
// L1 only, and a car that takes 10 A takes 10 A of a 16 A offer.
func TestCarTakesWhatItCan(t *testing.T) {
	// Amps per phase at the 227-230 V the simulator varies over.
	amps := func(w float64) float64 { return w / 229 }
	near := func(w, a float64) bool { return math.Abs(amps(w)-a) < 0.2 }
	s := charging(t, 22.08, false, txProfileA(16, 3))
	s.SetCar(0, 1)
	s.GenerateValues(time.Now())
	if got := s.PhasePowers(); !near(got[0], 16) || got[1] != 0 || got[2] != 0 {
		t.Fatalf("one-phase car: %v, want 16 A on L1 only", got)
	}
	s.SetCar(10, 0)
	s.GenerateValues(time.Now())
	if got := s.PhasePowers(); !near(got[0], 10) || !near(got[2], 10) {
		t.Fatalf("10 A car: %v, want 10 A on each phase", got)
	}
	s.SetCar(0, 0)
	s.GenerateValues(time.Now())
	if got := s.PhasePowers(); !near(got[1], 16) {
		t.Fatalf("car without limits: %v, want the 16 A offer", got)
	}
}

// The charger's own load balancer caps the draw per phase below the offer.
func TestLoadBalancerCapsTheDraw(t *testing.T) {
	s := charging(t, 22.08, false, txProfileA(16, 3))
	s.SetLoadBalancer(10)
	s.GenerateValues(time.Now())
	if got := s.PhasePowers(); got[0]/229 > 10.2 || got[0]/229 < 9.8 {
		t.Fatalf("load balancer at 10 A: %v", got)
	}
	s.mu.RLock()
	offer := s.computeEffectivePowerLocked(1, time.Now())
	s.mu.RUnlock()
	if offer < 10900 {
		t.Fatalf("the offer itself is %v W, want the profile's 16 A", offer)
	}
}

// After an offer rises from zero the car waits the start delay, while the
// charger says Charging.
// Every session waits out the delay, not only the first.
func TestStartupDelayEachSession(t *testing.T) {
	s := charging(t, 22.08, false, txProfileA(16, 3))
	s.SetStartupDelay(time.Minute)
	s.connectors[1].offerSince = time.Time{}
	t0 := time.Now()
	s.GenerateValues(t0)
	s.GenerateValues(t0.Add(61 * time.Second))
	if got := s.connectors[1].CurrentPower; got < 11000 {
		t.Fatalf("first session after the delay: %v W", got)
	}
	delete(s.transactions, 1)
	s.GenerateValues(t0.Add(62 * time.Second))
	s.transactions[2] = &Transaction{ID: 2, ConnectorID: 1, StartTime: t0.Add(63 * time.Second)}
	s.connectors[1].Status = StatusCharging
	s.GenerateValues(t0.Add(63 * time.Second))
	if got := s.connectors[1].CurrentPower; got != 0 {
		t.Fatalf("second session at once: %v W, want 0 until the delay passes", got)
	}
	s.GenerateValues(t0.Add(124 * time.Second))
	if got := s.connectors[1].CurrentPower; got < 11000 {
		t.Fatalf("second session after the delay: %v W", got)
	}
}

func TestStartupDelay(t *testing.T) {
	s := charging(t, 22.08, false, txProfileA(16, 3))
	s.SetStartupDelay(time.Minute)
	s.connectors[1].offerSince = time.Time{}
	t0 := time.Now()
	s.GenerateValues(t0)
	if got := s.connectors[1].CurrentPower; got != 0 || s.connectors[1].Status != StatusCharging {
		t.Fatalf("at the offer: %v W, %s", got, s.connectors[1].Status)
	}
	s.GenerateValues(t0.Add(30 * time.Second))
	if got := s.connectors[1].CurrentPower; got != 0 {
		t.Fatalf("after 30 s: %v W", got)
	}
	s.GenerateValues(t0.Add(61 * time.Second))
	if got := s.connectors[1].CurrentPower; got < 11000 {
		t.Fatalf("after the delay: %v W", got)
	}
}
