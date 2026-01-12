# Inverter Simulator

Modbus/TCP inverter simulator with web interface for testing solar inverter systems.

## Components

- **Modbus Server**: TCP server on port 502 with configurable register values
- **Web Interface**: React frontend with FastAPI backend on port 8000

## Usage

```bash
docker compose up --build
```

- Modbus server: `localhost:502`  
- Web interface: `http://localhost:8000`

The simulator includes predefined solar inverter registers (power, voltage, current, battery, etc.) with realistic default values.