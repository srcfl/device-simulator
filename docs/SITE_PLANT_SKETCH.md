# Site plant sketch

This is the picture of a **site** as one electrical node, with physics as a
module that can be swapped. The drawing:

![Site plant sketch](site-plant-sketch.svg)

Source of the bus equation: [`modelica/SiteEnergy.mo`](../modelica/SiteEnergy.mo).
Go stand-in (no OpenModelica required): `internal/plant.NativeSite`.
Lua talks only Modbus: [`srcfl/device-drivers`](https://github.com/srcfl/device-drivers).

## What is in the sketch

Three strips, left to right:

1. **Plant** — Modelica connectors on an AC bus. Inverters inject `P_pv + P_batt`. House and EV consume. Grid is slack. `P_meter = -grid.P` so Lua import is positive.
2. **Facade** — semantic watts become vendor registers. No physics here.
3. **Lua** — `driver_poll` / `driver_command` over Modbus TCP. Not the HTTP API.

Under the plant: the **site clock** is the only integrator. Scenario `physical` calls `StepSite(dt)` once per tick.

## Connection law

```mermaid
flowchart LR
  subgraph plant["SiteEnergy.Site"]
    PV1["Inverter 1\nP_pv + P_batt"]
    PVN["Inverter N"]
    Load["House load"]
    EV["EV"]
    Bus(("AC bus\nΣP = 0"))
    Grid["Grid slack"]
    PV1 --> Bus
    PVN --> Bus
    Load --> Bus
    EV --> Bus
    Bus --> Grid
  end
  GHI[GHI / T_amb] --> PV1
  GHI --> PVN
  Lua[Lua setpoints] --> PV1
  Lua --> PVN
  Grid --> Meter["P_meter import+"]
```

`flow P` is positive **into** the component. That is the Modelica convention and it matches the Lua sign table after `P_meter = -grid.P`.

## Plug

```mermaid
flowchart TB
  MO["modelica/SiteEnergy.mo"]
  MO -->|same equations| Native["NativeSite\nGo, default"]
  MO -.->|omc / Dymola later| FMU["Site.fmu\nFMI 2.0 CS"]
  Native --> I["plant.Model"]
  FMU -.-> I
  I --> Clock["site clock"]
  Clock --> Maps["vendor maps"]
  Maps --> TCP["Modbus TCP"]
  TCP --> Lua["sungrow.lua"]
```

Keep self-consumption slack in the **master** (Go). A mode-0 battery must see every other unit's live Lua write in the same step. Putting that algebraic loop inside the FMU makes the slave non-causal.

## Tick

```text
site tick (dt includes time acceleration)
  read P_load from site phase loads
  read Control from each inverter's holding registers  ← Lua writes land here
  NativeSite.StepSite(dt)
    preview PV from GHI
    EMS/forced batteries follow setpoint
    mode-0 batteries share residual so the meter wants to be ~0
    export cap curtails PV if needed
    integrate SoC
  write semantic registers
  write site meter (P_grid on phases, then fuses)
```

## Out of scope for this sketch

- Full 3-phase electrical (only power split by `phase_factors`)
- Thermal networks, MPPT tracking dynamics, transformer impedance
- Compiling and loading the FMU (`LoadFMU` is the hole for that)

Those can grow **inside** `SiteEnergy.mo` without changing Lua.
