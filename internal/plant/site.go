package plant

import "math"

// NativeSite is the in-process backend. It implements the same connection
// equations as modelica/SiteEnergy.mo so the simulator runs without OpenModelica.
// Swap it for a compiled FMU via siteplant.Open.
type NativeSite struct {
	units   map[string]*Plant
	order   []string
	fac     Facility
	indoorC float64
	dhwC    float64
}

func NewNativeSite() *NativeSite {
	return &NativeSite{units: make(map[string]*Plant), indoorC: 19, dhwC: 45}
}

func (s *NativeSite) Backend() string { return "native" }

// SetFacility attaches envelope / HP / DHW parameters. Empty ID is electrical-only.
func (s *NativeSite) SetFacility(f Facility) {
	s.fac = f
	if s.indoorC == 0 {
		s.indoorC = 19
	}
	if s.dhwC == 0 {
		s.dhwC = 45
	}
}

func (s *NativeSite) Facility() Facility { return s.fac }

// Attach adds or replaces a member unit. The key should be stable (simulator id).
func (s *NativeSite) Attach(key string, unit *Plant) {
	if _, ok := s.units[key]; !ok {
		s.order = append(s.order, key)
	}
	s.units[key] = unit
}

func (s *NativeSite) Unit(key string) *Plant { return s.units[key] }

func (s *NativeSite) StepSite(dt float64, in SiteStep) (SiteResult, error) {
	if dt < 0 {
		dt = 0
	}
	if dt > 6*3600 {
		dt = 6 * 3600
	}
	pHouse := math.Max(0, in.LoadW)
	if s.fac.ID != "" {
		pHouse += occupancyPlugW(in.Time, s.fac.Building.FloorM2)
	}
	th := s.stepThermal(dt, in, in.HeatPump)
	pHP := math.Max(0, th.ElectricalW+th.BackupW)
	pLoad := pHouse + pHP
	pEV := math.Max(0, in.EVW)

	type row struct {
		key      string
		unit     *Plant
		ctrl     Control
		pPV, pDC float64
		pBatt    float64
		self     bool
		maxChg   float64
		maxDis   float64
	}

	rows := make([]row, 0, len(in.Members))
	for _, m := range in.Members {
		u := s.units[m.Key]
		if u == nil {
			u = New(DefaultParams())
			s.Attach(m.Key, u)
		}
		pPV, pDC := u.previewPV(in.Weather, m.Control)
		self := m.Control.EMSMode != ModeForced && m.Control.EMSMode != ModeEMS
		pBatt := 0.0
		if !self {
			pBatt = u.clipBattery(batterySetpoint(m.Control, pPV, 0, 0), m.Control)
		}
		rows = append(rows, row{
			key:    m.Key,
			unit:   u,
			ctrl:   m.Control,
			pPV:    pPV,
			pDC:    pDC,
			pBatt:  pBatt,
			self:   self,
			maxChg: firstPositive(m.Control.MaxChargeW, u.Params.MaxChargeW),
			maxDis: firstPositive(m.Control.MaxDischargeW, u.Params.MaxDischargeW),
		})
	}

	// Site self-consumption: batteries in default mode share the residual
	// that would otherwise hit the grid. EMS/forced packs already have a
	// setpoint and are not slacks.
	var pPVsum, pBattEMS float64
	var slackChg, slackDis float64
	for _, r := range rows {
		pPVsum += r.pPV
		if r.self {
			slackChg += r.maxChg
			slackDis += r.maxDis
		} else {
			pBattEMS += r.pBatt
		}
	}
	residual := pLoad + pEV + pPVsum + pBattEMS // power that wants to leave the bus to the grid
	// residual > 0 → need discharge (or import). residual < 0 → need charge (or export).
	wantBatt := -residual
	for i := range rows {
		if !rows[i].self {
			continue
		}
		share := 0.0
		if wantBatt > 0 && slackChg > 0 {
			share = wantBatt * (rows[i].maxChg / slackChg)
		} else if wantBatt < 0 && slackDis > 0 {
			share = wantBatt * (rows[i].maxDis / slackDis)
		}
		rows[i].pBatt = rows[i].unit.clipBattery(share, rows[i].ctrl)
	}

	var pBattSum float64
	for _, r := range rows {
		pBattSum += r.pBatt
	}
	pGrid := -pPVsum - pBattSum - pLoad - pEV

	if in.ExportLimitEnable && in.ExportLimitW > 0 && pGrid < -in.ExportLimitW {
		// Curtail PV (prefer self-consumption units) so export stays at the cap.
		need := -in.ExportLimitW - pGrid // positive watts of extra curtailment
		for i := range rows {
			if need <= 0 {
				break
			}
			avail := -rows[i].pPV
			if avail <= 0 {
				continue
			}
			cut := math.Min(avail, need)
			rows[i].pPV += cut // pPV is negative; adding cut reduces generation
			need -= cut
		}
		pPVsum = 0
		pBattSum = 0
		for _, r := range rows {
			pPVsum += r.pPV
			pBattSum += r.pBatt
		}
		pGrid = -pPVsum - pBattSum - pLoad - pEV
	}

	ambient := in.Weather.AmbientC
	if ambient == 0 {
		ambient = 20
	}

	out := SiteResult{
		GridW:   pGrid,
		LoadW:   pLoad,
		HouseW:  pHouse,
		HPW:     pHP,
		EVW:     pEV,
		Thermal: th,
		Members: make([]MemberResult, 0, len(rows)),
	}
	var check float64
	check += pGrid + pLoad + pEV
	for _, r := range rows {
		minSOC, maxSOC := socBounds(r.ctrl)
		memOut := r.unit.commit(dt, in.Time, in.Weather, r.pPV, r.pDC, r.pBatt, 0, 0, 0, ambient, minSOC, maxSOC)
		memOut.LoadW = pLoad
		memOut.EVW = pEV
		memOut.GridW = pGrid
		memOut.ResidualW = 0
		out.Members = append(out.Members, MemberResult{Key: r.key, Out: memOut})
		check += r.pPV + r.pBatt
	}
	out.ResidualW = check
	return out, nil
}
