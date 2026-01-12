import { Fragment, useEffect, useMemo, useState } from 'react';

const REFRESH_INTERVAL_MS = 1000;

// Essential registers for power flow dashboard
const ESSENTIAL_REGISTERS = new Set([
  5016,   // PV Power
  13021,  // Battery Power
  13022,  // Battery SoC
  5600,   // Meter Power
  13052,  // Load Power
  13049,  // Grid Mode
  13050,  // Charge/Discharge Command
  13051,  // Battery Setpoint
  13073,  // PV Command
  13086,  // Export Limit Enable
  33046,  // Max Charge Power
  33047,  // Max Discharge Power
  5241,   // Grid Frequency HP
]);

const REGISTER_TITLES = {
  5016: 'PV Power',
  13021: 'Battery Power',
  13022: 'Battery SoC',
  5600: 'Meter Power',
  13052: 'Load Power',
  13049: 'Grid Mode',
  13050: 'Charge/Discharge Cmd',
  13051: 'Battery Setpoint',
  13073: 'PV Command',
  13086: 'Export Limit Enable',
  33046: 'Max Charge Power',
  33047: 'Max Discharge Power',
  5241: 'Grid Frequency HP (5241)',
};

// Layout is driven directly in the component (no grouped configs)

const formatValue = (value, unit, address) => {
  if (typeof value !== 'number' || Number.isNaN(value)) {
    return '--';
  }
  
  if (address === 13050) {
    const commands = { 170: 'Charge', 187: 'Discharge', 204: 'Stop' };
    return commands[value] || `${value}`;
  }
  
  if (address === 13049) {
    const modes = { 0: 'Self-consumption', 2: 'Forced', 3: 'External EMS' };
    return modes[value] || `${value}`;
  }
  
  if (address === 13086) {
    const states = { 170: 'Enabled', 85: 'Disabled' };
    return states[value] || `${value}`;
  }
  
  const formatter = new Intl.NumberFormat(undefined, {
    maximumFractionDigits: Math.abs(value) < 10 ? 2 : 1
  });
  return `${formatter.format(value)} ${unit}`.trim();
};

const sortRegisters = (registers) => {
  return [...registers].sort((a, b) => a.address - b.address);
};

const useRegisters = () => {
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [timestamp, setTimestamp] = useState(null);

  useEffect(() => {
    let isMounted = true;
    let timerId;

    const load = async () => {
      try {
        const response = await fetch('/api/registers');
        if (!response.ok) {
          throw new Error(`Request failed with status ${response.status}`);
        }
        const payload = await response.json();
        if (isMounted) {
          setData(sortRegisters(payload));
          setTimestamp(new Date());
          setError(null);
          setLoading(false);
        }
      } catch (err) {
        if (isMounted) {
          setError(err.message || 'Unable to fetch data');
          setLoading(false);
        }
      } finally {
        if (isMounted) {
          timerId = window.setTimeout(load, REFRESH_INTERVAL_MS);
        }
      }
    };

    load();

    return () => {
      isMounted = false;
      if (timerId) {
        window.clearTimeout(timerId);
      }
    };
  }, []);

  return { data, setData, loading, error, timestamp, setTimestamp };
};

const RegisterCard = ({ item, onWrite }) => {
  const [inputValue, setInputValue] = useState(item.value ?? '');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const displayName = REGISTER_TITLES[item.address] ?? item.name;

  useEffect(() => {
    setInputValue(item.value ?? '');
  }, [item.value]);

  const handleSubmit = async (event) => {
    event.preventDefault();
    const parsed = Number(inputValue);
    if (Number.isNaN(parsed)) {
      setError('Enter a numeric value');
      return;
    }

    setPending(true);
    setError('');
    try {
      await onWrite(item.address, parsed);
    } catch (err) {
      setError(err.message || 'Write failed');
    } finally {
      setPending(false);
    }
  };

  return (
    <div className="register-card">
      <div className="register-title">
        <span className="name">{displayName}</span>
        <span className="value">{formatValue(item.value, item.unit, item.address)}</span>
      </div>
      <div className="register-description">{item.description}</div>
      <div className="register-meta">
        <span>Address {item.address}</span>
      </div>
      <form className="register-actions" onSubmit={handleSubmit}>
        <input
          type="number"
          step="any"
          value={inputValue}
          onChange={(event) => setInputValue(event.target.value)}
          disabled={pending}
        />
        <button type="submit" disabled={pending}>Set</button>
        {error && <div className="form-error">{error}</div>}
      </form>
    </div>
  );
};

