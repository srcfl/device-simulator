package main

import (
	"testing"

	"github.com/srcfl/device-simulator/internal/modbus"
)

func TestGetBatterySignedPower(t *testing.T) {
	server := modbus.NewServer(65536)

	powerDef := modbus.RegisterLookup[13022]
	flagsDef := modbus.RegisterLookup[13000]
	if powerDef == nil || flagsDef == nil {
		t.Fatalf("missing register defs: power=%v flags=%v", powerDef != nil, flagsDef != nil)
	}

	if err := server.WriteRegisterValue(powerDef, 500); err != nil {
		t.Fatalf("WriteRegisterValue power error: %v", err)
	}

	// Default: no flags -> treat as charging/positive
	if err := server.WriteRegisterValue(flagsDef, 0); err != nil {
		t.Fatalf("WriteRegisterValue flags error: %v", err)
	}
	if got := getBatterySignedPower(server); got != 500 {
		t.Fatalf("signed power = %v, want 500", got)
	}

	// Discharging flag (0x0004) -> negative
	if err := server.WriteRegisterValue(flagsDef, 4); err != nil {
		t.Fatalf("WriteRegisterValue flags error: %v", err)
	}
	if got := getBatterySignedPower(server); got != -500 {
		t.Fatalf("signed power = %v, want -500", got)
	}

	// Charging flag (0x0002) -> positive
	if err := server.WriteRegisterValue(flagsDef, 2); err != nil {
		t.Fatalf("WriteRegisterValue flags error: %v", err)
	}
	if got := getBatterySignedPower(server); got != 500 {
		t.Fatalf("signed power = %v, want 500", got)
	}
}
