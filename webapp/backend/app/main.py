import logging
import os
from dataclasses import dataclass
from enum import Enum
from typing import List, Optional

from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from fastapi.staticfiles import StaticFiles
from pymodbus.client.sync import ModbusTcpClient
from pydantic import BaseModel

from .defaults import DEFAULTS


class DataType(str, Enum):
    U16 = "U16"
    I16 = "I16"
    U32 = "U32"
    I32 = "I32"


class Endianness(str, Enum):
    BIG = "BIG"
    LITTLE = "LITTLE"


@dataclass(frozen=True)
class RegisterDefinition:
    address: int
    name: str
    description: str
    unit: str
    data_type: DataType
    scale: float
    words: int
    endianness: Endianness
    use_holding: bool = False


# Register mirroring configuration: source_register -> [target_register1, target_register2, ...]
# When source is written, its value is mirrored to all target registers
REGISTER_MIRRORS = {
    13073: [5016],  # PV Command Power -> Total DC Power
    13051: [13021],  # Battery Setpoint -> Battery Power (absolute value)
}

REGISTER_DEFINITIONS: List[RegisterDefinition] = [
    # Power Flow Essential Registers
    RegisterDefinition(5016, "PV Power", "Total PV power", "W", DataType.U32, 1.0, 2, Endianness.LITTLE),
    RegisterDefinition(13021, "Battery Power", "Battery power magnitude", "W", DataType.U16, 1.0, 1, Endianness.BIG),
    RegisterDefinition(13022, "Battery SoC", "Battery state of charge", "%", DataType.U16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(5600, "Meter Power", "Meter active power (+ = import, - = export)", "W", DataType.I32, 1.0, 2, Endianness.LITTLE),
    RegisterDefinition(13052, "Load Power", "Load power", "W", DataType.I16, 1.0, 1, Endianness.BIG, use_holding=True),
    
    # Energy Totals
    RegisterDefinition(13036, "Total Import Energy", "Total import energy", "Wh", DataType.U32, 1.0, 2, Endianness.LITTLE),
    RegisterDefinition(13040, "Total Charge Energy", "Total charge energy", "Wh", DataType.U32, 1.0, 2, Endianness.LITTLE),
    RegisterDefinition(13045, "Total Export Energy", "Total export energy", "Wh", DataType.U32, 1.0, 2, Endianness.LITTLE),
    RegisterDefinition(13002, "Total PV Generation", "Total PV generation", "Wh", DataType.U32, 1.0, 2, Endianness.LITTLE),
    RegisterDefinition(13026, "Total Battery Discharge", "Total battery discharge energy", "Wh", DataType.U32, 1.0, 2, Endianness.LITTLE),
    
    # Control Registers
    RegisterDefinition(13000, "Battery Flags", "Bit1: charging, Bit2: discharging", "", DataType.U16, 1.0, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(13049, "Grid Mode", "0: Self-consumption; 2: Forced; 3: External EMS", "", DataType.U16, 1.0, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(13050, "Charge/Discharge Cmd", "170=charge; 187=discharge; 204=stop", "", DataType.U16, 1.0, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(13051, "Battery Setpoint", "Battery power setpoint", "W", DataType.U16, 1.0, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(13073, "PV Command", "PV power limit", "W", DataType.U16, 1.0, 2, Endianness.BIG, use_holding=True),
    RegisterDefinition(13086, "Export Limit Enable", "170=enabled; 85=disabled", "", DataType.U16, 1.0, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(33046, "Max Charge Power", "Max charge power", "kW", DataType.U16, 0.01, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(33047, "Max Discharge Power", "Max discharge power", "kW", DataType.U16, 0.01, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(5241, "Grid Frequency HP", "Grid Frequency - High precision", "Hz", DataType.U16, 0.01, 1, Endianness.BIG, use_holding=True),
]


REGISTER_LOOKUP = {definition.address: definition for definition in REGISTER_DEFINITIONS}


MODBUS_HOST = os.getenv("MODBUS_HOST", "modbus-server")
MODBUS_PORT = int(os.getenv("MODBUS_PORT", "502"))
MODBUS_UNIT_ID = int(os.getenv("MODBUS_UNIT_ID", "1"))

SOLAR_RADIANCE_VALUE: Optional[float] = None


app = FastAPI()

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"]
)


class WriteRegisterRequest(BaseModel):
    value: float


def _decode_value(registers: List[int], definition: RegisterDefinition) -> float:
    if not registers:
        raise ValueError("No registers returned")

    if definition.data_type in {DataType.U16, DataType.I16}:
        raw_value = registers[0]
        if definition.data_type == DataType.I16 and raw_value >= 0x8000:
            raw_value -= 0x10000
    else:
        if len(registers) < 2:
            raise ValueError("Expected two registers for 32-bit value")
        if definition.endianness == Endianness.BIG:
            raw_value = (registers[0] << 16) | registers[1]
        else:
            raw_value = (registers[1] << 16) | registers[0]
        if definition.data_type == DataType.I32 and raw_value >= 0x80000000:
            raw_value -= 0x100000000

    return float(raw_value) * definition.scale if definition.scale else float(raw_value)


def _encode_value(value: float, definition: RegisterDefinition) -> List[int]:
    if definition.scale:
        scaled_value = value / definition.scale
    else:
        scaled_value = value

    raw = int(round(scaled_value))

    def _words(word_value: int) -> List[int]:
        high = (word_value >> 16) & 0xFFFF
        low = word_value & 0xFFFF
        if definition.endianness == Endianness.BIG:
            return [high, low]
        return [low, high]

    if definition.data_type == DataType.U16:
        if 0 <= raw <= 0xFFFF:
            return [raw]
        # If value is slightly out of range due to floating point math, clamp it
        if raw > 0xFFFF:
             return [0xFFFF]
        if raw < 0:
             return [0]
        raise ValueError(f"Value {raw} out of range for U16")

    if definition.data_type == DataType.I16:
        if -0x8000 <= raw <= 0x7FFF:
            return [raw & 0xFFFF]
        raise ValueError("Value out of range for I16")

    if definition.data_type == DataType.U32:
        if 0 <= raw <= 0xFFFFFFFF:
            return _words(raw)
        raise ValueError("Value out of range for U32")

    if definition.data_type == DataType.I32:
        if -0x80000000 <= raw <= 0x7FFFFFFF:
            return _words(raw & 0xFFFFFFFF)
        raise ValueError("Value out of range for I32")

    raise ValueError("Unsupported data type")


def _format_payload(definition: RegisterDefinition, registers: List[int]) -> dict:
    value = _decode_value(registers, definition)
    return {
        "address": definition.address,
        "name": definition.name,
        "description": definition.description,
        "unit": definition.unit,
        "value": value,
        "scale": definition.scale,
        "raw": registers,
        "data_type": definition.data_type.value,
        "endianness": definition.endianness.value,
        "words": definition.words,
    }


def _read_definition_registers(
    client: ModbusTcpClient,
    definition: RegisterDefinition
) -> Optional[List[int]]:
    if definition.use_holding:
        response = client.read_holding_registers(
            definition.address,
            definition.words,
            unit=MODBUS_UNIT_ID,
        )
    else:
        response = client.read_input_registers(
            definition.address,
            definition.words,
            unit=MODBUS_UNIT_ID,
        )

    if hasattr(response, "isError") and response.isError():
        return None

    registers = getattr(response, "registers", None)
    if not registers:
        return None
    return registers


def _read_register_value(client: ModbusTcpClient, address: int) -> Optional[float]:
    definition = REGISTER_LOOKUP.get(address)
    if definition is None:
        return None

    registers = _read_definition_registers(client, definition)
    if not registers:
        return None
    try:
        return _decode_value(registers, definition)
    except Exception:
        logging.exception("Failed to decode register value for address %s", address)
        return None


def _apply_default_registers(client: ModbusTcpClient) -> None:
    global SOLAR_RADIANCE_VALUE
    for definition in REGISTER_DEFINITIONS:
        default = DEFAULTS.get(definition.address, 0)
        registers = _encode_value(default, definition)
        response = client.write_registers(definition.address, registers, unit=MODBUS_UNIT_ID)
        if hasattr(response, "isError") and response.isError():
            raise RuntimeError(f"Error writing register {definition.address}")

        verified_registers = _read_definition_registers(client, definition)
        if not verified_registers:
            raise RuntimeError(f"No data when verifying register {definition.address}")

    SOLAR_RADIANCE_VALUE = 0.0
    try:
        _recompute_meter_and_battery(client)
    except Exception as exc:
        logging.warning("Failed to recompute derived registers after applying defaults: %s", exc)


@app.get("/api/registers")
def read_registers():
    client = ModbusTcpClient(MODBUS_HOST, port=MODBUS_PORT)

    if not client.connect():
        raise HTTPException(status_code=503, detail="Unable to reach Modbus server")

    try:
        try:
            _recompute_meter_and_battery(client)
        except Exception:
            logging.exception("Failed to recompute meter during register read")

        payload = []
        for definition in REGISTER_DEFINITIONS:
            registers = _read_definition_registers(client, definition)
            if not registers:
                raise HTTPException(status_code=502, detail=f"Error reading register {definition.address}")
            entry = _format_payload(definition, registers)
            payload.append(entry)
        return payload
    finally:
        client.close()


def _apply_register_mirrors(client: ModbusTcpClient, source_address: int, value: float):
    """Mirror a register value to its target registers based on REGISTER_MIRRORS mapping."""
    if source_address not in REGISTER_MIRRORS:
        return
    
    for target_address in REGISTER_MIRRORS[source_address]:
        target_def = REGISTER_LOOKUP.get(target_address)
        if target_def:
            try:
                # Special handling: 13051 -> 13021 mirrors absolute value only
                if source_address == 13051 and target_address == 13021:
                    mirror_value = abs(value)
                else:
                    mirror_value = value
                    
                target_regs = _encode_value(mirror_value, target_def)
                client.write_registers(target_address, target_regs, unit=MODBUS_UNIT_ID)
            except Exception:
                logging.exception(f"Failed to mirror {source_address} -> {target_address}")


def _recompute_meter_and_battery(client: ModbusTcpClient):
    """
    Energy conservation at PCC: ΔE(PCC) = LOAD + PV + BATT
    
    Sign convention:
    - Positive (+): energy INTO site (import, consumption, charging)
    - Negative (-): energy OUT of site (export, generation, discharging)
    """
    global SOLAR_RADIANCE_VALUE

    def _safe_decode(address: int) -> float:
        value = _read_register_value(client, address)
        return value if value is not None else 0.0

    # Apply PV limit (min of command and solar radiance)
    pv_command = _read_register_value(client, 13073)
    pv_power = 0.0
    
    if pv_command is not None and SOLAR_RADIANCE_VALUE is not None:
        pv_power = min(pv_command, SOLAR_RADIANCE_VALUE)
    elif pv_command is not None:
        pv_power = pv_command
    elif SOLAR_RADIANCE_VALUE is not None:
        pv_power = SOLAR_RADIANCE_VALUE
    
    # Mirror to PV register
    _apply_register_mirrors(client, 13073, pv_power)

    # Read battery setpoint and command
    battery_setpoint_abs = _safe_decode(13051)
    command_value = int(_safe_decode(13050))

    # Determine battery sign and flags from command
    battery_setpoint = 0
    desired_flags = 0
    
    if command_value == 170:  # Charge
        battery_setpoint = battery_setpoint_abs
        desired_flags = 2
    elif command_value == 187:  # Discharge
        battery_setpoint = -battery_setpoint_abs
        desired_flags = 4

    # Update battery flags
    flags_def = REGISTER_LOOKUP.get(13000)
    if flags_def:
        try:
            flags_regs = _encode_value(desired_flags, flags_def)
            client.write_registers(13000, flags_regs, unit=MODBUS_UNIT_ID)
        except Exception:
            logging.exception("Failed to update battery flags")
    
    # Mirror battery setpoint to battery power
    _apply_register_mirrors(client, 13051, battery_setpoint_abs)

    # Energy conservation: ΔE(PCC) = LOAD + PV + BATT
    load_power = _safe_decode(13052)
    meter_power = load_power + (-pv_power) + battery_setpoint

    # Write to meter register
    meter_def = REGISTER_LOOKUP.get(5600)
    if meter_def:
        try:
            meter_regs = _encode_value(meter_power, meter_def)
            client.write_registers(5600, meter_regs, unit=MODBUS_UNIT_ID)
        except Exception:
            logging.exception("Failed to update meter power")


@app.post("/api/registers/{address}")
def write_register(address: int, request: WriteRegisterRequest):
    definition = REGISTER_LOOKUP.get(address)
    if definition is None:
        raise HTTPException(status_code=404, detail=f"Register {address} is not defined")

    client = ModbusTcpClient(MODBUS_HOST, port=MODBUS_PORT)

    if not client.connect():
        raise HTTPException(status_code=503, detail="Unable to reach Modbus server")

    try:
        # Encode and write the value
        try:
            registers = _encode_value(request.value, definition)
        except ValueError as exc:
            raise HTTPException(status_code=422, detail=str(exc)) from exc

        response = client.write_registers(address, registers, unit=MODBUS_UNIT_ID)
        if hasattr(response, "isError") and response.isError():
            raise HTTPException(status_code=502, detail=f"Error writing register {address}")

        verified_registers = _read_definition_registers(client, definition)
        if not verified_registers:
            raise HTTPException(status_code=502, detail=f"Error verifying register {address}")

        # Recompute derived registers
        try:
            _recompute_meter_and_battery(client)
        except Exception:
            logging.exception("Failed to recompute after write")

        return _format_payload(definition, verified_registers)
    finally:
        client.close()


@app.post("/api/reset")
def reset_registers():
    client = ModbusTcpClient(MODBUS_HOST, port=MODBUS_PORT)

    if not client.connect():
        raise HTTPException(status_code=503, detail="Unable to reach Modbus server")

    try:
        try:
            _apply_default_registers(client)
        except RuntimeError as exc:
            raise HTTPException(status_code=502, detail=str(exc)) from exc
        return {"status": "reset"}
    finally:
        client.close()


@app.post("/api/solar-radiance")
def set_solar_radiance(request: WriteRegisterRequest):
    """Store solar radiance limit and recompute PV register."""
    global SOLAR_RADIANCE_VALUE
    SOLAR_RADIANCE_VALUE = float(request.value)

    client = ModbusTcpClient(MODBUS_HOST, port=MODBUS_PORT)
    if not client.connect():
        raise HTTPException(status_code=503, detail="Unable to reach Modbus server")

    try:
        try:
            _recompute_meter_and_battery(client)
        except Exception:
            logging.exception("Failed to recompute after setting solar radiance")
        return {"status": "ok", "value": SOLAR_RADIANCE_VALUE}
    finally:
        client.close()


@app.post("/api/load")
def set_load(request: WriteRegisterRequest):
    """Set load power and recompute meter."""
    client = ModbusTcpClient(MODBUS_HOST, port=MODBUS_PORT)

    if not client.connect():
        raise HTTPException(status_code=503, detail="Unable to reach Modbus server")

    try:
        load_def = REGISTER_LOOKUP.get(13052)
        if load_def is None:
            raise HTTPException(status_code=404, detail="Load register not defined")
        try:
            regs = _encode_value(request.value, load_def)
        except ValueError as exc:
            raise HTTPException(status_code=422, detail=str(exc)) from exc

        resp = client.write_registers(13052, regs, unit=MODBUS_UNIT_ID)
        if hasattr(resp, "isError") and resp.isError():
            raise HTTPException(status_code=502, detail="Error writing load register")

        try:
            _recompute_meter_and_battery(client)
        except Exception:
            logging.exception("Failed to recompute after setting load")

        return {"status": "ok", "load": request.value}
    finally:
        client.close()


STATIC_DIR = os.getenv("STATIC_DIR", "static")
if os.path.isdir(STATIC_DIR):
    app.mount("/", StaticFiles(directory=STATIC_DIR, html=True), name="static")


@app.on_event("startup")
def apply_defaults_on_startup():
    client = ModbusTcpClient(MODBUS_HOST, port=MODBUS_PORT)

    if not client.connect():
        logging.warning("Unable to reach Modbus server during startup default application")
        return

    try:
        try:
            _apply_default_registers(client)
        except RuntimeError as exc:
            logging.warning("Failed to apply default registers on startup: %s", exc)
    finally:
        client.close()


@app.get("/health")
def health_check():
    return {"status": "ok"}
