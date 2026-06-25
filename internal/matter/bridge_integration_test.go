//go:build matter_integration

// Real end-to-end tests that spawn the embedded matter.js bridge with a live
// Node.js runtime. Excluded from the default build so `go test ./...` stays
// Node-free on CI. Run with:
//
//	go test -tags matter_integration ./internal/matter/...
package matter

import (
	"os/exec"
	"testing"
	"time"
)

func requireNode(t *testing.T) {
	t.Helper()
	if info := DetectNode(); !info.Available {
		t.Skipf("node not available: %s", info.Reason)
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
}

func TestIntegrationCommissionAllTypes(t *testing.T) {
	requireNode(t)
	t.Setenv("MATTER_STORAGE_PATH", t.TempDir())
	t.Setenv("MATTER_LOG_LEVEL", "fatal")

	m := Shared()
	t.Cleanup(m.Shutdown)

	port := 5740
	seen := map[string]bool{}
	for _, dt := range deviceTypes {
		dt := dt
		t.Run(dt.Key, func(t *testing.T) {
			s, err := NewServer(port, dt.Key)
			if err != nil {
				t.Fatal(err)
			}
			s.SetPort(port)
			port++

			if err := s.Start(); err != nil {
				t.Fatalf("Start %s: %v", dt.Key, err)
			}
			t.Cleanup(func() { _ = s.Stop() })

			code := s.PairingCode()
			qr := s.QRCode()
			if len(code) != 11 {
				t.Errorf("%s: manual pairing code = %q (want 11 digits)", dt.Key, code)
			}
			if len(qr) < 5 || qr[:3] != "MT:" {
				t.Errorf("%s: qr payload = %q (want MT:...)", dt.Key, qr)
			}
			if seen[code] {
				t.Errorf("%s: duplicate pairing code %q (should be unique per device)", dt.Key, code)
			}
			seen[code] = true

			// Drive one simulation tick; must not error.
			s.GenerateValues(time.Now())
		})
	}
}

func TestIntegrationSmartPlugControl(t *testing.T) {
	requireNode(t)
	t.Setenv("MATTER_STORAGE_PATH", t.TempDir())
	t.Setenv("MATTER_LOG_LEVEL", "fatal")

	m := Shared()
	t.Cleanup(m.Shutdown)

	s, err := NewServer(5800, "smartplug")
	if err != nil {
		t.Fatal(err)
	}
	s.SetPort(5800)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop() })

	if err := s.SetValue("on", true); err != nil {
		t.Fatalf("turn on: %v", err)
	}
	for i := 0; i < 3; i++ {
		s.GenerateValues(time.Now())
	}
	if s.getF("energy_wh", 0) <= 0 {
		t.Error("expected energy to accumulate after turning plug on")
	}
}
