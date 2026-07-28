package matter

import (
	"math"
	"strings"
	"time"
)

// deviceType describes one Matter device type the simulator supports: its
// bridge key, UI metadata, the Matter attribute defaults sent at creation, the
// initial physics state, and the functions that drive/control it. Device
// physics live here in Go; the bridge only mirrors the resulting attributes.
type deviceType struct {
	Key            string // bridge key, e.g. "thermostat"
	SimType        string // createSimulator type, e.g. "matter_thermostat"
	Label          string // UI label
	Icon           string // UI icon
	SerialTag      string // short serial infix
	MatterDefaults map[string]float64
	InitPhys       map[string]float64
	Generate       func(s *Server, t time.Time) []AttrChange
	SetValue       func(s *Server, key string, value interface{}) ([]AttrChange, bool)
	OnExternal     func(s *Server, cluster, attribute string, value interface{})
}

// energyChange wraps a cumulative imported energy value (mWh) for the bridge.
func energyChange(wh float64) AttrChange {
	return AttrChange{Cluster: "electricalEnergyMeasurement", Attribute: "cumulativeEnergyImported", Value: int64(wh * 1000)}
}

func powerChanges(watts, voltage float64) []AttrChange {
	current := 0.0
	if voltage > 0 {
		current = watts / voltage
	}
	return []AttrChange{
		{Cluster: "electricalPowerMeasurement", Attribute: "activePower", Value: int64(watts * 1000)},
		{Cluster: "electricalPowerMeasurement", Attribute: "rmsVoltage", Value: int64(voltage * 1000)},
		{Cluster: "electricalPowerMeasurement", Attribute: "rmsCurrent", Value: int64(current * 1000)},
	}
}

const mainsVoltage = 230.0

