# AGENTS.md

## Cursor Cloud specific instructions

This is a single Go application (a multi-protocol device simulator). Standard build,
test, and run commands live in `README.md`, `CLAUDE.md`, and the `Makefile`; the notes
below only cover Cloud-specific caveats.

### Services & how to run them (dev)

There is only one service: the simulator process. Two modes exist, but only **server
mode** is usable in the cloud VM (desktop mode needs Wails + CGO + GTK/WebKit2GTK and a
display, which are not installed here).

- Run server mode directly (no CGO, no build tags — same as `Dockerfile`):
  - `CGO_ENABLED=0 go build -o /tmp/device-simulator ./cmd/simulator/`
  - `DEVICE_SIMULATOR_DATA_DIR=/tmp/dsim-data /tmp/device-simulator`
- The HTTP dashboard/API defaults to **port 8762** in server mode (the `--http` flag
  default), *not* port 80. Port 80 in the README/CLAUDE refers to the Docker container's
  `HTTP_PORT=80`. Do not bind port 80 in the VM (requires root); use the 8762 default or
  set `HTTP_PORT`.
- Protocol servers bind lazily when simulators are created: Modbus TCP from `5000+`,
  MQTT from `1883+`, OCPP WebSocket from `9000+`.
- Set `DEVICE_SIMULATOR_DATA_DIR` to a writable path so persisted state doesn't try to
  write to a read-only/default location.

### Lint / test / build

- Lint: `go vet ./...` (CONTRIBUTING also expects `gofmt`).
- Test: `go test ./...` (CI runs this and it must pass before releases).
- Build (server): see the command above. `make build-linux-server` produces the same
  CGO-disabled binary under `build/`.

### Quick end-to-end smoke test (no external deps)

With the server running on 8762:

- `curl -s http://localhost:8762/api/version`
- Create a device: `curl -s -X POST http://localhost:8762/api/simulators -H 'Content-Type: application/json' -d '{"type":"inverter"}'`
- Start data generation: `curl -s -X POST http://localhost:8762/api/simulators/1/automation/start`
- The device then serves live Modbus TCP on port `5000`. Battery SoC is exposed as an
  **input register at address 13022** (value is scaled ×10, e.g. `780` = 78.0%). A raw
  Modbus FC4 read of `13022` is a good protocol-level check.

### Docker

`docker compose up --build` also works (server mode on port 80, Modbus mapped to
5100-5104, MQTT to 11883-11885). Running the Go binary directly is faster for iteration
and avoids Docker-in-Docker setup.
