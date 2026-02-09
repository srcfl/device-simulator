package mdns

import (
	"testing"
)

// newTestAdvertiser creates an Advertiser with network lookup disabled
// so tests don't depend on mDNS network state.
func newTestAdvertiser() *Advertiser {
	a := New()
	a.lookup = nil // disable network collision checks in tests
	return a
}

func TestNewAdvertiser(t *testing.T) {
	a := New()
	if a == nil {
		t.Fatal("New() returned nil")
	}
	if len(a.servers) != 0 {
		t.Errorf("expected empty servers map, got %d entries", len(a.servers))
	}
	if len(a.hosts) != 0 {
		t.Errorf("expected empty hosts map, got %d entries", len(a.hosts))
	}
}

func TestAdvertiseEmptyHostname(t *testing.T) {
	a := newTestAdvertiser()
	err := a.Advertise(1, "", 1883, "_mqtt._tcp", nil)
	if err != nil {
		t.Errorf("expected nil error for empty hostname, got %v", err)
	}
	if len(a.servers) != 0 {
		t.Error("expected no servers for empty hostname")
	}
}

func TestAdvertiseAndRemove(t *testing.T) {
	a := newTestAdvertiser()
	defer a.Shutdown()

	err := a.Advertise(1, "test-device", 1883, "_mqtt._tcp", nil)
	if err != nil {
		t.Fatalf("Advertise failed: %v", err)
	}

	hostname := a.ActiveHostname(1)
	if hostname != "test-device" {
		t.Errorf("expected hostname 'test-device', got '%s'", hostname)
	}

	a.Remove(1)

	hostname = a.ActiveHostname(1)
	if hostname != "" {
		t.Errorf("expected empty hostname after remove, got '%s'", hostname)
	}
}

func TestInternalCollisionDetection(t *testing.T) {
	a := newTestAdvertiser()
	defer a.Shutdown()

	err := a.Advertise(1, "sid-os", 1883, "_mqtt._tcp", nil)
	if err != nil {
		t.Fatalf("first Advertise failed: %v", err)
	}

	err = a.Advertise(2, "sid-os", 1884, "_mqtt._tcp", nil)
	if err != nil {
		t.Fatalf("second Advertise failed: %v", err)
	}

	host1 := a.ActiveHostname(1)
	host2 := a.ActiveHostname(2)

	if host1 == host2 {
		t.Errorf("expected different hostnames, both got '%s'", host1)
	}
	if host1 != "sid-os" {
		t.Errorf("expected first hostname 'sid-os', got '%s'", host1)
	}
	if host2 != "sid-os-1" {
		t.Errorf("expected second hostname 'sid-os-1', got '%s'", host2)
	}
}

func TestNetworkCollisionDetection(t *testing.T) {
	// Simulate a network where "taken-host" is already in use
	a := New()
	a.lookup = func(hostname string) bool {
		return hostname == "taken-host"
	}
	defer a.Shutdown()

	err := a.Advertise(1, "taken-host", 1883, "_mqtt._tcp", nil)
	if err != nil {
		t.Fatalf("Advertise failed: %v", err)
	}

	hostname := a.ActiveHostname(1)
	if hostname != "taken-host-1" {
		t.Errorf("expected hostname 'taken-host-1' (network collision avoidance), got '%s'", hostname)
	}
}

func TestRemoveNonexistent(t *testing.T) {
	a := newTestAdvertiser()
	// Should not panic
	a.Remove(999)
}

func TestShutdown(t *testing.T) {
	a := newTestAdvertiser()

	err := a.Advertise(1, "device-a", 1883, "_mqtt._tcp", nil)
	if err != nil {
		t.Fatalf("Advertise failed: %v", err)
	}
	err = a.Advertise(2, "device-b", 1884, "_mqtt._tcp", nil)
	if err != nil {
		t.Fatalf("Advertise failed: %v", err)
	}

	a.Shutdown()

	if len(a.servers) != 0 {
		t.Errorf("expected empty servers after shutdown, got %d", len(a.servers))
	}
	if len(a.hosts) != 0 {
		t.Errorf("expected empty hosts after shutdown, got %d", len(a.hosts))
	}
}

func TestActiveHostnameUnknown(t *testing.T) {
	a := newTestAdvertiser()
	hostname := a.ActiveHostname(42)
	if hostname != "" {
		t.Errorf("expected empty hostname for unknown sim, got '%s'", hostname)
	}
}

func TestReAdvertiseSameSimulator(t *testing.T) {
	a := newTestAdvertiser()
	defer a.Shutdown()

	err := a.Advertise(1, "device-x", 1883, "_mqtt._tcp", nil)
	if err != nil {
		t.Fatalf("first Advertise failed: %v", err)
	}

	// Re-advertise same simulator with different hostname
	err = a.Advertise(1, "device-y", 1884, "_mqtt._tcp", nil)
	if err != nil {
		t.Fatalf("second Advertise failed: %v", err)
	}

	hostname := a.ActiveHostname(1)
	if hostname != "device-y" {
		t.Errorf("expected hostname 'device-y' after re-advertise, got '%s'", hostname)
	}

	// Old hostname should be freed
	if _, taken := a.hosts["device-x"]; taken {
		t.Error("old hostname 'device-x' should have been freed")
	}
}

func TestLocalIP(t *testing.T) {
	ip := localIP()
	if ip == nil {
		t.Fatal("localIP() returned nil")
	}
	// Should be an IPv4 address
	if ip.To4() == nil {
		t.Errorf("expected IPv4 address, got %v", ip)
	}
}

func TestDefaultServiceType(t *testing.T) {
	a := newTestAdvertiser()
	defer a.Shutdown()

	// Empty service type should default to _device._tcp
	err := a.Advertise(1, "test-default-svc", 5000, "", nil)
	if err != nil {
		t.Fatalf("Advertise with empty service type failed: %v", err)
	}

	hostname := a.ActiveHostname(1)
	if hostname != "test-default-svc" {
		t.Errorf("expected hostname 'test-default-svc', got '%s'", hostname)
	}
}
