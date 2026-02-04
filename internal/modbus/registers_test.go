package modbus

import (
	"testing"
)

func TestEncodeDecodeU16(t *testing.T) {
	def := &RegisterDef{
		Address:  1000,
		DataType: U16,
		Scale:    1.0,
		Words:    1,
	}

	tests := []struct {
		value float64
		want  uint16
	}{
		{0, 0},
		{100, 100},
		{65535, 65535},
	}

	for _, tt := range tests {
		regs, err := EncodeValue(tt.value, def)
		if err != nil {
			t.Errorf("EncodeValue(%v) error: %v", tt.value, err)
			continue
		}
		if len(regs) != 1 || regs[0] != tt.want {
			t.Errorf("EncodeValue(%v) = %v, want [%d]", tt.value, regs, tt.want)
		}

		decoded, err := DecodeValue(regs, def)
		if err != nil {
			t.Errorf("DecodeValue(%v) error: %v", regs, err)
			continue
		}
		if decoded != tt.value {
			t.Errorf("DecodeValue(%v) = %v, want %v", regs, decoded, tt.value)
		}
	}
}

func TestEncodeDecodeI16(t *testing.T) {
	def := &RegisterDef{
		Address:  1000,
		DataType: I16,
		Scale:    1.0,
		Words:    1,
	}

	tests := []struct {
		value float64
		want  int16
	}{
		{0, 0},
		{100, 100},
		{-100, -100},
		{32767, 32767},
		{-32768, -32768},
	}

	for _, tt := range tests {
		regs, err := EncodeValue(tt.value, def)
		if err != nil {
			t.Errorf("EncodeValue(%v) error: %v", tt.value, err)
			continue
		}

		decoded, err := DecodeValue(regs, def)
		if err != nil {
			t.Errorf("DecodeValue(%v) error: %v", regs, err)
			continue
		}
		if decoded != tt.value {
			t.Errorf("DecodeValue roundtrip %v -> %v -> %v", tt.value, regs, decoded)
		}
	}
}

func TestEncodeDecodeU32BigEndian(t *testing.T) {
	def := &RegisterDef{
		Address:    1000,
		DataType:   U32,
		Scale:      1.0,
		Words:      2,
		Endianness: Big,
	}

	tests := []struct {
		value    float64
		wantHigh uint16
		wantLow  uint16
	}{
		{0, 0, 0},
		{65536, 1, 0},           // 0x10000
		{65537, 1, 1},           // 0x10001
		{4294967295, 65535, 65535}, // 0xFFFFFFFF
	}

	for _, tt := range tests {
		regs, err := EncodeValue(tt.value, def)
		if err != nil {
			t.Errorf("EncodeValue(%v) error: %v", tt.value, err)
			continue
		}
		if len(regs) != 2 || regs[0] != tt.wantHigh || regs[1] != tt.wantLow {
			t.Errorf("EncodeValue(%v) = [%d, %d], want [%d, %d]",
				tt.value, regs[0], regs[1], tt.wantHigh, tt.wantLow)
		}

		decoded, err := DecodeValue(regs, def)
		if err != nil {
			t.Errorf("DecodeValue(%v) error: %v", regs, err)
			continue
		}
		if decoded != tt.value {
			t.Errorf("DecodeValue(%v) = %v, want %v", regs, decoded, tt.value)
		}
	}
}

func TestEncodeDecodeU32LittleEndian(t *testing.T) {
	def := &RegisterDef{
		Address:    1000,
		DataType:   U32,
		Scale:      1.0,
		Words:      2,
		Endianness: Little,
	}

	tests := []struct {
		value    float64
		wantLow  uint16
		wantHigh uint16
	}{
		{0, 0, 0},
		{65536, 0, 1},              // Low first, then high
		{65537, 1, 1},
		{4294967295, 65535, 65535},
	}

	for _, tt := range tests {
		regs, err := EncodeValue(tt.value, def)
		if err != nil {
			t.Errorf("EncodeValue(%v) error: %v", tt.value, err)
			continue
		}
		if len(regs) != 2 || regs[0] != tt.wantLow || regs[1] != tt.wantHigh {
			t.Errorf("EncodeValue(%v) = [%d, %d], want [%d, %d]",
				tt.value, regs[0], regs[1], tt.wantLow, tt.wantHigh)
		}

		decoded, err := DecodeValue(regs, def)
		if err != nil {
			t.Errorf("DecodeValue(%v) error: %v", regs, err)
			continue
		}
		if decoded != tt.value {
			t.Errorf("DecodeValue(%v) = %v, want %v", regs, decoded, tt.value)
		}
	}
}