const SolarRadianceCard = ({ value, inputValue, onInputChange, onSubmit, pending, error }) => (
  <div className="register-card">
    <div className="register-title">
      <span className="name">Solar Radiance</span>
      <span className="value">{Number.isFinite(value) ? `${value} W` : '--'}</span>
    </div>
    <div className="register-description">Solar irradiance power limit</div>
    <div className="register-meta">
      <span>Synthetic Input</span>
    </div>
    <form className="register-actions" onSubmit={onSubmit}>
      <input
        type="number"
        step="any"
        value={inputValue}
        onChange={(event) => onInputChange(event.target.value)}
        disabled={pending}
      />
      <button type="submit" disabled={pending}>Set</button>
      {error && <div className="form-error">{error}</div>}
    </form>
  </div>
);

const LoadCard = ({ value, inputValue, onInputChange, onSubmit, pending, error }) => (
  <div className="register-card">
    <div className="register-title">
      <span className="name">Load</span>
      <span className="value">{Number.isFinite(value) ? `${Math.round(value)} W` : '--'}</span>
    </div>
    <div className="register-description">Site load power (positive)</div>
    <div className="register-meta">
      <span>Register 13052</span>
    </div>
    <form className="register-actions" onSubmit={onSubmit}>
      <input
        type="number"
        step="any"
        value={inputValue}
        onChange={(event) => onInputChange(event.target.value)}
        disabled={pending}
      />
      <button type="submit" disabled={pending}>Set</button>
      {error && <div className="form-error">{error}</div>}
    </form>
  </div>
);

