package plant

import "math"

// HeatPumpCommand is the live EMS / SG-Ready surface of the heat pump device.
type HeatPumpCommand struct {
	Connected bool    // true when a heat-pump facade is on the site and running
	Enable    bool    // holding hp_enable; ignored until Connected
	SetpointC float64 // 0 = heating curve
	Mode      int     // 0 auto, 1 heat, 2 off, 3 dhw
	SGReady   int     // 0 normal, 1 block, 2 rec on, 3 boost
}

// Thermal is the lumped building + DHW + HP after one site tick.
type Thermal struct {
	IndoorC      float64 `json:"indoor_c"`
	SetpointC    float64 `json:"setpoint_c"`
	AmbientC     float64 `json:"ambient_c"`
	DHWC         float64 `json:"dhw_c"`
	QHpW         float64 `json:"q_hp_w"`
	QSpaceW      float64 `json:"q_space_w"`
	QDHWW        float64 `json:"q_dhw_w"`
	QLossW       float64 `json:"q_loss_w"`
	QSolarW      float64 `json:"q_solar_w"`
	QInternalW   float64 `json:"q_internal_w"`
	QDrawW       float64 `json:"q_draw_w"`
	ElectricalW  float64 `json:"electrical_w"`
	BackupW      float64 `json:"backup_w"`
	COP          float64 `json:"cop"`
	HPType       string  `json:"hp_type"`
	Occupied     bool    `json:"occupied"`
	NightSetback bool    `json:"night_setback"`
	SGReady      int     `json:"sg_ready"`
}

func (s *NativeSite) stepThermal(dt float64, in SiteStep, cmd HeatPumpCommand) Thermal {
	f := s.fac
	amb := in.Weather.AmbientC
	if amb == 0 {
		amb = OutdoorTemp(in.Time, f.Climate)
	}
	th := Thermal{
		AmbientC: amb,
		IndoorC:  s.indoorC,
		DHWC:     s.dhwC,
		HPType:   string(f.HeatPump.Type),
	}
	if f.ID == "" {
		return th
	}
	if s.indoorC == 0 {
		s.indoorC = 19
	}
	if s.dhwC == 0 && f.DHW.VolumeL > 0 {
		s.dhwC = 45
	}

	occ := occupied(in.Time)
	th.Occupied = occ
	th.NightSetback = nightSetback(in.Time)
	th.SGReady = cmd.SGReady
	tSet := heatingCurve(amb, in.Time, cmd)
	th.SetpointC = tSet

	b := f.Building
	if b.UAWPerK <= 0 {
		b = BuildingSpec{UAWPerK: 140, CapJPerK: 18e6, FloorM2: 140, SolarGainM2: 8}
	}
	qLoss := b.UAWPerK * (s.indoorC - amb)
	qSolar := math.Max(0, in.Weather.GHIWm2) * b.SolarGainM2 * 0.55
	qInt := 1.4 * b.FloorM2
	if occ {
		qInt = 4.0 * b.FloorM2
	}

	hpOn := f.HasHeatPump() && hpAllowed(cmd)
	cop := heatPumpCOP(f.HeatPump, amb)
	qRated := f.HeatPump.RatedThW
	if qRated <= 0 {
		qRated = 8000
	}
	if cmd.SGReady == 3 {
		qRated *= 1.15
	}

	qSpaceWant := 0.0
	if hpOn && cmd.Mode != 3 {
		qFF := b.UAWPerK*(tSet-amb) - qSolar - qInt
		qP := 4 * b.UAWPerK * (tSet - s.indoorC)
		qSpaceWant = math.Max(0, qFF+qP)
		if s.indoorC > tSet+0.6 {
			qSpaceWant = 0
		}
	}

	qDHWWant := 0.0
	if hpOn && f.DHW.VolumeL > 0 && cmd.Mode != 2 {
		dhwSet := f.DHW.SetpointC
		if dhwSet <= 0 {
			dhwSet = 50
		}
		if s.dhwC < dhwSet-1 {
			qDHWWant = math.Min(4000, (dhwSet-s.dhwC)*800)
			if cmd.Mode == 3 {
				qDHWWant = math.Min(qRated, qDHWWant+2000)
			}
		}
	}

	qHp := math.Min(qRated, qSpaceWant+qDHWWant)
	qSpace := 0.0
	qDHW := 0.0
	if qHp > 0 {
		if qSpaceWant+qDHWWant > 0 {
			qSpace = qHp * (qSpaceWant / (qSpaceWant + qDHWWant))
			qDHW = qHp - qSpace
		}
	}
	pEl := 0.0
	if qHp > 1 && cop > 0.5 {
		pEl = qHp / cop
	}

	backup := 0.0
	if f.DHW.VolumeL > 0 && s.dhwC < 40 && qDHW < 500 {
		backup = 3000
	}

	if dt > 0 && b.CapJPerK > 0 {
		s.indoorC += (qSpace + qSolar + qInt - qLoss) * dt / b.CapJPerK
	}
	if f.DHW.VolumeL > 0 {
		cap := f.DHW.VolumeL * 4186 // J/K, water
		ua := f.DHW.UAWPerK
		if ua <= 0 {
			ua = 8
		}
		qDraw := dhwDrawW(in.Time)
		th.QDrawW = qDraw
		if dt > 0 && cap > 0 {
			s.dhwC += (qDHW + backup - ua*(s.dhwC-s.indoorC) - qDraw) * dt / cap
		}
		s.dhwC = clip(s.dhwC, 8, 75)
	}
	s.indoorC = clip(s.indoorC, 5, 32)

	th.IndoorC = s.indoorC
	th.DHWC = s.dhwC
	th.QHpW = qHp
	th.QSpaceW = qSpace
	th.QDHWW = qDHW
	th.QLossW = qLoss
	th.QSolarW = qSolar
	th.QInternalW = qInt
	th.ElectricalW = pEl
	th.BackupW = backup
	th.COP = cop
	return th
}

