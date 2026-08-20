package plant

import (
	"math"
	"time"
)

// Flow is one directed stream for the site schematic (watts).
type Flow struct {
	From string  `json:"from"`
	To   string  `json:"to"`
	Kind string  `json:"kind"` // elec | heat
	W    float64 `json:"w"`
}

// MemberSnap is one inverter as the schematic sees it.
type MemberSnap struct {
	Key      string  `json:"key"`
	PVW      float64 `json:"pv_w"`
	BatteryW float64 `json:"battery_w"`
	SOC      float64 `json:"soc"`
}

// Snapshot is the dashboard / API view of the site plant after a tick.
type Snapshot struct {
	Backend      string       `json:"backend"`
	FacilityID   string       `json:"facility_id"`
	FacilityName string       `json:"facility_name"`
	Time         string       `json:"time"`
	Electrical   Electrical   `json:"electrical"`
	Thermal      Thermal      `json:"thermal"`
	Flows        []Flow       `json:"flows"`
	Members      []MemberSnap `json:"members"`
}

// Electrical is the AC-node totals (Lua sign convention).
type Electrical struct {
	GridW     float64 `json:"grid_w"`
	PVW       float64 `json:"pv_w"`
	BatteryW  float64 `json:"battery_w"`
	HouseW    float64 `json:"house_w"`
	HPW       float64 `json:"hp_w"`
	EVW       float64 `json:"ev_w"`
	ResidualW float64 `json:"residual_w"`
}

// BuildSnapshot projects a solved SiteResult onto the schematic.
func BuildSnapshot(backend, facilityID string, t time.Time, res SiteResult) Snapshot {
	name := ""
	if f, ok := Preset(facilityID); ok {
		name = f.Name
	}
	when := ""
	if !t.IsZero() {
		when = t.Format("2006-01-02 15:04:05")
	}
	var pv, batt float64
	members := make([]MemberSnap, 0, len(res.Members))
	for _, m := range res.Members {
		pv += m.Out.PVW
		batt += m.Out.BatteryW
		members = append(members, MemberSnap{
			Key:      m.Key,
			PVW:      m.Out.PVW,
			BatteryW: m.Out.BatteryW,
			SOC:      m.Out.SOC,
		})
	}
	el := Electrical{
		GridW:     res.GridW,
		PVW:       pv,
		BatteryW:  batt,
		HouseW:    res.HouseW,
		HPW:       res.HPW,
		EVW:       res.EVW,
		ResidualW: res.ResidualW,
	}
	return Snapshot{
		Backend:      backend,
		FacilityID:   facilityID,
		FacilityName: name,
		Time:         when,
		Electrical:   el,
		Thermal:      res.Thermal,
		Flows:        snapshotFlows(el, res.Thermal),
		Members:      members,
	}
}

func snapshotFlows(el Electrical, th Thermal) []Flow {
	var flows []Flow
	add := func(from, to, kind string, w float64) {
		if math.Abs(w) < 5 {
			return
		}
		flows = append(flows, Flow{From: from, To: to, Kind: kind, W: w})
	}
	if el.PVW < 0 {
		add("pv", "bus", "elec", -el.PVW)
	}
	if el.BatteryW < 0 {
		add("batt", "bus", "elec", -el.BatteryW)
	} else if el.BatteryW > 0 {
		add("bus", "batt", "elec", el.BatteryW)
	}
	if el.GridW > 0 {
		add("grid", "bus", "elec", el.GridW)
	} else if el.GridW < 0 {
		add("bus", "grid", "elec", -el.GridW)
	}
	add("bus", "house", "elec", math.Max(0, el.HouseW))
	add("bus", "hp", "elec", math.Max(0, el.HPW))
	add("bus", "ev", "elec", math.Max(0, el.EVW))
	add("hp", "building", "heat", math.Max(0, th.QSpaceW))
	add("hp", "dhw", "heat", math.Max(0, th.QDHWW))
	add("sun", "building", "heat", math.Max(0, th.QSolarW))
	add("building", "outdoor", "heat", math.Max(0, th.QLossW))
	return flows
}
