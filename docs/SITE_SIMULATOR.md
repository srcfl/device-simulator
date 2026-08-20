# Site simulator

This app is a **device** simulator that can also be a **site** simulator.

Pick a facility on the dashboard. That instantiates a physics plant (electrical
bus + lumped building / heat pump / DHW) and the Modbus facades an EMS such as
FTW would talk to. The site clock is the only integrator. Devices do not invent
power; they project what the plant just solved.

```text
EMS (FTW / FTV)
  Modbus TCP  →  inverter (Sungrow hybrid)     ← PV, battery, meter window
              →  heat pump (generic SG Ready)  ← indoor, DHW, COP, enable
              →  site meter (SDM630)           ← P_grid on phases
                       ▲
                       │ site tick
              plant.Model  (NativeSite; optional Site4.fmu for AC bus)
              P_grid + P_pv + P_batt + P_house + P_hp + P_ev = 0
              C·dT_in/dt = Q_hp_space + Q_solar + Q_int − Q_loss
```

Sketch of the bus: [SITE_PLANT_SKETCH.md](SITE_PLANT_SKETCH.md). Lua contract:
[LUA_DRIVER_PLANT.md](LUA_DRIVER_PLANT.md). Modelica source:
[`modelica/SiteEnergy.mo`](../modelica/SiteEnergy.mo).

## Facilities

| Id | What you get |
|----|----------------|
| `house-ashp` | Nordic house, air-source HP, DHW tank, 8 kW PV + 10 kWh battery |
| `house-gshp` | Same house, ground-source HP (stable winter COP) |
| `apartment-ashp` | Heavier envelope, larger ASHP, modest PV |
| `pv-battery` | Occupancy plug loads only, no heat pump |

`POST /api/sites/{id}/facility` with `{ "preset": "house-ashp" }` (or **Apply**
on the site-plant band) creates a Sungrow hybrid and a heat-pump device if the
site does not already have them, sets scenario `physical`, and starts the site
clock.

## Operating routines

Not a full FTV stack. Enough that the site feels occupied:

- Occupancy plug loads and internal heat (morning / evening)
- Heating curve vs outdoor temperature, night setback
- DHW morning / evening draws, tank + backup heater
- SG Ready on the heat pump: 0 normal, 1 block, 2 recommend on, 3 boost

EMS writes holding registers. The plant reads them on the next tick.

## Heat pump Modbus map

Holding (EMS → plant):

| Addr | Semantic | Meaning |
|------|----------|---------|
| 1100 | `hp_enable` | 1 = allowed to run |
| 1101 | `hp_setpoint` | room °C; 0 = heating curve |
| 1102 | `hp_mode` | 0 auto, 1 heat, 2 off, 3 DHW |
| 1103 | `hp_sg_ready` | 0–3 as above |

Input (plant → EMS): indoor / outdoor / DHW °C, electrical W, thermal W, COP, status.

## Visualization

The **Site plant** band on a selected site is a live schematic: grid, PV,
battery, AC bus residual, plug loads, heat pump, building, DHW. Green strokes
are electrical watts (Lua sign: PV ≤ 0, grid import > 0). Orange strokes are
heat. Numbers come from `GET /api/sites/{id}` → `plant` (same as
`GET /api/sites/{id}/plant`).

## Thermal vs FMU

Live thermal (building, HP, DHW) runs in **native Go** (`internal/plant`). The
Modelica models `Building`, `HeatPump`, `DHWTank`, `HouseFacility` match those
equations for documentation and a future thermal FMU. `Site4.fmu` stays the
electrical co-simulation slave (`make fmu`). Applying a facility forces the
native backend so heat and electricity stay on one clock.
