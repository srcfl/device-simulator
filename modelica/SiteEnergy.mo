within ;

package SiteEnergy
  "Lumped AC site plant. Compile SiteEnergy.Site4 to an FMI 2.0 CS FMU
   (see export.mos). The Go module siteplant loads that FMU, or falls
   back to a native backend that implements the same equations.

   Connector flow P is positive INTO the component.
   Lua meter (positive import) is -grid.ac.P."

  type Power = Real(final unit = "W");
  type Irradiance = Real(final unit = "W/m2");
  type Temperature = Real(final unit = "degC");
  type Energy = Real(final unit = "W.h");

  connector PowerPort
    "Balanced connector: one potential (frequency) and one flow (power)."
    Real freq "shared frequency in Hz";
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
    for i in 1:nInv loop
      inv[i].freq = grid.freq;
    end for;
    load.freq = grid.freq;
    ev.freq = grid.freq;
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
    ac.freq = 50;
  end GridSlack;

  model Battery
    input Power P_cmd;
    parameter Real soc_min = 0;
    parameter Real soc_max = 100;
    parameter Energy E_nom = 10000;
    parameter Power P_chg_max = 5000;
    parameter Power P_dis_max = 5000;
    parameter Real eta_c = 0.95;
    parameter Real eta_d = 0.95;
    output Real soc(start = 50);
    output Power P;
  equation
    P = min(P_chg_max, max(-P_dis_max, P_cmd));
    // E_nom is W.h; Modelica time is seconds.
    der(soc) = 100.0 * (if P > 0 then P * eta_c else P / eta_d) / (E_nom * 3600.0);
  end Battery;

  model PVArray
    input Irradiance ghi;
    input Temperature T_amb;
    input Power P_limit;
    parameter Real u_ratio(min = 0, max = 1) = 1;
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
    input Power u_pv_limit;
    input Power u_self_batt "FMI master assigns site self-consumption slack";
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
     assigned by the FMI master each step."
    parameter Integer nInv(min = 1) = 1;
    input Irradiance ghi;
    input Temperature T_amb;
    input Power P_load;
    input Power P_ev;
    input Integer u_mode[nInv];
    input Integer u_cmd[nInv];
    input Power u_sp[nInv];
    input Power u_self_batt[nInv];
    input Power u_pv_limit[nInv];
    ACBus bus(nInv = nInv);
    InverterUnit unit[nInv];
    HouseLoad house;
    EVCharger ev;
    GridSlack grid;
    output Power P_meter;
    output Real soc[nInv];
    output Power P_pv[nInv];
    output Power P_batt[nInv];
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
      unit[i].u_pv_limit = u_pv_limit[i];
      soc[i] = unit[i].soc;
      P_pv[i] = unit[i].P_pv;
      P_batt[i] = unit[i].P_batt;
    end for;
    P_meter = grid.P_meter;
  end Site;

  model Site4
    "Causal FMI 2.0 co-simulation slave. Same physics as Site, no connectors.
     The master (siteplant) assigns u_self_batt for mode-0 slack."
    constant Integer n = 4;
    input Irradiance ghi;
    input Temperature T_amb;
    input Power P_load;
    input Power P_ev;
    input Real u_mode[n] "0=self, 2=forced, 3=EMS (Real for FMI)";
    input Real u_cmd[n] "170=charge, 187=discharge, 204=stop";
    input Power u_sp[n];
    input Power u_self_batt[n];
    input Power u_pv_limit[n];
    input Real u_on[n] "1=inverter present, 0=empty FMI slot";
    PVArray pv[n];
    Battery bat[n];
    output Power P_meter;
    output Real soc[n];
    output Power P_pv[n];
    output Power P_batt[n];
  equation
    for i in 1:n loop
      pv[i].ghi = ghi;
      pv[i].T_amb = T_amb;
      pv[i].P_limit = u_pv_limit[i];
      bat[i].P_cmd = if u_on[i] < 0.5 then 0.0 elseif u_mode[i] < 0.5 then u_self_batt[i] else
        (if u_cmd[i] > 169.5 and u_cmd[i] < 170.5 then abs(u_sp[i])
         elseif u_cmd[i] > 186.5 and u_cmd[i] < 187.5 then -abs(u_sp[i])
         else 0.0);
      P_pv[i] = if u_on[i] < 0.5 then 0.0 else pv[i].P_pv;
      P_batt[i] = if u_on[i] < 0.5 then 0.0 else bat[i].P;
      soc[i] = bat[i].soc;
    end for;
    P_meter = -(sum(P_pv) + sum(P_batt) + noEvent(max(0.0, P_load)) + noEvent(max(0.0, P_ev)));
  end Site4;
end SiteEnergy;
