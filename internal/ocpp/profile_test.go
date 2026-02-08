package ocpp

import (
	"testing"
	"time"
)

func newTestServer() *Server {
	return NewServer(1, 0, "TestVendor", "TestModel", 1, 22.0)
}

// TestEvaluateProfileLimit_RecurringDaily tests a recurring daily schedule with 3 periods.
func TestEvaluateProfileLimit_RecurringDaily(t *testing.T) {
	s := newTestServer()

	// Daily schedule starting at 06:00 UTC:
	//   0s (06:00) → 0W, 3600s (07:00) → 5000W, 7200s (08:00) → 11000W
	startSchedule := time.Date(2025, 1, 1, 6, 0, 0, 0, time.UTC)
	profile := &ChargingProfile{
		ChargingProfileId:      1,
		StackLevel:             0,
		ChargingProfilePurpose: "TxDefaultProfile",
		ChargingProfileKind:    "Recurring",
		RecurrencyKind:         "Daily",
		ChargingSchedule: ChargingSchedule{
			ChargingRateUnit: "W",
			StartSchedule:    FormatTimestamp(startSchedule),
			ChargingSchedulePeriod: []ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 0},
				{StartPeriod: 3600, Limit: 5000},
				{StartPeriod: 7200, Limit: 11000},
			},
		},
	}

	today := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC) // arbitrary day

	tests := []struct {
		name       string
		now        time.Time
		wantLimit  float64
		wantActive bool
	}{
		{
			name:       "at 06:00 → period 0 (0W)",
			now:        today.Add(6 * time.Hour),
			wantLimit:  0,
			wantActive: true,
		},
		{
			name:       "at 06:30 → still period 0 (0W)",
			now:        today.Add(6*time.Hour + 30*time.Minute),
			wantLimit:  0,
			wantActive: true,
		},
		{
			name:       "at 07:00 → period 1 (5000W)",
			now:        today.Add(7 * time.Hour),
			wantLimit:  5000,
			wantActive: true,
		},
		{
			name:       "at 07:30 → still period 1 (5000W)",
			now:        today.Add(7*time.Hour + 30*time.Minute),
			wantLimit:  5000,
			wantActive: true,
		},
		{
			name:       "at 08:00 → period 2 (11000W)",
			now:        today.Add(8 * time.Hour),
			wantLimit:  11000,
			wantActive: true,
		},
		{
			name:       "at 23:59 → still period 2 (11000W, last period continues)",
			now:        today.Add(23*time.Hour + 59*time.Minute),
			wantLimit:  11000,
			wantActive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit, active := s.evaluateProfileLimit(profile, tt.now, time.Time{})
			if active != tt.wantActive {
				t.Errorf("active = %v, want %v", active, tt.wantActive)
			}
			if limit != tt.wantLimit {
				t.Errorf("limit = %v, want %v", limit, tt.wantLimit)
			}
		})
	}
}

// TestEvaluateProfileLimit_Relative tests a relative profile with duration.
func TestEvaluateProfileLimit_Relative(t *testing.T) {
	s := newTestServer()

	dur := int64(1800) // 30 minutes
	profile := &ChargingProfile{
		ChargingProfileId:      2,
		StackLevel:             0,
		ChargingProfilePurpose: "TxProfile",
		ChargingProfileKind:    "Relative",
		ChargingSchedule: ChargingSchedule{
			Duration:         &dur,
			ChargingRateUnit: "W",
			ChargingSchedulePeriod: []ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 7000},
				{StartPeriod: 600, Limit: 11000}, // after 10 min
			},
		},
	}

	txStart := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		now        time.Time
		wantLimit  float64
		wantActive bool
	}{
		{
			name:       "at tx start → period 0 (7000W)",
			now:        txStart,
			wantLimit:  7000,
			wantActive: true,
		},
		{
			name:       "5 min after tx start → still period 0",
			now:        txStart.Add(5 * time.Minute),
			wantLimit:  7000,
			wantActive: true,
		},
		{
			name:       "10 min after tx start → period 1 (11000W)",
			now:        txStart.Add(10 * time.Minute),
			wantLimit:  11000,
			wantActive: true,
		},
		{
			name:       "29 min after tx start → still period 1",
			now:        txStart.Add(29 * time.Minute),
			wantLimit:  11000,
			wantActive: true,
		},
		{
			name:       "30 min after tx start → expired (duration exceeded)",
			now:        txStart.Add(30 * time.Minute),
			wantLimit:  0,
			wantActive: false,
		},
		{
			name:       "before tx start → inactive",
			now:        txStart.Add(-1 * time.Minute),
			wantLimit:  0,
			wantActive: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit, active := s.evaluateProfileLimit(profile, tt.now, txStart)
			if active != tt.wantActive {
				t.Errorf("active = %v, want %v", active, tt.wantActive)
			}
			if limit != tt.wantLimit {
				t.Errorf("limit = %v, want %v", limit, tt.wantLimit)
			}
		})
	}
}

