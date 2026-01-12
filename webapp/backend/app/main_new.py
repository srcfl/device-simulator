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

logging.basicConfig(level=logging.INFO)

# ============================================================================
# Data Types & Encoding/Decoding
# ============================================================================

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
    """Defines a Modbus register with its encoding parameters."""
    address: int
    name: str
    description: str
    unit: str
    data_type: DataType
    scale: float
    words: int
    endianness: Endianness
    use_holding: bool = False


def encode_value(value: float, definition: RegisterDefinition) -> List[int]:
    """Encode a physical value into Modbus register(s)."""
    scaled = int(value / definition.scale)
    
    if definition.data_type == DataType.U16:
        return [scaled & 0xFFFF]
    elif definition.data_type == DataType.I16:
        if scaled < 0:
            scaled = (1 << 16) + scaled
        return [scaled & 0xFFFF]
    elif definition.data_type == DataType.U32:
        high = (scaled >> 16) & 0xFFFF
        low = scaled & 0xFFFF
        return [low, high] if definition.endianness == Endianness.LITTLE else [high, low]
    elif definition.data_type == DataType.I32:
        if scaled < 0:
            scaled = (1 << 32) + scaled
        high = (scaled >> 16) & 0xFFFF
        low = scaled & 0xFFFF
        return [low, high] if definition.endianness == Endianness.LITTLE else [high, low]
    else:
        raise ValueError(f"Unsupported data type: {definition.data_type}")


def decode_value(registers: List[int], definition: RegisterDefinition) -> float:
    """Decode Modbus register(s) into a physical value."""
    if definition.data_type == DataType.U16:
        raw = registers[0]
    elif definition.data_type == DataType.I16:
        raw = registers[0]
        if raw & 0x8000:
            raw -= (1 << 16)
    elif definition.data_type == DataType.U32:
        if definition.endianness == Endianness.LITTLE:
            raw = (registers[1] << 16) | registers[0]
        else:
            raw = (registers[0] << 16) | registers[1]
    elif definition.data_type == DataType.I32:
        if definition.endianness == Endianness.LITTLE:
            raw = (registers[1] << 16) | registers[0]
        else:
            raw = (registers[0] << 16) | registers[1]
        if raw & 0x80000000:
            raw -= (1 << 32)
    else:
        raise ValueError(f"Unsupported data type: {definition.data_type}")
    
    return raw * definition.scale


# ============================================================================
# Register Definitions
# ============================================================================

REGISTER_DEFINITIONS: List[RegisterDefinition] = [
    # PV
    RegisterDefinition(5016, "PV Power", "Total PV power (negative = generation)", "W", DataType.I32, 1.0, 2, Endianness.BIG),
    
    # Meter (PCC)
    RegisterDefinition(5599, "Meter Power", "Meter total active power at PCC (+ = import, - = export)", "W", DataType.I32, 1.0, 2, Endianness.BIG),
    
    # Battery
    RegisterDefinition(13000, "Battery Flags", "Bit 1: charging, Bit 2: discharging", "", DataType.U16, 1.0, 1, Endianness.BIG, use_holding=True),
    RegisterDefinition(13021, "Battery Power", "Battery power (+ = charging, - = discharging)", "W", DataType.I16, 1.0, 1, Endianness.BIG),
    RegisterDefinition(13051, "Battery Setpoint", "Battery charge/discharge setpoint (+ = charge, - = discharge)", "W", DataType.I16, 1.0, 1, Endianness.BIG, use_holding=True),
    
    # Load
    RegisterDefinition(13052, "Load Power", "Load power (always positive = consumption)", "W", DataType.I16, 1.0, 1, Endianness.BIG, use_holding=True),
]

REGISTER_LOOKUP = {definition.address: definition for definition in REGISTER_DEFINITIONS}

# ============================================================================
# Modbus Client
# ============================================================================

MODBUS_HOST = os.getenv("MODBUS_HOST", "modbus-server")
MODBUS_PORT = int(os.getenv("MODBUS_PORT", "502"))
MODBUS_UNIT_ID = int(os.getenv("MODBUS_UNIT_ID", "1"))


def read_registers(client: ModbusTcpClient, definition: RegisterDefinition) -> Optional[List[int]]:
    """Read register(s) from Modbus server."""
    if definition.use_holding:
        response = client.read_holding_registers(definition.address, definition.words, unit=MODBUS_UNIT_ID)
    else:
        response = client.read_input_registers(definition.address, definition.words, unit=MODBUS_UNIT_ID)
    
    if hasattr(response, 'isError') and response.isError():
        return None
    return response.registers if hasattr(response, 'registers') else None


def write_registers(client: ModbusTcpClient, definition: RegisterDefinition, value: float) -> bool:
    """Write value to register(s) on Modbus server."""
    registers = encode_value(value, definition)
    response = client.write_registers(definition.address, registers, unit=MODBUS_UNIT_ID)
    return not (hasattr(response, 'isError') and response.isError())


# ============================================================================
# Energy Conservation Formula
# ============================================================================

