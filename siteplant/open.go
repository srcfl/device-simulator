// Package siteplant is the pluggable site physics backend.
//
//	modelica/SiteEnergy.mo  --(omc)-->  Site4.fmu
//	                                |
//	                     siteplant.Open  -->  plant.Model
//	                                |
//	                     native Go DAE if no FMU / CGO / >4 units
package siteplant

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/srcfl/device-simulator/internal/plant"
)

// MaxUnits is the inverter count compiled into SiteEnergy.Site4.
const MaxUnits = 4

// Options select which physics backend to plug in.
type Options struct {
	// FMU is a path to an FMI 2.0 CS FMU compiled from modelica/SiteEnergy.Site4.
	// Empty means: SITEPLANT_FMU, then <repo>/modelica/Site4.fmu, then native.
	// An explicit path (or SITEPLANT_FMU) fails closed if the FMU cannot load.
	FMU string
	// Native forces the Go backend even if an FMU is present.
	Native bool
}

type mux struct {
	fmu    plant.Model
	native *plant.NativeSite
}

func (m *mux) Backend() string {
	if m.fmu != nil {
		return m.fmu.Backend()
	}
	return m.native.Backend()
}

func (m *mux) Attach(key string, unit *plant.Plant) {
	if m.fmu != nil {
		m.fmu.Attach(key, unit)
	}
	m.native.Attach(key, unit)
}

// SetFacility attaches envelope / HP / DHW. Thermal is native Go; Site4.fmu
// is electrical-only, so a facility forces the native StepSite path.
func (m *mux) SetFacility(f plant.Facility) {
	m.native.SetFacility(f)
}

func (m *mux) StepSite(dt float64, in plant.SiteStep) (plant.SiteResult, error) {
	if m.native.Facility().ID != "" {
		return m.native.StepSite(dt, in)
	}
	if m.fmu != nil && len(in.Members) <= MaxUnits {
		return m.fmu.StepSite(dt, in)
	}
	return m.native.StepSite(dt, in)
}

// Open returns a site plant. The simulator talks only to plant.Model.
//
// Default (no FMU on disk, Docker CGO_ENABLED=0, macOS/Windows): native Go.
// Linux amd64 with a compiled Site4.fmu: FMI 2.0 co-simulation, falling back
// to native when the site has more than MaxUnits inverters.
func Open(opts Options) (plant.Model, error) {
	native := plant.NewNativeSite()
	if opts.Native || os.Getenv("SITEPLANT_NATIVE") == "1" {
		return native, nil
	}

	explicit := opts.FMU != ""
	path := opts.FMU
	if path == "" {
		if env := os.Getenv("SITEPLANT_FMU"); env != "" {
			path = env
			explicit = true
		}
	}
	if path == "" {
		path = lookupDefaultFMU()
	}
	if path == "" {
		return native, nil
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		if explicit {
			return nil, fmt.Errorf("siteplant: FMU not found: %s", path)
		}
		return native, nil
	}

	m, err := LoadFMU(path)
	if err != nil {
		if explicit {
			return nil, fmt.Errorf("siteplant: fmu %s: %w", path, err)
		}
		return native, nil
	}
	return &mux{fmu: m, native: native}, nil
}

func lookupDefaultFMU() string {
	var candidates []string
	if _, file, _, ok := runtime.Caller(0); ok {
		root := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
		candidates = append(candidates, filepath.Join(root, "modelica", "Site4.fmu"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "modelica", "Site4.fmu"))
	}
	candidates = append(candidates, "modelica/Site4.fmu", "Site4.fmu")
	seen := map[string]struct{}{}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}
