from pymodbus.server.sync import StartTcpServer
from pymodbus.datastore import ModbusSequentialDataBlock, ModbusServerContext, ModbusSlaveContext
import logging
import os

from webapp.backend.app.defaults import DEFAULTS

# Disable default logging
logging.getLogger('pymodbus').setLevel(logging.WARNING)
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(message)s',
    datefmt='%Y-%m-%d %H:%M:%S'
)

class ModbusTrackingBlock(ModbusSequentialDataBlock):
    def __init__(self, address, values, input_block=None):
        super().__init__(address, values)
        self.input_block = input_block
    
    def setValues(self, address, values):
        logging.info(f"Write: address={address}, count={len(values)}, values={values}")
        super().setValues(address, values)
        # Mirror writes to input registers if available
        if self.input_block:
            self.input_block.setValues(address, values)
        
    def getValues(self, address, count=1):
        logging.info(f"Read: address={address}, count={count}")
        return super().getValues(address, count)

def _initialize_defaults(block: ModbusTrackingBlock):
    # Special handling for multi-word registers
    u32_registers = {5016}  # PV power (U32 BIG)
    i32_big_registers = {5600}  # Meter power (I32 BIG)
    u32_little_registers = {13036, 13040, 13045, 13002, 13026}  # Energy totals (U32 LITTLE)
    frequency_registers = {5241}  # Frequency registers (scale x100)
    
    # Initialize serial number from environment variable
    serial_number = os.getenv('SERIAL_NUMBER', '1234567890')
    _set_serial_number(block, serial_number)
    
    for address, value in DEFAULTS.items():
        if address in frequency_registers:
            # Scale by 100 (e.g. 50 -> 5000)
            scaled_value = int(value * 100)
            if 0 <= scaled_value <= 65535:
                block.setValues(address, [scaled_value])
            else:
                logging.warning(f"Value {scaled_value} out of range for Frequency at {address}")
        elif address in u32_registers:
            # Encode as U32 BIG endian (two 16-bit words)
            if 0 <= value <= 0xFFFFFFFF:
                high = (value >> 16) & 0xFFFF
                low = value & 0xFFFF
                block.setValues(address, [high, low])
            else:
                logging.warning(f"Value {value} out of range for U32 at {address}")
        elif address in u32_little_registers:
            # Encode as U32 LITTLE endian (two 16-bit words, low first)
            if 0 <= value <= 0xFFFFFFFF:
                low = value & 0xFFFF
                high = (value >> 16) & 0xFFFF
                block.setValues(address, [low, high])  # LITTLE: low word first
            else:
                logging.warning(f"Value {value} out of range for U32 (LITTLE) at {address}")
        elif address in i32_big_registers:
            # Encode as I32 BIG endian (two 16-bit words, high first)
            if -0x80000000 <= value <= 0x7FFFFFFF:
                raw = value & 0xFFFFFFFF
                high = (raw >> 16) & 0xFFFF
                low = raw & 0xFFFF
                block.setValues(address, [high, low])  # BIG: high word first
            else:
                logging.warning(f"Value {value} out of range for I32 at {address}")
        else:
            # Single register value - clamp to U16 range
            if -32768 <= value <= 65535:
                block.setValues(address, [value & 0xFFFF])
            else:
                logging.warning(f"Value {value} out of range for U16 at {address}")

def _set_serial_number(block: ModbusTrackingBlock, serial_number: str):
    """Set serial number starting at register 4990 (10 registers)"""
    # Pad or truncate to 20 characters (10 registers * 2 bytes each)
    padded_sn = serial_number[:20].ljust(20, '\0')
    
    # Convert string to list of 16-bit values (2 bytes per register)
    registers = []
    for i in range(0, len(padded_sn), 2):
        if i + 1 < len(padded_sn):
            # Combine two bytes into one 16-bit register (big endian)
            reg_value = (ord(padded_sn[i]) << 8) | ord(padded_sn[i + 1])
        else:
            # Single byte case
            reg_value = ord(padded_sn[i]) << 8
        registers.append(reg_value)
    
    # Set the 10 registers starting at address 4990
    block.setValues(4990, registers)
    logging.info(f"Serial number '{serial_number}' set at registers 4990-4999")

def run_server():
    # Create blocks starting at address 0 with 60001 registers initialized to 0
    input_block = ModbusTrackingBlock(0, [0] * 60001)
    holding_block = ModbusTrackingBlock(0, [0] * 60001, input_block)

    _initialize_defaults(holding_block)
    _initialize_defaults(input_block)
    
    # Create the context with both holding and input registers
    store = ModbusSlaveContext(hr=holding_block, ir=input_block)
    context = ModbusServerContext(slaves=store, single=True)
    
    # Start server
    logging.info("Starting Modbus Server on port 502...")
    StartTcpServer(context, address=("0.0.0.0", 502))

if __name__ == "__main__":
    run_server()