var deviceTypes = map[string]*deviceType{
	"tempsensor": {
		Key: "tempsensor", SimType: "matter_tempsensor", Label: "Temperature Sensor", Icon: "🌡️", SerialTag: "TS",
		MatterDefaults: map[string]float64{"measuredValue": 2000},
		InitPhys:       map[string]float64{"base_temp": 20, "amplitude": 6},
		Generate: func(s *Server, t time.Time) []AttrChange {
			var tempC float64
			if s.getF("override", 0) != 0 {
				tempC = s.getF("override_c", 20)
			} else {
				hour := float64(t.Hour()) + float64(t.Minute())/60
				tempC = s.getF("base_temp", 20) + s.getF("amplitude", 6)*math.Sin((hour-9)/24*2*math.Pi)
			}
			s.setF("temperature_c", round1(tempC))
			return []AttrChange{{Cluster: "temperatureMeasurement", Attribute: "measuredValue", Value: int(math.Round(tempC * 100))}}
		},
		SetValue: func(s *Server, key string, value interface{}) ([]AttrChange, bool) {
			switch key {
			case "temperature_c", "value":
				if f, ok := toFloat(value); ok {
					s.setF("override", 1)
					s.setF("override_c", f)
					s.setF("temperature_c", round1(f))
					return []AttrChange{{Cluster: "temperatureMeasurement", Attribute: "measuredValue", Value: int(math.Round(f * 100))}}, true
				}
			case "auto":
				s.setF("override", 0)
				return nil, true
			}
			return nil, false
		},
	},

	"lightsensor": {
		Key: "lightsensor", SimType: "matter_lightsensor", Label: "Light Sensor", Icon: "💡", SerialTag: "LS",
		MatterDefaults: map[string]float64{"measuredValue": 1},
		InitPhys:       map[string]float64{"max_lux": 50000, "sunrise": 6, "sunset": 20},
		Generate: func(s *Server, t time.Time) []AttrChange {
			var lux float64
			if s.getF("override", 0) != 0 {
				lux = s.getF("override_lux", 0)
			} else {
				hour := float64(t.Hour()) + float64(t.Minute())/60
				sunrise, sunset := s.getF("sunrise", 6), s.getF("sunset", 20)
				if hour > sunrise && hour < sunset {
					lux = s.getF("max_lux", 50000) * math.Sin((hour-sunrise)/(sunset-sunrise)*math.Pi)
				}
			}
			s.setF("lux", math.Round(lux))
			return []AttrChange{{Cluster: "illuminanceMeasurement", Attribute: "measuredValue", Value: luxToMeasured(lux)}}
		},
		SetValue: func(s *Server, key string, value interface{}) ([]AttrChange, bool) {
			switch key {
			case "lux", "value":
				if f, ok := toFloat(value); ok {
					s.setF("override", 1)
					s.setF("override_lux", f)
					s.setF("lux", math.Round(f))
					return []AttrChange{{Cluster: "illuminanceMeasurement", Attribute: "measuredValue", Value: luxToMeasured(f)}}, true
				}
			case "auto":
				s.setF("override", 0)
				return nil, true
			}
			return nil, false
		},
	},

	"smartplug": {
		Key: "smartplug", SimType: "matter_smartplug", Label: "Smart Plug", Icon: "🔌", SerialTag: "SP",
		MatterDefaults: map[string]float64{"onOff": 0},
		InitPhys:       map[string]float64{"on": 0, "load_w": 200, "energy_wh": 0},
		Generate: func(s *Server, t time.Time) []AttrChange {
			on := s.getBool("on", false)
			var watts float64
			if on {
				load := s.getF("load_w", 200)
				watts = load * (1 + 0.02*math.Sin(float64(t.Unix())))
				s.addF("energy_wh", watts/3600.0)
			}
			s.setF("power_w", round1(watts))
			changes := powerChanges(watts, mainsVoltage)
			changes = append(changes, energyChange(s.getF("energy_wh", 0)))
			return changes
		},
		SetValue: func(s *Server, key string, value interface{}) ([]AttrChange, bool) {
			switch key {
			case "on":
				on := toBool(value)
				s.setBool("on", on)
				return []AttrChange{{Cluster: "onOff", Attribute: "onOff", Value: on}}, true
			case "load_w":
				if f, ok := toFloat(value); ok {
					s.setF("load_w", f)
					return nil, true
				}
			}
			return nil, false
		},
		OnExternal: func(s *Server, cluster, attribute string, value interface{}) {
			if cluster == "onOff" && attribute == "onOff" {
				s.setBool("on", toBool(value))
			}
		},
	},

	"evse": {
		Key: "evse", SimType: "matter_evse", Label: "EVSE Charger", Icon: "🚗", SerialTag: "EV",
		MatterDefaults: map[string]float64{"minimumChargeCurrent": 6000, "maximumChargeCurrent": 32000},
		InitPhys:       map[string]float64{"session": 0, "max_current_a": 32, "energy_wh": 0, "session_wh": 0},
		Generate: func(s *Server, t time.Time) []AttrChange {
			session := int(s.getF("session", 0)) // 0 unplugged, 2 plugged, 3 charging
			var watts float64
			if session == 3 {
				watts = s.getF("max_current_a", 32) * mainsVoltage
				s.addF("energy_wh", watts/3600.0)
				s.addF("session_wh", watts/3600.0)
			}
			s.setF("power_w", round1(watts))
			changes := []AttrChange{
				{Cluster: "energyEvse", Attribute: "state", Value: session},
				{Cluster: "energyEvse", Attribute: "sessionEnergyCharged", Value: int64(s.getF("session_wh", 0) * 1000)},
			}
			changes = append(changes, powerChanges(watts, mainsVoltage)...)
			changes = append(changes, energyChange(s.getF("energy_wh", 0)))
			return changes
		},
		SetValue: func(s *Server, key string, value interface{}) ([]AttrChange, bool) {
			switch key {
			case "plugged":
				if toBool(value) {
					s.setF("session", 2)
				} else {
					s.setF("session", 0)
					s.setF("session_wh", 0)
				}
				return []AttrChange{{Cluster: "energyEvse", Attribute: "state", Value: int(s.getF("session", 0))}}, true
			case "charging":
				if toBool(value) {
					s.setF("session", 3)
				} else if s.getF("session", 0) == 3 {
					s.setF("session", 2)
				}
				return []AttrChange{{Cluster: "energyEvse", Attribute: "state", Value: int(s.getF("session", 0))}}, true
			case "max_current_a":
				if f, ok := toFloat(value); ok {
					s.setF("max_current_a", f)
					return nil, true
				}
			}
			return nil, false
		},
	},

	"thermostat": {
		Key: "thermostat", SimType: "matter_thermostat", Label: "Thermostat", Icon: "🎛️", SerialTag: "TH",
		MatterDefaults: map[string]float64{
			"localTemperature": 2100, "occupiedHeatingSetpoint": 2100,
			"occupiedCoolingSetpoint": 2600, "systemMode": 4,
		},
		InitPhys: map[string]float64{"local_c": 21, "ambient_c": 18, "heat_sp_c": 21, "cool_sp_c": 26, "system_mode": 4},
		Generate: func(s *Server, t time.Time) []AttrChange {
			return thermostatStep(s, nil)
		},
		SetValue:   thermostatSetValue,
		OnExternal: thermostatOnExternal,
	},

	"heatpump": {
		Key: "heatpump", SimType: "matter_heatpump", Label: "Heat Pump", Icon: "♨️", SerialTag: "HP",
		MatterDefaults: map[string]float64{
			"localTemperature": 2100, "occupiedHeatingSetpoint": 2100,
			"occupiedCoolingSetpoint": 2600, "systemMode": 4,
		},
		InitPhys: map[string]float64{
			"local_c": 21, "ambient_c": 5, "heat_sp_c": 21, "cool_sp_c": 26,
			"system_mode": 4, "cop": 3.5, "thermal_w": 4000, "energy_wh": 0,
		},
		Generate: func(s *Server, t time.Time) []AttrChange {
			running := 0
			changes := thermostatStep(s, &running)
			var watts float64
			if running != 0 {
				watts = s.getF("thermal_w", 4000) / s.getF("cop", 3.5)
				s.addF("energy_wh", watts/3600.0)
			}
			s.setF("power_w", round1(watts))
			changes = append(changes, powerChanges(watts, mainsVoltage)...)
			changes = append(changes, energyChange(s.getF("energy_wh", 0)))
			return changes
		},
		SetValue:   thermostatSetValue,
		OnExternal: thermostatOnExternal,
	},

	"dishwasher": {
		Key: "dishwasher", SimType: "matter_dishwasher", Label: "Dishwasher", Icon: "🍽️", SerialTag: "DW",
		InitPhys: map[string]float64{"running": 0, "countdown": 0, "cycle_len": 5400},
		Generate: applianceGenerate,
		SetValue: applianceSetValue,
	},

	"laundrywasher": {
		Key: "laundrywasher", SimType: "matter_laundrywasher", Label: "Washing Machine", Icon: "🧺", SerialTag: "WM",
		InitPhys: map[string]float64{"running": 0, "countdown": 0, "cycle_len": 7200},
		Generate: applianceGenerate,
		SetValue: applianceSetValue,
	},
}

