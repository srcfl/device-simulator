# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

> Don't Panic. It's just a Modbus simulator.

## Build and Run

```bash
# Build and start (single container, 10 simulators)
docker compose up --build

# Run Go tests
go test ./...
```

**Ports:**
- Web dashboard: http://localhost (port 80)
- Modbus TCP servers: 5000-5009 (one per simulator)

## Architecture

**Single Go application** running all simulator instances in one process:

```
device-simulator
├── HTTP Server :80 (unified dashboard + API)
├── Simulator 0: Modbus :5000
├── Simulator 1: Modbus :5001
├── ...
└── Simulator 9: Modbus :5009
```

### Go Backend (`cmd/simulator/`, `internal/`)

**Main components:**
- `cmd/simulator/main.go` - Entry point, unified HTTP server + Modbus servers
- `cmd/simulator/templates/index.html` - Embedded dashboard UI

**Internal packages:**
- `internal/modbus/server.go` - Modbus TCP server with register storage and logging
- `internal/modbus/registers.go` - Register definitions, encoding/decoding (U16, I16, U32, I32)
- `internal/generator/` - Data generators (daynight, sine, schedule)
- `internal/profiles/loader.go` - Device profile management

**Supported device profiles:**
- Sungrow (hybrid, inverter)
- Solis, SolarEdge, SMA
- Huawei, Fronius, Deye
- Pixii PowerShaper
- FerroAmp EnergyHub
- SDM630 Energy Meter

## API Endpoints

Single HTTP server on port 80 with unified API:

```
GET  /                                    # Dashboard UI
GET  /api/simulators                      # List all simulators
GET  /api/simulators/{id}                 # Get simulator details
POST /api/simulators/{id}/config          # Update serial/slave ID
GET  /api/simulators/{id}/registers       # Get all registers
POST /api/simulators/{id}/registers/{addr} # Write register value
GET  /api/simulators/{id}/logs            # Get Modbus operation logs
GET  /api/simulators/{id}/profiles        # List profiles
POST /api/simulators/{id}/profiles/{name}/apply  # Apply profile
GET  /api/simulators/{id}/automation      # Get automation status
POST /api/simulators/{id}/automation/start # Start data generation
POST /api/simulators/{id}/automation/stop  # Stop data generation
POST /api/simulators/{id}/automation/config # Update time multiplier
POST /api/simulators/{id}/reset           # Reset to defaults
```

## Key Files

| File | Purpose |
|------|---------|
| `cmd/simulator/main.go` | Single-app entry point with all servers |
| `cmd/simulator/templates/index.html` | Embedded unified dashboard |
| `internal/modbus/registers.go` | Register definitions with data types, scaling |
| `internal/profiles/loader.go` | Device profiles with embedded defaults |
| `profiles/*.json` | JSON profile files |

## Adding New Device Profiles

1. Add profile to `internal/profiles/loader.go` in `EmbeddedProfiles` map
2. Or create `profiles/{name}.json`:
```json
{
  "name": "device-name",
  "description": "Device description",
  "device_type": "manufacturer",
  "defaults": {
    "5016": 0,
    "13022": 50
  },
  "generators": {
    "solar_radiance": {
      "type": "daynight",
      "config": {"peak_power": 5000}
    }
  }
}
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| NUM_INSTANCES | 10 | Number of simulator instances |
| MODBUS_BASE_PORT | 5000 | First Modbus port |
| HTTP_PORT | 80 | Web dashboard port |

## Testing

```bash
# Run all Go tests
go test ./...

# Run specific package tests
go test ./internal/modbus/
go test ./internal/generator/

# Test Modbus connection
modpoll -m tcp -a 1 -r 5000 -c 10 localhost:5000
```
