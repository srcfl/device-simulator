package plant

// SemanticValues projects plant outputs onto the simulator's device-agnostic
// register names. Vendor maps (Sungrow, Huawei, …) then encode these into
// the addresses a Lua driver actually reads.
//
// Device registers usually report PV as a positive generation magnitude.
// The Lua driver negates that at the emit boundary. We therefore store
// positive PV watts here, matching existing simulator behaviour.
func SemanticValues(out Outputs) map[string]float64 {
	pvPos := -out.PVW
	if pvPos < 0 {
		pvPos = 0
	}
	battFlags := 0.0
	if out.BatteryW > 1 {
		battFlags = 2 // charging
	} else if out.BatteryW < -1 {
		battFlags = 4 // discharging
	}
	return map[string]float64{
		"pv_power":             pvPos,
		"pv1_voltage":          out.MPPTs[0].V,
		"pv1_current":          out.MPPTs[0].A,
		"pv2_voltage":          out.MPPTs[1].V,
		"pv2_current":          out.MPPTs[1].A,
		"load_power":           out.LoadW,
		"meter_power":          out.GridW,
		"meter_l1_power":       out.GridW / 3,
		"meter_l2_power":       out.GridW / 3,
		"meter_l3_power":       out.GridW / 3,
		"battery_power":        mathAbs(out.BatteryW),
		"battery_voltage":      out.BatteryV,
		"battery_current":      out.BatteryA,
		"battery_soc":          out.SOC,
		"battery_flags":        battFlags,
		"battery_temperature":  out.BatteryC,
		"inverter_temperature": out.InverterC,
		"grid_frequency":       out.GridHz,
		"total_pv_gen":         out.TotalPVWh,
		"total_charge":         out.TotalChargeWh,
		"total_discharge":      out.TotalDischargeWh,
		"total_import":         out.TotalImportWh,
		"total_export":         out.TotalExportWh,
		"running_state":        1,
	}
}

func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
