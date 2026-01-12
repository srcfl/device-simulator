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


REGISTER_DEFINITIONS: List[RegisterDefinition] = [
    RegisterDefinition(5000, "Nominal Output Power", "Nominal output power of the inverter", "kW", DataType.U16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(5007, "Inside Temperature", "Internal temperature of the inverter", "degC", DataType.I16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(5010, "MPPT 1 Voltage", "Maximum Power Point Tracker 1 Voltage", "V", DataType.U16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(5011, "MPPT 1 Current", "Maximum Power Point Tracker 1 Current", "A", DataType.U16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(5012, "MPPT 1 Power", "Instantaneous MPPT 1 power", "kW", DataType.U16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(5013, "MPPT 2 Current", "Maximum Power Point Tracker 2 Current", "A", DataType.U16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(5016, "Total DC Power", "Total PV (Photovoltaic) Power", "W", DataType.U32, 1.0, 2, Endianness.LITTLE),
    RegisterDefinition(5241, "Grid Frequency", "Grid frequency", "Hz", DataType.U16, 1.0, 1, Endianness.BIG),
    RegisterDefinition(5599, "Meter Total Active Power", "Meter total active power", "W", DataType.I32, 1.0, 2, Endianness.BIG),
    RegisterDefinition(5602, "Meter Phase A Active Power", "Meter phase A active power", "W", DataType.I32, 1.0, 2, Endianness.LITTLE),
    RegisterDefinition(5604, "Meter Phase B Active Power", "Meter phase B active power", "W", DataType.I32, 1.0, 2, Endianness.LITTLE),
    RegisterDefinition(5606, "Meter Phase C Active Power", "Meter phase C active power", "W", DataType.I32, 1.0, 2, Endianness.LITTLE),
    RegisterDefinition(5740, "Daily PV Energy", "Daily PV production", "kWh", DataType.U16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(5741, "Daily Load Energy", "Daily load consumption", "kWh", DataType.U16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(5742, "Daily Grid Energy", "Daily grid energy", "kWh", DataType.U16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(5743, "Alarm Code 1", "Primary alarm code", "", DataType.U16, 1.0, 1, Endianness.BIG),
    RegisterDefinition(5744, "Alarm Code 2", "Secondary alarm code", "", DataType.U16, 1.0, 1, Endianness.BIG),
    RegisterDefinition(5745, "Alarm Code 3", "Tertiary alarm code", "", DataType.U16, 1.0, 1, Endianness.BIG),
    RegisterDefinition(13000, "Running Flags", "Bit flags for charge/discharge direction", "", DataType.U16, 1.0, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(13002, "Total PV Generation", "Total photovoltaic generation", "kWh", DataType.U32, 0.1, 2, Endianness.LITTLE),
    RegisterDefinition(13019, "Battery Voltage", "Battery voltage", "V", DataType.U16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(13020, "Battery Current", "Battery current", "A", DataType.U16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(13021, "Battery Power", "Battery power magnitude", "W", DataType.U16, 1.0, 1, Endianness.BIG),
    RegisterDefinition(13022, "Battery Level", "Battery state of charge", "%", DataType.U16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(13024, "Battery Temperature", "Battery temperature", "degC", DataType.I16, 0.1, 1, Endianness.BIG),
    RegisterDefinition(13026, "Total Battery Discharge Energy", "Total battery discharge energy", "kWh", DataType.U32, 0.1, 2, Endianness.LITTLE),
    RegisterDefinition(13036, "Total Import Energy", "Total import energy", "kWh", DataType.U32, 0.1, 2, Endianness.LITTLE),
    RegisterDefinition(13040, "Total Charge Energy", "Total charge energy", "kWh", DataType.U32, 0.1, 2, Endianness.LITTLE),
    RegisterDefinition(13045, "Total Export Energy", "Total export energy", "kWh", DataType.U32, 0.1, 2, Endianness.LITTLE),
    RegisterDefinition(13049, "Grid Mode", "0: Self-consumption; 2: Forced; 3: External EMS; 4: VPP", "", DataType.U16, 1.0, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(13050, "Charge/Discharge Command", "0xAA charge; 0xBB discharge; 0xCC stop", "", DataType.U16, 1.0, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(13051, "Charge/Discharge Power", "Battery charge or discharge set-point", "W", DataType.I16, 1.0, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(33046, "Max Charge Power", "Maximum charge power", "kW", DataType.U16, 0.01, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(33047, "Max Discharge Power", "Maximum discharge power", "kW", DataType.U16, 0.01, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(13052, "Load", "External load contribution", "W", DataType.I16, 1.0, 1, Endianness.BIG, use_holding=True),
]


REGISTER_LOOKUP = {definition.address: definition for definition in REGISTER_DEFINITIONS}


MODBUS_HOST = os.getenv("MODBUS_HOST", "modbus-server")
MODBUS_PORT = int(os.getenv("MODBUS_PORT", "502"))
MODBUS_UNIT_ID = int(os.getenv("MODBUS_UNIT_ID", "1"))


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
        raise ValueError("Value out of range for U16")

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


def _refresh_payload_entry(
    client: ModbusTcpClient,
    address: int,
    payload: List[dict],
    index: Optional[int]
) -> None:
    if index is None:
        return
    definition = REGISTER_LOOKUP.get(address)
    if definition is None:
        return
    registers = _read_definition_registers(client, definition)
    if not registers:
        return
    payload[index] = _format_payload(definition, registers)


def _apply_direction_from_flags(power_value: float, flags_value: Optional[float]) -> float:
    """Apply direction to magnitude based on charge/discharge flags.
    
    Returns signed power for display: negative=charging, positive=discharging.
    """
    magnitude = abs(power_value) if power_value is not None else 0.0
    if flags_value is None:
        return magnitude
    try:
        flags_int = int(flags_value)
    except (TypeError, ValueError):
        return magnitude

    is_charging = bool(flags_int & 0x0002)      # Bit 1
    is_discharging = bool(flags_int & 0x0004)  # Bit 2

    if is_charging and not is_discharging:
        return -magnitude
    if is_discharging and not is_charging:
        return magnitude
    if is_charging and is_discharging:
        return magnitude
    return 0.0 if magnitude != 0.0 else magnitude


def _write_power_level(client: ModbusTcpClient, power_value: float) -> None:
    power_def = REGISTER_LOOKUP.get(13051)
    if power_def is None:
        return
    try:
        registers = _encode_value(power_value, power_def)
    except ValueError:
        logging.exception("Invalid power value when writing to 13051")
        return

    response = client.write_registers(13051, registers, unit=MODBUS_UNIT_ID)
    if hasattr(response, "isError") and response.isError():
        logging.warning("Failed to update register 13051")


def _mirror_power_level_to_battery(client: ModbusTcpClient, power_value: float) -> None:
    """Write magnitude to 13021 and set charge/discharge bits in 13000."""
    if power_value is None:
        return
    
    # Battery register stores unsigned magnitude
    magnitude = abs(power_value)
    
    batt_def = REGISTER_LOOKUP.get(13021)
    if batt_def is None:
        return
    try:
        batt_regs = _encode_value(magnitude, batt_def)
    except ValueError:
        logging.exception("Invalid magnitude when mirroring power level to battery register")
        return

    response = client.write_registers(13021, batt_regs, unit=MODBUS_UNIT_ID)
    if hasattr(response, "isError") and response.isError():
        logging.warning("Failed to mirror register 13051 into 13021")
        return
    
    # Update direction bits in register 13000
    flags_def = REGISTER_LOOKUP.get(13000)
    if flags_def is None:
        return
    
    # Read current flags
    flags_regs = _read_definition_registers(client, flags_def)
    if not flags_regs:
        flags_int = 0
    else:
        try:
            flags_int = int(_decode_value(flags_regs, flags_def))
        except Exception:
            flags_int = 0
    
    # Clear bits 1 and 2 (charging/discharging)
    flags_int &= ~0x0006
    
    # Set direction: bit1=charging (negative), bit2=discharging (positive)
    if power_value < 0:
        flags_int |= 0x0002  # Bit 1: Charging
    elif power_value > 0:
        flags_int |= 0x0004  # Bit 2: Discharging
    
    try:
        flags_regs = _encode_value(float(flags_int), flags_def)
        client.write_registers(13000, flags_regs, unit=MODBUS_UNIT_ID)
    except Exception:
        logging.exception("Failed to update direction flags in register 13000")


def _synchronize_power_registers(
    client: ModbusTcpClient,
    power_value: Optional[float] = None,
    flags_value: Optional[float] = None
) -> Optional[float]:
    """Synchronize 13051 → 13021 magnitude and update direction flags in 13000."""
    if power_value is None:
        power_value = _read_register_value(client, 13051)
    if power_value is None:
        return None

    # Mirror magnitude to 13021 and update direction bits in 13000
    _mirror_power_level_to_battery(client, power_value)
    
    return power_value


def _apply_default_registers(client: ModbusTcpClient) -> None:
    for definition in REGISTER_DEFINITIONS:
        default = DEFAULTS.get(definition.address, 0)
        registers = _encode_value(default, definition)
        response = client.write_registers(definition.address, registers, unit=MODBUS_UNIT_ID)
        if hasattr(response, "isError") and response.isError():
            raise RuntimeError(f"Error writing register {definition.address}")

        if definition.address == 13051:
            _mirror_power_level_to_battery(client, default)

        verified_registers = _read_definition_registers(client, definition)
        if not verified_registers:
            raise RuntimeError(f"No data when verifying register {definition.address}")

    _synchronize_power_registers(client)


@app.get("/api/registers")
def read_registers():
    client = ModbusTcpClient(MODBUS_HOST, port=MODBUS_PORT)

    if not client.connect():
        raise HTTPException(status_code=503, detail="Unable to reach Modbus server")

    try:
        payload = []
        battery_payload_index: Optional[int] = None
        power_level_value: Optional[float] = None
        for definition in REGISTER_DEFINITIONS:
            registers = _read_definition_registers(client, definition)
            if not registers:
                raise HTTPException(status_code=502, detail=f"Error reading register {definition.address}")
            entry = _format_payload(definition, registers)
            payload.append(entry)
            if definition.address == 13021:
                battery_payload_index = len(payload) - 1
            elif definition.address == 13051:
                power_level_value = entry.get("value")

        if power_level_value is not None:
            try:
                _mirror_power_level_to_battery(client, power_level_value)
                # Refresh battery entry to show magnitude + direction via flags
                batt_def = REGISTER_LOOKUP.get(13021)
                if batt_def is not None:
                    batt_regs = _read_definition_registers(client, batt_def)
                    if batt_regs and battery_payload_index is not None:
                        # Read flags to compute signed display value
                        flags_value = _read_register_value(client, 13000)
                        magnitude = _decode_value(batt_regs, batt_def)
                        signed_power = _apply_direction_from_flags(magnitude, flags_value)
                        
                        # Override the payload entry with signed value for display
                        payload[battery_payload_index] = {
                            **_format_payload(batt_def, batt_regs),
                            "value": signed_power
                        }
            except Exception:
                logging.exception("Failed to mirror power level during register read")
        return payload
    finally:
        client.close()


def _recompute_meter_and_battery(client: ModbusTcpClient):
    """Recalculate meter = LOAD + (−PV) + signed_BATT."""

    def _safe_decode(address: int) -> float:
        value = _read_register_value(client, address)
        return value if value is not None else 0.0

    # Read inputs
    pv_value = _safe_decode(5016)
    load_value = _safe_decode(13052)
    cd_power_value = _safe_decode(13051)

    # Mirror magnitude to 13021 and update direction bits in 13000
    _mirror_power_level_to_battery(client, cd_power_value)

    # For meter equation: charging=positive (consuming), discharging=negative (feeding)
    # This is inverted from display convention where discharging is positive
    battery_for_meter = -cd_power_value

    # Meter = LOAD + (−PV) + battery_contribution
    # When discharging (cd_power positive): battery_for_meter is negative (reduces grid import)
    # When charging (cd_power negative): battery_for_meter is positive (adds to grid import)
    meter_total = load_value + (-pv_value) + battery_for_meter

    try:
        meter_def = REGISTER_LOOKUP.get(5599)
        if meter_def is not None:
            meter_regs = _encode_value(meter_total, meter_def)
            client.write_registers(5599, meter_regs, unit=MODBUS_UNIT_ID)
    except Exception:
        logging.exception("Failed to update meter power from equation")


@app.post("/api/registers/{address}")
def write_register(address: int, request: WriteRegisterRequest):
    definition = REGISTER_LOOKUP.get(address)
    if definition is None:
        raise HTTPException(status_code=404, detail=f"Register {address} is not defined")

    client = ModbusTcpClient(MODBUS_HOST, port=MODBUS_PORT)

    if not client.connect():
        raise HTTPException(status_code=503, detail="Unable to reach Modbus server")

    try:
        try:
            registers = _encode_value(request.value, definition)
        except ValueError as exc:
            raise HTTPException(status_code=422, detail=str(exc)) from exc

        response = client.write_registers(definition.address, registers, unit=MODBUS_UNIT_ID)
        if hasattr(response, "isError") and response.isError():
            raise HTTPException(status_code=502, detail=f"Error writing register {definition.address}")

        verified_registers = _read_definition_registers(client, definition)
        if not verified_registers:
            raise HTTPException(status_code=502, detail=f"Error verifying register {definition.address}")

        # Recompute meter whenever PV, battery power, or load changes
        if address in {5016, 13051, 13052}:
            try:
                _recompute_meter_and_battery(client)
            except Exception:
                logging.exception("Failed to recompute meter after write")

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


class ReadRangeRequest(BaseModel):
    start: int
    end: int


@app.post("/api/read-range")
def read_register_range(request: ReadRangeRequest):
    if request.start < 0 or request.end < request.start or request.end - request.start > 100:
        raise HTTPException(status_code=400, detail="Invalid range (max 100 registers)")
    
    client = ModbusTcpClient(MODBUS_HOST, port=MODBUS_PORT)

    if not client.connect():
        raise HTTPException(status_code=503, detail="Unable to reach Modbus server")

    try:
        count = request.end - request.start + 1
        
        # Read holding registers
        holding_response = client.read_holding_registers(request.start, count, unit=MODBUS_UNIT_ID)
        if hasattr(holding_response, "isError") and holding_response.isError():
            holding_registers = None
        else:
            holding_registers = getattr(holding_response, "registers", None)
        
        # Read input registers
        input_response = client.read_input_registers(request.start, count, unit=MODBUS_UNIT_ID)
        if hasattr(input_response, "isError") and input_response.isError():
            input_registers = None
        else:
            input_registers = getattr(input_response, "registers", None)
        
        return {
            "start": request.start,
            "end": request.end,
            "holding": holding_registers,
            "input": input_registers
        }
    finally:
        client.close()


@app.post("/api/load")
def set_load(request: WriteRegisterRequest):
    """Set external LOAD (register 13052) and recompute meter and battery.

    Body: { "value": <watts> }
    """
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

        # recompute derived values
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