// TestEvaluateProfileLimit_Absolute tests an absolute schedule with multiple periods.
func TestEvaluateProfileLimit_Absolute(t *testing.T) {
	s := newTestServer()

	scheduleStart := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	profile := &ChargingProfile{
		ChargingProfileId:      3,
		StackLevel:             0,
		ChargingProfilePurpose: "TxProfile",
		ChargingProfileKind:    "Absolute",
		ChargingSchedule: ChargingSchedule{
			ChargingRateUnit: "W",
			StartSchedule:    FormatTimestamp(scheduleStart),
			ChargingSchedulePeriod: []ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 3000},
				{StartPeriod: 1800, Limit: 7000},   // +30 min
				{StartPeriod: 3600, Limit: 11000},   // +60 min
			},
		},
	}

	tests := []struct {
		name       string
		now        time.Time
		wantLimit  float64
		wantActive bool
	}{
		{
			name:       "before schedule start → inactive",
			now:        scheduleStart.Add(-1 * time.Minute),
			wantLimit:  0,
			wantActive: false,
		},
		{
			name:       "at schedule start → period 0 (3000W)",
			now:        scheduleStart,
			wantLimit:  3000,
			wantActive: true,
		},
		{
			name:       "15 min in → still period 0 (3000W)",
			now:        scheduleStart.Add(15 * time.Minute),
			wantLimit:  3000,
			wantActive: true,
		},
		{
			name:       "30 min in → period 1 (7000W)",
			now:        scheduleStart.Add(30 * time.Minute),
			wantLimit:  7000,
			wantActive: true,
		},
		{
			name:       "60 min in → period 2 (11000W)",
			now:        scheduleStart.Add(60 * time.Minute),
			wantLimit:  11000,
			wantActive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit, active := s.evaluateProfileLimit(profile, tt.now, time.Time{})
			if active != tt.wantActive {
				t.Errorf("active = %v, want %v", active, tt.wantActive)
			}
			if limit != tt.wantLimit {
				t.Errorf("limit = %v, want %v", limit, tt.wantLimit)
			}
		})
	}
}

// TestComputeEffectivePower_TxProfileOverridesTxDefault tests that TxProfile takes priority.
func TestComputeEffectivePower_TxProfileOverridesTxDefault(t *testing.T) {
	s := newTestServer()

	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	txStart := now.Add(-5 * time.Minute)

	// Add an active transaction on connector 1
	s.transactions[1] = &Transaction{
		ID:          1,
		ConnectorID: 1,
		IdTag:       "TEST",
		StartTime:   txStart,
	}

	// TxDefaultProfile → 3000W
	s.chargingProfiles[profileKey(1, "TxDefaultProfile", 0)] = &storedProfile{
		ConnectorID: 1,
		Profile: ChargingProfile{
			ChargingProfileId:      10,
			StackLevel:             0,
			ChargingProfilePurpose: "TxDefaultProfile",
			ChargingProfileKind:    "Absolute",
			ChargingSchedule: ChargingSchedule{
				ChargingRateUnit: "W",
				StartSchedule:    FormatTimestamp(txStart),
				ChargingSchedulePeriod: []ChargingSchedulePeriod{
					{StartPeriod: 0, Limit: 3000},
				},
			},
		},
	}

	// TxProfile → 7000W
	s.chargingProfiles[profileKey(1, "TxProfile", 0)] = &storedProfile{
		ConnectorID: 1,
		Profile: ChargingProfile{
			ChargingProfileId:      20,
			StackLevel:             0,
			ChargingProfilePurpose: "TxProfile",
			ChargingProfileKind:    "Absolute",
			ChargingSchedule: ChargingSchedule{
				ChargingRateUnit: "W",
				StartSchedule:    FormatTimestamp(txStart),
				ChargingSchedulePeriod: []ChargingSchedulePeriod{
					{StartPeriod: 0, Limit: 7000},
				},
			},
		},
	}

	power := s.computeEffectivePowerLocked(1, now)
	if power != 7000 {
		t.Errorf("expected TxProfile power 7000W, got %v", power)
	}
}