// thermostatStep advances a first-order thermal plant toward the active
// setpoint. When runningOut is non-nil it receives the running-state bitmap.
func thermostatStep(s *Server, runningOut *int) []AttrChange {
	local := s.getF("local_c", 21)
	ambient := s.getF("ambient_c", 18)
	mode := int(s.getF("system_mode", 4))
	heatSp := s.getF("heat_sp_c", 21)
	coolSp := s.getF("cool_sp_c", 26)

	local += (ambient - local) * 0.01 // passive drift toward ambient
	running := 0
	if (mode == 4 || mode == 1) && local < heatSp { // Heat or Auto
		local += math.Min(0.2, (heatSp-local)*0.2)
		running |= 0x01 // heat
	}
	if (mode == 3 || mode == 1) && local > coolSp { // Cool or Auto
		local -= math.Min(0.2, (local-coolSp)*0.2)
		running |= 0x02 // cool
	}
	s.setF("local_c", round1(local))
	if runningOut != nil {
		*runningOut = running
	}
	return []AttrChange{
		{Cluster: "thermostat", Attribute: "localTemperature", Value: int(math.Round(local * 100))},
		{Cluster: "thermostat", Attribute: "thermostatRunningState", Value: running},
	}
}

func thermostatSetValue(s *Server, key string, value interface{}) ([]AttrChange, bool) {
	f, ok := toFloat(value)
	switch key {
	case "heating_setpoint_c":
		if ok {
			s.setF("heat_sp_c", f)
			return []AttrChange{{Cluster: "thermostat", Attribute: "occupiedHeatingSetpoint", Value: int(math.Round(f * 100))}}, true
		}
	case "cooling_setpoint_c":
		if ok {
			s.setF("cool_sp_c", f)
			return []AttrChange{{Cluster: "thermostat", Attribute: "occupiedCoolingSetpoint", Value: int(math.Round(f * 100))}}, true
		}
	case "system_mode":
		if ok {
			s.setF("system_mode", f)
			return []AttrChange{{Cluster: "thermostat", Attribute: "systemMode", Value: int(f)}}, true
		}
	}
	return nil, false
}

