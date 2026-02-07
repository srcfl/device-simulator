package modbus

import "testing"

func TestSungrowRegisterDefinitions(t *testing.T) {
	type expected struct {
		name       string
		dataType   DataType
		scale      float64
		words      int
		endianness Endianness
		useHolding bool
	}

	expectations := map[uint16]expected{
		// PV
		5000:  {name: "Nominal Output Power", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		5007:  {name: "Inside Temperature", dataType: I16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		5010:  {name: "MPPT 1 Voltage", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		5011:  {name: "MPPT 1 Current", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		5012:  {name: "MPPT 2 Voltage", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		5013:  {name: "MPPT 2 Current", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		5016:  {name: "PV Power", dataType: U32, scale: 1.0, words: 2, endianness: Little, useHolding: false},
		13002: {name: "Total PV Generation", dataType: U32, scale: 100.0, words: 2, endianness: Little, useHolding: false},

		// Battery
		13000: {name: "System State Flags", dataType: U16, scale: 1.0, words: 1, endianness: Big, useHolding: false},
		13019: {name: "Battery Voltage", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		13020: {name: "Battery Current", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		13021: {name: "Battery Power", dataType: U16, scale: 1.0, words: 1, endianness: Big, useHolding: false},
		13022: {name: "Battery SoC", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		13024: {name: "Battery Temperature", dataType: I16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		13026: {name: "Total Battery Discharge", dataType: U32, scale: 100.0, words: 2, endianness: Little, useHolding: false},
		13040: {name: "Total Charge Energy", dataType: U32, scale: 100.0, words: 2, endianness: Little, useHolding: false},
		13057: {name: "Battery Max SoC", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: true},
		13058: {name: "Battery Min SoC", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: true},

		// Meter
		5600:  {name: "Meter Power", dataType: I32, scale: 1.0, words: 2, endianness: Little, useHolding: false},
		5602:  {name: "Meter L1 Power", dataType: I32, scale: 1.0, words: 2, endianness: Little, useHolding: false},
		5604:  {name: "Meter L2 Power", dataType: I32, scale: 1.0, words: 2, endianness: Little, useHolding: false},
		5606:  {name: "Meter L3 Power", dataType: I32, scale: 1.0, words: 2, endianness: Little, useHolding: false},
		5740:  {name: "Meter L1 Voltage", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		5741:  {name: "Meter L2 Voltage", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		5742:  {name: "Meter L3 Voltage", dataType: U16, scale: 0.1, words: 1, endianness: Big, useHolding: false},
		5743:  {name: "Meter L1 Current", dataType: U16, scale: 0.01, words: 1, endianness: Big, useHolding: false},
		5744:  {name: "Meter L2 Current", dataType: U16, scale: 0.01, words: 1, endianness: Big, useHolding: false},
		5745:  {name: "Meter L3 Current", dataType: U16, scale: 0.01, words: 1, endianness: Big, useHolding: false},
		13036: {name: "Total Import Energy", dataType: U32, scale: 100.0, words: 2, endianness: Little, useHolding: false},
		13045: {name: "Total Export Energy", dataType: U32, scale: 100.0, words: 2, endianness: Little, useHolding: false},

		// Grid
		5241: {name: "Grid Frequency HP", dataType: U16, scale: 0.01, words: 1, endianness: Big, useHolding: false},
	}

	for addr, want := range expectations {
		def := RegisterLookup[addr]
		if def == nil {
			t.Fatalf("Register %d not found", addr)
		}
		if def.Name != want.name {
			t.Errorf("Register %d name = %s, want %s", addr, def.Name, want.name)
		}
		if def.DataType != want.dataType {
			t.Errorf("Register %d data type = %v, want %v", addr, def.DataType, want.dataType)
		}
		if def.Scale != want.scale {
			t.Errorf("Register %d scale = %v, want %v", addr, def.Scale, want.scale)
		}
		if def.Words != want.words {
			t.Errorf("Register %d words = %d, want %d", addr, def.Words, want.words)
		}
		if def.Endianness != want.endianness {
			t.Errorf("Register %d endianness = %v, want %v", addr, def.Endianness, want.endianness)
		}
		if def.UseHolding != want.useHolding {
			t.Errorf("Register %d UseHolding = %v, want %v", addr, def.UseHolding, want.useHolding)
		}
	}
}

func TestSungrowDefaultValues(t *testing.T) {
	required := []uint16{
		5016, 13021, 13022, 5600,
		13036, 13040, 13045, 13002, 13026,
		13057, 13058, 13050,
	}

	for _, addr := range required {
		if _, ok := DefaultValues[addr]; !ok {
			t.Errorf("DefaultValues missing register %d", addr)
		}
	}

	if DefaultValues[13057] != 100 {
		t.Errorf("DefaultValues[13057] = %d, want 100", DefaultValues[13057])
	}
	if DefaultValues[13058] != 0 {
		t.Errorf("DefaultValues[13058] = %d, want 0", DefaultValues[13058])
	}
}
