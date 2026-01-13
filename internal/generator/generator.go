package generator

import (
	"math"
	"math/rand"
	"time"
)

// Generator produces values based on simulated time
type Generator interface {
	Generate(t time.Time) float64
}

// Config is the generator configuration from a profile
type Config struct {
	Type   string
	Config map[string]interface{}
}

// New creates a generator from configuration
func New(cfg Config) Generator {
	switch cfg.Type {
	case "constant":
		return NewConstant(cfg.Config)
	case "sine":
		return NewSine(cfg.Config)
	case "daynight":
		return NewDaynight(cfg.Config)
	case "schedule":
		return NewSchedule(cfg.Config)
	case "random_walk":
		return NewRandomWalk(cfg.Config)
	default:
		return &Constant{Value: 0}
	}
}

// Constant generator returns a fixed value
type Constant struct {
	Value float64
}

func NewConstant(config map[string]interface{}) *Constant {
	value := getFloat(config, "value", 0)
	return &Constant{Value: value}
}

func (c *Constant) Generate(t time.Time) float64 {
	return c.Value
}

// Sine generator produces a sinusoidal pattern
type Sine struct {
	Amplitude  float64
	Offset     float64
	PeriodHrs  float64
	PhaseHrs   float64
}

func NewSine(config map[string]interface{}) *Sine {
	return &Sine{
		Amplitude: getFloat(config, "amplitude", 1000),
		Offset:    getFloat(config, "offset", 1000),
		PeriodHrs: getFloat(config, "period", 24),
		PhaseHrs:  getFloat(config, "phase", 0),
	}
}

func (s *Sine) Generate(t time.Time) float64 {
	hour := float64(t.Hour()) + float64(t.Minute())/60.0 + float64(t.Second())/3600.0
	periodSecs := s.PeriodHrs * 3600
	phaseSecs := s.PhaseHrs * 3600

	// Calculate position in cycle
	totalSecs := hour * 3600
	angle := 2 * math.Pi * (totalSecs + phaseSecs) / periodSecs

	return s.Offset + s.Amplitude*math.Sin(angle)
}

// Daynight generator simulates solar production curve
type Daynight struct {
	PeakPower   float64
	SunriseHour float64
	SunsetHour  float64
	Noise       float64
}

func NewDaynight(config map[string]interface{}) *Daynight {
	return &Daynight{
		PeakPower:   getFloat(config, "peak_power", 5000),
		SunriseHour: getFloat(config, "sunrise_hour", 6),
		SunsetHour:  getFloat(config, "sunset_hour", 20),
		Noise:       getFloat(config, "noise", 0),
	}
}

func (d *Daynight) Generate(t time.Time) float64 {
	hour := float64(t.Hour()) + float64(t.Minute())/60.0

	// Night time
	if hour < d.SunriseHour || hour > d.SunsetHour {
		return 0
	}

	// Sinusoidal curve from sunrise to sunset, peaking at noon
	dayLength := d.SunsetHour - d.SunriseHour
	progress := (hour - d.SunriseHour) / dayLength * math.Pi
	baseValue := d.PeakPower * math.Sin(progress)

	// Add noise if configured
	if d.Noise > 0 {
		noiseAmount := baseValue * d.Noise / 100
		baseValue += (rand.Float64()*2 - 1) * noiseAmount
	}

	return math.Max(0, baseValue)
}

// Schedule generator returns values based on time-of-day schedule
type Schedule struct {
	BaseLoad float64
	Slots    []ScheduleSlot
}

type ScheduleSlot struct {
	HourStart float64
	HourEnd   float64
	Load      float64
}

func NewSchedule(config map[string]interface{}) *Schedule {
	s := &Schedule{
		BaseLoad: getFloat(config, "base_load", 500),
	}

	// Parse schedule array
	if scheduleRaw, ok := config["schedule"]; ok {
		if scheduleList, ok := scheduleRaw.([]interface{}); ok {
			for _, item := range scheduleList {
				if slot, ok := item.(map[string]interface{}); ok {
					s.Slots = append(s.Slots, ScheduleSlot{
						HourStart: getFloat(slot, "hour_start", 0),
						HourEnd:   getFloat(slot, "hour_end", 24),
						Load:      getFloat(slot, "load", s.BaseLoad),
					})
				}
			}
		}
	}

	return s
}

func (s *Schedule) Generate(t time.Time) float64 {
	hour := float64(t.Hour()) + float64(t.Minute())/60.0

	for _, slot := range s.Slots {
		if hour >= slot.HourStart && hour < slot.HourEnd {
			return slot.Load
		}
	}

	return s.BaseLoad
}

// RandomWalk generator produces a random walk pattern
type RandomWalk struct {
	Min       float64
	Max       float64
	StepSize  float64
	current   float64
	lastTime  time.Time
}

func NewRandomWalk(config map[string]interface{}) *RandomWalk {
	min := getFloat(config, "min", 0)
	max := getFloat(config, "max", 1000)
	return &RandomWalk{
		Min:      min,
		Max:      max,
		StepSize: getFloat(config, "step", 50),
		current:  (min + max) / 2,
	}
}

func (r *RandomWalk) Generate(t time.Time) float64 {
	// Step in random direction
	step := (rand.Float64()*2 - 1) * r.StepSize
	r.current += step

	// Clamp to bounds
	if r.current < r.Min {
		r.current = r.Min
	}
	if r.current > r.Max {
		r.current = r.Max
	}

	r.lastTime = t
	return r.current
}

// Helper to get float from config
func getFloat(config map[string]interface{}, key string, defaultVal float64) float64 {
	if val, ok := config[key]; ok {
		switch v := val.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case int64:
			return float64(v)
		}
	}
	return defaultVal
}

// Manager manages multiple generators
type Manager struct {
	generators map[string]Generator
}

func NewManager() *Manager {
	return &Manager{
		generators: make(map[string]Generator),
	}
}

func (m *Manager) Set(name string, gen Generator) {
	m.generators[name] = gen
}

func (m *Manager) Get(name string) Generator {
	return m.generators[name]
}

func (m *Manager) Generate(t time.Time) map[string]float64 {
	values := make(map[string]float64)
	for name, gen := range m.generators {
		values[name] = gen.Generate(t)
	}
	return values
}

// LoadFromConfig creates generators from profile configuration
func (m *Manager) LoadFromConfig(configs map[string]Config) {
	for name, cfg := range configs {
		m.generators[name] = New(cfg)
	}
}
