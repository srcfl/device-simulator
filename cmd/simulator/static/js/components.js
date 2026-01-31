// Alpine Components - Must load before Alpine.js (not a module)

document.addEventListener('alpine:init', () => {
    // Config row component with local state to prevent overwriting during edits
    Alpine.data('configRow', () => ({
        // Local state for editable fields
        localSerial: '',
        localSlaveId: 1,
        localOcppUrl: '',
        localProfile: '',

        // Track if user is currently editing
        editingSerial: false,
        editingSlaveId: false,
        editingOcppUrl: false,
        changingProfile: false,

        // Track last known store ID to detect simulator changes
        lastStoreId: null,

        ocppUrlDebounceTimer: null,

        init() {
            // Initialize local state from store
            this.syncFromStore();

            // Watch for store changes, but only update if not editing
            this.$watch('$store.simulator.data', () => {
                const currentId = Alpine.store('simulator').id;

                // Always sync if simulator changed
                if (this.lastStoreId !== currentId) {
                    this.lastStoreId = currentId;
                    this.changingProfile = false;
                    this.syncFromStore();
                    return;
                }

                // Only sync fields that aren't being edited
                if (!this.editingSerial) {
                    this.localSerial = Alpine.store('simulator').data?.serial || '';
                }
                if (!this.editingSlaveId) {
                    this.localSlaveId = Alpine.store('simulator').data?.slave_id || 1;
                }
                if (!this.editingOcppUrl) {
                    this.localOcppUrl = Alpine.store('simulator').data?.ocpp_url || '';
                }
                // Only sync profile if not in the middle of changing it
                if (!this.changingProfile) {
                    this.localProfile = Alpine.store('simulator').activeProfile || '';
                }
            });
        },

        syncFromStore() {
            const data = Alpine.store('simulator').data;
            this.localSerial = data?.serial || '';
            this.localSlaveId = data?.slave_id || 1;
            this.localOcppUrl = data?.ocpp_url || '';
            this.localProfile = Alpine.store('simulator').activeProfile || '';
        },

        // Serial field handlers
        onSerialFocus() {
            this.editingSerial = true;
        },

        async onSerialBlur() {
            this.editingSerial = false;
            const storeSerial = Alpine.store('simulator').data?.serial || '';
            if (this.localSerial !== storeSerial && this.localSerial.trim()) {
                await Alpine.store('simulator').updateConfig({
                    serial: this.localSerial.trim(),
                    slave_id: this.localSlaveId
                });
            }
        },

        // Meter serial validation (must be numeric U32: 0-4294967295)
        validateMeterSerial(event) {
            let value = event.target.value;
            // Remove non-numeric characters
            value = value.replace(/[^0-9]/g, '');
            // Clamp to U32 max
            if (value && parseInt(value) > 4294967295) {
                value = '4294967295';
            }
            this.localSerial = value;
            event.target.value = value;
        },

        async onMeterSerialBlur() {
            this.editingSerial = false;
            const storeSerial = Alpine.store('simulator').data?.serial || '';
            let serial = this.localSerial.toString().trim();

            // Ensure it's a valid number
            if (serial && !/^\d+$/.test(serial)) {
                // Convert legacy string to numeric using simple hash
                serial = this.stringToMeterSerial(serial);
            }

            // Clamp to U32 range
            if (serial) {
                const num = parseInt(serial);
                if (isNaN(num) || num < 0) {
                    serial = '0';
                } else if (num > 4294967295) {
                    serial = '4294967295';
                }
            }

            this.localSerial = serial;

            if (serial !== storeSerial && serial) {
                await Alpine.store('simulator').updateConfig({
                    serial: serial,
                    slave_id: this.localSlaveId
                });
            }
        },

        // Convert legacy string serial to numeric for meters
        stringToMeterSerial(str) {
            let hash = 1000000;
            for (let i = 0; i < str.length; i++) {
                hash += str.charCodeAt(i) * (i + 1) * 7;
            }
            return String(hash % 4294967296);
        },

        // Slave ID field handlers
        onSlaveIdFocus() {
            this.editingSlaveId = true;
        },

        async onSlaveIdBlur() {
            this.editingSlaveId = false;
            const storeSlaveId = Alpine.store('simulator').data?.slave_id || 1;
            const newSlaveId = parseInt(this.localSlaveId) || 1;
            if (newSlaveId !== storeSlaveId) {
                await Alpine.store('simulator').updateConfig({
                    serial: this.localSerial,
                    slave_id: newSlaveId
                });
            }
        },

        // OCPP URL field handlers
        onOcppUrlFocus() {
            this.editingOcppUrl = true;
        },

        onOcppUrlInput() {
            if (this.ocppUrlDebounceTimer) {
                clearTimeout(this.ocppUrlDebounceTimer);
            }
            this.ocppUrlDebounceTimer = setTimeout(() => {
                Alpine.store('simulator').updateConfig({ ocpp_url: this.localOcppUrl.trim() });
            }, 500);
        },

        onOcppUrlBlur() {
            this.editingOcppUrl = false;
            // Final save on blur
            if (this.ocppUrlDebounceTimer) {
                clearTimeout(this.ocppUrlDebounceTimer);
            }
            Alpine.store('simulator').updateConfig({ ocpp_url: this.localOcppUrl.trim() });
        },

        // Profile selection handler
        async onProfileChange(profileName) {
            // Compare against store's activeProfile, not localProfile (x-model already updated localProfile)
            const storeProfile = Alpine.store('simulator').activeProfile || '';
            if (profileName && profileName !== storeProfile) {
                this.changingProfile = true;
                this.localProfile = profileName;
                try {
                    await Alpine.store('simulator').applyProfile(profileName);
                    // Wait for server to process before allowing sync
                    await new Promise(resolve => setTimeout(resolve, 500));
                } finally {
                    this.changingProfile = false;
                }
            }
        },

        // Legacy methods for backwards compatibility
        async updateSerial(serial) {
            this.localSerial = serial;
            await Alpine.store('simulator').updateConfig({ serial, slave_id: this.localSlaveId });
        },

        async updateSlaveId(slaveId) {
            this.localSlaveId = slaveId;
            await Alpine.store('simulator').updateConfig({ serial: this.localSerial, slave_id: slaveId });
        },

        onOCPPUrlInput(event) {
            this.localOcppUrl = event.target.value;
            this.onOcppUrlInput();
        }
    }));

    // Modbus Controls
    Alpine.data('modbusControls', () => ({
        scenario: 'sunny',
        timeMultiplier: 1,
        updatingConfig: false,

        scenarios: [
            { value: 'sunny', label: '☀️ Sunny Day' },
            { value: 'cloudy', label: '⛅ Cloudy Day' },
            { value: 'overcast', label: '☁️ Overcast' },
            { value: 'rainy', label: '🌧️ Rainy Day' },
            { value: 'night', label: '🌙 Night Mode' },
            { value: 'peak_load', label: '⚡ Peak Load' },
            { value: 'battery_test', label: '🔋 Battery Cycle' }
        ],

        speeds: [
            { value: 1, label: '1× Real-time' },
            { value: 60, label: '60×' },
            { value: 360, label: '360×' },
            { value: 3600, label: '3600×' }
        ],

        init() {
            // Initial sync
            const automation = Alpine.store('simulator').automation;
            if (automation) {
                this.scenario = automation.scenario || 'sunny';
                this.timeMultiplier = automation.time_multiplier || 1;
            }

            this.$watch('$store.simulator.automation', (automation) => {
                // Only sync from store if not currently updating AND values match what server has
                // This prevents reverting user selections before the server has processed them
                if (automation && !this.updatingConfig) {
                    this.scenario = automation.scenario || 'sunny';
                    this.timeMultiplier = automation.time_multiplier || 1;
                }
            });
        },

        async updateConfig() {
            this.updatingConfig = true;
            try {
                await Alpine.store('simulator').updateAutomationConfig(
                    parseInt(this.timeMultiplier),
                    this.scenario
                );
                // Wait a bit for server to process before allowing sync
                await new Promise(resolve => setTimeout(resolve, 500));
            } finally {
                this.updatingConfig = false;
            }
        },

        get simulatedTime() {
            const automation = Alpine.store('simulator').automation;
            if (automation?.simulated_time) {
                return new Date(automation.simulated_time).toLocaleString();
            }
            return '';
        }
    }));

    // Modbus Registers
    Alpine.data('modbusRegisters', () => ({
        inputValues: {},

        setInputValue(address, value) {
            this.inputValues[address] = value;
        },

        async writeRegister(address) {
            const value = parseFloat(this.inputValues[address]);
            if (isNaN(value)) return;
            await Alpine.store('simulator').writeRegister(address, value);
            this.inputValues[address] = '';
        },

        formatValue(reg) {
            const decimals = reg.scale < 1 ? 1 : 0;
            return (reg.value || 0).toFixed(decimals);
        }
    }));

    // MQTT Controls
    Alpine.data('mqttControls', () => ({
        powerSliderValue: 0,
        socSliderValue: 50,
        powerDragging: false,
        socDragging: false,
        controlMode: 'CONTROLLABLE',
        changingControlMode: false,
        SNAP_THRESHOLD: 500,

        init() {
            this.$watch('$store.simulator.data', (data) => {
                if (data?.state && !this.powerDragging) {
                    const state = data.state;
                    this.powerSliderValue = state.requestedTargetPower || 0;
                    if (!this.socDragging) {
                        this.socSliderValue = state.soc ?? 50;
                    }
                    if (!this.changingControlMode) {
                        this.controlMode = state.controlMode || 'CONTROLLABLE';
                    }
                }
            });
        },

        get state() {
            return Alpine.store('simulator').data?.state || {};
        },

        get evConnected() {
            return this.state.evConnected || false;
        },

        get isAwake() {
            const sessionState = this.state.sessionState;
            return sessionState === 'CHARGE_LOOP' || sessionState === 'PAUSED';
        },

        get maxCharge() {
            return this.state.chargePowerMax || 11000;
        },

        get maxDischarge() {
            return this.state.dischargePowerMax || 11000;
        },

        get powerDisplayValue() {
            const value = this.powerSliderValue;
            const absValue = Math.abs(value);
            if (value === 0) return '0 W';
            if (value > 0) {
                return absValue >= 1000
                    ? `Discharging +${(value / 1000).toFixed(1)} kW`
                    : `Discharging +${value} W`;
            }
            return absValue >= 1000
                ? `Charging ${(value / 1000).toFixed(1)} kW`
                : `Charging ${value} W`;
        },

        get powerDisplayClass() {
            if (this.powerSliderValue === 0) return 'power-slider-value stopped';
            if (this.powerSliderValue > 0) return 'power-slider-value discharging';
            return 'power-slider-value charging';
        },

        get socDisplayClass() {
            if (this.socSliderValue <= 20) return 'soc-slider-value low';
            if (this.socSliderValue <= 40) return 'soc-slider-value medium';
            return 'soc-slider-value high';
        },

        onPowerInput(event) {
            this.powerDragging = true;
            let val = parseInt(event.target.value);
            if (Math.abs(val) < this.SNAP_THRESHOLD) {
                val = 0;
                event.target.value = 0;
            }
            this.powerSliderValue = val;
        },

        async onPowerChange(event) {
            this.powerDragging = false;
            let val = parseInt(event.target.value);
            if (Math.abs(val) < this.SNAP_THRESHOLD) {
                val = 0;
                event.target.value = 0;
            }
            this.powerSliderValue = val;
            await Alpine.store('simulator').setPower(val);
        },

        onSocInput(event) {
            this.socDragging = true;
            this.socSliderValue = parseInt(event.target.value);
        },

        async onSocChange(event) {
            this.socDragging = false;
            this.socSliderValue = parseInt(event.target.value);
            await Alpine.store('simulator').setState('soc', this.socSliderValue);
        },

        async toggleEVConnected() {
            await Alpine.store('simulator').toggleEVConnected();
        },

        async toggleWakeUp() {
            await Alpine.store('simulator').toggleWakeUp();
        },

        async setControlMode() {
            this.changingControlMode = true;
            try {
                await Alpine.store('simulator').setState('controlMode', this.controlMode);
            } finally {
                this.changingControlMode = false;
            }
        }
    }));

    // MQTT State
    Alpine.data('mqttState', () => ({
        get state() {
            return Alpine.store('simulator').data?.state || {};
        },

        get stateCards() {
            const s = this.state;
            return [
                { label: 'Session State', value: s.sessionState || 'IDLE', highlight: true },
                { label: 'Control Mode', value: s.controlMode || 'CONTROLLABLE', highlight: true },
                { label: 'EV Connected', value: s.evConnected ? 'Yes' : 'No' },
                { label: 'Charge Protocol', value: s.chargeProtocol || 'CCS' },
                { label: 'Power AC', value: `${(s.powerAc || 0).toFixed(0)} W`, highlight: true, colorClass: this.getPowerColorClass(s.powerAc) },
                { label: 'Power DC', value: `${(s.powerDc || 0).toFixed(0)} W` },
                { label: 'State of Charge', value: `${(s.soc || 0).toFixed(1)} %`, highlight: true },
                { label: 'Voltage AC (L1/L2/L3)', value: `${(s.voltageAc1 || 0).toFixed(0)} V` },
                { label: 'Current AC (L1)', value: `${(s.currentAc1 || 0).toFixed(1)} A` },
                { label: 'Frequency', value: `${(s.frequency || 50).toFixed(1)} Hz` },
                { label: 'Energy Import', value: `${(s.energyAcImport || 0).toFixed(0)} Wh` },
                { label: 'Energy Export', value: `${(s.energyAcExport || 0).toFixed(0)} Wh` },
                { label: 'Max Charge Power', value: `${(s.chargePowerMax || 0).toFixed(0)} W` },
                { label: 'Max Discharge Power', value: `${(s.dischargePowerMax || 0).toFixed(0)} W` }
            ];
        },

        getPowerColorClass(power) {
            if ((power || 0) < 0) return 'positive';
            if ((power || 0) > 0) return 'negative';
            return '';
        }
    }));

    // MQTT Clients
    Alpine.data('mqttClients', () => ({
        get clients() {
            return Alpine.store('simulator').clients || [];
        },

        get count() {
            return this.clients.length;
        }
    }));

    // Modbus Clients
    Alpine.data('modbusClients', () => ({
        get clients() {
            return Alpine.store('simulator').clients || [];
        },

        get count() {
            return this.clients.length;
        },

        formatTime(timestamp) {
            if (!timestamp) return '';
            const date = new Date(timestamp);
            return date.toLocaleTimeString();
        }
    }));

    // OCPP Controls
    Alpine.data('ocppControls', () => ({
        powerSliderValue: 0,
        powerDragging: false,
        wasCharging: false,
        ocppUrlDebounceTimer: null,

        init() {
            this.$watch('$store.simulator.data', (data) => {
                if (data?.state && !this.powerDragging) {
                    const state = data.state;
                    this.wasCharging = state.hasActiveTransaction || false;
                    if (!this.wasCharging) {
                        this.powerSliderValue = 0;
                    } else {
                        this.powerSliderValue = state.targetPowerW || this.maxPower;
                    }
                }
            });
        },

        get state() {
            return Alpine.store('simulator').data?.state || {};
        },

        get evConnected() {
            return this.state.evConnected || false;
        },

        get maxPower() {
            return (this.state.maxPowerKW || 7.7) * 1000;
        },

        get maxPowerLabel() {
            return `${(this.maxPower / 1000).toFixed(1)} kW`;
        },

        get powerDisplayValue() {
            if (!this.evConnected) return 'EV not connected';
            if (this.powerSliderValue === 0) return 'Not charging';
            return this.powerSliderValue >= 1000
                ? `Charging at ${(this.powerSliderValue / 1000).toFixed(1)} kW`
                : `Charging at ${this.powerSliderValue} W`;
        },

        get powerDisplayClass() {
            if (!this.evConnected) return 'power-slider-value disabled';
            if (this.powerSliderValue === 0) return 'power-slider-value stopped';
            return 'power-slider-value charging';
        },

        onPowerInput(event) {
            this.powerDragging = true;
            this.powerSliderValue = parseInt(event.target.value);
        },

        async onPowerChange(event) {
            this.powerDragging = false;
            const powerW = parseInt(event.target.value);
            this.powerSliderValue = powerW;

            const store = Alpine.store('simulator');

            if (powerW > 0 && !this.wasCharging) {
                await store.setState('startCharging', true);
                this.wasCharging = true;
            }

            if (powerW === 0 && this.wasCharging) {
                await store.setState('stopCharging', true);
                this.wasCharging = false;
            }

            await store.setState('chargingPower', powerW);
        },

        async toggleEVConnected() {
            await Alpine.store('simulator').setState('evConnected', !this.evConnected);
        }
    }));

    // OCPP State
    Alpine.data('ocppState', () => ({
        get state() {
            return Alpine.store('simulator').data?.state || {};
        },

        get stateCards() {
            const s = this.state;
            return [
                { label: 'Connection', value: s.connected ? 'Connected' : 'Disconnected', highlight: true, colorStyle: s.connected ? 'color: var(--color-primary);' : 'color: var(--text-muted);' },
                { label: 'Vendor', value: s.vendor || 'SimulatorCo' },
                { label: 'Model', value: s.model || 'SimCharger' },
                { label: 'Total Power', value: `${(s.totalPower || 0).toFixed(0)} W`, highlight: true },
                { label: 'L1 Voltage', value: `${(s.voltageL1 || 0).toFixed(1)} V` },
                { label: 'L2 Voltage', value: `${(s.voltageL2 || 0).toFixed(1)} V` },
                { label: 'L3 Voltage', value: `${(s.voltageL3 || 0).toFixed(1)} V` },
                { label: 'L1 Current', value: `${(s.currentL1 || 0).toFixed(3)} A` },
                { label: 'L2 Current', value: `${(s.currentL2 || 0).toFixed(3)} A` },
                { label: 'L3 Current', value: `${(s.currentL3 || 0).toFixed(3)} A` }
            ];
        },

        get connectors() {
            const data = Alpine.store('simulator').data;
            return data?.connectors || data?.state?.connectors || [];
        }
    }));

    // OCPP Debug
    Alpine.data('ocppDebug', () => ({
        copySuccess: false,

        get debug() {
            return Alpine.store('simulator').debugMeterValues || {};
        },

        async refresh() {
            await Alpine.store('simulator').loadDebugMeterValues();
        },

        formatVoltage(key) {
            return this.debug[key]?.toFixed(2) ?? '--';
        },

        formatCurrent(key) {
            return this.debug[key]?.toFixed(3) ?? '--';
        },

        formatPower() {
            return this.debug.power_W?.toFixed(0) ?? '--';
        },

        formatEnergy() {
            return this.debug.energy_Wh?.toFixed(1) ?? '--';
        },

        get sampleJson() {
            return this.debug.sample_meter_values_json || '';
        },

        copyJson() {
            const json = this.sampleJson;
            if (!json) return;

            const textarea = document.createElement('textarea');
            textarea.value = json;
            textarea.style.position = 'fixed';
            textarea.style.opacity = '0';
            document.body.appendChild(textarea);
            textarea.select();
            document.execCommand('copy');
            document.body.removeChild(textarea);

            this.copySuccess = true;
            setTimeout(() => this.copySuccess = false, 2000);
        }
    }));

    // Logs Viewer
    Alpine.data('logsViewer', () => ({
        copySuccess: false,

        getOpClass(log) {
            if (log.operation?.includes('read')) return 'log-op read';
            if (log.operation?.includes('write')) return 'log-op write';
            if (log.operation?.includes('connect')) return 'log-op connect';
            return 'log-op message';
        },

        formatTime(timestamp) {
            return new Date(timestamp).toLocaleTimeString();
        },

        formatDetails(log) {
            if (log.values) {
                return log.values.join(', ');
            }
            if (log.details) {
                const d = log.details;
                if (d.topic) return `${d.topic}: ${d.payload}`;
                if (d.action) {
                    let payloadStr = '';
                    if (d.payload && d.payload !== '{}' && d.payload !== 'null') {
                        try {
                            const parsed = typeof d.payload === 'string' ? JSON.parse(d.payload) : d.payload;
                            payloadStr = Object.keys(parsed).length > 0 ? ` ${JSON.stringify(parsed)}` : '';
                        } catch { payloadStr = ` ${d.payload}`; }
                    }
                    return `${d.action}${payloadStr}`;
                }
                return JSON.stringify(d);
            }
            if (log.count) {
                return `x${log.count}`;
            }
            return '';
        },

        copyLogs() {
            const store = Alpine.store('simulator');
            const logText = store.logs.map(log => {
                const time = this.formatTime(log.timestamp);
                const op = log.operation || '';
                const addr = log.address !== undefined ? log.address : '';
                const details = this.formatDetails(log);
                return `${time} ${op} ${addr} ${details}`.trim().replace(/\s+/g, ' ');
            }).join('\n');

            const textarea = document.createElement('textarea');
            textarea.value = logText;
            textarea.style.position = 'fixed';
            textarea.style.opacity = '0';
            document.body.appendChild(textarea);
            textarea.select();
            document.execCommand('copy');
            document.body.removeChild(textarea);

            this.copySuccess = true;
            setTimeout(() => this.copySuccess = false, 1500);
        }
    }));

    // Site Member Controls (compact controls for site member devices)
    Alpine.data('siteMemberControls', () => ({
        powerSliderValue: 0,
        socSliderValue: 50,
        powerDragging: false,
        socDragging: false,

        getSimulator(simId) {
            const app = Alpine.store('app');
            return app.simulators.find(s => s.id === simId) || null;
        },

        // Navigate to full simulator view
        openSimulator(simId) {
            const sim = this.getSimulator(simId);
            if (sim) {
                Alpine.store('app').selectSimulator(simId, sim.protocol);
            }
        },

        // MQTT controls
        getMqttState(simId) {
            const sim = this.getSimulator(simId);
            return sim?.mqtt_state || {};
        },

        getMqttEvConnected(simId) {
            const sim = this.getSimulator(simId);
            return sim?.ev_connected || false;
        },

        getMqttPower(simId) {
            const sim = this.getSimulator(simId);
            return sim?.power_ac || 0;
        },

        getMqttSoc(simId) {
            const sim = this.getSimulator(simId);
            return sim?.soc || 50;
        },

        getMqttMaxCharge(simId) {
            return 11000; // Default max charge power
        },

        getMqttMaxDischarge(simId) {
            return 11000; // Default max discharge power
        },

        async toggleMqttEvConnected(simId) {
            const current = this.getMqttEvConnected(simId);
            await api.setState(simId, 'evConnected', !current);
            await Alpine.store('app').loadSimulators();
        },

        async setMqttPower(simId, power) {
            await api.setPower(simId, power);
        },

        formatMqttPower(power) {
            const absValue = Math.abs(power);
            if (power === 0) return '0 W';
            if (power > 0) {
                return absValue >= 1000 ? `+${(power / 1000).toFixed(1)} kW` : `+${power} W`;
            }
            return absValue >= 1000 ? `${(power / 1000).toFixed(1)} kW` : `${power} W`;
        },

        // OCPP controls
        getOcppEvConnected(simId) {
            const sim = this.getSimulator(simId);
            return sim?.ev_connected || false;
        },

        getOcppPower(simId) {
            const sim = this.getSimulator(simId);
            return sim?.total_power || 0;
        },

        getOcppMaxPower(simId) {
            const sim = this.getSimulator(simId);
            return (sim?.max_power_kw || 7.7) * 1000;
        },

        async toggleOcppEvConnected(simId) {
            const current = this.getOcppEvConnected(simId);
            await api.setState(simId, 'evConnected', !current);
            await Alpine.store('app').loadSimulators();
        },

        async setOcppPower(simId, power) {
            const sim = this.getSimulator(simId);
            const wasCharging = sim?.has_active_transaction || false;

            if (power > 0 && !wasCharging) {
                await api.setState(simId, 'startCharging', true);
            }
            if (power === 0 && wasCharging) {
                await api.setState(simId, 'stopCharging', true);
            }
            await api.setState(simId, 'chargingPower', power);
        },

        formatOcppPower(power) {
            if (power === 0) return '0 W';
            return power >= 1000 ? `${(power / 1000).toFixed(1)} kW` : `${power} W`;
        },

        // Modbus controls
        getModbusRunning(simId) {
            const sim = this.getSimulator(simId);
            return sim?.running || false;
        },

        async toggleModbusRunning(simId) {
            const current = this.getModbusRunning(simId);
            if (current) {
                await api.stopAutomation(simId);
            } else {
                await api.startAutomation(simId);
            }
            await Alpine.store('app').loadSimulators();
        },

        // Home Load controls
        formatLoadPower(power) {
            if (power >= 1000) {
                return `${(power / 1000).toFixed(1)} kW`;
            }
            return `${power} W`;
        },

        async setBaseLoad(power) {
            await Alpine.store('site').updateConfig({ base_load_w: power });
        },

        // General helpers
        getPowerColorStyle(power, protocol) {
            if (protocol === 'mqtt') {
                // MQTT: negative = charging (importing), positive = discharging (exporting)
                if (power < 0) return 'color: var(--color-warning);'; // Charging = import = orange
                if (power > 0) return 'color: var(--color-primary);'; // Discharging = export = green
            } else if (protocol === 'ocpp') {
                // OCPP: always positive when charging
                if (power > 0) return 'color: var(--color-warning);'; // Charging = import = orange
            } else {
                // Modbus: use pv_power and load logic
                return 'color: var(--text-primary);';
            }
            return '';
        }
    }));

    // Site Tabs Component
    Alpine.data('siteTabs', () => ({
        async createNewSite() {
            try {
                // Generate name: "Site 2", "Site 3", etc.
                const sites = Alpine.store('site').sites;
                const name = `Site ${sites.length + 1}`;

                // Create an empty site (no meter, no simulators)
                await Alpine.store('site').createSite(name, 0, [], 1000);
            } catch (e) {
                alert('Error creating site: ' + e.message);
            }
        },

        async deleteSiteTab(siteId) {
            if (Alpine.store('site').sites.length <= 1) return;
            if (!confirm('Delete this site?')) return;
            await Alpine.store('site').deleteSite(siteId);
        }
    }));

    // Live Formula for Site View
    Alpine.data('liveFormula', () => ({
        get baseLoad() {
            return Alpine.store('site').data?.base_load_w || 0;
        },

        get virtualDevices() {
            return Alpine.store('site').data?.virtual_devices || [];
        },

        get pvPower() {
            const pv = this.virtualDevices.find(d => d.type === 'pv');
            return pv?.power || 0;
        },

        get batteryPower() {
            const battery = this.virtualDevices.find(d => d.type === 'battery');
            if (!battery) return 0;
            const power = battery.power || 0;
            // charging = positive (consuming), discharging = negative (generating)
            return battery.direction === 'charging' ? power : -power;
        },

        get hasMeterInverter() {
            return Alpine.store('site').isMeterInverter();
        },

        get contributions() {
            const data = Alpine.store('site').data;
            const powerContribs = data?.power_contributions || {};
            const simIds = data?.simulator_ids || [];
            const meterId = data?.meter_id;
            const app = Alpine.store('app');

            return simIds
                .filter(id => id !== meterId)
                .map(id => {
                    const sim = app.simulators.find(s => s.id === id);
                    return {
                        id,
                        name: sim?.serial || `ID ${id}`,
                        power: powerContribs[id] || 0
                    };
                })
                .filter(c => c.power !== 0 || true); // Show all
        },

        get totalContributions() {
            return this.contributions.reduce((sum, c) => sum + c.power, 0);
        },

        get meterPower() {
            return Alpine.store('site').data?.meter_power || 0;
        },

        formatValue(val) {
            const absVal = Math.abs(val);
            const sign = val >= 0 ? '+' : '';
            if (absVal >= 1000) {
                return `${sign}${(val / 1000).toFixed(2)} kW`;
            }
            return `${sign}${val.toFixed(0)} W`;
        }
    }));

    // Energy Meter View
    Alpine.data('energyMeterView', () => ({
        selectedSiteId: null,
        changingSite: false,

        init() {
            // Initialize selected site from current meter assignment
            this.updateSelectedSite();

            // Watch for site changes
            this.$watch('$store.site.sites', () => {
                if (!this.changingSite) {
                    this.updateSelectedSite();
                }
            });
        },

        updateSelectedSite() {
            const meterId = Alpine.store('simulator').id;
            const sites = Alpine.store('site').sites || [];
            const connectedSite = sites.find(s => s.meter_id === meterId);
            this.selectedSiteId = connectedSite?.id || null;
        },

        get sites() {
            return Alpine.store('site').sites || [];
        },

        get connectedSiteName() {
            if (!this.selectedSiteId) return null;
            const site = this.sites.find(s => s.id === this.selectedSiteId);
            return site?.name || null;
        },

        async onSiteChange(siteId) {
            const meterId = Alpine.store('simulator').id;
            const previousSiteId = this.selectedSiteId;

            this.changingSite = true;
            try {
                if (siteId === '') {
                    // Disconnect from current site - set meter_id to 0
                    if (previousSiteId) {
                        await siteApi.setSiteMeter(previousSiteId, 0);
                        await Alpine.store('site').loadSites();
                    }
                    this.selectedSiteId = null;
                } else {
                    const newSiteId = parseInt(siteId);
                    // Connect to new site
                    await siteApi.setSiteMeter(newSiteId, meterId);
                    await Alpine.store('site').loadSites();
                    this.selectedSiteId = newSiteId;
                }
            } finally {
                this.changingSite = false;
            }
        },

        get meterPower() {
            const regs = Alpine.store('simulator').registers || [];
            const reg = regs.find(r => r.address === 5600);
            return reg?.value || 0;
        },

        get importEnergy() {
            const regs = Alpine.store('simulator').registers || [];
            const reg = regs.find(r => r.address === 13036);
            return reg?.value || 0;
        },

        get exportEnergy() {
            const regs = Alpine.store('simulator').registers || [];
            const reg = regs.find(r => r.address === 13045);
            return reg?.value || 0;
        },

        formatPowerDisplay(power) {
            return Math.abs(power).toFixed(0);
        },

        formatEnergy(wh) {
            if (wh >= 1000000) {
                return `${(wh / 1000000).toFixed(2)} MWh`;
            }
            if (wh >= 1000) {
                return `${(wh / 1000).toFixed(2)} kWh`;
            }
            return `${wh.toFixed(0)} Wh`;
        },

        getP1Mode() {
            if (!this.selectedSiteId) return 'direct';
            const site = this.sites.find(s => s.id === this.selectedSiteId);
            return site?.p1_mode || 'direct';
        },

        async setP1Mode(mode) {
            if (!this.selectedSiteId) return;
            try {
                await siteApi.setP1Mode(this.selectedSiteId, mode);
                await Alpine.store('site').loadSites();
            } catch (e) {
                console.error('Set P1 mode error:', e);
                alert('Error: ' + e.message);
            }
        }
    }));

    // Inverter View (Connected Site dropdown for inverters)
    Alpine.data('inverterView', () => ({
        selectedSiteId: null,
        changingSite: false,

        init() {
            // Initialize selected site from current meter assignment
            this.updateSelectedSite();

            // Watch for site changes
            this.$watch('$store.site.sites', () => {
                if (!this.changingSite) {
                    this.updateSelectedSite();
                }
            });
        },

        updateSelectedSite() {
            const inverterId = Alpine.store('simulator').id;
            const sites = Alpine.store('site').sites || [];
            const connectedSite = sites.find(s => s.meter_id === inverterId);
            this.selectedSiteId = connectedSite?.id || null;
        },

        get sites() {
            return Alpine.store('site').sites || [];
        },

        get connectedSiteName() {
            if (!this.selectedSiteId) return null;
            const site = this.sites.find(s => s.id === this.selectedSiteId);
            return site?.name || null;
        },

        async onSiteChange(siteId) {
            const inverterId = Alpine.store('simulator').id;
            const previousSiteId = this.selectedSiteId;

            this.changingSite = true;
            try {
                if (siteId === '') {
                    // Disconnect from current site - set meter_id to 0
                    if (previousSiteId) {
                        await siteApi.setSiteMeter(previousSiteId, 0);
                        await Alpine.store('site').loadSites();
                    }
                    this.selectedSiteId = null;
                } else {
                    const newSiteId = parseInt(siteId);
                    // Connect to new site
                    await siteApi.setSiteMeter(newSiteId, inverterId);
                    await Alpine.store('site').loadSites();
                    this.selectedSiteId = newSiteId;
                }
            } finally {
                this.changingSite = false;
            }
        }
    }));

    // Virtual Device Controls (for PV/Battery cards in site view)
    Alpine.data('virtualDeviceControls', () => ({
        pvPower: 0,
        batteryPower: 0,
        batterySoc: 50,
        dragging: null,

        init() {
            this.syncFromSite();

            this.$watch('$store.site.data', () => {
                if (!this.dragging) {
                    this.syncFromSite();
                }
            });
        },

        syncFromSite() {
            const virtualDevices = Alpine.store('site').data?.virtual_devices || [];
            const pv = virtualDevices.find(d => d.type === 'pv');
            const battery = virtualDevices.find(d => d.type === 'battery');

            if (pv) this.pvPower = pv.power || 0;
            if (battery) {
                const power = battery.power || 0;
                const direction = battery.direction || 'idle';
                // Convert to signed: charging = negative, discharging = positive
                this.batteryPower = direction === 'charging' ? -power : power;
                this.batterySoc = battery.soc || 50;
            }
        },

        get isSimulationRunning() {
            // Check if the meter inverter has simulation running
            const meterId = Alpine.store('site').data?.meter_id;
            if (!meterId) return false;
            const meterSim = Alpine.store('app').simulators.find(s => s.id === meterId);
            // Note: simulators list returns automation as a boolean directly
            return meterSim?.automation || false;
        },

        // PV Power handlers
        onPvPowerInput(value) {
            this.dragging = 'pv';
            this.pvPower = parseFloat(value);
        },

        async onPvPowerChange(value) {
            this.dragging = null;
            this.pvPower = parseFloat(value);
            const siteId = Alpine.store('site').selectedId;
            if (siteId) {
                await siteApi.writeVirtualDevice(siteId, 'pv', { power: this.pvPower });
                await Alpine.store('site').loadSiteData();
            }
        },

        // Battery Power handlers
        onBatteryPowerInput(value) {
            this.dragging = 'battery';
            this.batteryPower = parseFloat(value);
        },

        async onBatteryPowerChange(value) {
            this.dragging = null;
            this.batteryPower = parseFloat(value);
            const siteId = Alpine.store('site').selectedId;
            if (siteId) {
                await siteApi.writeVirtualDevice(siteId, 'battery', { power: this.batteryPower });
                await Alpine.store('site').loadSiteData();
            }
        },

        // Battery SoC handlers
        onSocInput(value) {
            this.dragging = 'soc';
            this.batterySoc = parseFloat(value);
        },

        async onSocChange(value) {
            this.dragging = null;
            this.batterySoc = parseFloat(value);
            const siteId = Alpine.store('site').selectedId;
            if (siteId) {
                await siteApi.writeVirtualDevice(siteId, 'battery', { soc: this.batterySoc });
                await Alpine.store('site').loadSiteData();
            }
        },

        // UI helpers
        formatPvPower() {
            const power = this.pvPower;
            if (power >= 1000) return `${(power / 1000).toFixed(1)} kW`;
            return `${power.toFixed(0)} W`;
        },

        formatBatteryPower() {
            const power = this.batteryPower;
            const abs = Math.abs(power);
            if (power === 0) return '0 W';
            if (power < 0) {
                return abs >= 1000 ? `Charging ${(abs / 1000).toFixed(1)} kW` : `Charging ${abs.toFixed(0)} W`;
            }
            return abs >= 1000 ? `Discharging ${(abs / 1000).toFixed(1)} kW` : `Discharging ${abs.toFixed(0)} W`;
        },

        getBatteryFillStyle() {
            const soc = Math.max(0, Math.min(100, this.batterySoc));
            let color;
            if (soc <= 20) {
                color = 'rgba(239, 68, 68, 0.8)';
            } else if (soc <= 40) {
                color = 'rgba(245, 158, 11, 0.8)';
            } else {
                color = 'rgba(139, 92, 246, 0.8)'; // purple for virtual battery
            }
            return `height: ${soc}%; background: ${color};`;
        },

        getBatteryPowerClass() {
            if (this.batteryPower < 0) return 'charging';
            if (this.batteryPower > 0) return 'discharging';
            return 'idle';
        }
    }));

    // Inverter Default Tab Component
    Alpine.data('inverterDefaultTab', () => ({
        activeTab: 'default',

        // Local state for sliders
        pvPower: 0,
        batterySoc: 0,
        batteryPower: 0,
        meterPower: 0,

        // Max values for sliders
        maxPvPower: 10000,
        maxBatteryPower: 5000,

        // Dragging state to prevent sync during drag
        dragging: null,

        // Dialog state
        showSimModeDialog: false,
        pendingAction: null,

        // Track last known simulator ID
        lastSimulatorId: null,

        init() {
            // Initial sync from registers
            this.syncFromRegisters();
            this.lastSimulatorId = Alpine.store('simulator').id;

            // Watch for register changes
            this.$watch('$store.simulator.registers', () => {
                // Only sync if not dragging
                if (!this.dragging) {
                    this.syncFromRegisters();
                }
            });

            // Watch for simulator changes
            this.$watch('$store.simulator.id', (newId) => {
                if (newId !== this.lastSimulatorId) {
                    this.lastSimulatorId = newId;
                    this.syncFromRegisters();
                }
            });
        },

        syncFromRegisters() {
            const regs = Alpine.store('simulator').registers || [];

            // PV Power (address 5016, U32 Little Endian)
            const pvReg = regs.find(r => r.address === 5016);
            if (pvReg) this.pvPower = pvReg.value || 0;

            // Battery Power (address 13021, U16)
            const batPowerReg = regs.find(r => r.address === 13021);
            if (batPowerReg) {
                // Determine sign from command register (13050)
                const cmdReg = regs.find(r => r.address === 13050);
                const cmd = cmdReg?.value || 0;
                const absVal = Math.abs(batPowerReg.value || 0);
                // 170 = charge (negative, importing), 187 = discharge (positive, exporting)
                this.batteryPower = cmd === 170 ? -absVal : absVal;
            }

            // Battery SoC (address 13022, scale 0.1)
            const socReg = regs.find(r => r.address === 13022);
            if (socReg) this.batterySoc = socReg.value || 0;

            // Meter Power (address 5600, I32 Little Endian)
            const meterReg = regs.find(r => r.address === 5600);
            if (meterReg) this.meterPower = meterReg.value || 0;
        },

        get isSimulationRunning() {
            return Alpine.store('simulator').automation?.enabled || false;
        },

        // Slider event handlers
        onSliderMouseDown(type) {
            if (this.isSimulationRunning) {
                this.pendingAction = { type };
                this.showSimModeDialog = true;
            } else {
                this.dragging = type;
            }
        },

        // PV Power handlers
        onPvPowerInput(value) {
            if (!this.dragging) return;
            this.pvPower = parseFloat(value);
        },

        async onPvPowerChange(value) {
            if (!this.dragging) return;
            this.dragging = null;
            this.pvPower = parseFloat(value);
            await api.writeRegister(Alpine.store('simulator').id, 5016, this.pvPower);
            await Alpine.store('simulator').loadRegisters();
        },

        // Battery SoC handlers
        onSocInput(value) {
            if (!this.dragging) return;
            this.batterySoc = parseFloat(value);
        },

        async onSocChange(value) {
            if (!this.dragging) return;
            this.dragging = null;
            this.batterySoc = parseFloat(value);
            await api.writeRegister(Alpine.store('simulator').id, 13022, this.batterySoc);
            await Alpine.store('simulator').loadRegisters();
        },

        // Battery Power handlers
        onBatteryPowerInput(value) {
            if (!this.dragging) return;
            this.batteryPower = parseFloat(value);
        },

        async onBatteryPowerChange(value) {
            if (!this.dragging) return;
            this.dragging = null;
            this.batteryPower = parseFloat(value);
            const simId = Alpine.store('simulator').id;

            // Write absolute power value
            await api.writeRegister(simId, 13021, Math.abs(this.batteryPower));

            // Write command register: 170=charge, 187=discharge, 204=stop
            let cmd = 204; // stop
            if (this.batteryPower < 0) cmd = 170; // charge
            else if (this.batteryPower > 0) cmd = 187; // discharge
            await api.writeRegister(simId, 13050, cmd);

            await Alpine.store('simulator').loadRegisters();
        },

        // Dialog handlers
        async stopSimAndApply() {
            const simId = Alpine.store('simulator').id;
            await api.stopSimulation(simId);
            await Alpine.store('simulator').loadAutomation();

            // Now allow the control to be used
            this.showSimModeDialog = false;
            this.pendingAction = null;
        },

        cancelDialog() {
            this.showSimModeDialog = false;
            this.pendingAction = null;
            // Sync back from registers to revert any visual changes
            this.syncFromRegisters();
        },

        // Reset to profile defaults
        async resetToDefaults() {
            if (!confirm('Reset all values to profile defaults?')) return;
            const simId = Alpine.store('simulator').id;
            await api.reset(simId);
            await Alpine.store('simulator').loadRegisters();
            this.syncFromRegisters();
        },

        // UI helpers
        getBatteryFillStyle() {
            const soc = Math.max(0, Math.min(100, this.batterySoc));
            let color;
            if (soc <= 20) {
                color = 'rgba(239, 68, 68, 0.8)'; // red
            } else if (soc <= 40) {
                color = 'rgba(245, 158, 11, 0.8)'; // yellow/orange
            } else {
                color = 'rgba(0, 200, 100, 0.8)'; // green
            }
            return `height: ${soc}%; background: ${color};`;
        },

        getBatteryPowerClass() {
            if (this.batteryPower < 0) return 'charging';
            if (this.batteryPower > 0) return 'discharging';
            return 'idle';
        },

        formatBatteryPower() {
            const power = this.batteryPower;
            const abs = Math.abs(power);
            if (power === 0) return '0 W';
            if (power < 0) {
                return abs >= 1000 ? `Charging ${(abs / 1000).toFixed(1)} kW` : `Charging ${abs.toFixed(0)} W`;
            }
            return abs >= 1000 ? `Discharging ${(abs / 1000).toFixed(1)} kW` : `Discharging ${abs.toFixed(0)} W`;
        },

        getSunGlowStyle() {
            const intensity = Math.min(1, this.pvPower / this.maxPvPower);
            const blur = 5 + intensity * 20;
            const spread = intensity * 10;
            return `filter: drop-shadow(0 0 ${blur}px rgba(255, 215, 0, ${intensity})) drop-shadow(0 0 ${spread}px rgba(255, 215, 0, ${intensity * 0.5}));`;
        }
    }));

    // Site View
    Alpine.data('siteView', () => ({
        get meterPower() {
            return Alpine.store('site').data?.meter_power || 0;
        },

        formatMeterPower() {
            const power = this.meterPower;
            const absPower = Math.abs(power);
            const direction = power >= 0 ? 'Import' : 'Export';
            if (absPower >= 1000) {
                return `${direction} ${(absPower / 1000).toFixed(2)} kW`;
            }
            return `${direction} ${absPower.toFixed(0)} W`;
        },

        getSimInfo(simId) {
            const app = Alpine.store('app');
            const sim = app.simulators.find(s => s.id === simId);
            return sim || { serial: `ID ${simId}`, protocol: 'unknown', running: false };
        },

        getPowerContribution(simId) {
            const contributions = Alpine.store('site').data?.power_contributions || {};
            return contributions[simId] || 0;
        },

        formatPowerContribution(simId) {
            const power = this.getPowerContribution(simId);
            const absPower = Math.abs(power);
            const direction = power >= 0 ? '+' : '-';
            if (absPower >= 1000) {
                return `${direction}${(absPower / 1000).toFixed(2)} kW`;
            }
            return `${direction}${absPower.toFixed(0)} W`;
        },

        getAvailableToAdd() {
            const site = Alpine.store('site');
            const currentIds = new Set(site.data?.simulator_ids || []);
            // Exclude energy meters (they can only be used as the site meter, not as member devices)
            return site.getAvailableSimulators().filter(s =>
                !currentIds.has(s.id) && s.category !== 'energy_meter'
            );
        },

        async addDevice(simId) {
            try {
                await Alpine.store('site').addSimulator(simId);
            } catch (e) {
                alert('Error: ' + e.message);
            }
        },

        async removeDevice(simId) {
            try {
                await Alpine.store('site').removeSimulator(simId);
            } catch (e) {
                alert('Error: ' + e.message);
            }
        },

        async changeMeter(meterId) {
            if (!meterId) return;
            try {
                await Alpine.store('site').setMeter(meterId);
            } catch (e) {
                alert('Error: ' + e.message);
            }
        }
    }));

    // Phase Visualization Component
    Alpine.data('phaseVisualization', () => ({
        showSettingsModal: false,
        localPhaseFactors: { l1: 0.333, l2: 0.333, l3: 0.334 },
        fuseBlown: { l1: false, l2: false, l3: false },

        init() {
            // Initialize local phase factors from store
            this.syncPhaseFactors();
            this.$watch('$store.site.data', () => this.syncPhaseFactors());
        },

        syncPhaseFactors() {
            const factors = Alpine.store('site').data?.phase_factors;
            if (factors) {
                this.localPhaseFactors = {
                    l1: factors.l1 || 0.333,
                    l2: factors.l2 || 0.333,
                    l3: factors.l3 || 0.334
                };
            }
        },

        get meterPower() {
            return Alpine.store('site').data?.meter_power || 0;
        },

        formatMeterPower() {
            const power = this.meterPower;
            const absPower = Math.abs(power);
            const direction = power >= 0 ? 'IMPORTING' : 'EXPORTING';
            if (absPower >= 1000) {
                return `${(absPower / 1000).toFixed(2)} kW ${direction}`;
            }
            return `${absPower.toFixed(0)} W ${direction}`;
        },

        getPhaseCurrents() {
            const currents = Alpine.store('site').data?.phase_currents || { l1: 0, l2: 0, l3: 0 };
            return {
                l1: currents.l1 || 0,
                l2: currents.l2 || 0,
                l3: currents.l3 || 0
            };
        },

        getFuseSize() {
            return Alpine.store('site').data?.fuse_size || 16;
        },

        toggleFuse(phase) {
            this.fuseBlown[phase] = !this.fuseBlown[phase];
        },

        // Non-linear scale: 0-fuseSize = 70% of bar, fuseSize to 3*fuseSize = remaining 30%
        getPhaseBarStyle(current, isBlown) {
            if (isBlown) {
                return 'width: 0%; background: transparent;';
            }

            const fuseSize = this.getFuseSize();
            const maxOverload = fuseSize * 3; // 300% overload tolerance

            let percentage;
            let color;

            if (current <= fuseSize) {
                // Normal range: 0 to fuseSize maps to 0-70%
                percentage = (current / fuseSize) * 70;
                color = 'var(--color-success)'; // Green
            } else {
                // Overload range: fuseSize to 3*fuseSize maps to 70-100%
                const overloadRatio = Math.min((current - fuseSize) / (maxOverload - fuseSize), 1);
                percentage = 70 + (overloadRatio * 30);

                // Color gradient: yellow (0%) -> orange (50%) -> red (100%)
                if (overloadRatio < 0.5) {
                    // Yellow to orange
                    const t = overloadRatio * 2;
                    const r = 245;
                    const g = Math.round(158 - (t * 58)); // 158 -> 100
                    const b = Math.round(11 + (t * 19)); // 11 -> 30
                    color = `rgb(${r}, ${g}, ${b})`;
                } else {
                    // Orange to red
                    const t = (overloadRatio - 0.5) * 2;
                    const r = Math.round(245 - (t * 6)); // 245 -> 239
                    const g = Math.round(100 - (t * 32)); // 100 -> 68
                    const b = Math.round(30 + (t * 38)); // 30 -> 68
                    color = `rgb(${r}, ${g}, ${b})`;
                }
            }

            return `width: ${percentage}%; background: ${color};`;
        },

        isPhaseFactorsValid() {
            const sum = this.localPhaseFactors.l1 + this.localPhaseFactors.l2 + this.localPhaseFactors.l3;
            return sum >= 0.99 && sum <= 1.01;
        },

        async savePhaseFactors() {
            if (!this.isPhaseFactorsValid()) return;
            try {
                await Alpine.store('site').updateConfig({
                    phase_factors: this.localPhaseFactors
                });
                this.showSettingsModal = false;
            } catch (e) {
                alert('Error: ' + e.message);
            }
        }
    }));

    // Confirmation modal component (just a wrapper, logic is in store)
    Alpine.data('confirmModal', () => ({}));
});
