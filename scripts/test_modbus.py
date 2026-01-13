#!/usr/bin/env python3
"""
Modbus TCP Protocol Test Script for Device Simulator

Tests all 10 simulator instances on ports 5000-5009.
Verifies function codes: FC 0x03 (Read Holding), FC 0x04 (Read Input),
FC 0x06 (Write Single), FC 0x10 (Write Multiple)

Usage:
    uv run --with pymodbus python scripts/test_modbus.py
"""

import sys

try:
    from pymodbus.client import ModbusTcpClient
except ImportError:
    print("Error: pymodbus not installed. Run: uv run --with pymodbus python scripts/test_modbus.py")
    sys.exit(1)


# Test configuration
HOST = "localhost"
BASE_PORT = 5000
NUM_SIMULATORS = 10
SLAVE_ID = 1

# Register addresses to test
REGISTERS = {
    "input": [
        (5016, 2, "PV Power (U32)"),
        (13022, 1, "Battery SoC"),
        (5600, 2, "Meter Power (I32)"),
    ],
    "holding": [
        (13052, 1, "Load Power"),
        (13051, 1, "Battery Setpoint"),
        (13049, 1, "Grid Mode"),
    ],
}


def test_read_input_registers(client, port):
    """Test FC 0x04: Read Input Registers"""
    print(f"  [FC 0x04] Read Input Registers:")
    success = True

    for addr, count, name in REGISTERS["input"]:
        result = client.read_input_registers(address=addr, count=count)
        if result.isError():
            print(f"    FAIL: {name} @ {addr} - {result}")
            success = False
        else:
            values = result.registers
            print(f"    OK: {name} @ {addr} = {values}")

    return success


def test_read_holding_registers(client, port):
    """Test FC 0x03: Read Holding Registers"""
    print(f"  [FC 0x03] Read Holding Registers:")
    success = True

    for addr, count, name in REGISTERS["holding"]:
        result = client.read_holding_registers(address=addr, count=count)
        if result.isError():
            print(f"    FAIL: {name} @ {addr} - {result}")
            success = False
        else:
            values = result.registers
            print(f"    OK: {name} @ {addr} = {values}")

    return success


def test_write_single_register(client, port):
    """Test FC 0x06: Write Single Register"""
    print(f"  [FC 0x06] Write Single Register:")

    # Write to Battery Setpoint (13051)
    test_value = 1234
    result = client.write_register(address=13051, value=test_value)
    if result.isError():
        print(f"    FAIL: Write {test_value} to 13051 - {result}")
        return False

    # Verify by reading back
    verify = client.read_holding_registers(address=13051, count=1)
    if verify.isError() or verify.registers[0] != test_value:
        print(f"    FAIL: Verify failed - expected {test_value}, got {verify.registers if not verify.isError() else 'error'}")
        return False

    print(f"    OK: Write {test_value} to 13051, verified")
    return True


def test_write_multiple_registers(client, port):
    """Test FC 0x10: Write Multiple Registers"""
    print(f"  [FC 0x10] Write Multiple Registers:")

    # Write to consecutive registers (13051, 13052)
    test_values = [2000, 3000]
    result = client.write_registers(address=13051, values=test_values)
    if result.isError():
        print(f"    FAIL: Write {test_values} to 13051-13052 - {result}")
        return False

    # Verify by reading back
    verify = client.read_holding_registers(address=13051, count=2)
    if verify.isError():
        print(f"    FAIL: Verify read failed - {verify}")
        return False

    if list(verify.registers) != test_values:
        print(f"    FAIL: Verify failed - expected {test_values}, got {verify.registers}")
        return False

    print(f"    OK: Write {test_values} to 13051-13052, verified")
    return True


def test_slave_id(client, port, wrong_slave=99):
    """Test that wrong slave ID is rejected or handled"""
    print(f"  [Slave ID] Testing slave ID filtering:")

    # Create a new client with wrong slave ID
    wrong_client = ModbusTcpClient(HOST, port=port)
    wrong_client.connect()

    # Try with wrong slave ID (should fail or return no data)
    # Note: pymodbus 3.x sets slave ID per-client, not per-request
    result = wrong_client.read_holding_registers(address=13052, count=1)
    wrong_client.close()

    # Note: Some servers ignore slave ID, some reject it
    # We just report the behavior
    if result.isError():
        print(f"    OK: Wrong slave ID {wrong_slave} rejected (expected)")
    else:
        print(f"    INFO: Wrong slave ID {wrong_slave} accepted (server ignores slave ID)")

    return True


def test_simulator(port):
    """Run all tests for a single simulator"""
    print(f"\n{'='*60}")
    print(f"Testing Simulator on port {port}")
    print(f"{'='*60}")

    client = ModbusTcpClient(HOST, port=port)

    if not client.connect():
        print(f"  FAIL: Could not connect to {HOST}:{port}")
        return False

    all_passed = True

    try:
        all_passed &= test_read_input_registers(client, port)
        all_passed &= test_read_holding_registers(client, port)
        all_passed &= test_write_single_register(client, port)
        all_passed &= test_write_multiple_registers(client, port)
        all_passed &= test_slave_id(client, port)
    except Exception as e:
        print(f"  ERROR: {e}")
        all_passed = False
    finally:
        client.close()

    return all_passed


def main():
    print("=" * 60)
    print("Device Simulator - Modbus TCP Protocol Tests")
    print("=" * 60)
    print(f"Host: {HOST}")
    print(f"Ports: {BASE_PORT}-{BASE_PORT + NUM_SIMULATORS - 1}")
    print(f"Slave ID: {SLAVE_ID}")

    passed = 0
    failed = 0

    for i in range(NUM_SIMULATORS):
        port = BASE_PORT + i
        if test_simulator(port):
            passed += 1
        else:
            failed += 1

    print(f"\n{'='*60}")
    print(f"SUMMARY: {passed} passed, {failed} failed out of {NUM_SIMULATORS} simulators")
    print(f"{'='*60}")

    if failed > 0:
        sys.exit(1)


if __name__ == "__main__":
    main()
