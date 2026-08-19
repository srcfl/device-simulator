// Package plant is a lumped, Modelica-style electrical energy system:
// algebraic power balance at the site node, and a few explicit ODEs
// (battery SoC, lifetime energy, inverter temperature).
//
// Sign convention matches srcfl/device-drivers Lua drivers:
// positive watts flow INTO the site electrical node.
//
//	P_grid + P_pv + P_batt + P_load + P_ev = 0
//
//	grid     positive = importing, negative = exporting
//	pv       always ≤ 0 (generation)
//	battery  positive = charging, negative = discharging
//	load/ev  positive = consumption
package plant

import (
	"math"
	"time"
)

const (
	CmdCharge    = 170
	CmdDischarge = 187
	CmdStop      = 204

	ModeSelfConsumption = 0
	ModeForced          = 2
	ModeEMS             = 3

	EnableOn  = 170 // 0xAA
	EnableOff = 85  // 0x55
)

// Params are slowly varying physical ratings. They are not the live control
// setpoints written by a Lua driver.
type Params struct {
	RatedACW            float64 // inverter AC clip, W
	RatedPVW            float64 // STC array rating, W
	BatteryCapacityWh   float64
	BatteryNominalV     float64
	ChargeEfficiency    float64 // 0-1, energy stored / energy taken from bus
	DischargeEfficiency float64 // 0-1, energy delivered / energy taken from pack
	MaxChargeW          float64
	MaxDischargeW       float64
	TempCoeffPerK       float64 // PV power temp coefficient (negative), 1/K
	InverterEta         float64 // DC→AC, constant for now
	ThermalTauS         float64 // inverter temperature first-order lag
	AmbientC            float64
}

// Control is the live command surface a driver writes over Modbus.
type Control struct {
	EMSMode           int
	BatteryCmd        int
	BatterySetpointW  float64 // magnitude, W
	PVLimitW          float64 // feed-in / PV watt cap; 0 = no extra cap
	PVLimitEnable     bool
	ActiveLimitEnable bool
	ActiveLimitRatio  float64 // 0-1 of rated AC
	ExportLimitEnable bool
	MaxChargeW        float64 // live, from holding regs; 0 = use Params
	MaxDischargeW     float64
	MinSOC            float64 // 0-100
	MaxSOC            float64 // 0-100
}

// Weather is the exogenous environment.
type Weather struct {
	GHIWm2   float64
	AmbientC float64
}

// Inputs are everything the plant needs for one step besides its own state.
type Inputs struct {
	Time    time.Time
	LoadW   float64
	EVW     float64
	Weather Weather
	Control Control
}

// MPPT is one DC string, derived from total DC power.
type MPPT struct {
	V float64
	A float64
	W float64
}

// Outputs are the quantities a vendor register map should project.
type Outputs struct {
	GridW     float64
	PVW       float64 // ≤ 0
	BatteryW  float64
	LoadW     float64
	EVW       float64
	PVDCW     float64
	MPPTs     [2]MPPT
	BatteryV  float64
	BatteryA  float64
	SOC       float64
	GridHz    float64
	InverterC float64
	BatteryC  float64
	ResidualW float64 // |power-balance error|; should be ~0

	TotalPVWh        float64
	TotalChargeWh    float64
	TotalDischargeWh float64
	TotalImportWh    float64
	TotalExportWh    float64
}

// State is the differential part of the model.
type State struct {
	SOC              float64
	InverterC        float64
	TotalPVWh        float64
	TotalChargeWh    float64
	TotalDischargeWh float64
	TotalImportWh    float64
	TotalExportWh    float64
}

// Plant is one hybrid inverter + battery + on-board meter.
type Plant struct {
	Params Params
	State  State
	lastT  time.Time
	haveT  bool
}

func DefaultParams() Params {
	return Params{
		RatedACW:            8000,
		RatedPVW:            8000,
		BatteryCapacityWh:   10000,
		BatteryNominalV:     48,
		ChargeEfficiency:    0.95,
		DischargeEfficiency: 0.95,
		MaxChargeW:          5000,
		MaxDischargeW:       5000,
		TempCoeffPerK:       -0.004,
		InverterEta:         0.97,
		ThermalTauS:         300,
		AmbientC:            20,
	}
}

func New(p Params) *Plant {
	if p.RatedACW <= 0 {
		p = DefaultParams()
	}
	return &Plant{
		Params: p,
		State: State{
			SOC:       50,
			InverterC: p.AmbientC + 10,
		},
	}
}

// Step advances the plant to in.Time. dt is taken from the previous call so
// the site clock (including time acceleration) is the only integrator clock.
func (p *Plant) Step(in Inputs) Outputs {
	dt := 0.0
	if p.haveT && !in.Time.IsZero() {
		dt = in.Time.Sub(p.lastT).Seconds()
	}
	if dt < 0 {
		dt = 0
	}
	// Cap a single step so a paused UI does not dump days of energy.
	// Time acceleration uses the simulated clock; 3600× with a 1s tick is 1h.
	if dt > 6*3600 {
		dt = 6 * 3600
	}
	p.lastT = in.Time
	p.haveT = true
	return p.step(dt, in)
}

