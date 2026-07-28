package matter

import (
	"testing"
	"time"
)

// newPhysServer builds a Server for physics tests without touching the bridge.
func newPhysServer(t *testing.T, typeKey string) *Server {
	t.Helper()
	s, err := NewServer(1, typeKey)
	if err != nil {
		t.Fatalf("NewServer(%q): %v", typeKey, err)
	}
	return s
}

func findChange(changes []AttrChange, cluster, attr string) (AttrChange, bool) {
	for _, c := range changes {
		if c.Cluster == cluster && c.Attribute == attr {
			return c, true
		}
	}
	return AttrChange{}, false
}

func TestAllDeviceTypesResolve(t *testing.T) {
	want := []string{
		"matter_tempsensor", "matter_lightsensor", "matter_smartplug", "matter_evse",
		"matter_thermostat", "matter_heatpump", "matter_dishwasher", "matter_laundrywasher",
	}
	for _, st := range want {
		if !IsMatterType(st) {
			t.Errorf("expected %q to be a matter type", st)
		}
		if lookupDeviceType(st) == nil {
			t.Errorf("lookupDeviceType(%q) == nil", st)
		}
	}
	if len(SimTypes()) != len(want) {
		t.Errorf("SimTypes len = %d, want %d", len(SimTypes()), len(want))
	}
}

func TestSmartPlugPhysics(t *testing.T) {
	s := newPhysServer(t, "smartplug")
	dt := s.dt
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	// Off: no power.
	changes := dt.Generate(s, now)
	if c, ok := findChange(changes, "electricalPowerMeasurement", "activePower"); !ok || c.Value.(int64) != 0 {
		t.Errorf("off activePower = %v, want 0", c.Value)
	}

	// Turn on via SetValue.
	setChanges, handled := dt.SetValue(s, "on", true)
	if !handled {
		t.Fatal("SetValue on not handled")
	}
	if c, ok := findChange(setChanges, "onOff", "onOff"); !ok || c.Value != true {
		t.Errorf("onOff change = %v", c.Value)
	}

	// On: power flows and energy accumulates.
	changes = dt.Generate(s, now)
	c, ok := findChange(changes, "electricalPowerMeasurement", "activePower")
	if !ok || c.Value.(int64) <= 0 {
		t.Errorf("on activePower = %v, want > 0", c.Value)
	}
	if s.getF("energy_wh", 0) <= 0 {
		t.Error("expected energy to accumulate while on")
	}

	// External off reflects into physics.
	dt.OnExternal(s, "onOff", "onOff", false)
	if s.getBool("on", true) {
		t.Error("external off did not update physics")
	}
}

func TestThermostatStepHeats(t *testing.T) {
	s := newPhysServer(t, "thermostat")
	s.setF("local_c", 15)
	s.setF("heat_sp_c", 22)
	s.setF("system_mode", 4) // Heat

	running := 0
	thermostatStep(s, &running)
	if running&0x01 == 0 {
		t.Error("expected heat running bit set")
	}
	if s.getF("local_c", 0) <= 15 {
		t.Error("expected local temperature to rise toward setpoint")
	}

	// Setpoint change via SetValue emits a Matter attribute write.
	changes, handled := s.dt.SetValue(s, "heating_setpoint_c", 23.0)
	if !handled {
		t.Fatal("setpoint not handled")
	}
	if c, ok := findChange(changes, "thermostat", "occupiedHeatingSetpoint"); !ok || c.Value.(int) != 2300 {
		t.Errorf("occupiedHeatingSetpoint = %v, want 2300", c.Value)
	}
}

func TestEvseChargingAccumulates(t *testing.T) {
	s := newPhysServer(t, "evse")
	now := time.Now()
	if _, handled := s.dt.SetValue(s, "charging", true); !handled {
		t.Fatal("charging not handled")
	}
	changes := s.dt.Generate(s, now)
	if c, ok := findChange(changes, "energyEvse", "state"); !ok || c.Value.(int) != 3 {
		t.Errorf("evse state = %v, want 3 (charging)", c.Value)
	}
	if s.getF("session_wh", 0) <= 0 {
		t.Error("expected session energy to accumulate while charging")
	}
}

func TestApplianceCycle(t *testing.T) {
	s := newPhysServer(t, "dishwasher")
	s.setF("cycle_len", 3) // short cycle for the test
	changes, handled := s.dt.SetValue(s, "start", nil)
	if !handled {
		t.Fatal("start not handled")
	}
	if c, ok := findChange(changes, "operationalState", "operationalState"); !ok || c.Value.(int) != 1 {
		t.Errorf("start operationalState = %v, want 1 (Running)", c.Value)
	}
	// Tick down to completion; capture the last emitted change (the stop event,
	// after which Generate returns nil).
	var last []AttrChange
	for i := 0; i < 5; i++ {
		if ch := s.dt.Generate(s, time.Now()); ch != nil {
			last = ch
		}
	}
	if c, ok := findChange(last, "operationalState", "operationalState"); !ok || c.Value.(int) != 0 {
		t.Errorf("ended operationalState = %v, want 0 (Stopped)", c.Value)
	}
	if s.getBool("running", true) {
		t.Error("expected cycle to stop")
	}
}

func TestLuxToMeasured(t *testing.T) {
	if luxToMeasured(0) != 1 {
		t.Errorf("luxToMeasured(0) = %d, want 1", luxToMeasured(0))
	}
	dark := luxToMeasured(10)
	bright := luxToMeasured(50000)
	if !(bright > dark) {
		t.Errorf("expected brighter lux to encode higher: dark=%d bright=%d", dark, bright)
	}
}

func TestPasscodeAndDiscriminatorStableAndValid(t *testing.T) {
	for id := 0; id < 50; id++ {
		p := derivePasscode(id)
		if p < 1 || p > 99999998 {
			t.Errorf("passcode %d out of range for id %d", p, id)
		}
		d := deriveDiscriminator(id)
		if d < 0 || d > 0x0FFF {
			t.Errorf("discriminator %d out of range for id %d", d, id)
		}
		if derivePasscode(id) != p || deriveDiscriminator(id) != d {
			t.Errorf("derivation not stable for id %d", id)
		}
	}
}
