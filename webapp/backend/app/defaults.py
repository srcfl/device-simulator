import random


def _rand_wh() -> int:
    """Return a random energy value in the range 120–250 kWh, as Wh.

    Returns the value in Wh (120,000–250,000).
    """
    return random.randint(120_000, 250_000)  # Wh


DEFAULTS = {
    # Device Ratings
    5000: 100,     # Rated Power
    5001: 100,
    5241: 50,      # Grid Frequency High Precision (50.00 Hz)
    
    # Power Flow Registers
    5016: 0,        # PV Power
    13021: 0,       # Battery Power
    13022: 78,      # Battery SoC (%)
    5600: 0,        # Meter Power
    13052: 0,       # Load Power
    
    # Control Registers
    13000: 0,       # Battery Flags (0=idle, 2=charging, 4=discharging)
    13049: 0,       # Grid Mode (0=Self-consumption)
    13050: 204,     # Charge/Discharge Command (204=Stop)
    13051: 0,       # Battery Setpoint
    13073: 10000,   # PV Command Power (10kW limit)
    13086: 170,     # Export Limit Enable (170=Enabled)
    33046: 500,     # Max Charge Power (5kW)
    33047: 500,     # Max Discharge Power (5kW)
    
    # Energy totals (U32 little-endian, scale=0.1 kWh). Values below are random within 120–250 kWh.
    13036: _rand_wh(),  # Total Import Energy
    13040: _rand_wh(),  # Total Charge Energy
    13045: _rand_wh(),  # Total Export Energy
    13002: _rand_wh(),  # Total PV Generation
    13026: _rand_wh(),  # Total Battery Discharge Energy
}