// SeedSOC sets the pack SoC before the first step, so a device that already
// has a register value does not jump back to the plant default.
func (p *Plant) SeedSOC(soc float64) {
	if p.haveT {
		return
	}
	if soc < 0 {
		soc = 0
	}
	if soc > 100 {
		soc = 100
	}
	p.State.SOC = soc
}

func (p *Plant) step(dt float64, in Inputs) Outputs {
	pr := p.Params
	ctrl := in.Control
	ambient := in.Weather.AmbientC
	if ambient == 0 {
		ambient = pr.AmbientC
	}

	maxChg := firstPositive(ctrl.MaxChargeW, pr.MaxChargeW)
	maxDis := firstPositive(ctrl.MaxDischargeW, pr.MaxDischargeW)
	minSOC := ctrl.MinSOC
	maxSOC := ctrl.MaxSOC
	if maxSOC <= 0 || maxSOC > 100 {
		maxSOC = 100
	}
	if minSOC < 0 || minSOC >= maxSOC {
		minSOC = 0
	}

	cellC := ambient + 5
	ghi := math.Max(0, in.Weather.GHIWm2)
	pAvailDC := (ghi / 1000.0) * pr.RatedPVW * (1 + pr.TempCoeffPerK*(cellC-25))
	if pAvailDC < 0 {
		pAvailDC = 0
	}

	eta := pr.InverterEta
	if eta <= 0 || eta > 1 {
		eta = 0.97
	}
	pAvailAC := pAvailDC * eta
	if pAvailAC > pr.RatedACW {
		pAvailAC = pr.RatedACW
	}

	cap := pAvailAC
	if ctrl.ActiveLimitEnable && ctrl.ActiveLimitRatio > 0 {
		cap = math.Min(cap, pr.RatedACW*ctrl.ActiveLimitRatio)
	}
	if ctrl.PVLimitEnable && ctrl.PVLimitW > 0 {
		cap = math.Min(cap, ctrl.PVLimitW)
	}
	pPV := -cap // generation is negative at the site node

	pLoad := math.Max(0, in.LoadW)
	pEV := math.Max(0, in.EVW)

	pBatt := batterySetpoint(ctrl, pPV, pLoad, pEV)
	pBatt = clip(pBatt, -maxDis, maxChg)
	pBatt = applySOCLimits(pBatt, p.State.SOC, minSOC, maxSOC)

	pGrid := -pPV - pBatt - pLoad - pEV

	if ctrl.ExportLimitEnable && ctrl.PVLimitW > 0 && pGrid < -ctrl.PVLimitW {
		// Curtail PV to honour a feed-in cap. Battery already committed.
		// P_grid_target = -PVLimitW (max export)
		// -pPV = pGrid_target + pBatt + pLoad + pEV
		wantPV := -(-ctrl.PVLimitW + pBatt + pLoad + pEV)
		if wantPV > 0 {
			wantPV = 0
		}
		if wantPV < pPV { // pPV is more negative than allowed
			pPV = wantPV
		}
		pGrid = -pPV - pBatt - pLoad - pEV
	}

	// Integrate SoC. Charging: bus power enters the pack through eta_c.
	// Discharging: pack energy leaves through 1/eta_d to deliver pBatt.
	hours := dt / 3600.0
	capWh := pr.BatteryCapacityWh
	if capWh <= 0 {
		capWh = 10000
	}
	if hours > 0 && pBatt != 0 {
		var dWh float64
		if pBatt > 0 {
			dWh = pBatt * hours * clamp01(pr.ChargeEfficiency)
			p.State.TotalChargeWh += pBatt * hours
		} else {
			dWh = pBatt * hours / clamp01(pr.DischargeEfficiency) // pBatt negative
			p.State.TotalDischargeWh += -pBatt * hours
		}
		p.State.SOC = clip(p.State.SOC+(dWh/capWh)*100.0, minSOC, maxSOC)
	}

	if hours > 0 {
		p.State.TotalPVWh += -pPV * hours
		if pGrid > 0 {
			p.State.TotalImportWh += pGrid * hours
		} else if pGrid < 0 {
			p.State.TotalExportWh += -pGrid * hours
		}
	}

	// First-order inverter temperature from conversion losses.
	lossW := pAvailDC - (-pPV)
	if lossW < 0 {
		lossW = 0
	}
	tTarget := ambient + 10 + 20*(lossW/math.Max(pr.RatedACW, 1))
	tau := pr.ThermalTauS
	if tau < 1 {
		tau = 1
	}
	if dt > 0 {
		alpha := 1 - math.Exp(-dt/tau)
		p.State.InverterC += (tTarget - p.State.InverterC) * alpha
	}

	vBatt := pr.BatteryNominalV
	if vBatt <= 0 {
		vBatt = 48
	}
	aBatt := 0.0
	if pBatt != 0 {
		aBatt = math.Abs(pBatt) / vBatt
	}

	mppts := splitMPPT(-pPV, ghi)

	hz := 50.0
	if !in.Time.IsZero() {
		hz = 50.0 + 0.04*math.Sin(float64(in.Time.Second())*0.1)
	}

	out := Outputs{
		GridW:            pGrid,
		PVW:              pPV,
		BatteryW:         pBatt,
		LoadW:            pLoad,
		EVW:              pEV,
		PVDCW:            pAvailDC,
		MPPTs:            mppts,
		BatteryV:         vBatt,
		BatteryA:         aBatt,
		SOC:              p.State.SOC,
		GridHz:           hz,
		InverterC:        p.State.InverterC,
		BatteryC:         ambient + 5,
		ResidualW:        pGrid + pPV + pBatt + pLoad + pEV,
		TotalPVWh:        p.State.TotalPVWh,
		TotalChargeWh:    p.State.TotalChargeWh,
		TotalDischargeWh: p.State.TotalDischargeWh,
		TotalImportWh:    p.State.TotalImportWh,
		TotalExportWh:    p.State.TotalExportWh,
	}
	return out
}

