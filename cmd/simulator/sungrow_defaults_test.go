package main

import (
	"path/filepath"
	"testing"

	"github.com/srcfl/device-simulator/internal/modbus"
	"github.com/srcfl/device-simulator/internal/modbus/devices"
	"github.com/srcfl/device-simulator/internal/profiles"
)

// Register defaults are engineering values. The Sungrow profile once wrote
// raw register values there, which made an SH10RT a 100 kW inverter with
// 500 kW battery limits.
func TestSungrowHybridDefaultsAreAnSH10RT(t *testing.T) {
	for _, dir := range []string{t.TempDir(), filepath.Join("..", "..", "profiles")} {
		pm := profiles.NewManager(dir)
		pm.InitEmbedded()
		if err := pm.LoadAll(); err != nil {
			t.Fatal(err)
		}
		profile := pm.Get("sungrow-hybrid")
		rs := devices.GetRegisterSet(profile.RegisterSetName)
		server := modbus.NewServer(65535)
		server.InitDefaultsFromSet(rs)
		for addr, value := range profile.Defaults {
			if def := rs.Lookup[addr]; def != nil {
				server.WriteRegisterValue(def, value)
			}
		}
		for _, c := range []struct {
			addr uint16
			want float64
			raw  uint16
		}{{5000, 10, 100}, {33046, 5, 500}, {33047, 5, 500}} {
			def := rs.Lookup[c.addr]
			got, err := server.ReadRegisterValue(def)
			if err != nil {
				t.Fatal(err)
			}
			regs := server.GetHoldingRegisters(c.addr, 1)
			if !def.UseHolding {
				regs = server.GetInputRegisters(c.addr, 1)
			}
			if got != c.want || len(regs) != 1 || regs[0] != c.raw {
				t.Errorf("%s: %s = %v %s (raw %v), want %v (raw %d)", dir, def.Name, got, def.Unit, regs, c.want, c.raw)
			}
		}
	}
}
