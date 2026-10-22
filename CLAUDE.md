# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

> Don't Panic. It's just a multi-protocol device simulator.

## Build and Run

### Docker Development (Recommended)

```bash
# Build and start
docker compose up --build
# or
make up

# View logs
make logs
```

**Docker Ports:**
- Web dashboard: http://localhost (port 80)
- Modbus TCP: 5100-5104
- MQTT: 11883-11885 (mapped from 1883-1885)
- OCPP: 9000-9001

### Desktop App (Wails)

```bash
# macOS - build and run
make build-macos
open "build/macos/Device Simulator.app"

# Or manual build
CGO_ENABLED=1 CGO_LDFLAGS="-framework UniformTypeIdentifiers" \
  go build -tags "desktop,production" -ldflags "-s -w -X main.Version=dev" \
  -o build/macos/device-simulator ./cmd/simulator/
./build/macos/device-simulator --desktop

# Linux - build via Docker
make build-linux
```

**Desktop Ports:**
- HTTP API: localhost:8762
- Modbus TCP: 5000+
- MQTT: 1883+
- OCPP: 9000+

### Run Tests

```bash
go test ./...
```

## Architecture

**Single Go application** with dual-mode operation:

```
device-simulator
├── Mode Selection (--desktop flag or WAILS_DESKTOP=1)
│   ├── Desktop Mode: Wails window + HTTP API on :8762
│   └── Server Mode: HTTP-only on :80 (Docker default)
├── HTTP API Server (unified dashboard + REST API)
├── Modbus TCP Servers (:5000+)
├── MQTT Brokers (:1883+)
└── OCPP WebSocket Servers (:9000+)
```

### Dual-Mode Operation

The app supports two modes controlled by the `--desktop` flag:

| Mode | Trigger | UI | Use Case |
|------|---------|-----|----------|
| Desktop | `--desktop` or `WAILS_DESKTOP=1` | Native Wails window | End users |
| Server | Default (no flag) | Browser at HTTP port | Docker, development |

### Go Backend (`cmd/simulator/`, `internal/`)

**Main components:**
- `cmd/simulator/main.go` - Entry point, dual-mode HTTP/Wails server
- `cmd/simulator/app.go` - Wails bindings (~35 methods exposed to frontend)
- `cmd/simulator/updater.go` - Auto-update via GitHub Releases
- `cmd/simulator/templates/index.html` - Embedded dashboard UI
- `cmd/simulator/wails.json` - Wails configuration

**Public package:**
- `fuse/` - IEC 60269 fuse curves and per-phase damage; no dependencies, so an EMS's own simulation (FTW's `go/internal/sitesim`) imports the same model

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

HTTP API (port 80 in Docker, 8762 in desktop mode):

```
GET  /                                    # Dashboard UI
GET  /api/simulators                      # List all simulators
POST /api/simulators                      # Create simulator {type: "inverter"|"energy_meter"|"v2x_charger"|"ocpp_charger"}
DELETE /api/simulators/{id}               # Delete simulator
GET  /api/simulators/{id}                 # Get simulator details
POST /api/simulators/{id}/config          # Update serial/slave ID
GET  /api/simulators/{id}/registers       # Get all registers
POST /api/simulators/{id}/registers/{addr} # Write register value
GET  /api/simulators/{id}/logs            # Get protocol operation logs
POST /api/simulators/{id}/automation/start # Start data generation
POST /api/simulators/{id}/automation/stop  # Stop data generation
POST /api/simulators/{id}/reset           # Reset to defaults
GET  /api/sites                           # List all sites
POST /api/sites                           # Create site
GET  /api/system                          # Get system info (local IP)
GET  /api/version                         # Get app version
GET  /api/update/check                    # Check for updates
POST /api/update/apply                    # Apply update
```

**API Change Policy:** Whenever any API endpoint is added, removed, or modified, update the OpenAPI spec (`cmd/simulator/static/openapi.json`), the MCP tool mapping (`internal/mcp/server.go`), and the in-app docs at `/docs` to keep them in sync.

## Wails Integration

