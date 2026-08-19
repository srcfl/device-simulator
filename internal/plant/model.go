package plant

import "time"

// Model is the plug the simulator talks to. Native Go and a future
// FMI-2 co-simulation FMU both implement it. Names match public
// variables in modelica/SiteEnergy.mo.
type Model interface {
	Backend() string
	StepSite(dt float64, in SiteStep) (SiteResult, error)
}

// SiteStep is everything the site bus needs for one integrator tick.
// Load is the house (site meter), not a per-inverter phantom load.
type SiteStep struct {
	Time              time.Time
	Weather           Weather
	LoadW             float64
	EVW               float64
	ExportLimitW      float64
	ExportLimitEnable bool
	Members           []MemberStep
}

// MemberStep is one inverter/battery attached to the AC bus.
type MemberStep struct {
	Key     string
	Control Control
}

// MemberResult is one member's terminals after the bus has been solved.
type MemberResult struct {
	Key string
	Out Outputs
}

// SiteResult is the solved AC node plus per-member terminals.
type SiteResult struct {
	GridW     float64
	LoadW     float64
	EVW       float64
	ResidualW float64
	Members   []MemberResult
}
