# Headless runs

`simulator run` builds one house from a JSON spec, serves it on ports from a
base, applies a timeline of events and records the house once a second. An
EMS under test talks to it over Modbus and OCPP like a real site; its test
harness reads the record and can post events while the house runs. FTW's
site test (`go/test/site` in srcfl/ftw) uses it.

```bash
simulator run -spec house.json -port-base 18700 -out run/
```

The run ends after `duration_s`, on SIGINT or SIGTERM, or on a `stop` event.

## Ports

| Port | What |
|---|---|
| base | HTTP API (the usual one, plus `/api/run`), on 127.0.0.1 |
| base + 10 … | Modbus devices, in spec order, then the site meter |
| base + 30 … | OCPP servers (unused by chargers in client mode) |
| base + 50 … | MQTT |

Two runs on one machine need bases at least 100 apart. `ports.json` in the
output directory says where each device is.

## Spec

```json
{
  "name": "chargenow-25A",
  "duration_s": 0,
  "site": {"fuse_a": 25, "fuse_profile": "iec_gg", "house_w": [690, 690, 690]},
  "devices": [
    {"id": "inverter", "driver": "sungrow", "battery_kwh": 10, "soc_pct": 50, "min_soc_pct": 5, "pv_w": 0},
    {"id": "garage", "driver": "ocpp", "ocpp_url": "ws://ftw:secret@127.0.0.1:8887/",
     "max_kw": 11.04, "phase_switch": false, "command_response_s": 60,
     "ocpp_config": {"MeterValueSampleInterval": "10"}, "car_phases": 3, "plugged": false}
  ],
  "timeline": [{"at_s": 120, "do": "loads", "a": [13, 13, 13]}]
}
```

- `driver` is the srcfl/device-drivers driver the device emulates (the file
  name without `.lua`), or `ocpp` for an OCPP 1.6 charger. A Modbus device
  takes the model (profile) that names that driver; its timing comes from
  the model's `model` block and can be overridden per device
  (`command_response_s`).
- A hybrid inverter's battery follows its EMS registers (`ems_control`, on by
  default in a run).
- The site meter is an SDM630 behind a fuse per phase (`fuse_a`,
  `fuse_profile` from package `fuse`: a thermal model, gG for 16-63 A). Its
  id is `meter`, which no device may take. `fuse_ambient_c` is the
  cabinet's air (default 25 °C); `fuse_preheat_a`, a current per phase the
  fuses have carried long enough to settle, starts the run warm instead of
  cold. A size the profile does not model is refused.
- A charger dials `ocpp_url`; `user:password@` goes out as Basic auth.
  `car_max_a` and `car_phases` are what the car takes.

## Events

Each has `do` and, on the timeline, `at_s`. Post one to `/api/run/event` to
apply it now, or `{"label": "..."}` to `/api/run/mark`.

| `do` | Fields | Effect |
|---|---|---|
| `mark` | `label` | a named moment in the record |
| `loads` | `w` or `a` | house load per phase |
| `pv` | `pv_w`, `device` | PV power of an inverter (default the first) |
| `soc` | `pct`, `device` | battery state of charge |
| `plug` | `on`, `device` | car plugged in or out |
| `car` | `max_a`, `phases`, `device` | what the car takes |
| `offline` | `device`, `for_s` | the device stops answering and drops out of the site; back after `for_s` if given |
| `online` | `device` | back again |
| `reconnect` | `device`, `for_s` | a charger loses its connection for `for_s`; the car keeps charging |
| `silence` | `device`, `for_s` | a Modbus device keeps its connections but answers nothing (timeouts, not refusals); 0 until `online` |
| `meter_late` | `for_s` | the site meter's readings stand still; the house goes on |
| `meter_glitch` | | the next reading's total reads 0 W while its phases are right |
| `signed_currents` | `on` | the meter's phase currents carry the sign of their power (an SDM630 reports magnitudes) |
| `load_balancer` | `device`, `max_a` | the charger's own cap per phase, whatever it is offered; 0 none |
| `stop` | | end the run |

## Model parameters

Each model's timing and quirks sit in its profile's `model` block and can
be overridden per device in the spec:

| Parameter | Device | Meaning |
|---|---|---|
| `command_response_s` | hybrid, OCPP charger | the battery follows an EMS write, or the charger applies an accepted profile, this much later |
| `startup_delay_s` | OCPP charger | the car draws this long after an offer rises from zero; the charger says Charging meanwhile |
| `load_balancer_a` | OCPP charger | the charger's own cap per phase |
| `signed_phase_currents` | meter | currents carry a sign (`site.signed_phase_currents` in a spec) |

## Random timelines

```json
"random": {"seed": 42, "duration_s": 1800, "gap_s": 40, "kinds": ["loads_step", "meter_late"]}
```

draws events between 30 s and `duration_s` − 30 s, on average `gap_s`
apart: house load steps, drift and one-phase spikes (each phase stays under
the fuse less 2 A), PV cloud edges, the battery near its floor, the car
taking less than offered, device silence and outages, charger reconnects,
a late or glitched meter, signed currents and a load-balancer cap. It also
picks each charger's `command_response_s` (0, 5, 40 or 60 s) and
`startup_delay_s`, a one-phase car now and then, and the meter's sign,
where the spec leaves them open. `kinds` limits the draw. The run writes
`timeline.json`: the spec with the drawn timeline, which replays it.

## Output

| File | What |
|---|---|
| `ports.json` | the HTTP port, and each device's Modbus port and unit or OCPP identity |
| `plant.jsonl` | a sample a second: `phase_w` and `phase_a` through each fuse (+ into the site), `meter_w`, `fuse` (`damage`, the element's way from ambient to melting, 0 to 1; `blown`; `rating_a`), and each device's state; plus every event (`event`) and mark (`mark`) as it happened |
| `ocpp-<id>.jsonl` | every OCPP-J frame a charger read (`cs->cp`) or wrote (`cp->cs`), with its time |
| `result.json` | at the end: samples, events, whether a fuse blew |