def compute_meter_power(pv_power: float, battery_power: float, load_power: float) -> float:
    """
    Energy conservation at the PCC (Point of Common Coupling):
    
    ΔE(PCC) = LOAD + PV + BATT
    
    Sign convention:
    - Positive (+): energy flowing INTO the site (import, consumption, charging)
    - Negative (-): energy flowing OUT of the site (export, generation, discharging)
    
    Examples:
    - PV generating 5000W → PV = -5000 (flowing out)
    - Battery discharging 3000W → BATT = -3000 (flowing out)
    - Battery charging 2000W → BATT = +2000 (flowing in)
    - Load consuming 4000W → LOAD = +4000 (flowing in)
    """
    return load_power + pv_power + battery_power


def update_battery_from_setpoint(client: ModbusTcpClient, setpoint: float) -> None:
    """
    Update battery power register and flags based on setpoint.
    
    Setpoint convention:
    - Positive: charging (energy into battery)
    - Negative: discharging (energy out of battery)
    """
    # Write setpoint to 13051 (holding register)
    setpoint_def = REGISTER_LOOKUP[13051]
    write_registers(client, setpoint_def, setpoint)
    
    # Mirror to battery power register 13021 (input register via holding mirror)
    battery_def = REGISTER_LOOKUP[13021]
    write_registers(client, battery_def, setpoint)
    
    # Update flags register 13000
    flags_def = REGISTER_LOOKUP[13000]
    flags_regs = read_registers(client, flags_def)
    flags = int(decode_value(flags_regs, flags_def)) if flags_regs else 0
    
    # Clear bits 1 and 2
    flags &= ~0x0006
    
    # Set appropriate bit
    if setpoint > 0:
        flags |= 0x0002  # Bit 1: charging
    elif setpoint < 0:
        flags |= 0x0004  # Bit 2: discharging
    
    write_registers(client, flags_def, float(flags))


def update_meter_register(client: ModbusTcpClient) -> None:
    """Recalculate and update meter power register based on energy balance."""
    # Read current values
    pv_def = REGISTER_LOOKUP[5016]
    battery_def = REGISTER_LOOKUP[13021]
    load_def = REGISTER_LOOKUP[13052]
    
    pv_regs = read_registers(client, pv_def)
    battery_regs = read_registers(client, battery_def)
    load_regs = read_registers(client, load_def)
    
    pv_power = decode_value(pv_regs, pv_def) if pv_regs else 0.0
    battery_power = decode_value(battery_regs, battery_def) if battery_regs else 0.0
    load_power = decode_value(load_regs, load_def) if load_regs else 0.0
    
    # Compute meter power using energy conservation
    meter_power = compute_meter_power(pv_power, battery_power, load_power)
    
    # Write to meter register
    meter_def = REGISTER_LOOKUP[5599]
    write_registers(client, meter_def, meter_power)


# ============================================================================
# FastAPI Application
# ============================================================================

app = FastAPI()

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"]
)


class WriteRegisterRequest(BaseModel):
    value: float


@app.get("/api/registers")
def get_registers():
    """Read all defined registers."""
    client = ModbusTcpClient(MODBUS_HOST, port=MODBUS_PORT)
    if not client.connect():
        raise HTTPException(status_code=503, detail="Cannot connect to Modbus server")
    
    try:
        payload = []
        for definition in REGISTER_DEFINITIONS:
            registers = read_registers(client, definition)
            if registers:
                value = decode_value(registers, definition)
                payload.append({
                    "address": definition.address,
                    "name": definition.name,
                    "description": definition.description,
                    "unit": definition.unit,
                    "value": value,
                    "raw": registers,
                    "data_type": definition.data_type.value
                })
        return payload
    finally:
        client.close()


@app.post("/api/registers/{address}")
def write_register(address: int, request: WriteRegisterRequest):
    """Write a value to a register."""
    definition = REGISTER_LOOKUP.get(address)
    if definition is None:
        raise HTTPException(status_code=404, detail=f"Register {address} not defined")
    
    client = ModbusTcpClient(MODBUS_HOST, port=MODBUS_PORT)
    if not client.connect():
        raise HTTPException(status_code=503, detail="Cannot connect to Modbus server")
    
    try:
        # Special handling for battery setpoint
        if address == 13051:
            update_battery_from_setpoint(client, request.value)
            update_meter_register(client)
        # Special handling for load
        elif address == 13052:
            write_registers(client, definition, request.value)
            update_meter_register(client)
        # Special handling for PV
        elif address == 5016:
            write_registers(client, definition, request.value)
            update_meter_register(client)
        else:
            write_registers(client, definition, request.value)
        
        return {"success": True}
    finally:
        client.close()


@app.post("/api/load")
def set_load(request: WriteRegisterRequest):
    """Set load power (convenience endpoint)."""
    return write_register(13052, request)


@app.post("/api/reset")
def reset_to_defaults():
    """Reset all registers to default values."""
    client = ModbusTcpClient(MODBUS_HOST, port=MODBUS_PORT)
    if not client.connect():
        raise HTTPException(status_code=503, detail="Cannot connect to Modbus server")
    
    try:
        for definition in REGISTER_DEFINITIONS:
            default_value = DEFAULTS.get(definition.address, 0)
            write_registers(client, definition, default_value)
            
            if definition.address == 13051:
                update_battery_from_setpoint(client, default_value)
        
        update_meter_register(client)
        return {"success": True}
    finally:
        client.close()


# Mount frontend
app.mount("/", StaticFiles(directory="/app/frontend", html=True), name="static")
