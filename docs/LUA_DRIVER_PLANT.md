# Lua drivers and a physical plant

This is the picture of a **site** as one electrical node, with physics as a
module that can be swapped.

![Site plant sketch](site-plant-sketch.svg)

Full notes: [SITE_PLANT_SKETCH.md](SITE_PLANT_SKETCH.md). Source of the bus
equation: [`modelica/SiteEnergy.mo`](../modelica/SiteEnergy.mo).
Go stand-in (no OpenModelica required): `internal/plant.NativeSite`.
Compiled Modelica: `make fmu` → `siteplant.Open` loads `Site4.fmu`.
Lua talks only Modbus: [`srcfl/device-drivers`](https://github.com/srcfl/device-drivers).

Drivers never talk to this app's HTTP API. They talk to a device. The job of the simulator is to *be* that device, with a plant behind the register map.

## Two layers

```text
  srcfl/device-drivers          this repo
  ┌──────────────────┐          ┌─────────────────────────────────────────┐
  │  sungrow.lua     │  Modbus  │  vendor facade     physical plant       │
  │  driver_poll()   │  TCP     │  (addresses,       (Modelica-style)     │
  │  driver_command()│─────────►│   scale,            algebraic node      │
  │  emit(pv, batt,  │  FC 03/04│   endianness,       + SoC / energy ODEs │
  │       meter)     │  06/10   │   input vs hold)                        │
  └──────────────────┘          └─────────────────────────────────────────┘
         ▲                                      │
         │ host.modbus_read/write               │ semantic names
         │ (FTW / Blixt / hugin-agent)          │ pv_power, battery_soc, …
```

**The plant does not know Sungrow.** It knows watts, SoC, irradiance and setpoints.

**The register map does not know physics.** It encodes plant outputs the way that inverter's firmware would.

That split is what makes a second vendor cheap: new Lua driver → fill in `internal/modbus/devices/<vendor>.go` → same plant.

## Sign convention (same as the drivers)

Positive watts flow **into** the site node:

```text
P_grid + P_pv + P_batt + P_load + P_ev = 0
```

| Stream   | Positive means        | On the wire |
|----------|-----------------------|-------------|
| grid     | import                | signed meter register |
| pv       | never (always ≤ 0)    | device usually reports **magnitude**; Lua negates at `emit` |
| battery  | charging              | many maps are unsigned + flag bits |
| load, ev | consumption           | |

If a Lua driver and this plant disagree on sign, EMS tests will look green and still be wrong. Match `blueprint/BLUEPRINT.lua`.

## What “Modelica-style” means here

Not a Modelica compiler. The same modelling split:

| Kind | Examples | Role |
|------|----------|------|
| Algebraic | Kirchhoff-style power balance; inverter clip; MPPT split | Instantaneous, no memory |
| Differential | Battery SoC, lifetime kWh, inverter temperature lag | Integrated on the **site/automation clock** (including time acceleration) |
| Inputs | GHI, ambient T, household load, Modbus setpoints | Exogenous |
| Outputs | Terminal powers, SoC, Hz, string V/I | What the facade encodes |

The site electrical node is the slack connection. In self-consumption the battery tries to zero the grid; in EMS/forced mode the battery follows the Lua setpoint and the grid takes the residual. Export limits curtail PV rather than fabricating meter zeros.

Implementation: `internal/plant`. Scenario **Physical plant** (`physical`) on a **site** steps the site bus (house load + every hybrid) on the site clock. A lone inverter still steps a one-unit plant from its automation loop.

## Site bus + a pluggable Modelica module

The site is one AC node, not N independent sine waves. The connection law is the same as in Modelica:

```text
0 = sum(inverter.P) + load.P + ev.P + grid.P
```

`flow P` is positive **into** the component. Lua's meter (positive import) is `-grid.P`.

The easiest way to keep that physics editable without dragging OpenModelica into the Go binary:

```text
modelica/SiteEnergy.mo     <-- source of truth (connectors, SoC ODE)
        |
        |  make fmu  (omc)  -->  Site4.fmu   (optional, linux/amd64)
        |
siteplant.Open             <-- plug: FMU if present, else NativeSite
        |
internal/plant.Model
        |
site clock (calculateAndUpdateMeter)
        |
vendor register maps / Lua
```

`NativeSite` implements the same bus equation in Go so `go test` and Docker work with zero extra tools. Self-consumption slack (mode 0) is assigned by the **master** each step: it depends on every unit's live Lua writes, so it must not sit inside the FMU as a hidden algebraic loop.

```bash
make fmu                          # needs omc; writes modelica/Site4.fmu
# or point at one:
SITEPLANT_FMU=/path/to/Site4.fmu
# force Go even if an FMU is on disk:
SITEPLANT_NATIVE=1
```

## Lua compatibility (Sungrow first)

`srcfl/device-drivers` `drivers/lua/sungrow.lua` uses **host addresses** (often documented register minus one for the 13xxx control block). A failed `modbus_read` fails the **entire poll**, so every address the driver asks for must exist.

Covered in this pass (see `TestSungrowLuaDriverRegisterCoverage`):

| Host address | Kind | Why the driver asks |
|--------------|------|---------------------|
| 4990 | input STR | serial (`modbus_read(4990, 10)`) — not 4989 |
| 4999 | input U16 | device type fingerprint (`0x0E0E` = SH8.0RT) |
| 5000, 5007, 5010, 5016, 5241 | input | rated, temp, MPPT, PV W, Hz |
| 5600–5607, 5740–5745 | input | meter P/V/I |
| 12999, 13000 | input | running state + charge/discharge flags |
| 13019–13022, 13026, 13040 | input | battery V/A/W/SoC and energy |
| 13049–13051 | holding | EMS mode, cmd, setpoint (`driver_command`) |
| 13057–13058, 33046–33047 | holding | SoC and power limits |
| 13073, 13086, 13088–13089 | holding | feed-in / active power curtail |

Holding and input are mirrored on the TCP server, which matters because the driver also does a 30-register **input** read at 13049 for diagnostics.

## How to test a driver against this process

1. Start the simulator (Docker or desktop).
2. Create a **sungrow-hybrid** inverter; note its Modbus TCP port (desktop: 5000+, Docker: 5100+).
3. Set scenario to **Physical plant** and start automation (or site simulation).
4. Point FTW, [hugin-agent](https://github.com/srcfl/hugin-agent), or the device-drivers Lua harness at `host:<port>`, unit id 1.
5. `driver_poll` should emit `pv`, `battery`, `meter`. `driver_command("charge"|"discharge", watts)` should change SoC over accelerated time.

The device-drivers repo's own tests (`drivers/tests/lua_harness`) mock the host. That is the **contract** test. This simulator is the **closed-loop** test: real TCP, real integration, physics that can refuse an impossible charge.

## What is still curve-based

Sunny / cloudy / … still write independent PV and load sines, then a late meter = load − PV + battery. That is useful for demos. It is not a plant: the battery does not close a self-consumption loop unless something else writes `battery_cmd`.

Next couplings, in order:

1. Drive **all** weather scenarios through `plant.WeatherForScenario` so Lua tests do not need a special scenario.
2. Per-driver fixture packs: the register window each Lua file reads, generated from the driver source (same idea as `test_modbus_drivers.py`).
3. MQTT / OCPP facades on the same bus (Ambibox, OCPP chargers) so mixed-protocol sites still conserve energy.

## Package map

| Path | Responsibility |
|------|----------------|
| `modelica/SiteEnergy.mo` | Modelica site (ACBus, hybrids, grid slack, Site4 FMU) |
| `siteplant` | Plug: compile FMU or native Go |
| `internal/plant` | NativeSite + semantic projection |
| `internal/modbus/devices` | Vendor addresses and encoding |
| `internal/modbus/server.go` | Modbus TCP (what Lua actually hits) |
| `cmd/simulator/main.go` | site clock → `runSitePlantAndApply` when scenario is `physical` |
| `srcfl/device-drivers` | Lua, host API, emit schema — **not copied here** |
