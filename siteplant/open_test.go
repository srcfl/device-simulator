package siteplant

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/srcfl/device-simulator/internal/plant"
)

func TestOpenNative(t *testing.T) {
	m, err := Open(Options{Native: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.Backend() != "native" {
		t.Fatalf("backend %s", m.Backend())
	}
	u := plant.New(plant.DefaultParams())
	m.Attach("a", u)
	out, err := m.StepSite(1, plant.SiteStep{
		Time:    time.Now(),
		Weather: plant.Weather{},
		LoadW:   1000,
		Members: []plant.MemberStep{{
			Key: "a",
			Control: plant.Control{
				EMSMode:          plant.ModeEMS,
				BatteryCmd:       plant.CmdDischarge,
				BatterySetpointW: 2500,
				MaxSOC:           100,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(out.GridW-1500) > 1 {
		t.Fatalf("grid %v want 1500", out.GridW)
	}
}

func TestOpenExplicitMissingFMU(t *testing.T) {
	_, err := Open(Options{FMU: filepath.Join(t.TempDir(), "nope.fmu")})
	if err == nil {
		t.Fatal("expected error for missing explicit FMU")
	}
}

func TestOpenEnvNative(t *testing.T) {
	t.Setenv("SITEPLANT_NATIVE", "1")
	t.Setenv("SITEPLANT_FMU", filepath.Join(t.TempDir(), "missing.fmu"))
	m, err := Open(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Backend() != "native" {
		t.Fatalf("backend %s", m.Backend())
	}
}

func TestCompileModelicaWhenAsked(t *testing.T) {
	if os.Getenv("SITEPLANT_COMPILE") != "1" {
		t.Skip("set SITEPLANT_COMPILE=1 to force omc export")
	}
	p := compileTestFMU(t)
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}

func compileTestFMU(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("omc"); err != nil {
		t.Skip("Site4.fmu not built and omc not on PATH")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	src := filepath.Join(filepath.Dir(file), "..", "modelica", "SiteEnergy.mo")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SiteEnergy.mo"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	mos := "loadFile(\"SiteEnergy.mo\"); getErrorString();\n" +
		"buildModelFMU(SiteEnergy.Site4, version=\"2.0\", fmuType=\"cs\", fileNamePrefix=\"Site4\");\n" +
		"getErrorString();\n"
	if err := os.WriteFile(filepath.Join(dir, "export.mos"), []byte(mos), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("omc", "export.mos")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("omc: %v\n%s", err, out)
	}
	fmu := filepath.Join(dir, "Site4.fmu")
	if _, err := os.Stat(fmu); err != nil {
		t.Fatalf("omc did not write Site4.fmu:\n%s", out)
	}
	return fmu
}