func TestEncodeDecodeI32(t *testing.T) {
	def := &RegisterDef{
		Address:    1000,
		DataType:   I32,
		Scale:      1.0,
		Words:      2,
		Endianness: Big,
	}

	tests := []float64{
		0,
		1000,
		-1000,
		2147483647,  // Max I32
		-2147483648, // Min I32
	}

	for _, tt := range tests {
		regs, err := EncodeValue(tt, def)
		if err != nil {
			t.Errorf("EncodeValue(%v) error: %v", tt, err)
			continue
		}

		decoded, err := DecodeValue(regs, def)
		if err != nil {
			t.Errorf("DecodeValue(%v) error: %v", regs, err)
			continue
		}
		if decoded != tt {
			t.Errorf("Roundtrip %v -> %v -> %v", tt, regs, decoded)
		}
	}
}

func TestScaling(t *testing.T) {
	// Test 0.1 scale factor (like Battery SoC)
	def := &RegisterDef{
		Address:  13023,
		DataType: U16,
		Scale:    0.1,
		Words:    1,
	}

	// 78.0% -> should encode as 780
	regs, err := EncodeValue(78.0, def)
	if err != nil {
		t.Fatalf("EncodeValue error: %v", err)
	}
	if regs[0] != 780 {
		t.Errorf("EncodeValue(78.0) = %d, want 780", regs[0])
	}

	// 780 -> should decode as 78.0
	decoded, err := DecodeValue([]uint16{780}, def)
	if err != nil {
		t.Fatalf("DecodeValue error: %v", err)
	}
	if decoded != 78.0 {
		t.Errorf("DecodeValue([780]) = %v, want 78.0", decoded)
	}
}

func TestRegisterLookup(t *testing.T) {
	// Verify that all register definitions are in the lookup
	for _, def := range RegisterDefinitions {
		lookup := RegisterLookup[def.Address]
		if lookup == nil {
			t.Errorf("Register %d (%s) not in lookup", def.Address, def.Name)
		}
		if lookup.Name != def.Name {
			t.Errorf("Lookup mismatch for %d: got %s, want %s", def.Address, lookup.Name, def.Name)
		}
	}

	// Verify specific registers exist
	pvPower := RegisterLookup[5017]
	if pvPower == nil {
		t.Error("PV Power register (5017) not found")
	} else if pvPower.Name != "PV Power" {
		t.Errorf("Register 5017 name = %s, want 'PV Power'", pvPower.Name)
	}

	batterySoc := RegisterLookup[13023]
	if batterySoc == nil {
		t.Error("Battery SoC register (13023) not found")
	} else if batterySoc.Scale != 0.1 {
		t.Errorf("Battery SoC scale = %v, want 0.1", batterySoc.Scale)
	}
}

func TestToPayload(t *testing.T) {
	def := RegisterLookup[13023] // Battery SoC
	if def == nil {
		t.Fatal("Battery SoC register not found")
	}

	payload, err := def.ToPayload([]uint16{780})
	if err != nil {
		t.Fatalf("ToPayload error: %v", err)
	}

	if payload.Address != 13023 {
		t.Errorf("Address = %d, want 13023", payload.Address)
	}
	if payload.Value != 78.0 {
		t.Errorf("Value = %v, want 78.0", payload.Value)
	}
	if payload.Name != "Battery SoC" {
		t.Errorf("Name = %s, want 'Battery SoC'", payload.Name)
	}
	if payload.Unit != "%" {
		t.Errorf("Unit = %s, want '%%'", payload.Unit)
	}
}
