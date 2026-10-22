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
  `fuse_profile` from package `fuse`).
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
| `stop` | | end the run |

## Output

| File | What |
|---|---|
| `ports.json` | the HTTP port, and each device's Modbus port and unit or OCPP identity |
| `plant.jsonl` | a sample a second: `phase_w` and `phase_a` through each fuse (+ into the site), `meter_w`, `fuse` (`damage`, `blown`, `rating_a`), and each device's state; plus every event (`event`) and mark (`mark`) as it happened |
| `ocpp-<id>.jsonl` | every OCPP-J frame a charger read (`cs->cp`) or wrote (`cp->cs`), with its time |
| `result.json` | at the end: samples, events, whether a fuse blew |
