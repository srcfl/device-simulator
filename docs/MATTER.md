# Matter Support

The simulator can emulate **commissionable Matter devices** — real Matter nodes you can
pair into Home Assistant, Apple Home, or Google Home and control like physical hardware.
Under the hood it uses [matter.js](https://github.com/matter-js/matter.js)
(`@matter/main` v0.16, Matter 1.4.2), the reference JavaScript implementation of the
Matter standard.

## Requirements

Matter support needs **Node.js v18+ (v20+ recommended) installed on the host**. Nothing
else — the matter.js code and all its dependencies ship inside the simulator binary and
are extracted automatically. If Node is not found, Matter is disabled gracefully: the
rest of the simulator works normally, `GET /api/matter/status` reports why Matter is
unavailable, and the UI hides the Matter device buttons.

## Device types

| Type (`POST /api/simulators`) | Emulates | Matter clusters |
|---|---|---|
| `matter_tempsensor` | Temperature sensor | TemperatureMeasurement |
| `matter_lightsensor` | Light (illuminance) sensor | IlluminanceMeasurement |
| `matter_smartplug` | Smart plug with energy metering | OnOff, ElectricalPowerMeasurement, ElectricalEnergyMeasurement |
| `matter_evse` | EV charger (EVSE) | EnergyEvse (+Mode), ElectricalPowerMeasurement, ElectricalEnergyMeasurement |
| `matter_thermostat` | Room thermostat | Thermostat (Heating, Cooling, AutoMode) |
| `matter_heatpump` | Heat pump with energy metering | Thermostat, ElectricalPowerMeasurement, ElectricalEnergyMeasurement |
| `matter_dishwasher` | Dishwasher | OperationalState, DishwasherMode |
| `matter_laundrywasher` | Washing machine | OperationalState, LaundryWasherMode |

Each device is a full Matter node on its own UDP port (5540+ by default) with a unique,
stable **manual pairing code** and **`MT:` QR payload**.

## Quick start

```bash
# 1. Check Matter is available (Node.js detected)
curl http://localhost:8762/api/matter/status

# 2. Create a device
curl -X POST http://localhost:8762/api/simulators -d '{"type": "matter_smartplug"}'
# → {"id": 3, ...}

# 3. Start the simulation loop (drives the built-in physics, 1 tick/second)
curl -X POST http://localhost:8762/api/simulators/3/automation/start

# 4. Read the pairing code and QR payload
curl http://localhost:8762/api/simulators/3
# → state.pairing_code = "3497-011-2332", state.qr = "MT:..."

# 5. Commission it into your controller (see "Pairing into real ecosystems" below),
#    then control it:
curl -X POST http://localhost:8762/api/simulators/3/state -d '{"key": "on", "value": true}'
```

In the web UI / desktop app the same flow is point-and-click: the Matter buttons appear
in the *Add Simulator* panel when Node.js is detected.

## Controlling devices

`POST /api/simulators/{id}/state` with a JSON body `{"key": "...", "value": ...}`:

| Type | Key | Effect |
|---|---|---|
| `matter_tempsensor` | `temperature_c` | Override the reported temperature (°C) |
| | `auto` | Resume the automatic diurnal curve |
| `matter_lightsensor` | `lux` | Override the reported illuminance |
| | `auto` | Resume the automatic daylight curve |
| `matter_smartplug` | `on` (bool) | Switch the plug on/off |
| | `load_w` | Set the connected load in watts (default 200) |
| `matter_evse` | `plugged` (bool) | Plug in / unplug the EV (unplugging resets session energy) |
| | `charging` (bool) | Start/stop the charging session |
| | `max_current_a` | Charge current in amps (default 32) |
| `matter_thermostat`, `matter_heatpump` | `heating_setpoint_c` | Occupied heating setpoint (°C) |
| | `cooling_setpoint_c` | Occupied cooling setpoint (°C) |
| | `system_mode` | Matter SystemMode enum: 0=Off, 1=Auto, 3=Cool, 4=Heat |
| `matter_dishwasher`, `matter_laundrywasher` | `start` | Start a cycle (90 min / 120 min) |
| | `stop` | Abort the running cycle |

## Built-in behavior (automation)

Each device ships with physics that emulate it being used. The physics run inside the
regular automation loop — start it with `POST /api/simulators/{id}/automation/start`
(or the UI toggle); it is **not running by default** when a device is created. Once
started, the device updates its Matter attributes once per second:

- **Temperature sensor** — diurnal sine wave (20 °C ± 6 °C, warmest mid-afternoon).
- **Light sensor** — daylight curve from sunrise (06:00) to sunset (20:00), peaking at
  50 000 lux; dark at night.
- **Smart plug** — off until switched on; while on it draws the configured `load_w`
  with a small ripple and integrates the energy counter.
- **EVSE** — idle until an EV is `plugged` in and `charging` starts; then it charges at
  `max_current_a` × 230 V (default ≈ 7.4 kW), reporting session energy
  (`sessionEnergyCharged`) and lifetime energy.
- **Thermostat** — a first-order thermal model: the room drifts toward ambient and is
  heated/cooled toward the setpoints; `thermostatRunningState` reflects activity.
- **Heat pump** — the same thermal model against a cold (5 °C) ambient; while running
  it draws thermal power ÷ COP (≈ 1.1 kW) and integrates energy.
- **Dishwasher / washing machine** — after `start`, `operationalState` = Running and
  `countdownTime` counts down the cycle; the device stops itself when done.
  *(These two do not report power/energy yet.)*

### Energy reporting

Smart plug, EVSE, and heat pump expose the standard Matter metering clusters
(`ElectricalPowerMeasurement`: active power, RMS voltage/current;
`ElectricalEnergyMeasurement`: cumulative imported energy). A commissioned controller —
e.g. the Home Assistant energy dashboard — sees live power and a monotonically
increasing energy counter while the simulation runs.

### External control is bidirectional

Changes made from a paired controller flow back into the simulation. Toggle the plug
from Apple Home and the simulator's `on` state (and therefore power draw and energy
accrual) follows; change a thermostat setpoint from Home Assistant and the thermal
model heats toward the new target. The Go simulation is the source of truth, so the
UI/API and the Matter fabric always agree.