// TestComputeEffectivePower_TxProfileExpired_FallsBack tests fallback when TxProfile expires.
func TestComputeEffectivePower_TxProfileExpired_FallsBack(t *testing.T) {
	s := newTestServer()

	txStart := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	now := txStart.Add(35 * time.Minute) // 35 min after tx start

	s.transactions[1] = &Transaction{
		ID:          1,
		ConnectorID: 1,
		IdTag:       "TEST",
		StartTime:   txStart,
	}

	// TxDefaultProfile → 3000W (no duration, always active)
	s.chargingProfiles[profileKey(1, "TxDefaultProfile", 0)] = &storedProfile{
		ConnectorID: 1,
		Profile: ChargingProfile{
			ChargingProfileId:      10,
			StackLevel:             0,
			ChargingProfilePurpose: "TxDefaultProfile",
			ChargingProfileKind:    "Absolute",
			ChargingSchedule: ChargingSchedule{
				ChargingRateUnit: "W",
				StartSchedule:    FormatTimestamp(txStart),
				ChargingSchedulePeriod: []ChargingSchedulePeriod{
					{StartPeriod: 0, Limit: 3000},
				},
			},
		},
	}

	// TxProfile → 7000W with 30 min duration (expired at +35 min)
	dur := int64(1800)
	s.chargingProfiles[profileKey(1, "TxProfile", 0)] = &storedProfile{
		ConnectorID: 1,
		Profile: ChargingProfile{
			ChargingProfileId:      20,
			StackLevel:             0,
			ChargingProfilePurpose: "TxProfile",
			ChargingProfileKind:    "Relative",
			ChargingSchedule: ChargingSchedule{
				Duration:         &dur,
				ChargingRateUnit: "W",
				ChargingSchedulePeriod: []ChargingSchedulePeriod{
					{StartPeriod: 0, Limit: 7000},
				},
			},
		},
	}

	power := s.computeEffectivePowerLocked(1, now)
	if power != 3000 {
		t.Errorf("expected fallback to TxDefaultProfile 3000W, got %v", power)
	}
}

// TestComputeEffectivePower_NoProfiles_UsesMaxPower tests that max power is used when no profiles exist.
func TestComputeEffectivePower_NoProfiles_UsesMaxPower(t *testing.T) {
	s := newTestServer() // maxPowerKW = 22.0

	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	power := s.computeEffectivePowerLocked(1, now)
	expected := 22.0 * 1000 // 22000W
	if power != expected {
		t.Errorf("expected max power %vW, got %v", expected, power)
	}
}