const App = () => {
  const { data, setData, loading, error, timestamp, setTimestamp } = useRegisters();
  const [resetting, setResetting] = useState(false);
  const [resetError, setResetError] = useState('');
  const [solarRadiance, setSolarRadiance] = useState(0);
  const [solarRadianceInput, setSolarRadianceInput] = useState('');
  const [solarRadianceError, setSolarRadianceError] = useState('');
  const [solarRadiancePending, setSolarRadiancePending] = useState(false);
  const [loadInput, setLoadInput] = useState('');
  const [loadError, setLoadError] = useState('');
  const [loadPending, setLoadPending] = useState(false);

  const handleSolarRadianceSubmit = async (event) => {
    event.preventDefault();
    const parsed = Number(solarRadianceInput);
    if (Number.isNaN(parsed)) {
      setSolarRadianceError('Enter a numeric value');
      return;
    }

    setSolarRadiancePending(true);
    setSolarRadianceError('');
    try {
      const response = await fetch('/api/solar-radiance', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ value: parsed })
      });

      if (!response.ok) {
        const payload = await response.json().catch(() => ({}));
        throw new Error(payload?.detail || `Set solar radiance failed (${response.status})`);
      }

      const result = await response.json();
      const storedValue = Number(result?.value ?? parsed);
      setSolarRadiance(storedValue);
      setSolarRadianceInput('');

      const registersResponse = await fetch('/api/registers');
      if (!registersResponse.ok) {
        throw new Error(`Failed to reload registers (${registersResponse.status})`);
      }
      const payload = await registersResponse.json();
      setData(sortRegisters(payload));
      setTimestamp(new Date());
    } catch (err) {
      setSolarRadianceError(err.message || 'Setting solar radiance failed');
    } finally {
      setSolarRadiancePending(false);
    }
  };

  const handleWrite = async (address, value) => {
    const response = await fetch(`/api/registers/${address}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ value })
    });

    if (!response.ok) {
      const payload = await response.json().catch(() => ({}));
      const message = payload?.detail || `Write failed with status ${response.status}`;
      throw new Error(message);
    }

    const registersResponse = await fetch('/api/registers');
    if (!registersResponse.ok) {
      throw new Error(`Failed to reload registers (${registersResponse.status})`);
    }
    const payload = await registersResponse.json();
    setData(sortRegisters(payload));
    setTimestamp(new Date());
    return payload;
  };
  const cardItems = useMemo(() => data.filter((item) => ESSENTIAL_REGISTERS.has(item.address)), [data]);
  const registerByAddress = useMemo(() => new Map(cardItems.map((it) => [it.address, it])), [cardItems]);
  const currentLoad = registerByAddress.get(13052)?.value ?? null;
  const batterySetpointCard = registerByAddress.get(13051) || null;
  const batteryCmdCard = registerByAddress.get(13050) || null;

  const remainingCards = useMemo(
    () => cardItems.filter((it) => ![13052, 13050, 13051].includes(it.address)),
    [cardItems]
  );
  const half = Math.ceil(remainingCards.length / 2);
  const rowOne = remainingCards.slice(0, half);
  const rowTwo = remainingCards.slice(half);

  const computeEquation = () => {
    const lookup = new Map(data.map((d) => [d.address, d.value]));
    
    const pv = -Number(lookup.get(5016) || 0);
    const battAbs = Number(lookup.get(13021) || 0);
    const load = Number(lookup.get(13052) || 0);
    
    const command = Number(lookup.get(13050) || 204);
    let battStatus = 'Idle';
    let batt = 0;
    if (command === 170) {
      battStatus = 'Charging';
      batt = battAbs;
    } else if (command === 187) {
      battStatus = 'Discharging';
      batt = -battAbs;
    }
    
    const total = load + pv + batt;
    
    return { pv, batt, load, total, battStatus };
  };

  const handleReset = async () => {
    setResetting(true);
    setResetError('');
    try {
      const response = await fetch('/api/reset', { method: 'POST' });
      if (!response.ok) {
        const payload = await response.json().catch(() => ({}));
        throw new Error(payload?.detail || `Reset failed with status ${response.status}`);
      }

      const registersResponse = await fetch('/api/registers');
      if (!registersResponse.ok) {
        throw new Error(`Reload failed with status ${registersResponse.status}`);
      }
      const payload = await registersResponse.json();
      setData(sortRegisters(payload));
      setTimestamp(new Date());
    } catch (err) {
      setResetError(err.message || 'Reset failed');
    } finally {
      setResetting(false);
    }
  };

  return (
    <div className="app-shell">
      <header className="app-header">
        <h1>Inverter Dashboard</h1>
        {timestamp && (
          <div className="timestamp">Last updated: {timestamp.toLocaleTimeString()}</div>
        )}
        <button
          type="button"
          className="reset-button"
          onClick={handleReset}
          disabled={resetting}
        >
          {resetting ? 'Resetting…' : 'Reset to Defaults'}
        </button>
      </header>

      {loading && <div className="status">Loading...</div>}
      {error && !loading && <div className="status error">{error}</div>}
      {resetError && <div className="status error">{resetError}</div>}

      {!loading && !error && (
        <>
          {/* Top row: PV Radiance + Load */}
          <section className="card-row top-row">
            <div className="top-row-grid">
              <SolarRadianceCard
                value={solarRadiance}
                inputValue={solarRadianceInput}
                onInputChange={setSolarRadianceInput}
                onSubmit={handleSolarRadianceSubmit}
                pending={solarRadiancePending}
                error={solarRadianceError}
              />
              <LoadCard
                value={currentLoad}
                inputValue={loadInput}
                onInputChange={setLoadInput}
                onSubmit={async (e) => {
                  e.preventDefault();
                  const parsed = Number(loadInput);
                  if (Number.isNaN(parsed)) {
                    setLoadError('Enter a numeric value');
                    return;
                  }
                  setLoadPending(true);
                  setLoadError('');
                  try {
                    const resp = await fetch('/api/load', {
                      method: 'POST',
                      headers: { 'Content-Type': 'application/json' },
                      body: JSON.stringify({ value: parsed })
                    });
                    if (!resp.ok) {
                      const payload = await resp.json().catch(() => ({}));
                      throw new Error(payload?.detail || `Set load failed (${resp.status})`);
                    }
                    setLoadInput('');
                    // Refresh
                    const registersResponse = await fetch('/api/registers');
                    if (!registersResponse.ok) {
                      throw new Error(`Failed to reload registers (${registersResponse.status})`);
                    }
                    const payload = await registersResponse.json();
                    setData(sortRegisters(payload));
                    setTimestamp(new Date());
                  } catch (err) {
                    setLoadError(err.message || 'Setting load failed');
                  } finally {
                    setLoadPending(false);
                  }
                }}
                pending={loadPending}
                error={loadError}
              />
            </div>
          </section>

          {/* Battery controls row: Charge/Discharge Cmd + Battery Setpoint */}
          <section className="card-row mid-row">
            <div className="mid-row-grid">
              {batteryCmdCard && (
                <RegisterCard key={batteryCmdCard.address} item={batteryCmdCard} onWrite={handleWrite} />
              )}
              {batterySetpointCard && (
                <RegisterCard key={batterySetpointCard.address} item={batterySetpointCard} onWrite={handleWrite} />
              )}
            </div>
          </section>

          {/* Power flow equation */}
          <section className="card-row card-row-equation">
            <div className="card-row-grid card-row-grid-equation">
              <div className="equation-card-full">
                {(() => {
                  const eq = computeEquation();
                  const formatNumber = (value) => (Number.isFinite(value) ? `${Math.round(value)} W` : '--');
                  const totalClass = !Number.isFinite(eq.total)
                    ? 'zero'
                    : (eq.total === 0 ? 'zero' : (eq.total > 0 ? 'import' : 'export'));
                  const pccStatus = Number.isFinite(eq.total)
                    ? (eq.total === 0 ? '' : (eq.total > 0 ? 'Importing' : 'Exporting'))
                    : '';
                  return (
                    <div className="equation-card">
                      <div className="equation-heading">
                        <h3>Power Flow Equation</h3>
                        <div className="equation-line">ΔE(PCC) = LOAD + PV + BATT</div>
                      </div>
                      <div className="equation-values">
                        <div className={`equation-value result equation-total-${totalClass}`}>
                          <span className="label">PCC (Meter)</span>
                          <span className="value">{formatNumber(eq.total)}{pccStatus && <span className="battery-status"> ({pccStatus})</span>}</span>
                        </div>
                        <div className="equation-value">
                          <span className="label">Load</span>
                          <span className="value">{formatNumber(eq.load)}</span>
                        </div>
                        <div className="equation-value">
                          <span className="label">PV</span>
                          <span className="value">{formatNumber(eq.pv)}</span>
                        </div>
                        <div className="equation-value">
                          <span className="label">Battery</span>
                          <span className="value">
                            {formatNumber(eq.batt)}
                            {eq.battStatus && <span className="battery-status"> ({eq.battStatus})</span>}
                          </span>
                        </div>
                      </div>
                    </div>
                  );
                })()}
              </div>
            </div>
          </section>

          {/* Remaining cards in two equal rows */}
          <section className="card-row inline-rows">
            <div className="inline-row">
              <div className="inline-row-grid">
                {rowOne.map((item) => (
                  <RegisterCard key={item.address} item={item} onWrite={handleWrite} />)
                )}
              </div>
            </div>
            <div className="inline-row">
              <div className="inline-row-grid">
                {rowTwo.map((item) => (
                  <RegisterCard key={item.address} item={item} onWrite={handleWrite} />)
                )}
              </div>
            </div>
          </section>
        </>
      )}
    </div>
  );
};

export default App;
