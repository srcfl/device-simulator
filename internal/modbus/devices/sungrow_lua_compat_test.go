package devices

import (
	"testing"

	"github.com/srcfl/device-simulator/internal/modbus"
)

// Addresses the srcfl/device-drivers Sungrow Lua driver actually reads or writes.
// The simulator must serve these so a live driver poll does not fail the whole poll.
func TestSungrowLuaDriverRegisterCoverage(t *testing.T) {
	rs := SungrowRegisters()
	need := []uint16{
		4990, 4999, 5000, 5007, 5010, 5016, 5241, 5600, 5602, 5740, 5743,
		12999, 13000, 13002, 13019, 13026, 13036, 13040, 13045,
		13049, 13050, 13051, 13057, 13073, 13086, 13088, 13089,
		33046, 33047,
	}
	for _, addr := range need {
		if rs.Lookup[addr] == nil {
			t.Errorf("Sungrow map missing address %d (required by drivers/lua/sungrow.lua)", addr)
		}
	}
	if rs.Semantic["device_type"] == nil {
		t.Error("missing semantic device_type")
	}
	if rs.Semantic["running_state"] == nil {
		t.Error("missing semantic running_state")
	}
	if rs.Semantic["pv_limit_enable"] == nil || rs.Semantic["pv_limit_ratio"] == nil {
		t.Error("missing active power limit holding registers 13088/13089")
	}
}

func TestSungrowLuaFingerprintAndSerial(t *testing.T) {
	s := modbus.NewServer(65535)
	rs := SungrowRegisters()
	s.SetSerialNumber("INVSIM01")
	s.InitDefaultsFromSet(rs)

	dev := s.GetInputRegisters(4999, 1)
	if len(dev) != 1 || dev[0] != 0x0E0E {
		t.Fatalf("device type at 4999 = %v, want [0x0E0E] so sungrow.lua detect_model sees an SH hybrid", dev)
	}

	sn := s.GetInputRegisters(4990, 10)
	if len(sn) != 10 {
		t.Fatalf("serial word count %d", len(sn))
	}
	if sn[0] == 0 && sn[1] == 0 {
		t.Fatal("Lua serial window at 4990 is empty; sungrow.lua reads 4990 not 4989")
	}

	run := s.GetInputRegisters(12999, 1)
	if len(run) != 1 || run[0] == 0 {
		t.Fatalf("running state at 12999 = %v, want non-zero", run)
	}
}
