# Device Simulator

> *"In the beginning the Universe was created. This has made a lot of people very angry and been widely regarded as a bad move."* - But Modbus? Modbus is mostly harmless.

A **Mostly Harmless** Modbus TCP device simulator for testing solar inverters, batteries, and energy systems. Don't Panic - it's just registers.

## Quick Start

```bash
# The Answer to Everything (well, at least to running this)
docker compose up --build
```

**That's it.** One container. Ten simulators. Infinite improbability.

| Port | What's There |
|------|--------------|
| 80 | Web Dashboard (your towel is in here) |
| 5000-5009 | Modbus TCP servers (one per simulator) |

Open http://localhost and start exploring the galaxy of registers.

## Features

- **10 Simulator Instances** - Each with its own Modbus port (5000-5009)
- **Unified Dashboard** - Single web interface to rule them all
- **Device Profiles** - Sungrow, SolarEdge, Fronius, Huawei, and more
- **Time Acceleration** - Compress 24 hours into minutes for testing
- **Modbus Logging** - See every read/write operation in real-time
- **Configurable Slave IDs** - Because sometimes you need unit ID 42

## Supported Profiles

| Profile | Description |
|---------|-------------|
| sungrow-hybrid | Sungrow hybrid inverter with battery |
| sungrow-inverter | Sungrow string inverter |
| solaredge | SolarEdge inverter |
| fronius | Fronius inverter |
| huawei | Huawei SUN2000 series |
| sma | SMA Sunny Boy/Tripower |
| solis | Solis inverter |
| deye | Deye hybrid inverter |
| pixii-powershaper | Pixii PowerShaper |
| ferroamp | FerroAmp EnergyHub |
| sdm630 | SDM630 Energy Meter |

## API Endpoints

All simulators are accessible through a unified API:

```
GET  /api/simulators                        # List all simulators
GET  /api/simulators/{id}                   # Get simulator details
POST /api/simulators/{id}/config            # Update serial/slave ID
GET  /api/simulators/{id}/registers         # Get all registers
POST /api/simulators/{id}/registers/{addr}  # Write register value
GET  /api/simulators/{id}/logs              # Get Modbus operation logs
GET  /api/simulators/{id}/profiles          # List available profiles
POST /api/simulators/{id}/profiles/{name}/apply  # Apply a profile
GET  /api/simulators/{id}/automation        # Get automation status
POST /api/simulators/{id}/automation/start  # Start data generation
POST /api/simulators/{id}/automation/stop   # Stop data generation
POST /api/simulators/{id}/automation/config # Configure time multiplier
GET  /health                                # Health check (always 42... I mean OK)
```

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| NUM_INSTANCES | 10 | Number of simulator instances |
| MODBUS_BASE_PORT | 5000 | First Modbus port |
| HTTP_PORT | 80 | Web dashboard port |

## Testing Modbus Connections

```bash
# Using modpoll (or any Modbus client)
modpoll -m tcp -a 1 -r 5000 -c 10 localhost:5000

# Read holding registers from simulator 3
modpoll -m tcp -a 1 -r 5000 -c 10 localhost:5002
```

## Development

```bash
# Run locally (no Docker)
go run ./cmd/simulator

# Run tests
go test ./...

# Build
go build -o simulator ./cmd/simulator
```

## Architecture

```
device-simulator (single process)
├── HTTP Server :8080 (unified dashboard)
├── Simulator 0: Modbus :5000
├── Simulator 1: Modbus :5001
├── ...
└── Simulator 9: Modbus :5009
```

The simulator is a single Go application that runs all instances in one process. Much more efficient than running separate containers - and unlike the Heart of Gold, it doesn't require an Infinite Improbability Drive.

## Why "Mostly Harmless"?

Because that's what the Hitchhiker's Guide to the Galaxy says about Earth. And if this simulator can make testing energy systems a little less painful, maybe we can upgrade our entry to "Largely Harmless."

---

*Remember: DON'T PANIC. And always know where your towel is.*
