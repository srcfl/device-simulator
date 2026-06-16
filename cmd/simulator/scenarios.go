package main

// LoadScenario represents a 24-hour load profile with per-phase loads
type LoadScenario struct {
	Key         string               `json:"key"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	PeakLoad    float64              `json:"peak_load"` // Peak load in Watts
	TimeSlots   []LoadScenarioSlot   `json:"time_slots"`
}

// LoadScenarioSlot defines the load for a time range
type LoadScenarioSlot struct {
	StartHour int     `json:"start_hour"` // 0-23
	EndHour   int     `json:"end_hour"`   // 0-23 (exclusive)
	L1        float64 `json:"l1"`         // Phase 1 load in Watts
	L2        float64 `json:"l2"`         // Phase 2 load in Watts
	L3        float64 `json:"l3"`         // Phase 3 load in Watts
}

// PhaseLoads represents per-phase power consumption
type PhaseLoads struct {
	L1 float64 `json:"l1"` // Phase 1 load in Watts
	L2 float64 `json:"l2"` // Phase 2 load in Watts
	L3 float64 `json:"l3"` // Phase 3 load in Watts
}

// Total returns the sum of all phases
func (p PhaseLoads) Total() float64 {
	return p.L1 + p.L2 + p.L3
}

// LoadScenarios contains predefined load scenarios
var LoadScenarios = map[string]*LoadScenario{
	"static": {
		Key:         "static",
		Name:        "Static",
		Description: "Fixed load - stays constant during simulation",
		PeakLoad:    0,
		TimeSlots:   []LoadScenarioSlot{},
	},
	"gaming_family": {
		Key:         "gaming_family",
		Name:        "Gaming Family",
		Description: "High evening load with gaming PCs (15:00-22:00), moderate daytime use",
		PeakLoad:    5500,
		TimeSlots: []LoadScenarioSlot{
			// Night (00:00-06:00): Low standby load
			{StartHour: 0, EndHour: 6, L1: 100, L2: 100, L3: 100},
			// Morning (06:00-09:00): Wake up, breakfast
			{StartHour: 6, EndHour: 9, L1: 400, L2: 300, L3: 200},
			// Day (09:00-15:00): Low activity
			{StartHour: 9, EndHour: 15, L1: 200, L2: 150, L3: 150},
			// After school gaming (15:00-18:00): Gaming starts
			{StartHour: 15, EndHour: 18, L1: 1200, L2: 800, L3: 500},
			// Evening peak (18:00-22:00): Full family gaming + cooking
			{StartHour: 18, EndHour: 22, L1: 2000, L2: 2000, L3: 1500},
			// Wind down (22:00-00:00): Moderate
			{StartHour: 22, EndHour: 24, L1: 600, L2: 400, L3: 300},
		},
	},
	"old_couple": {
		Key:         "old_couple",
		Name:        "Old Couple",
		Description: "Low consistent load, early morning/evening peaks, early bedtime",
		PeakLoad:    1500,
		TimeSlots: []LoadScenarioSlot{
			// Night (00:00-05:00): Very low
			{StartHour: 0, EndHour: 5, L1: 50, L2: 50, L3: 50},
			// Early morning (05:00-08:00): Wake up, tea/coffee
			{StartHour: 5, EndHour: 8, L1: 400, L2: 200, L3: 100},
			// Day (08:00-17:00): Light activity, TV
			{StartHour: 8, EndHour: 17, L1: 300, L2: 200, L3: 150},
			// Evening (17:00-20:00): Cooking, TV
			{StartHour: 17, EndHour: 20, L1: 600, L2: 500, L3: 400},
			// Night (20:00-22:00): Winding down
			{StartHour: 20, EndHour: 22, L1: 200, L2: 150, L3: 100},
			// Sleep (22:00-00:00): Minimal
			{StartHour: 22, EndHour: 24, L1: 80, L2: 60, L3: 60},
		},
	},
	"always_on": {
		Key:         "always_on",
		Name:        "Always-On Facility",
		Description: "Constant high load 24/7 from industrial equipment and ventilation",
		PeakLoad:    10500,
		TimeSlots: []LoadScenarioSlot{
			// 24/7 constant high load (equipment and ventilation run continuously)
			{StartHour: 0, EndHour: 24, L1: 3500, L2: 3500, L3: 3500},
		},
	},
	"work_from_home": {
		Key:         "work_from_home",
		Name:        "Work From Home",
		Description: "Daytime peaks during work hours with office equipment",
		PeakLoad:    2600,
		TimeSlots: []LoadScenarioSlot{
			// Night (00:00-07:00): Low
			{StartHour: 0, EndHour: 7, L1: 100, L2: 100, L3: 100},
			// Morning routine (07:00-09:00)
			{StartHour: 7, EndHour: 9, L1: 500, L2: 400, L3: 300},
			// Work hours (09:00-12:00): Computer, monitors, coffee
			{StartHour: 9, EndHour: 12, L1: 800, L2: 600, L3: 400},
			// Lunch (12:00-13:00): Cooking
			{StartHour: 12, EndHour: 13, L1: 1000, L2: 800, L3: 400},
			// Afternoon work (13:00-17:00)
			{StartHour: 13, EndHour: 17, L1: 800, L2: 600, L3: 400},
			// Evening (17:00-21:00): Relaxing, cooking dinner
			{StartHour: 17, EndHour: 21, L1: 700, L2: 500, L3: 400},
			// Night (21:00-00:00): Winding down
			{StartHour: 21, EndHour: 24, L1: 300, L2: 200, L3: 150},
		},
	},
	"night_owl": {
		Key:         "night_owl",
		Name:        "Night Owl",
		Description: "Late night high usage, sleeps during the day",
		PeakLoad:    3800,
		TimeSlots: []LoadScenarioSlot{
			// Late night peak (00:00-04:00): Gaming, computing
			{StartHour: 0, EndHour: 4, L1: 1200, L2: 1000, L3: 800},
			// Sleep (04:00-14:00): Very low, just standby
			{StartHour: 4, EndHour: 14, L1: 80, L2: 80, L3: 80},
			// Wake up late (14:00-17:00): Breakfast/lunch
			{StartHour: 14, EndHour: 17, L1: 500, L2: 400, L3: 300},
			// Evening (17:00-22:00): Moderate activity
			{StartHour: 17, EndHour: 22, L1: 600, L2: 500, L3: 400},
			// Night activity starts (22:00-00:00): Ramping up
			{StartHour: 22, EndHour: 24, L1: 1000, L2: 900, L3: 700},
		},
	},
}

// GetLoadScenariosSlice returns all scenarios as a slice for API responses
func GetLoadScenariosSlice() []*LoadScenario {
	scenarios := make([]*LoadScenario, 0, len(LoadScenarios))
	// Ordered list
	order := []string{"static", "gaming_family", "old_couple", "always_on", "work_from_home", "night_owl"}
	for _, key := range order {
		if s, ok := LoadScenarios[key]; ok {
			scenarios = append(scenarios, s)
		}
	}
	return scenarios
}

// GetLoadForTime returns the per-phase loads for a given hour (0-23)
func (s *LoadScenario) GetLoadForTime(hour int) PhaseLoads {
	// Normalize hour to 0-23
	hour = hour % 24
	if hour < 0 {
		hour += 24
	}

	for _, slot := range s.TimeSlots {
		// Handle wrap-around (e.g., 22:00 to 02:00)
		if slot.StartHour <= slot.EndHour {
			if hour >= slot.StartHour && hour < slot.EndHour {
				return PhaseLoads{L1: slot.L1, L2: slot.L2, L3: slot.L3}
			}
		} else {
			// Wrap around midnight
			if hour >= slot.StartHour || hour < slot.EndHour {
				return PhaseLoads{L1: slot.L1, L2: slot.L2, L3: slot.L3}
			}
		}
	}

	// Default fallback
	return PhaseLoads{L1: 100, L2: 100, L3: 100}
}