func thermostatOnExternal(s *Server, cluster, attribute string, value interface{}) {
	if cluster != "thermostat" {
		return
	}
	f, ok := toFloat(value)
	if !ok {
		return
	}
	switch attribute {
	case "occupiedHeatingSetpoint":
		s.setF("heat_sp_c", f/100)
	case "occupiedCoolingSetpoint":
		s.setF("cool_sp_c", f/100)
	case "systemMode":
		s.setF("system_mode", f)
	}
}

// applianceGenerate drives the dishwasher / washing-machine timed cycle.
func applianceGenerate(s *Server, t time.Time) []AttrChange {
	if !s.getBool("running", false) {
		return nil
	}
	countdown := s.addF("countdown", -1)
	if countdown <= 0 {
		s.setBool("running", false)
		s.setF("countdown", 0)
		return []AttrChange{
			{Cluster: "operationalState", Attribute: "operationalState", Value: 0}, // Stopped
			{Cluster: "operationalState", Attribute: "countdownTime", Value: 0},
		}
	}
	return []AttrChange{
		{Cluster: "operationalState", Attribute: "operationalState", Value: 1}, // Running
		{Cluster: "operationalState", Attribute: "countdownTime", Value: int(countdown)},
	}
}

func applianceSetValue(s *Server, key string, value interface{}) ([]AttrChange, bool) {
	switch key {
	case "start":
		s.setBool("running", true)
		s.setF("countdown", s.getF("cycle_len", 5400))
		return []AttrChange{
			{Cluster: "operationalState", Attribute: "operationalState", Value: 1},
			{Cluster: "operationalState", Attribute: "countdownTime", Value: int(s.getF("cycle_len", 5400))},
		}, true
	case "stop":
		s.setBool("running", false)
		s.setF("countdown", 0)
		return []AttrChange{
			{Cluster: "operationalState", Attribute: "operationalState", Value: 0},
			{Cluster: "operationalState", Attribute: "countdownTime", Value: 0},
		}, true
	}
	return nil, false
}

// lookupDeviceType accepts both the short key ("thermostat") and the full
// createSimulator type ("matter_thermostat").
func lookupDeviceType(key string) *deviceType {
	key = strings.TrimPrefix(key, "matter_")
	return deviceTypes[key]
}

// SimTypes returns every supported createSimulator type string.
func SimTypes() []string {
	out := make([]string, 0, len(deviceTypes))
	for _, dt := range deviceTypes {
		out = append(out, dt.SimType)
	}
	return out
}

// IsMatterType reports whether simType is a supported Matter device type.
func IsMatterType(simType string) bool {
	return lookupDeviceType(simType) != nil
}

// luxToMeasured encodes lux into the IlluminanceMeasurement.measuredValue
// logarithmic scale: 10000*log10(lux)+1, min 1.
func luxToMeasured(lux float64) int {
	if lux < 1 {
		return 1
	}
	v := 10000*math.Log10(lux) + 1
	if v < 1 {
		return 1
	}
	if v > 0xFFFE {
		return 0xFFFE
	}
	return int(math.Round(v))
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func toFloat(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case bool:
		return boolToF(x), true
	}
	return 0, false
}

func toBool(v interface{}) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case int:
		return x != 0
	case string:
		return x == "true" || x == "on" || x == "1"
	}
	return false
}