The app uses [Wails v2](https://wails.io/) for the desktop UI. Key concepts:

**Frontend-Backend Communication:**
- In desktop mode: Frontend calls Go methods via `window.go.main.App.MethodName()`
- In server mode: Frontend uses `fetch()` to HTTP API
- `stores.js` has `isWails` detection that chooses the right method automatically

**Wails Bindings (app.go):**
```go
// Methods exposed to frontend via Wails
func (a *App) GetSimulators(filters map[string]string) []map[string]interface{}
func (a *App) CreateSimulator(simType string) (map[string]interface{}, error)
func (a *App) DeleteSimulator(id int) error
func (a *App) CheckForUpdate() (*UpdateInfo, error)
// ... ~35 methods total
```

**Build Tags:**
- `desktop,production` - Required for Wails desktop build
- CGO must be enabled for Wails (links to WebKit/GTK on Linux, WebView on Windows)

**Why Wails over Electron:**
- Single binary (~11MB vs 150MB+ for Electron)
- Native performance (Go + system WebView)
- Same codebase for desktop and server modes

## Key Files

| File | Purpose |
|------|---------|
| `cmd/simulator/main.go` | Entry point, dual-mode (Wails/HTTP) server |
| `cmd/simulator/app.go` | Wails App struct with bound methods for frontend |
| `cmd/simulator/updater.go` | Auto-update logic using GitHub Releases |
| `cmd/simulator/wails.json` | Wails v2 configuration |
| `cmd/simulator/templates/index.html` | Embedded dashboard UI (Alpine.js) |
| `cmd/simulator/static/js/stores.js` | Frontend state management, Wails/HTTP API |
| `internal/modbus/registers.go` | Register definitions with data types, scaling |
| `internal/profiles/loader.go` | Device profiles with embedded defaults |
| `profiles/*.json` | JSON profile files |
| `.github/workflows/build.yml` | CI/CD: builds macOS/Linux/Windows, creates releases |
| `Dockerfile` | Docker image for server mode |
| `Dockerfile.desktop-linux` | Docker build for Linux desktop app |
| `Makefile` | Build targets for Docker and desktop |

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
| MODBUS_BASE_PORT | 5000 | First Modbus port |
| MQTT_BASE_PORT | 1883 | First MQTT port |
| OCPP_BASE_PORT | 9000 | First OCPP port |
| HTTP_PORT | 80 (Docker) / 8762 (Desktop) | Web dashboard port |
| WAILS_DESKTOP | 0 | Set to 1 to force desktop mode |
| HOST_IP | auto-detected | Override displayed host IP |

## CI/CD (GitHub Actions)

The `.github/workflows/build.yml` workflow runs on every push to `main`.

### Triggering Releases

Releases are triggered by **commit message prefixes**:

| Prefix | Version Bump | Example |
|--------|--------------|---------|
| `patch:` | 0.1.X → 0.1.(X+1) | `patch: Fix login bug` |
| `minor:` | 0.X.0 → 0.(X+1).0 | `minor: Add new feature` |
| `major:` | X.0.0 → (X+1).0.0 | `major: Breaking API change` |

Commits without these prefixes **do not** create releases.

**Example commit:**
```bash
git commit -m "patch: Fix WebSocket error handling

Added error logging for failed message sends.
This improves debugging of connection issues."
```

The commit body (after the first line) becomes the release notes under "In this version:".

### Build Process

1. **Tests must pass** - `go test ./...` runs before any builds
2. **Parallel builds** for macOS, Linux, Windows (after tests pass)
3. **Version calculation** from latest git tag + bump type
4. **Version embedding** via ldflags (`-X main.Version=${VERSION}`)
5. **Release creation** with semantic version tag and assets

**Build matrix:**
| Platform | Runner | Dependencies |
|----------|--------|--------------|
| macOS | `macos-latest` | Xcode (for CGO) |
| Linux | `ubuntu-22.04` | GTK3, WebKit2GTK 4.0 |
| Windows | `windows-latest` | MSVC (for CGO) |

## Auto-Update System

The desktop app auto-updates via `go-github-selfupdate`:

1. On startup, calls GitHub Releases API to check for newer version
2. Compares current version (embedded at build time) with latest release tag
3. If update available, shows "Update to vX.X.X" button in UI
4. User clicks → downloads platform-specific asset → replaces binary
5. User restarts to run new version

**Key files:**
- `cmd/simulator/updater.go` - Update check and apply logic
- `stores.js` - Frontend update UI state

**Version is set at build time:**
```bash
go build -ldflags "-X main.Version=0.1.42" ./cmd/simulator/
```

## Testing

Tests must pass before releases are created (enforced by CI).

```bash
# Run all Go tests
go test ./...

# Run with verbose output
go test ./... -v

# Run specific package tests
go test ./internal/modbus/
go test ./internal/mqtt/
go test ./internal/settings/
go test ./internal/ports/
go test ./internal/generator/

# Test Modbus connection
modpoll -m tcp -a 1 -r 5000 -c 10 localhost:5000
```

**Test packages:**
| Package | Tests | Description |
|---------|-------|-------------|
| `internal/generator` | 7 | Data generators (sine, daynight, etc.) |
| `internal/modbus` | 16 | Modbus server and register handling |
| `internal/mqtt` | 8 | MQTT broker start/stop, protocol |
| `internal/ports` | 13 | Port allocation and release |
| `internal/settings` | 10 | Settings persistence |
