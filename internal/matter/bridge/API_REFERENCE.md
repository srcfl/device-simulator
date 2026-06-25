# matter.js v0.16.11 Bridge API Reference

> Live-verified against `@matter/main@0.16.11` (Node v22). Every snippet below
> was confirmed by booting a real `ServerNode`. Keep this in sync when bumping
> matter.js.

## Imports
All re-exported from `@matter/main`: `ServerNode`, `Endpoint`, `Environment`, `StorageService`, `VendorId`.
Devices: `@matter/main/devices/<kebab>`. Behaviors: `@matter/main/behaviors/<kebab>`. Clusters: `@matter/main/clusters/<kebab>`. PascalCase subpaths do NOT resolve.

## ServerNode lifecycle
```js
const node = await ServerNode.create(ServerNode.RootEndpoint, {
  environment,                              // Environment.default
  id: "<storage-namespace>",                // becomes storage sub-folder
  network: { port },
  commissioning: { passcode, discriminator },
  productDescription: { name, deviceType: SomeDevice.deviceType },
  basicInformation: { vendorId: VendorId(0xfff1), vendorName, productId, productName, nodeLabel, serialNumber },
});
await node.add(endpoint);
await node.start();                         // resolves once online (non-blocking)
const { manualPairingCode, qrPairingCode } = node.state.commissioning.pairingCodes;
node.lifecycle.isCommissioned;             // boolean
Object.keys(node.state.commissioning.fabrics).length;  // fabric count
await node.close();
```
Storage dir: set env `MATTER_STORAGE_PATH` (== var `storage.path`) before create; node `id` namespaces each device under it, so fabrics persist across restarts.

### Events
```js
node.events.commissioning.commissioned.on(() => {});
node.events.commissioning.decommissioned.on(() => {});
node.events.commissioning.fabricsChanged.on((fabricIndex, action /* added|deleted|updated */) => {});
```

## Device types (verified imports)
| key | export | path | deviceType id |
|---|---|---|---|
| tempsensor | `TemperatureSensorDevice` | devices/temperature-sensor | 770 |
| lightsensor | `LightSensorDevice` | devices/light-sensor | 262 |
| smartplug | `OnOffPlugInUnitDevice` | devices/on-off-plug-in-unit | 266 |
| evse | `EnergyEvseDevice` | devices/energy-evse | 1292 |
| thermostat | `ThermostatDevice` | devices/thermostat | 769 |
| heatpump | `HeatPumpDevice` | devices/heat-pump | 777 |
| dishwasher | `DishwasherDevice` | devices/dishwasher | 117 |
| laundrywasher | `LaundryWasherDevice` | devices/laundry-washer | 115 |

## Composition / mandatory initial state
- Thermostat cluster is NOT auto-configured: `ThermostatDevice.with(ThermostatRequirements.ThermostatServer.with(Thermostat.Feature.Heating, .Cooling, .AutoMode))`.
- Electrical metering (plug/evse/heatpump): `.with(ElectricalPowerMeasurementServer.with(ElectricalPowerMeasurement.Feature.AlternatingCurrent), ElectricalEnergyMeasurementServer, PowerTopologyServer.with(PowerTopology.Feature.NodeTopology))`.
  - EPM initial state (mandatory): `{ powerMode: PowerMode.Ac, numberOfMeasurementTypes: 1, accuracy: [accuracyStruct(ActivePower)], activePower: 0 }`. `accuracy` is an ARRAY (len == numberOfMeasurementTypes).
  - EEM initial state: `{ accuracy: accuracyStruct(ElectricalEnergy) }`. `accuracy` is a SINGLE struct (not array). Cumulative energy is set as `{ energy: <mWh> }`.
  - accuracyStruct = `{ measurementType, measured: true, minMeasuredValue: 0, maxMeasuredValue: <max>, accuracyRanges: [{ rangeMin: 0, rangeMax: <max>, fixedMax: 1 }] }`.
- Mode clusters (`EnergyEvseMode`/`DishwasherMode`/`LaundryWasherMode`) need a `supportedModes` list (≥2) + `currentMode`. ModeOptionStruct = `{ label, mode, modeTags: [{ value: <ModeTag> }] }`.
- `OperationalState` needs `operationalStateList` (+ `operationalState`); `phaseList`/`currentPhase`/`countdownTime` may be null.

## Runtime attribute R/W + subscription
```js
await endpoint.set({ onOff: { onOff: true } });          // push simulated value
const v = endpoint.state.temperatureMeasurement.measuredValue; // read
endpoint.events.onOff.onOff$Changed.on((value, oldValue, context) => {
  const external = context?.fabric !== undefined;        // true => controller-driven
});
```

## Encodings (matter.js does NOT auto-scale — write the scaled integer)
- Temperature / thermostat setpoints: °C × 100 (Int16).
- Humidity: %RH × 100.
- Illuminance: `10000·log10(lux) + 1` (logarithmic), 0 = too low.
- Power cluster: mV / mA / mW / mHz (SI × 1000).
- Energy: mWh (Wh × 1000) inside `EnergyMeasurementStruct.energy`.
- EVSE currents: mA; sessionEnergyCharged: mWh.

## Test vendor ID / DCL test-net (commissioning into real ecosystems)

Simulated devices use the **CSA test Vendor ID `0xFFF1` (65521)** and a test
Product ID (`0x8000`). These are the standard *test* identifiers — they are **not
registered in the production Distributed Compliance Ledger (DCL)**, so a
controller that only trusts certified/production devices will reject them.

To commission a simulated device into a real ecosystem you must allow
uncertified / test devices:

- **Home Assistant** (Matter Server add-on): enable the **test/Test-Net DCL**
  option (a.k.a. "Allow non-certified / test devices"). Without it, pairing a
  `0xFFF1` device fails at attestation.
- **Apple Home / Google Home**: pairing an uncertified test device shows an
  "uncertified accessory" warning that must be accepted; some production builds
  refuse test VIDs entirely — use a developer/test controller (e.g. chip-tool,
  Home Assistant) for reliable testing.

The manual pairing code and `MT:` QR payload are surfaced per device by the
simulator (and via `GET /api/matter/status` for availability).

## Key enums
- `Thermostat.SystemMode`: Off=0, Auto=1, Cool=3, Heat=4, EmergencyHeat=5, Precooling=6, FanOnly=7, Dry=8, Sleep=9.
- `Thermostat.ControlSequenceOfOperation`: CoolingOnly=0, HeatingOnly=2, CoolingAndHeating=4.
- `EnergyEvse.State`: NotPluggedIn=0, PluggedInNoDemand=1, PluggedInDemand=2, PluggedInCharging=3, PluggedInDischarging=4, SessionEnding=5, Fault=6.
- `EnergyEvse.SupplyState`: Disabled=0, ChargingEnabled=1, DischargingEnabled=2, DisabledError=3, DisabledDiagnostics=4, Enabled=5.
- `OperationalState.OperationalStateEnum`: Stopped=0, Running=1, Paused=2, Error=3.