## Pairing into real ecosystems

Simulated devices use the CSA **test Vendor ID `0xFFF1`** and a test Product ID. These
are not registered in the production Distributed Compliance Ledger (DCL), so
controllers that only accept certified devices will reject them:

- **Home Assistant** — enable the Matter Server's **test / Test-Net DCL** option
  ("allow non-certified/test devices"), then pair with the manual pairing code or QR.
- **Apple Home / Google Home** — accept the "uncertified accessory" warning; some
  production builds refuse test VIDs entirely, so a developer/test controller
  (Home Assistant, chip-tool) is the most reliable choice.

Commissioning requires the controller to reach the device via **LAN multicast (mDNS)**.
This works in desktop mode and with Docker *host* networking; under Docker's default
bridge networking the devices won't be discoverable.

## Persistence

Fabric credentials are stored per device under the Matter storage root (see
`MATTER_STORAGE_ROOT` below), so **commissioning survives app restarts** — a paired
device reconnects to its fabric without re-pairing. Pairing codes are derived
deterministically from the simulator id, so they are stable across restarts too.
Deleting a simulator currently keeps its storage; delete the device's folder under the
storage root to force a factory-fresh pairing.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `MATTER_BASE_PORT` | 5540 | First UDP operational port for Matter devices |
| `MATTER_NODE_PATH` | auto-detected | Explicit path to the `node` binary |
| `MATTER_STORAGE_ROOT` | `<user config dir>/Device Simulator/matter` | Fabric/credential storage root |
| `MATTER_LOG_LEVEL` | notice | Bridge log level (debug/info/notice/warn/error/fatal) |

## How it works (architecture)

matter.js is a Node.js library, while the simulator is a single Go binary — so Matter
support is built as a **supervised Node.js bridge**:

```
Go (simulator)                          Node.js (bridge, spawned child)
├── internal/matter/manager.go  ──────▶ internal/matter/bridge/index.mjs
│     spawns/supervises one Node        one matter.js ServerNode per device,
│     process for all devices           each on its own UDP port
├── internal/matter/server.go           NDJSON RPC over stdin/stdout
│     per-device adapter; physics       (stderr carries logs)
│     live here in Go
└── internal/matter/devicetypes.go
      per-type defaults, physics,
      control keys
```

- The bridge program and its vendored `node_modules` are **embedded in the Go binary**
  (`node_modules.tgz`) and extracted to a per-user cache directory on first use — the
  host only needs a Node runtime, never npm or network access.
- One Node process hosts all Matter devices. It is started lazily with the first
  device, auto-restarted with backoff if it crashes, and every live device is
  **replayed** into the new process (persisted storage means commissioning survives).
- Device physics (temperatures, power, cycles) are computed **in Go**; the bridge is a
  thin Matter protocol shim. Go pushes attribute changes down each tick, and
  controller-initiated writes come back as events.

## Development

```bash
# Unit tests (no Node.js required — bridge is faked in-process)
go test ./internal/matter/...

# Integration tests against a real Node bridge (skipped if Node is absent)
make test-matter

# Re-vendor the bridge dependencies after changing bridge/package.json
make vendor-matter
```

A live-verified reference of the matter.js APIs the bridge relies on is kept in
[`internal/matter/bridge/API_REFERENCE.md`](../internal/matter/bridge/API_REFERENCE.md).