// TestComputeEffectivePower_ChargePointMaxProfile_Caps tests that ChargePointMaxProfile caps the power.
func TestComputeEffectivePower_ChargePointMaxProfile_Caps(t *testing.T) {
	s := newTestServer() // maxPowerKW = 22.0

	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	txStart := now.Add(-5 * time.Minute)

	s.transactions[1] = &Transaction{
		ID:          1,
		ConnectorID: 1,
		IdTag:       "TEST",
		StartTime:   txStart,
	}

	// TxProfile → 15000W
	s.chargingProfiles[profileKey(1, "TxProfile", 0)] = &storedProfile{
		ConnectorID: 1,
		Profile: ChargingProfile{
			ChargingProfileId:      20,
			StackLevel:             0,
			ChargingProfilePurpose: "TxProfile",
			ChargingProfileKind:    "Absolute",
			ChargingSchedule: ChargingSchedule{
				ChargingRateUnit: "W",
				StartSchedule:    FormatTimestamp(txStart),
				ChargingSchedulePeriod: []ChargingSchedulePeriod{
					{StartPeriod: 0, Limit: 15000},
				},
			},
		},
	}

	// ChargePointMaxProfile on connector 0 → 10000W cap
	s.chargingProfiles[profileKey(0, "ChargePointMaxProfile", 0)] = &storedProfile{
		ConnectorID: 0,
		Profile: ChargingProfile{
			ChargingProfileId:      30,
			StackLevel:             0,
			ChargingProfilePurpose: "ChargePointMaxProfile",
			ChargingProfileKind:    "Absolute",
			ChargingSchedule: ChargingSchedule{
				ChargingRateUnit: "W",
				StartSchedule:    FormatTimestamp(txStart),
				ChargingSchedulePeriod: []ChargingSchedulePeriod{
					{StartPeriod: 0, Limit: 10000},
				},
			},
		},
	}

	power := s.computeEffectivePowerLocked(1, now)
	if power != 10000 {
		t.Errorf("expected ChargePointMaxProfile cap 10000W, got %v", power)
	}
}

// TestReadonlyConfigKeys verifies that ChangeConfiguration rejects readonly keys.
func TestReadonlyConfigKeys(t *testing.T) {
	s := newTestServer()

	tests := []struct {
		key        string
		wantStatus string
	}{
		{"SupportedFeatureProfiles", "Rejected"},
		{"NumberOfConnectors", "Rejected"},
		{"HeartbeatInterval", "Accepted"},
		{"MeterValueSampleInterval", "Accepted"},
		{"NewCustomKey", "Accepted"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			resp := s.handleChangeConfiguration(ChangeConfigurationRequest{
				Key:   tt.key,
				Value: "test",
			})
			if resp.Status != tt.wantStatus {
				t.Errorf("ChangeConfiguration(%q) status = %q, want %q", tt.key, resp.Status, tt.wantStatus)
			}
		})
	}
}

// TestGetConfiguration_ReportsReadonly verifies GetConfiguration includes readonly flag.
func TestGetConfiguration_ReportsReadonly(t *testing.T) {
	s := newTestServer()

	resp := s.handleGetConfiguration(GetConfigurationRequest{
		Key: []string{"SupportedFeatureProfiles", "HeartbeatInterval"},
	})

	for _, ck := range resp.ConfigurationKey {
		switch ck.Key {
		case "SupportedFeatureProfiles":
			if !ck.Readonly {
				t.Errorf("SupportedFeatureProfiles should be readonly")
			}
		case "HeartbeatInterval":
			if ck.Readonly {
				t.Errorf("HeartbeatInterval should not be readonly")
			}
		}
	}
}

// TestEvaluateProfileLimit_CurrentUnit tests profiles that use Ampere-based limits.
func TestEvaluateProfileLimit_CurrentUnit(t *testing.T) {
	s := newTestServer()
	// Default voltages are 230V per phase

	phases := 3
	profile := &ChargingProfile{
		ChargingProfileId:      5,
		StackLevel:             0,
		ChargingProfilePurpose: "TxProfile",
		ChargingProfileKind:    "Absolute",
		ChargingSchedule: ChargingSchedule{
			ChargingRateUnit: "A",
			StartSchedule:    FormatTimestamp(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
			ChargingSchedulePeriod: []ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 16, NumberPhases: &phases},
			},
		},
	}

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	limit, active := s.evaluateProfileLimit(profile, now, time.Time{})
	if !active {
		t.Fatal("expected active")
	}

	// 16A × 3 phases × 230V = 11040W
	expected := 16.0 * 3.0 * 230.0
	if limit != expected {
		t.Errorf("limit = %v, want %v", limit, expected)
	}
}
