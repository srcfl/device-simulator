# EVCC Profile Reference

## What is EVCC?

EVCC (Electric Vehicle Charge Controller) is an open-source solar charging management system. It defines Modbus register mappings for many inverter and battery manufacturers.

This device-simulator uses EVCC's register definitions as a reference to ensure compatibility with real devices and EVCC itself.

## EVCC Source Repository

- **GitHub:** https://github.com/evcc-io/evcc
- **Templates:** https://github.com/evcc-io/evcc/tree/master/templates/definition

## Profile Categories

Profiles use categories to determine which registers are relevant:

| Category | Description | Example Registers |
|----------|-------------|-------------------|
| `pv` | Solar/PV registers | PV Power, Total Generation, PV Command |
| `battery` | Battery registers | Battery Power, SoC, Charge/Discharge |
| `meter` | Energy meter registers | Meter Power, Import/Export Energy |
| `grid` | Grid connection registers | Grid Frequency, Grid Mode, Export Limit |
| `control` | Control/Load registers | Load Power |

## Supported Profiles

| Profile | Device Type | Categories | EVCC Reference |
|---------|-------------|------------|----------------|
| `sungrow-hybrid` | Sungrow SH Series | pv, battery, meter, grid, control | [sungrow-hybrid.yaml](https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/sungrow-hybrid.yaml) |
| `solis-hybrid` | Solis Hybrid | pv, battery, meter, grid, control | - |
| `solaredge` | SolarEdge StorEdge | pv, battery, meter, grid | [solaredge.yaml](https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/solaredge.yaml) |
| `sma-tripower` | SMA Sunny Tripower | pv, meter, grid | [sma-tripower.yaml](https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/sma-tripower.yaml) |
| `huawei-sun2000` | Huawei SUN2000 | pv, battery, meter, grid, control | [huawei-sun2000.yaml](https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/huawei-sun2000.yaml) |
| `fronius-gen24` | Fronius GEN24 Plus | pv, battery, meter, grid | [fronius.yaml](https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/fronius.yaml) |
| `deye-hybrid` | Deye Hybrid | pv, battery, meter, grid, control | - |
| `pixii-powershaper` | Pixii PowerShaper 2 | battery, meter, control | - |
| `ferroamp-energyhub` | FerroAmp EnergyHub | pv, battery, meter, grid, control | - |
| `sdm630` | Eastron SDM630 | meter | [sdm.yaml](https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/sdm.yaml) |
| `residential-5kw` | Generic 5kW System | pv, battery, meter, grid, control | - |
| `commercial-50kw` | Generic 50kW System | pv, battery, meter, grid, control | - |

## Register Mapping Reference

Common EVCC fields mapped to device-simulator registers:

| EVCC Field | Register Name | Address | Type | Scale |
|------------|---------------|---------|------|-------|
| `power` | PV Power | 5016 | U32 LE | 1.0 |
| `soc` | Battery SoC | 13022 | U16 | 0.1 |
| `batteryPower` | Battery Power | 13021 | U16 | 1.0 |
| `gridPower` / `power` | Meter Power | 5600 | I32 LE | 1.0 |
| `energy` (import) | Total Import | 13036 | U32 LE | 1.0 |
| `energy` (export) | Total Export | 13045 | U32 LE | 1.0 |

## How to Add a New Profile

### 1. Find EVCC Template

Browse: https://github.com/evcc-io/evcc/tree/master/templates/definition/meter

Example EVCC template structure:
```yaml
template: sungrow-hybrid
products:
  - brand: Sungrow
    description:
      generic: SH Series Hybrid Inverter
registers:
  power:
    source: modbus
    address: 5016
    type: uint32
    decode: uint32s
  soc:
    source: modbus
    address: 13022
    type: uint16
    scale: 0.1
```

### 2. Add to Embedded Profiles

Edit `internal/profiles/loader.go` and add to `EmbeddedProfiles`:

```go
"new-device": {
    Name:        "new-device",
    Description: "New Device Description",
    DeviceType:  "manufacturer",
    Categories:  []string{"pv", "battery", "meter", "grid", "control"},
    EVCCRef:     "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/new-device.yaml",
    Defaults: map[uint16]float64{
        5016:  0,     // PV Power
        13022: 50,    // Battery SoC
        13021: 0,     // Battery Power
        5600:  0,     // Meter Power
    },
},
```

### 3. Or Create JSON Profile File

Save as `profiles/new-device.json`:
```json
{
  "name": "new-device",
  "description": "New Device Description",
  "device_type": "manufacturer",
  "categories": ["pv", "battery", "meter", "grid", "control"],
  "evcc_ref": "https://github.com/evcc-io/evcc/blob/master/templates/definition/meter/new-device.yaml",
  "defaults": {
    "5016": 0,
    "13022": 50
  }
}
```

## Updating from EVCC

To sync profiles with EVCC updates:

1. Check EVCC repository for changes to template definitions
2. Compare register addresses and data types
3. Update `internal/modbus/registers.go` if new registers needed
4. Update profile defaults in `internal/profiles/loader.go`

### Key EVCC Directories

- **Meters:** `templates/definition/meter/` - Inverters, batteries, energy meters
- **Chargers:** `templates/definition/charger/` - EV chargers
- **Vehicles:** `templates/definition/vehicle/` - EV integrations

## Data Types

| EVCC Type | Simulator Type | Description |
|-----------|----------------|-------------|
| `uint16` | U16 | Unsigned 16-bit |
| `int16` | I16 | Signed 16-bit |
| `uint32` / `uint32s` | U32 | Unsigned 32-bit (word swap) |
| `int32` / `int32s` | I32 | Signed 32-bit (word swap) |

**Endianness:**
- `uint32s` / `int32s` = Little-endian (low word first)
- `uint32` / `int32` = Big-endian (high word first)

## Recommended Sync Schedule

Check EVCC repository monthly for updates to template definitions, especially after EVCC releases.
