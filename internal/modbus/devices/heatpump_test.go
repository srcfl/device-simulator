package devices

import (
	"math"
	"testing"

	"github.com/srcfl/device-simulator/internal/modbus"
)

func TestHeatPumpRegisterSet(t *testing.T) {
	rs := HeatPumpRegisters()
	if rs.Semantic["hp_enable"] == nil || !rs.Semantic["hp_enable"].UseHolding {
		t.Fatal("hp_enable must be a holding register")
	}
	if rs.Semantic["indoor_temp"] == nil || rs.Semantic["indoor_temp"].UseHolding {
		t.Fatal("indoor_temp must be an input register")
	}
	if rs.Semantic["hp_sg_ready"] == nil {
		t.Fatal("missing SG Ready")
	}
}

func TestHeatPumpDefaultsAreEngineeringValues(t *testing.T) {
	rs := HeatPumpRegisters()
	s := modbus.NewServer(65535)
	s.InitDefaultsFromSet(rs)
	indoor, err := s.ReadRegisterValue(rs.Semantic["indoor_temp"])
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(indoor-19) > 0.15 {
		t.Fatalf("indoor default %v, want 19 °C", indoor)
	}
	en, err := s.ReadRegisterValue(rs.Semantic["hp_enable"])
	if err != nil {
		t.Fatal(err)
	}
	if en < 0.5 {
		t.Fatalf("hp_enable default %v, want 1", en)
	}
	cop, err := s.ReadRegisterValue(rs.Semantic["hp_cop"])
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(cop-3) > 0.05 {
		t.Fatalf("hp_cop default %v, want 3", cop)
	}
}
