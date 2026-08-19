within ;

package SiteEnergy
  "Lumped AC site plant for srcfl/device-simulator.

   Physics source of truth. internal/plant.NativeSite implements the same
   bus equation so the app runs without a Modelica tool. Compile this
   package to an FMI 2.0 CS FMU and plant.LoadFMU replaces the backend
   without changing Modbus maps or Lua drivers.

   Connector flow P is positive INTO the component.
   Lua meter (positive import) is -grid.ac.P."

  type Power = Real(final unit = "W");
  type Irradiance = Real(final unit = "W/m2");
  type Temperature = Real(final unit = "degC");
  type Energy = Real(final unit = "W.h");

  connector PowerPort
    flow Power P;
  end PowerPort;

  model ACBus
    parameter Integer nInv(min = 1) = 1;
    PowerPort inv[nInv];
    PowerPort load;
    PowerPort ev;
    PowerPort grid;
  equation
    0 = sum(inv.P) + load.P + ev.P + grid.P;
  end ACBus;

  model HouseLoad
    PowerPort ac;
    input Power P_set;
  equation
    ac.P = noEvent(max(0.0, P_set));
  end HouseLoad;

  model EVCharger
    PowerPort ac;
    input Power P_set;
  equation
    ac.P = noEvent(max(0.0, P_set));
  end EVCharger;

  model GridSlack
    PowerPort ac;
    output Power P_meter "Lua convention: positive import";
  equation
    P_meter = -ac.P;
  end GridSlack;

  model Battery
    input Power P_cmd;
    input Real soc_min = 0;
    input Real soc_max = 100;
    parameter Energy E_nom = 10000;
    parameter Power P_chg_max = 5000;
    parameter Power P_dis_max = 5000;
    parameter Real eta_c = 0.95;
    parameter Real eta_d = 0.95;
    output Real soc(start = 50);
    output Power P;
  equation
    P = min(P_chg_max, max(-P_dis_max, P_cmd));
    der(soc) = 100.0 * (if P > 0 then P * eta_c / E_nom else P / (eta_d * E_nom));
  end Battery;

  model PVArray
    input Irradiance ghi;
    input Temperature T_amb;
    input Power P_limit;
    input Real u_ratio(min = 0, max = 1) = 1;
    parameter Power P_stc = 8000;
    parameter Power P_rated = 8000;
    parameter Real eta = 0.97;
    parameter Real gamma = -0.004;
    output Power P_pv "<= 0, site convention";
    output Power P_dc;
  protected
    Power p_ac;
  equation
    P_dc = noEvent(max(0.0, (ghi / 1000.0) * P_stc * (1.0 + gamma * (T_amb - 20.0))));
    p_ac = min(P_rated, min(P_rated * u_ratio, P_dc * eta));
    P_pv = -min(p_ac, if P_limit > 0 then P_limit else p_ac);
  end PVArray;

  model InverterUnit
    "Hybrid AC terminal: P_ac = P_pv + P_batt (both site convention)."
    PowerPort ac;
    input Irradiance ghi;
    input Temperature T_amb;
    input Integer u_mode;
    input Integer u_cmd;
    input Power u_sp;
    input Power u_pv_limit = 0;
    input Power u_self_batt = 0 "FMI master assigns site self-consumption slack";
    PVArray pv;
    Battery bat;
    output Power P_pv;
    output Power P_batt;
    output Real soc;
  equation
    pv.ghi = ghi;
    pv.T_amb = T_amb;
    pv.P_limit = u_pv_limit;
    P_pv = pv.P_pv;
    P_batt = bat.P;
    soc = bat.soc;
    bat.P_cmd = if u_mode == 0 then u_self_batt else
      (if u_cmd == 170 then abs(u_sp) elseif u_cmd == 187 then -abs(u_sp) else 0.0);
    ac.P = P_pv + P_batt;
  end InverterUnit;

  model Site
    "nInv hybrids + house + EV + grid. Mode-0 self-consumption slack is
     assigned by the FMI master (Go) each step, because it depends on
     the live Lua writes of every unit. Do not put that outer loop in
     the FMU — keep the slave causal."
    parameter Integer nInv(min = 1) = 1;
    input Irradiance ghi;
    input Temperature T_amb;
    input Power P_load;
    input Power P_ev = 0;
    input Integer u_mode[nInv];
    input Integer u_cmd[nInv];
    input Power u_sp[nInv];
    input Power u_self_batt[nInv] "master-assigned slack for mode 0";
    ACBus bus(nInv = nInv);
    InverterUnit unit[nInv];
    HouseLoad house;
    EVCharger ev;
    GridSlack grid;
    output Power P_meter;
    output Real soc[nInv];
  equation
    connect(house.ac, bus.load);
    connect(ev.ac, bus.ev);
    connect(grid.ac, bus.grid);
    house.P_set = P_load;
    ev.P_set = P_ev;
    for i in 1:nInv loop
      connect(unit[i].ac, bus.inv[i]);
      unit[i].ghi = ghi;
      unit[i].T_amb = T_amb;
      unit[i].u_mode = u_mode[i];
      unit[i].u_cmd = u_cmd[i];
      unit[i].u_sp = u_sp[i];
      unit[i].u_self_batt = u_self_batt[i];
      soc[i] = unit[i].soc;
    end for;
    P_meter = grid.P_meter;
  end Site;
end SiteEnergy;
