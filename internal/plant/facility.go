package plant

// HPType is the heat-pump family on a facility.
type HPType string

const (
	HPNone HPType = ""
	HPASHP HPType = "ashp"
	HPGSHP HPType = "gshp"
)

// HeatPumpSpec is the rated machine, not the live EMS writes.
type HeatPumpSpec struct {
	Type     HPType  `json:"type"`
	RatedThW float64 `json:"rated_th_w"`
	COPRated float64 `json:"cop_rated"`
	TSupplyC float64 `json:"t_supply_c"`
}

// BuildingSpec is a lumped RC envelope (UA and thermal mass).
type BuildingSpec struct {
	UAWPerK     float64 `json:"ua_w_per_k"`
	CapJPerK    float64 `json:"cap_j_per_k"`
	FloorM2     float64 `json:"floor_m2"`
	SolarGainM2 float64 `json:"solar_gain_m2"`
}

// DHWSpec is a stratified-enough single tank. VolumeL <= 0 disables DHW.
type DHWSpec struct {
	VolumeL   float64 `json:"volume_l"`
	UAWPerK   float64 `json:"ua_w_per_k"`
	SetpointC float64 `json:"setpoint_c"`
}

// Facility is one composable site: envelope + HP + DHW + electrical ratings.
// NativeSite and modelica/SiteEnergy.mo implement the same equations.
type Facility struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Climate     string       `json:"climate"`
	Building    BuildingSpec `json:"building"`
	HeatPump    HeatPumpSpec `json:"heat_pump"`
	DHW         DHWSpec      `json:"dhw"`
	PVRatedW    float64      `json:"pv_rated_w"`
	BatteryKWh  float64      `json:"battery_kwh"`
}

// HasHeatPump reports whether the facility includes a thermal machine.
func (f Facility) HasHeatPump() bool {
	return f.HeatPump.Type == HPASHP || f.HeatPump.Type == HPGSHP
}

// Preset returns a named facility template.
func Preset(id string) (Facility, bool) {
	for _, f := range Presets() {
		if f.ID == id {
			return f, true
		}
	}
	return Facility{}, false
}

// Presets are the choosable simulated facilities.
func Presets() []Facility {
	house := BuildingSpec{
		UAWPerK:     140,
		CapJPerK:    18e6,
		FloorM2:     140,
		SolarGainM2: 8,
	}
	apt := BuildingSpec{
		UAWPerK:     280,
		CapJPerK:    45e6,
		FloorM2:     320,
		SolarGainM2: 14,
	}
	dhw := DHWSpec{VolumeL: 200, UAWPerK: 8, SetpointC: 50}
	return []Facility{
		{
			ID:          "house-ashp",
			Name:        "House · air-source HP",
			Description: "Nordic house, ASHP, DHW tank, PV hybrid. COP falls when it is cold outside.",
			Climate:     "nordic",
			Building:    house,
			HeatPump:    HeatPumpSpec{Type: HPASHP, RatedThW: 8000, COPRated: 3.4, TSupplyC: 35},
			DHW:         dhw,
			PVRatedW:    8000,
			BatteryKWh:  10,
		},
		{
			ID:          "house-gshp",
			Name:        "House · ground-source HP",
			Description: "Same house, GSHP with a stable ground source. Better winter COP.",
			Climate:     "nordic",
			Building:    house,
			HeatPump:    HeatPumpSpec{Type: HPGSHP, RatedThW: 8000, COPRated: 4.4, TSupplyC: 35},
			DHW:         dhw,
			PVRatedW:    8000,
			BatteryKWh:  10,
		},
		{
			ID:          "apartment-ashp",
			Name:        "Apartment · air-source HP",
			Description: "Heavier envelope and occupancy, larger ASHP, modest PV.",
			Climate:     "nordic",
			Building:    apt,
			HeatPump:    HeatPumpSpec{Type: HPASHP, RatedThW: 14000, COPRated: 3.2, TSupplyC: 38},
			DHW:         DHWSpec{VolumeL: 400, UAWPerK: 12, SetpointC: 50},
			PVRatedW:    4000,
			BatteryKWh:  10,
		},
		{
			ID:          "pv-battery",
			Name:        "PV + battery",
			Description: "Electrical only: occupancy plug loads, no heat pump. Closest to the original plant.",
			Climate:     "nordic",
			Building:    house,
			HeatPump:    HeatPumpSpec{Type: HPNone},
			PVRatedW:    8000,
			BatteryKWh:  10,
		},
	}
}