func batterySetpoint(ctrl Control, pPV, pLoad, pEV float64) float64 {
	switch ctrl.EMSMode {
	case ModeForced, ModeEMS:
		switch ctrl.BatteryCmd {
		case CmdCharge:
			return math.Abs(ctrl.BatterySetpointW)
		case CmdDischarge:
			return -math.Abs(ctrl.BatterySetpointW)
		default:
			return 0
		}
	default:
		// Self-consumption: battery is the slack that tries to zero the grid.
		return -pPV - pLoad - pEV
	}
}

func applySOCLimits(pBatt, soc, minSOC, maxSOC float64) float64 {
	if pBatt > 0 && soc >= maxSOC-0.05 {
		return 0
	}
	if pBatt < 0 && soc <= minSOC+0.05 {
		return 0
	}
	return pBatt
}

func splitMPPT(pvPosW, ghi float64) [2]MPPT {
	if pvPosW <= 0 {
		return [2]MPPT{}
	}
	v := 300.0 + 80.0*(ghi/1000.0)
	if v < 80 {
		v = 80
	}
	half := pvPosW / 2
	a := half / v
	m := MPPT{V: v, A: a, W: half}
	return [2]MPPT{m, m}
}

func clip(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clamp01(v float64) float64 {
	if v <= 0.05 {
		return 0.05
	}
	if v > 1 {
		return 1
	}
	return v
}

func firstPositive(a, b float64) float64 {
	if a > 0 {
		return a
	}
	return b
}

// GHIClearSky is a simple elevation sine between sunrise and sunset, W/m².
func GHIClearSky(t time.Time, peakWm2, sunriseH, sunsetH float64) float64 {
	if peakWm2 <= 0 {
		peakWm2 = 1000
	}
	hour := float64(t.Hour()) + float64(t.Minute())/60.0 + float64(t.Second())/3600.0
	if hour < sunriseH || hour > sunsetH {
		return 0
	}
	day := sunsetH - sunriseH
	if day <= 0 {
		return 0
	}
	return peakWm2 * math.Sin((hour-sunriseH)/day*math.Pi)
}

// WeatherForScenario maps the existing UI weather names onto GHI.
func WeatherForScenario(t time.Time, scenario string) Weather {
	ghi := GHIClearSky(t, 1000, 6, 20)
	switch scenario {
	case "cloudy":
		ghi *= 0.45 * (0.7 + 0.3*math.Sin(hourAngle(t)*3))
	case "overcast":
		ghi *= 0.15
	case "rainy":
		ghi *= 0.05
	case "night":
		ghi = 0
	case "peak_load":
		// still a sunny irradiance; load is applied separately
	case "physical", "sunny", "battery_test", "":
		// clear sky
	}
	return Weather{GHIWm2: math.Max(0, ghi), AmbientC: 20}
}

func hourAngle(t time.Time) float64 {
	return float64(t.Hour()) + float64(t.Minute())/60.0
}

// LoadForScenario is a coarse household load in watts. Site aggregation can
// replace this with its own phase loads.
func LoadForScenario(t time.Time, scenario string) float64 {
	hour := hourAngle(t)
	load := 800.0
	if (hour >= 7 && hour <= 9) || (hour >= 17 && hour <= 22) {
		load = 2000
	}
	switch scenario {
	case "peak_load":
		return 4500
	case "night":
		return 600
	case "rainy":
		return 1500
	case "battery_test":
		return 1500
	}
	return load
}
