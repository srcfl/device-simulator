package plant

import (
	"fmt"
	"os"
)

// LoadFMU plugs in a Modelica-compiled FMI 2.0 co-simulation FMU as the
// site backend. The public variable names must match modelica/SiteEnergy.mo.
//
// Compile (OpenModelica):
//
//	omc -s -s FMI -n=SiteEnergy.Site modelica/SiteEnergy.mo
//
// This repository does not vendor libfmi; when an FMU exists, implement the
// mapping here (SetReal / DoStep / GetReal) and return it from this function.
func LoadFMU(path string) (Model, error) {
	if path == "" {
		return nil, fmt.Errorf("plant: empty FMU path")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("plant: FMU %s: %w", path, err)
	}
	return nil, fmt.Errorf("plant: FMU backend not linked yet (found %s); native site model is the default", path)
}
