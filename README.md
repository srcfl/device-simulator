# Device Simulator

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Build](https://github.com/srcfl/device-simulator/actions/workflows/build.yml/badge.svg)](https://github.com/srcfl/device-simulator/actions/workflows/build.yml)
[![Latest release](https://img.shields.io/github/v/release/srcfl/device-simulator)](https://github.com/srcfl/device-simulator/releases)
[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8.svg)](go.mod)

> *"In the beginning the Universe was created. This has made a lot of people very angry and been widely regarded as a bad move."* - But Modbus? Modbus is mostly harmless.

A **Mostly Harmless** multi-protocol device simulator for testing solar inverters, batteries, EV chargers, energy systems — and now commissionable **Matter** smart-home devices. Don't Panic - it's just registers.

## Quick Start

### Option 1: Desktop App (Recommended)

Download the latest release for your platform from [GitHub Releases](https://github.com/srcfl/device-simulator/releases):

| Platform | Download |
|----------|----------|
| macOS | `device-simulator_darwin_amd64.zip` |
| Linux | `device-simulator_linux_amd64.tar.gz` |
| Windows | `device-simulator_windows_amd64.zip` |

**macOS:** Unzip, drag to Applications, right-click → Open (first time only)

**Linux:**
```bash
tar -xzf device-simulator_linux_amd64.tar.gz
sudo apt install libgtk-3-0 libwebkit2gtk-4.0-37  # Debian/Ubuntu
./linux/device-simulator.sh
```

**Windows:** Unzip and run `device-simulator.exe --desktop`

The app auto-updates when new versions are available.

### Option 2: Docker (Development/Server)

```bash
# The Answer to Everything (well, at least to running this)
docker compose up --build
```

**That's it.** One container. Dynamic simulators. Infinite improbability.

| Port | Docker | Desktop App | What's There |
|------|--------|-------------|--------------|
| 80 | ✓ | - | Web Dashboard (Docker mode) |
| 8762 | - | ✓ | Web Dashboard (Desktop mode) |
| 5000+ | ✓ | ✓ | Modbus TCP servers |
| 1883+ | ✓ | ✓ | MQTT brokers (V2X chargers) |
| 9000+ | ✓ | ✓ | OCPP WebSocket servers |
| 5540+ (UDP) | ✓* | ✓ | Matter devices (matter.js) |

- **Docker:** Open http://localhost
- **Desktop:** The app opens automatically with a native window

\* Matter commissioning needs LAN multicast (mDNS) — use Docker *host* networking or the desktop app for pairing.

## Features

- **Multi-Protocol Support** - Modbus TCP, MQTT, OCPP, and Matter in one app
- **Matter Devices** - Commissionable sensors, smart plugs, EVSE, thermostats, heat pumps, and appliances via [matter.js](https://github.com/matter-js/matter.js) — pair them into Home Assistant and control them like real hardware ([details](docs/MATTER.md))
- **Desktop & Server Modes** - Native app or Docker container
- **Auto-Updates** - Desktop app updates itself from GitHub Releases
- **Dynamic Simulators** - Create/delete simulators on the fly
- **Site Management** - Group simulators into sites with energy meters
- **Device Profiles** - Sungrow, SolarEdge, Fronius, Huawei, and more
- **Time Acceleration** - Compress 24 hours into minutes for testing
- **Real-time Logging** - See every protocol operation as it happens

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

> **Disclaimer:** Device Simulator is an independent testing tool and is **not affiliated with, endorsed by, or sponsored by** any of the manufacturers listed above. Product and company names are trademarks of their respective owners and are used only to identify the device behavior being simulated.

## Matter Devices

The simulator emulates **real, commissionable Matter devices** using
[matter.js](https://github.com/matter-js/matter.js): temperature and light sensors,
smart plugs and EVSE chargers with energy metering, thermostats, heat pumps,
dishwashers, and washing machines. Each device gets its own pairing code and QR, can
be paired into Home Assistant / Apple Home / Google Home, reports live measurements
and accumulating energy while its built-in simulation runs, and reacts to controller
commands (toggles, setpoints) with the changes reflected back into the simulation.

Requires **Node.js v18+ on the host** (matter.js itself is embedded — no npm needed);
without Node, Matter is simply disabled and everything else works. Devices use the CSA
*test* Vendor ID, so enable Home Assistant's **Test-Net DCL** option to pair.

**→ Full guide: [docs/MATTER.md](docs/MATTER.md)** — device types, control API,
built-in behavior, pairing, and architecture.

## API Endpoints

All simulators are accessible through a unified API:

```
GET  /api/simulators                        # List all simulators
POST /api/simulators                        # Create simulator (Modbus/MQTT/OCPP/Matter types)
GET  /api/simulators/{id}                   # Get simulator details
POST /api/simulators/{id}/config            # Update serial/slave ID
POST /api/simulators/{id}/state             # Control device state {key, value} (see docs/MATTER.md)
GET  /api/matter/status                     # Matter availability (Node.js detection)
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
| MATTER_BASE_PORT | 5540 | First Matter (UDP) operational port |
| MATTER_NODE_PATH | auto-detected | Path to the `node` binary for Matter |
| MATTER_STORAGE_ROOT | OS config dir | Matter fabric/credential storage root |
| MATTER_LOG_LEVEL | notice | Matter bridge log level |
| HTTP_PORT | 80 | Web dashboard port |

## Testing Modbus Connections

```bash
# Using modpoll (or any Modbus client)
modpoll -m tcp -a 1 -r 5000 -c 10 localhost:5000

# Read holding registers from simulator 3
modpoll -m tcp -a 1 -r 5000 -c 10 localhost:5002
```

## Development

### Using Docker (Recommended for Development)

```bash
# Start development server
make up
# or
docker compose up --build

# View logs
make logs

# Stop
make down
```

Access the dashboard at http://localhost

### Building Desktop App Locally

**macOS:**
```bash
make build-macos
make run-macos  # Build and launch
```

**Linux (via Docker):**
```bash
make build-linux
```

**All Platforms:**
```bash
# Run tests
go test ./...

# Build server-only binary (no GUI)
make build-linux-server
make build-windows-server
```

### Makefile Targets

```bash
make help  # Show all available targets
```

| Target | Description |
|--------|-------------|
| `make up` | Start Docker development server |
| `make build-macos` | Build macOS desktop app |
| `make build-linux` | Build Linux desktop app (via Docker) |
| `make run-macos` | Build and run macOS app |
| `make test` | Run Go tests |
| `make clean` | Remove build artifacts |

## Architecture

```
device-simulator (single process)
├── Wails Desktop Window (desktop mode) or HTTP-only (Docker mode)
├── HTTP API Server (:80 Docker, :8762 Desktop)
├── Modbus TCP Servers (:5000+)
├── MQTT Brokers (:1883+)
├── OCPP WebSocket Servers (:9000+)
└── Matter Bridge (supervised Node.js child, matter.js, UDP :5540+)
```

**Dual-Mode Operation:**
- **Desktop Mode** (`--desktop`): Native window via Wails + HTTP API
- **Server Mode** (default): HTTP-only, ideal for Docker/headless

The simulator is a single Go application that runs all protocol servers in one process. Much more efficient than running separate containers - and unlike the Heart of Gold, it doesn't require an Infinite Improbability Drive.

## CI/CD

Every push to `main` triggers a GitHub Actions workflow that:
1. Builds desktop apps for macOS, Linux, and Windows
2. Creates a GitHub Release with semantic versioning (v0.1.X)
3. Uploads all platform binaries as release assets

The desktop app checks for updates on startup and can self-update.

## Why "Mostly Harmless"?

Because that's what the Hitchhiker's Guide to the Galaxy says about Earth. And if this simulator can make testing energy systems a little less painful, maybe we can upgrade our entry to "Largely Harmless."

---

*Remember: DON'T PANIC. And always know where your towel is.*