func hpAllowed(cmd HeatPumpCommand) bool {
	if cmd.Mode == 2 {
		return false
	}
	if cmd.SGReady == 1 {
		return false
	}
	if cmd.SGReady == 2 || cmd.SGReady == 3 {
		return true
	}
	// No facade yet: the plant still follows the heating curve so a site
	// feels alive before EMS connects. Once Connected, Enable is the master.
	if cmd.Connected && !cmd.Enable {
		return false
	}
	return true
}

func heatPumpCOP(spec HeatPumpSpec, tAmb float64) float64 {
	cop0 := spec.COPRated
	if cop0 <= 0 {
		cop0 = 3.2
	}
	tSrc := tAmb
	gain := 0.07
	if spec.Type == HPGSHP {
		tSrc = 8
		gain = 0.02
		if spec.COPRated <= 0 {
			cop0 = 4.4
		}
	}
	cop := cop0 + gain*(tSrc-7)
	return clip(cop, 1.5, 5.8)
}

func heatingCurve(tAmb float64, t TimeLike, cmd HeatPumpCommand) float64 {
	if cmd.SetpointC >= 12 && cmd.SetpointC <= 28 {
		return cmd.SetpointC
	}
	set := 21.5 - 0.22*(tAmb-2)
	set = clip(set, 18.5, 22.5)
	if nightSetback(t) {
		set -= 2.5
	}
	return set
}

func occupied(t TimeLike) bool {
	h := hourOf(t)
	return (h >= 6.5 && h <= 8.5) || (h >= 16.5 && h <= 22.5)
}

func nightSetback(t TimeLike) bool {
	h := hourOf(t)
	return h < 6 || h >= 22.5
}

func dhwDrawW(t TimeLike) float64 {
	h := hourOf(t)
	if (h >= 6.5 && h <= 8) || (h >= 18.5 && h <= 20) {
		return 2500
	}
	return 80
}

func occupancyPlugW(t TimeLike, floorM2 float64) float64 {
	base := 3.0 * math.Max(floorM2, 80)
	h := hourOf(t)
	if h >= 7 && h <= 9 {
		return base + 900
	}
	if h >= 17 && h <= 22 {
		return base + 1400
	}
	if h < 6 || h > 23 {
		return base * 0.45
	}
	return base
}

// TimeLike is anything with Hour/Minute — *time.Time or time.Time.
type TimeLike interface {
	Hour() int
	Minute() int
}

func hourOf(t TimeLike) float64 {
	if t == nil {
		return 12
	}
	return float64(t.Hour()) + float64(t.Minute())/60.0
}
