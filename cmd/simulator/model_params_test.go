package main

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/srcfl/device-simulator/internal/profiles"
)

// Each model names the device-drivers driver it emulates, so field evidence
// for that driver can reach the model later.
func TestProfilesNameTheirDriver(t *testing.T) {
	pm := profiles.NewManager(t.TempDir())
	pm.InitEmbedded()
	generic := map[string]bool{"residential-5kw": true, "commercial-50kw": true, "default": true}
	for _, info := range pm.List() {
		if info.DriverID == "" && !generic[info.Name] {
			t.Errorf("profile %s names no driver", info.Name)
		}
	}
	if got := pm.Get("sungrow-hybrid").DriverID; got != "sungrow" {
		t.Errorf("sungrow-hybrid emulates %q, want sungrow", got)
	}
}

// The model's command_response_s reaches the device when its profile is
// applied, and a simulator's config can override it.
func TestCommandResponseComesFromTheModel(t *testing.T) {
	sim, err := createSimulator("inverter")
	if err != nil {
		t.Fatal(err)
	}
	defer removeTestSimulators(sim.ID)
	rec := httptest.NewRecorder()
	handleProfile(rec, httptest.NewRequest("POST", "/", nil), sim, "sungrow-hybrid/apply")
	if rec.Code != 200 {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	if sim.EMSResponse != 2*time.Second {
		t.Fatalf("command response %v after applying sungrow-hybrid, want 2s", sim.EMSResponse)
	}

	rec = httptest.NewRecorder()
	handleSimulatorConfig(rec, httptest.NewRequest("POST", "/", jsonBody(`{"command_response_s": 0.5}`)), sim)
	if sim.EMSResponse != 500*time.Millisecond {
		t.Fatalf("command response %v after config 0.5 s", sim.EMSResponse)
	}

	cp, err := createSimulator("ocpp_charger")
	if err != nil {
		t.Fatal(err)
	}
	defer removeTestSimulators(cp.ID)
	rec = httptest.NewRecorder()
	handleSimulatorConfig(rec, httptest.NewRequest("POST", "/", jsonBody(`{"command_response_s": 60}`)), cp)
	if got := cp.OCPPServer.ProfileDelay(); got != time.Minute {
		t.Fatalf("charger profile delay %v, want 1m", got)
	}
}
