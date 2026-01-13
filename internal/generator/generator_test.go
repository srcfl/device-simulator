package generator

import (
	"math"
	"testing"
	"time"
)

func TestConstantGenerator(t *testing.T) {
	gen := NewConstant(map[string]interface{}{
		"value": 1234.0,
	})

	// Should always return the same value
	for i := 0; i < 10; i++ {
		testTime := time.Date(2024, 6, 15, i, 30, 0, 0, time.UTC)
		val := gen.Generate(testTime)
		if val != 1234.0 {
			t.Errorf("Constant.Generate() = %v, want 1234.0", val)
		}
	}
}

func TestSineGenerator(t *testing.T) {
	gen := NewSine(map[string]interface{}{
		"amplitude": 1000.0,
		"offset":    2000.0,
		"period":    24.0,
		"phase":     0.0,
	})

	// At hour 6 (1/4 of 24h period), sine should be at maximum
	t6 := time.Date(2024, 6, 15, 6, 0, 0, 0, time.UTC)
	val6 := gen.Generate(t6)
	if val6 < 2900 || val6 > 3100 { // ~3000 (offset + amplitude)
		t.Errorf("Sine at 6:00 = %v, expected ~3000", val6)
	}

	// At hour 18 (3/4 of period), sine should be at minimum
	t18 := time.Date(2024, 6, 15, 18, 0, 0, 0, time.UTC)
	val18 := gen.Generate(t18)
	if val18 < 900 || val18 > 1100 { // ~1000 (offset - amplitude)
		t.Errorf("Sine at 18:00 = %v, expected ~1000", val18)
	}
}

func TestDaynightGenerator(t *testing.T) {
	gen := NewDaynight(map[string]interface{}{
		"peak_power":   5000.0,
		"sunrise_hour": 6.0,
		"sunset_hour":  20.0,
	})

	// Night time - should be 0
	tNight := time.Date(2024, 6, 15, 3, 0, 0, 0, time.UTC)
	valNight := gen.Generate(tNight)
	if valNight != 0 {
		t.Errorf("Daynight at 3:00 = %v, expected 0", valNight)
	}

	// Noon - should be near peak
	tNoon := time.Date(2024, 6, 15, 13, 0, 0, 0, time.UTC)
	valNoon := gen.Generate(tNoon)
	if valNoon < 4500 || valNoon > 5000 {
		t.Errorf("Daynight at 13:00 = %v, expected ~5000", valNoon)
	}

	// Sunrise - should be relatively low (still ramping up)
	tSunrise := time.Date(2024, 6, 15, 6, 30, 0, 0, time.UTC)
	valSunrise := gen.Generate(tSunrise)
	if valSunrise > 1000 {
		t.Errorf("Daynight at 6:30 = %v, expected < 1000", valSunrise)
	}

	// Late evening - should be 0
	tLate := time.Date(2024, 6, 15, 21, 0, 0, 0, time.UTC)
	valLate := gen.Generate(tLate)
	if valLate != 0 {
		t.Errorf("Daynight at 21:00 = %v, expected 0", valLate)
	}
}

func TestScheduleGenerator(t *testing.T) {
	gen := NewSchedule(map[string]interface{}{
		"base_load": 500.0,
		"schedule": []interface{}{
			map[string]interface{}{"hour_start": 7.0, "hour_end": 9.0, "load": 2000.0},
			map[string]interface{}{"hour_start": 17.0, "hour_end": 22.0, "load": 3000.0},
		},
	})

	tests := []struct {
		hour     int
		expected float64
	}{
		{3, 500},    // Base load
		{7, 2000},   // Morning peak
		{8, 2000},   // Still morning peak
		{10, 500},   // Back to base
		{17, 3000},  // Evening peak
		{20, 3000},  // Still evening
		{22, 500},   // Back to base
	}

	for _, tc := range tests {
		testTime := time.Date(2024, 6, 15, tc.hour, 0, 0, 0, time.UTC)
		val := gen.Generate(testTime)
		if val != tc.expected {
			t.Errorf("Schedule at %d:00 = %v, expected %v", tc.hour, val, tc.expected)
		}
	}
}

func TestRandomWalkGenerator(t *testing.T) {
	gen := NewRandomWalk(map[string]interface{}{
		"min":  0.0,
		"max":  1000.0,
		"step": 100.0,
	})

	// Generate multiple values
	testTime := time.Now()
	for i := 0; i < 100; i++ {
		val := gen.Generate(testTime)
		if val < 0 || val > 1000 {
			t.Errorf("RandomWalk value %v out of bounds [0, 1000]", val)
		}
	}
}

func TestNewFromConfig(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected string
	}{
		{"constant", Config{Type: "constant", Config: map[string]interface{}{"value": 100.0}}, "constant"},
		{"sine", Config{Type: "sine", Config: map[string]interface{}{}}, "sine"},
		{"daynight", Config{Type: "daynight", Config: map[string]interface{}{}}, "daynight"},
		{"schedule", Config{Type: "schedule", Config: map[string]interface{}{}}, "schedule"},
		{"random_walk", Config{Type: "random_walk", Config: map[string]interface{}{}}, "random_walk"},
		{"unknown", Config{Type: "unknown", Config: map[string]interface{}{}}, "constant"}, // Falls back to constant
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gen := New(tc.config)
			if gen == nil {
				t.Fatal("New() returned nil")
			}
			// Just verify we can generate a value
			val := gen.Generate(time.Now())
			if math.IsNaN(val) {
				t.Error("Generated NaN value")
			}
		})
	}
}

func TestManager(t *testing.T) {
	m := NewManager()

	m.Set("solar", NewDaynight(map[string]interface{}{
		"peak_power": 5000.0,
	}))
	m.Set("load", NewConstant(map[string]interface{}{
		"value": 1000.0,
	}))

	// Generate all values
	testTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	values := m.Generate(testTime)

	if _, ok := values["solar"]; !ok {
		t.Error("Missing 'solar' in generated values")
	}
	if _, ok := values["load"]; !ok {
		t.Error("Missing 'load' in generated values")
	}
	if values["load"] != 1000.0 {
		t.Errorf("load = %v, expected 1000.0", values["load"])
	}
}
