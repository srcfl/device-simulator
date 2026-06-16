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

        getRuntimeSerial() {
            const data = Alpine.store('simulator').data;
            const runtime = data?.runtime_serial;
            if (runtime !== undefined && runtime !== null && runtime !== '') {
                return runtime;
            }
            return this.localSerial;
        },

        getRuntimeSlaveId() {
            const data = Alpine.store('simulator').data;
            if (data?.slave_id !== undefined && data?.slave_id !== null) {
                return data.slave_id;
            }
            return this.localSlaveId;
        },

        getRuntimeOcppUrl() {
            const data = Alpine.store('simulator').data;
            const runtime = data?.state?.ocppURL ?? data?.ocpp_url;
            if (runtime !== undefined && runtime !== null && runtime !== '') {
                return runtime;
            }
            return this.localOcppUrl || '';
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
        },

        // Toggle emulator - delegates to the simulator store
        async toggleEmulator() {
            const store = Alpine.store('simulator');
            if (store && typeof store.toggleEmulator === 'function') {
                await store.toggleEmulator();
            } else {
                console.error('Simulator store or toggleEmulator not found');
            }
        }
    }));

    // Site Simulation Status (for device detail view - synced with site)
    Alpine.data('siteSimulationStatus', () => ({
        scenarioLabels: {
            'sunny': '☀️ Sunny',
            'cloudy': '⛅ Cloudy',
            'overcast': '☁️ Overcast',
            'rainy': '🌧️ Rainy',
            'night': '🌙 Night',
            'peak_load': '⚡ Peak Load'
        },

        get connectedSiteId() {
            const simId = Alpine.store('simulator').id;
            const sites = Alpine.store('site').sites || [];
            const site = sites.find(s =>
                s.meter_id === simId ||
                (s.meter_ids || []).includes(simId) ||
                (s.simulator_ids || []).includes(simId)
            );
            return site?.id || null;
        },

        get connectedSite() {
            if (!this.connectedSiteId) return null;
            const sites = Alpine.store('site').sites || [];
            return sites.find(s => s.id === this.connectedSiteId) || null;
        },

        get connectedSiteName() {
            return this.connectedSite?.name || 'Unknown';
        },

        get siteSimRunning() {
            return this.connectedSite?.sim_running || false;
        },

        get siteSimulatedTime() {
            return this.connectedSite?.simulated_time || null;
        },

        get siteScenario() {
            return this.connectedSite?.scenario || 'sunny';
        },

        get siteScenarioLabel() {
            return this.scenarioLabels[this.siteScenario] || this.siteScenario;
        },

        get siteTimeMultiplier() {
            return this.connectedSite?.time_multiplier || 1;
        },

        async toggleSiteSimulation() {
            if (!this.connectedSiteId) return;
            try {
                if (this.siteSimRunning) {
                    await siteApi.stopSiteSimulation(this.connectedSiteId);
                } else {
                    await siteApi.startSiteSimulation(this.connectedSiteId);
                }
                await Alpine.store('site').loadSites();
            } catch (e) {
                console.error('Toggle site simulation error:', e);
            }
        }
    }));

    // Modbus Controls (legacy - kept for compatibility)
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
            return sessionState === 'CHARGE_LOOP' || sessionState === 'PAUSED' || sessionState === 'STOPPED' || sessionState === 'ERROR';
        },

        get hasError() {
            return this.state.errorState || false;
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
            const previousValue = this.powerSliderValue;
            this.powerSliderValue = val;
            try {
                await Alpine.store('simulator').setPower(val);
                await Alpine.store('simulator').loadAllData();
            } catch (e) {
                console.error('Set power error:', e);
                // Rollback on error
                this.powerSliderValue = previousValue;
            }
        },

        onSocInput(event) {
            this.socDragging = true;
            this.socSliderValue = parseInt(event.target.value);
        },

        async onSocChange(event) {
            this.socDragging = false;
            const previousValue = this.socSliderValue;
            this.socSliderValue = parseInt(event.target.value);
            try {
                await Alpine.store('simulator').setState('soc', this.socSliderValue);
            } catch (e) {
                console.error('Set SOC error:', e);
                // Rollback on error
                this.socSliderValue = previousValue;
            }
        },

        async toggleEVConnected() {
            await Alpine.store('simulator').toggleEVConnected();
        },

        async toggleWakeUp() {
            await Alpine.store('simulator').toggleWakeUp();
        },

        async toggleError() {
            await Alpine.store('simulator').setState('errorState', !this.hasError);
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
            const cards = [
                { label: 'Control Mode', value: s.controlMode || 'CONTROLLABLE', highlight: true },
                { label: 'EV Connected', value: s.evConnected ? 'Yes' : 'No' },
            ];
            // Car-related cards only when plugged in
            if (s.evConnected) {
                cards.unshift({ label: 'Session State', value: s.sessionState || 'PAUSED', highlight: true });
                cards.push(
                    { label: 'Charge Protocol', value: s.chargeProtocol || 'CCS' },
                    { label: 'Power AC', value: `${(s.powerAc || 0).toFixed(0)} W`, highlight: true, colorClass: this.getPowerColorClass(s.powerAc) },
                    { label: 'Power DC', value: `${(s.powerDc || 0).toFixed(0)} W` },
                    { label: 'State of Charge', value: `${(s.soc || 0).toFixed(1)} %`, highlight: true },
                    { label: 'Current AC (L1)', value: `${(s.currentAc1 || 0).toFixed(1)} A` },
                );
            }
            // Charger-level cards always shown
            cards.push(
                { label: 'Voltage AC (L1/L2/L3)', value: `${(s.voltageAc1 || 0).toFixed(0)} V` },
                { label: 'Frequency', value: `${(s.frequency || 50).toFixed(1)} Hz` },
                { label: 'Energy Import', value: `${(s.energyAcImport || 0).toFixed(0)} Wh` },
                { label: 'Energy Export', value: `${(s.energyAcExport || 0).toFixed(0)} Wh` },
                { label: 'Max Charge Power', value: `${(s.chargePowerMax || 0).toFixed(0)} W` },
                { label: 'Max Discharge Power', value: `${(s.dischargePowerMax || 0).toFixed(0)} W` },
            );
            return cards;
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
                        this.powerSliderValue = state.effectivePowerW ?? this.maxPower;
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
            await Alpine.store('simulator').loadAllData();
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
                { label: 'Phase Mode', value: s.phaseMode || 'unknown' },
                { label: 'Rate Unit', value: s.chargingRateUnitAllowed || 'unknown' },
                { label: 'Meter Interval', value: s.meterValueSampleInterval ? `${s.meterValueSampleInterval} s` : 'unknown' },
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
        debugOpen: false,

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
        // Locking state to prevent double-click race conditions
        toggleLocks: {},
        // Reactive trigger that updates when simulators change
        simulatorsVersion: 0,
        // Home load state (mirrors Site Load dashboard)
        loadScenarios: [],
        homeLoads: { l1: 0, l2: 0, l3: 0 },
        homeLoadLinks: { l1: true, l2: true, l3: true },
        homeLoadLinksInitialized: false,
        homeLoadLinksDirty: false,
        homeLoadSiteId: null,
        homeLoadScenario: '',
        homeLoadLoading: false,
        homeLoadDebounce: null,
        pvScenarios: [
            { value: 'sunny', label: '☀️ Sunny' },
            { value: 'cloudy', label: '⛅ Cloudy' },
            { value: 'overcast', label: '☁️ Overcast' },
            { value: 'rainy', label: '🌧️ Rainy' },
            { value: 'night', label: '🌙 Night' },
            { value: 'peak_load', label: '⚡ Peak Load' }
        ],

        init() {
            // Watch for simulator changes to trigger reactivity
            this.$watch('$store.app.simulators', () => {
                this.simulatorsVersion++;
            });

            this.loadHomeLoadScenarios();
            this.syncHomeLoadFromSite();

            this.$watch('$store.site.data', () => {
                this.syncHomeLoadFromSite();
            });
        },

        isToggleLocked(key) {
            return this.toggleLocks[key] || false;
        },

        async withToggleLock(key, fn) {
            if (this.toggleLocks[key]) return;
            this.toggleLocks[key] = true;
            try {
                await fn();
            } finally {
                this.toggleLocks[key] = false;
            }
        },

        getSimulator(simId) {
            const app = Alpine.store('app');
            return app.simulators.find(s => s.id === simId) || null;
        },

        // Get simulator info with reactivity for non-inverter cards
        getSimInfo(simId) {
            // Access simulatorsVersion to trigger Alpine reactivity when simulators change
            void this.simulatorsVersion;
            const app = Alpine.store('app');
            const sim = app.simulators.find(s => s.id === simId);
            return sim || { serial: `ID ${simId}`, protocol: 'unknown', running: false };
        },

        // Check if site simulation is currently running
        isSiteSimulating() {
            return Alpine.store('site').data?.sim_running || false;
        },

        // Format load power for display
        formatLoadPower(watts) {
            if (Math.abs(watts) >= 1000) {
                return (watts / 1000).toFixed(1) + ' kW';
            }
            return Math.round(watts) + ' W';
        },

        async loadHomeLoadScenarios() {
            try {
                this.loadScenarios = await siteApi.getLoadScenarios();
                this.syncHomeLoadFromSite();
                const current = Alpine.store('site').data?.load_scenario;
                if (Alpine.store('site').data && !current && this.loadScenarios.length > 0) {
                    await this.setHomeLoadScenario(this.loadScenarios[0].key);
                }
            } catch (e) {
                console.error('Failed to load home load scenarios:', e);
            }
        },

        syncHomeLoadFromSite() {
            const data = Alpine.store('site').data;
            if (!data) return;

            const siteId = Alpine.store('site').selectedId;
            if (siteId !== this.homeLoadSiteId) {
                this.homeLoadSiteId = siteId;
                this.homeLoadLinksDirty = false;
                this.homeLoadLinksInitialized = false;
            }

            this.homeLoadScenario = data.load_scenario || '';
            const linked = data.load_linked ?? true;
            if (!this.homeLoadLinksInitialized || !this.homeLoadLinksDirty) {
                this.homeLoadLinks = { l1: linked, l2: linked, l3: linked };
                this.homeLoadLinksInitialized = true;
            }

            if (!this.homeLoadScenario && this.loadScenarios.length > 0 && !this.homeLoadLoading) {
                this.setHomeLoadScenario(this.loadScenarios[0].key);
            }

            const shouldSyncLoads = !this.homeLoadLoading || this.isSiteSimulating();
            if (shouldSyncLoads) {
                const loads = data.phase_loads || { l1: 0, l2: 0, l3: 0 };
                this.homeLoads = {
                    l1: loads.l1 || 0,
                    l2: loads.l2 || 0,
                    l3: loads.l3 || 0
                };
            }
        },

        getHomeLoadTotal() {
            return this.homeLoads.l1 + this.homeLoads.l2 + this.homeLoads.l3;
        },

        // Get load scenario name from key
        getLoadScenarioName(key) {
            if (!key) return 'Profile';
            const scenario = this.loadScenarios.find(s => s.key === key);
            if (scenario) return scenario.name;
            const fallbackNames = {
                gaming_family: 'Gaming Family',
                old_couple: 'Old Couple',
                always_on: 'Always-On Facility',
                work_from_home: 'Work From Home',
                night_owl: 'Night Owl'
            };
            return fallbackNames[key] || key;
        },

        getLoadScenarioIcon(key) {
            const icons = {
                static: '📌',
                gaming_family: '🎮',
                old_couple: '👴',
                always_on: '🏭',
                work_from_home: '💻',
                night_owl: '🦉'
            };
            return icons[key] || '📊';
        },

        async setHomeLoadScenario(scenario) {
            const siteId = Alpine.store('site').selectedId;
            if (!siteId) return;

            this.homeLoadLoading = true;
            this.homeLoadScenario = scenario;
            try {
                const result = await siteApi.setSiteLoadScenario(siteId, scenario);
                if (result && result.phase_loads) {
                    this.homeLoads = {
                        l1: result.phase_loads.l1,
                        l2: result.phase_loads.l2,
                        l3: result.phase_loads.l3
                    };
                }
            } catch (e) {
                console.error('Failed to set home load scenario:', e);
            }
            this.homeLoadLoading = false;
        },

        getHomeLinkedPhases(phase) {
            if (!this.homeLoadLinks[phase]) return [phase];
            const phases = ['l1', 'l2', 'l3'].filter(p => this.homeLoadLinks[p]);
            return phases.length > 0 ? phases : [phase];
        },

        areHomeLinksAllActive() {
            return this.homeLoadLinks.l1 && this.homeLoadLinks.l2 && this.homeLoadLinks.l3;
        },

        toggleHomeLoadLink(phase) {
            if (this.isSiteSimulating()) return;
            this.homeLoadLinksDirty = true;
            this.homeLoadLinks = {
                ...this.homeLoadLinks,
                [phase]: !this.homeLoadLinks[phase]
            };
            this.sendHomePhaseLoads();
        },

        onHomePhaseInput(phase, value) {
            if (this.isSiteSimulating()) return;
            const newValue = parseFloat(value);

            const linkedPhases = this.getHomeLinkedPhases(phase);
            if (linkedPhases.length > 1) {
                const oldValue = this.homeLoads[phase];
                const delta = newValue - oldValue;

                linkedPhases.forEach(p => {
                    this.homeLoads[p] = Math.max(0, Math.min(5000, this.homeLoads[p] + delta));
                });
            } else {
                this.homeLoads[phase] = newValue;
            }

            this.homeLoadLoading = true;
            if (this.homeLoadDebounce) {
                clearTimeout(this.homeLoadDebounce);
            }
            this.homeLoadDebounce = setTimeout(async () => {
                await this.sendHomePhaseLoads();
                this.homeLoadLoading = false;
            }, 120);
        },

        async sendHomePhaseLoads() {
            try {
                const siteId = Alpine.store('site').selectedId;
                await siteApi.setSitePhaseLoads(
                    siteId,
                    this.homeLoads.l1,
                    this.homeLoads.l2,
                    this.homeLoads.l3,
                    this.areHomeLinksAllActive()
                );
            } catch (e) {
                console.error('Failed to set home phase loads:', e);
            }
        },

        getInverterScenario(inv) {
            return inv?.scenario || 'sunny';
        },

        async setInverterScenario(simId, value) {
            const next = value || 'sunny';
            try {
                await api.updateAutomationConfig(simId, { scenario: next });
                await Alpine.store('app').loadSimulators();
            } catch (e) {
                console.error('Failed to set PV scenario:', e);
            }
        },

        // Get inverters from site's simulator_ids
        getSiteInverters() {
            // Access simulatorsVersion to trigger Alpine reactivity when simulators change
            void this.simulatorsVersion;
            const siteData = Alpine.store('site').data;
            if (!siteData?.simulator_ids) return [];
            const app = Alpine.store('app');
            return siteData.simulator_ids
                .map(id => app.simulators.find(s => s.id === id))
                .filter(s => s && s.category === 'inverter');
        },

        // Get non-inverter simulators (for the generic device loop)
        getNonInverterSimulators() {
            // Access simulatorsVersion to trigger Alpine reactivity when simulators change
            void this.simulatorsVersion;
            const siteData = Alpine.store('site').data;
            if (!siteData?.simulator_ids) return [];
            const app = Alpine.store('app');
            return siteData.simulator_ids
                .map(id => app.simulators.find(s => s.id === id))
                .filter(s => s && s.category !== 'inverter');
        },

        // Inverter PV/Battery helpers
        formatInverterPvPower(inv) {
            const power = inv?.pv_power || 0;
            if (power >= 1000) {
                return `${(power / 1000).toFixed(1)} kW`;
            }
            return `${power.toFixed(0)} W`;
        },

        formatInverterBatteryPower(inv) {
            const power = Math.abs(inv?.load || 0);
            const direction = inv?.battery_direction;
            let signedPower = power;
            if (direction === 'discharging') signedPower = -power;
            else if (direction === 'charging') signedPower = power;
            else if (direction === 'idle') signedPower = 0;

            const absP = Math.abs(signedPower);
            const sign = signedPower > 0 ? '+' : signedPower < 0 ? '-' : '';
            if (absP >= 1000) {
                return `${sign}${(absP / 1000).toFixed(1)} kW`;
            }
            return `${sign}${absP.toFixed(0)} W`;
        },

        getInverterBatteryPowerClass(inv) {
            // Use battery_direction from backend (based on command register 13050)
            const direction = inv?.battery_direction;
            if (direction === 'charging') return 'charging';
            if (direction === 'discharging') return 'discharging';
            return 'idle';
        },

        // Get signed battery power for slider value (charging = negative, discharging = positive)
        getInverterBatterySliderValue(inv) {
            const power = Math.abs(inv?.load || 0);
            const direction = inv?.battery_direction;
            if (direction === 'charging') return -power;
            if (direction === 'discharging') return power;
            return 0;
        },

        async setInverterPvPower(simId, value) {
            try {
                // Write to PV power register - uses Sungrow address as default
                // Backend resolves via RegisterSet for non-Sungrow devices
                await api.writeRegister(simId, 5016, value);
                await Alpine.store('app').loadSimulators();
            } catch (e) {
                console.error('Set inverter PV power error:', e);
            }
        },

        async setInverterBatteryPower(simId, value) {
            try {
                // Write battery power - uses Sungrow addresses as default
                // Backend resolves via RegisterSet for non-Sungrow devices
                await api.writeRegister(simId, 13021, Math.abs(value));

                // Write command register: 170=charge, 187=discharge, 204=stop
                let cmd = 204; // stop/idle
                if (value < 0) cmd = 170; // charge (negative = consuming)
                else if (value > 0) cmd = 187; // discharge (positive = generating)
                await api.writeRegister(simId, 13050, cmd);

                await Alpine.store('app').loadSimulators();
            } catch (e) {
                console.error('Set inverter battery power error:', e);
            }
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
            await this.withToggleLock(`mqtt-ev-${simId}`, async () => {
                try {
                    const sim = this.getSimulator(simId);
                    if (!sim?.running) return;
                    const current = this.getMqttEvConnected(simId);
                    await api.setState(simId, 'evConnected', !current);
                    await Alpine.store('app').loadSimulators();
                } catch (e) {
                    console.error('Toggle MQTT EV connected error:', e);
                }
            });
        },

        async setMqttPower(simId, power) {
            const sim = this.getSimulator(simId);
            if (!sim?.running) return;
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
            await this.withToggleLock(`ocpp-ev-${simId}`, async () => {
                try {
                    const sim = this.getSimulator(simId);
                    if (!sim?.running) return;
                    const current = this.getOcppEvConnected(simId);
                    await api.setState(simId, 'evConnected', !current);
                    await Alpine.store('app').loadSimulators();
                } catch (e) {
                    console.error('Toggle OCPP EV connected error:', e);
                }
            });
        },

        async setOcppPower(simId, power) {
            const sim = this.getSimulator(simId);
            if (!sim?.running) return;
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
            await this.withToggleLock(`modbus-${simId}`, async () => {
                try {
                    const current = this.getModbusRunning(simId);
                    if (current) {
                        await api.stopEmulator(simId);
                    } else {
                        await api.startEmulator(simId);
                    }
                    await Alpine.store('app').loadSimulators();
                } catch (e) {
                    console.error('Toggle Modbus running error:', e);
                }
            });
        },

        async toggleEmulator(simId) {
            await this.withToggleLock(`emu-${simId}`, async () => {
                try {
                    await Alpine.store('app').toggleEmulatorRunning(simId);
                } catch (e) {
                    console.error('Toggle emulator error:', e);
                }
            });
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

    // Site Simulation Controls (sidebar)
    Alpine.data('siteSimulationControls', () => ({
        localTimeMultiplier: 1,
        changingTimeMultiplier: false,
        resettingClock: false,
        speedOpen: false,
        speedOptions: [
            { value: 1, label: '1x' },
            { value: 10, label: '10x' },
            { value: 60, label: '60x' },
            { value: 360, label: '360x' },
            { value: 720, label: '720x' },
            { value: 1440, label: '1440x' },
            { value: 3600, label: '3600x' },
            { value: 7200, label: '7200x' }
        ],
        lastSiteId: null,

        init() {
            this.syncFromStore(true);

            this.$watch('$store.site.data', () => {
                const currentSiteId = Alpine.store('site').selectedId;

                if (this.lastSiteId !== currentSiteId) {
                    this.lastSiteId = currentSiteId;
                    this.changingTimeMultiplier = false;
                    this.syncFromStore(true);
                    return;
                }

                this.syncFromStore(false);
            });
        },

        syncFromStore(force) {
            const data = Alpine.store('site').data;
            if (!data) return;

            if (force || !this.changingTimeMultiplier) {
                this.localTimeMultiplier = data.time_multiplier || 1;
            }
        },

        async onTimeMultiplierChange(value) {
            const next = parseFloat(value) || 1;
            const current = Alpine.store('site').data?.time_multiplier || 1;
            if (next === current) return;

            this.changingTimeMultiplier = true;
            this.localTimeMultiplier = next;
            try {
                await Alpine.store('site').setTimeMultiplier(next);
            } finally {
                this.changingTimeMultiplier = false;
            }
        },

        get speedLabel() {
            const match = this.speedOptions.find(opt => opt.value === this.localTimeMultiplier);
            return match ? match.label : `${this.localTimeMultiplier}x`;
        },

        async selectSpeed(value) {
            this.speedOpen = false;
            await this.onTimeMultiplierChange(value);
        },

        get clockValue() {
            return Alpine.store('site').data?.simulated_time || 'Not started';
        },

        get clockTime() {
            const parts = this.parseClockParts(this.clockValue);
            return parts.time;
        },

        get clockDate() {
            const parts = this.parseClockParts(this.clockValue);
            return parts.date;
        },

        parseClockParts(value) {
            if (!value || value === 'Not started') {
                return { time: '--:--:--', date: '---- -- --' };
            }
            const parts = value.split(' ');
            return {
                date: parts[0] || '---- -- --',
                time: parts[1] || '--:--:--'
            };
        },

        async resetClock() {
            if (this.resettingClock) return;
            this.resettingClock = true;
            try {
                await Alpine.store('site').resetClock(Date.now());
            } finally {
                this.resettingClock = false;
            }
        }
    }));

    // Site Tabs Component
    Alpine.data('siteTabs', () => ({
        async createNewSite() {
            try {
                const sites = Alpine.store('site').sites;
                if (sites.length >= 5) {
                    await Alpine.store('confirm').alert('Limit Reached', 'Maximum of 5 sites allowed.');
                    return;
                }
                // Generate name: "Site 2", "Site 3", etc.
                const name = `Site ${sites.length + 1}`;

                // Create an empty site (no meter, no simulators)
                await Alpine.store('site').createSite(name, 0, [], 1000);
            } catch (e) {
                await Alpine.store('confirm').alert('Error', 'Error creating site: ' + e.message);
            }
        },

        async deleteSiteTab(siteId) {
            const siteStore = Alpine.store('site');
            if (siteStore.sites.length <= 1) return;

            const site = siteStore.sites.find(s => s.id === siteId);
            const siteName = site?.name || 'this site';

            const confirmed = await Alpine.store('confirm').prompt(
                'Delete Site',
                `Are you sure you want to delete ${siteName}?`,
                { okLabel: 'Delete', okVariant: 'danger' }
            );
            if (!confirmed) return;

            await siteStore.deleteSite(siteId);
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
            // For power balance: charging = positive (consuming like load), discharging = negative (generating like solar)
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
        p1Mode: 'direct',

        init() {
            // Initialize selected site from current meter assignment
            this.updateSelectedSite();
            this.updateP1Mode();

            // Watch for site changes
            this.$watch('$store.site.sites', () => {
                if (!this.changingSite) {
                    this.updateSelectedSite();
                }
                this.updateP1Mode();
            });
        },

        updateP1Mode() {
            if (!this.selectedSiteId) {
                this.p1Mode = 'direct';
                return;
            }
            const sites = Alpine.store('site').sites || [];
            const site = sites.find(s => s.id === this.selectedSiteId);
            this.p1Mode = site?.p1_mode || 'direct';
        },

        updateSelectedSite() {
            const simId = Alpine.store('simulator').id;
            const sites = Alpine.store('site').sites || [];
            // Check meter_id, meter_ids array, and simulator_ids array
            const connectedSite = sites.find(s =>
                s.meter_id === simId ||
                (s.meter_ids || []).includes(simId) ||
                (s.simulator_ids || []).includes(simId)
            );
            this.selectedSiteId = connectedSite?.id || null;
        },

        get sites() {
            return Alpine.store('site').sites || [];
        },

        getSiteName() {
            if (!this.selectedSiteId) return null;
            const site = this.sites.find(s => s.id === this.selectedSiteId);
            return site?.name || null;
        },

        get connectedSiteName() {
            return this.getSiteName();
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
                // Refresh simulator data to ensure UI stays in sync
                await Alpine.store('simulator').loadAllData();
                await Alpine.store('app').loadSimulators();
            } finally {
                this.changingSite = false;
            }
        },

        get meterPower() {
            const regs = Alpine.store('simulator').registers || [];
            const reg = regs.find(r => r.semantic_name === 'meter_power') ||
                        regs.find(r => r.address === 5600);
            return reg?.value || 0;
        },

        get importEnergy() {
            const regs = Alpine.store('simulator').registers || [];
            const reg = regs.find(r => r.semantic_name === 'total_import') ||
                        regs.find(r => r.address === 13036);
            return reg?.value || 0;
        },

        get exportEnergy() {
            const regs = Alpine.store('simulator').registers || [];
            const reg = regs.find(r => r.semantic_name === 'total_export') ||
                        regs.find(r => r.address === 13045);
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
                const siteStore = Alpine.store('site');
                await Promise.all([siteStore.loadSites(), siteStore.loadSiteData()]);
                this.updateP1Mode();
            } catch (e) {
                console.error('Set P1 mode error:', e);
                await Alpine.store('confirm').alert('Error', 'Error: ' + e.message);
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
            const simId = Alpine.store('simulator').id;
            const sites = Alpine.store('site').sites || [];
            // Check meter_id, meter_ids array, and simulator_ids array
            const connectedSite = sites.find(s =>
                s.meter_id === simId ||
                (s.meter_ids || []).includes(simId) ||
                (s.simulator_ids || []).includes(simId)
            );
            this.selectedSiteId = connectedSite?.id || null;
        },

        get sites() {
            return Alpine.store('site').sites || [];
        },

        getSiteName() {
            if (!this.selectedSiteId) return null;
            const site = this.sites.find(s => s.id === this.selectedSiteId);
            return site?.name || null;
        },

        get connectedSiteName() {
            return this.getSiteName();
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
                // Refresh simulator data to ensure UI stays in sync
                await Alpine.store('simulator').loadAllData();
                await Alpine.store('app').loadSimulators();
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
                color = 'rgba(255, 13, 13, 0.8)';
            } else if (soc <= 40) {
                color = 'rgba(255, 213, 0, 0.8)';
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
        batteryCapacityKWh: 10,

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
            this.syncFromConfig();
            this.lastSimulatorId = Alpine.store('simulator').id;

            // Watch for register changes
            this.$watch('$store.simulator.registers', () => {
                // Only sync if not dragging
                if (!this.dragging) {
                    this.syncFromRegisters();
                }
            });

            this.$watch('$store.simulator.data', () => {
                this.syncFromConfig();
            });

            // Watch for simulator changes
            this.$watch('$store.simulator.id', (newId) => {
                if (newId !== this.lastSimulatorId) {
                    this.lastSimulatorId = newId;
                    this.syncFromRegisters();
                    this.syncFromConfig();
                }
            });
        },

        syncFromRegisters() {
            const regs = Alpine.store('simulator').registers || [];

            // Find registers by semantic name (device-agnostic), fallback to address
            const findReg = (semanticName, fallbackAddr) => {
                return regs.find(r => r.semantic_name === semanticName) ||
                       regs.find(r => r.address === fallbackAddr);
            };

            // PV Power
            const pvReg = findReg('pv_power', 5016);
            if (pvReg) this.pvPower = pvReg.value || 0;

            // Battery Power
            const batPowerReg = findReg('battery_power', 13021);
            if (batPowerReg) {
                // Determine sign from command register
                const cmdReg = findReg('battery_cmd', 13050);
                const cmd = cmdReg?.value || 0;
                const absVal = Math.abs(batPowerReg.value || 0);
                // 170 = charge (negative, importing), 187 = discharge (positive, exporting)
                this.batteryPower = cmd === 170 ? -absVal : absVal;
            }

            // Battery SoC
            const socReg = findReg('battery_soc', 13022);
            if (socReg) this.batterySoc = socReg.value || 0;

            // Meter Power
            const meterReg = findReg('meter_power', 5600);
            if (meterReg) this.meterPower = meterReg.value || 0;
        },

        syncFromConfig() {
            const data = Alpine.store('simulator').data;
            if (!data) return;
            const capacity = data.battery_capacity_kwh;
            if (typeof capacity === 'number' && capacity > 0) {
                this.batteryCapacityKWh = capacity;
            }
        },

        // Get register address by semantic name, with fallback to a default address
        getRegAddr(semanticName, fallbackAddr) {
            const regs = Alpine.store('simulator').registers || [];
            const reg = regs.find(r => r.semantic_name === semanticName);
            return reg ? reg.address : fallbackAddr;
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
            const addr = this.getRegAddr('pv_power', 5016);
            await api.writeRegister(Alpine.store('simulator').id, addr, this.pvPower);
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
            const addr = this.getRegAddr('battery_soc', 13022);
            await api.writeRegister(Alpine.store('simulator').id, addr, this.batterySoc);
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
            const powerAddr = this.getRegAddr('battery_power', 13021);
            await api.writeRegister(simId, powerAddr, Math.abs(this.batteryPower));

            // Write command register: 170=charge, 187=discharge, 204=stop
            let cmd = 204; // stop
            if (this.batteryPower < 0) cmd = 170; // charge
            else if (this.batteryPower > 0) cmd = 187; // discharge
            const cmdAddr = this.getRegAddr('battery_cmd', 13050);
            await api.writeRegister(simId, cmdAddr, cmd);

            await Alpine.store('simulator').loadRegisters();
        },

        onBatteryCapacityInput(value) {
            const parsed = parseFloat(value);
            if (Number.isFinite(parsed)) {
                this.batteryCapacityKWh = parsed;
            }
        },

        async onBatteryCapacityChange(value) {
            const parsed = parseFloat(value);
            if (!Number.isFinite(parsed) || parsed < 2 || parsed > 300) {
                this.syncFromConfig();
                return;
            }
            this.batteryCapacityKWh = parsed;
            await Alpine.store('simulator').updateConfig({ battery_capacity_kwh: parsed });
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
            const confirmed = await Alpine.store('confirm').prompt(
                'Reset Values',
                'Reset all values to profile defaults?',
                { okLabel: 'Reset', okVariant: 'danger' }
            );
            if (!confirmed) return;
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
                color = 'rgba(255, 13, 13, 0.8)';
            } else if (soc <= 40) {
                color = 'rgba(255, 213, 0, 0.8)';
            } else {
                color = 'rgba(0, 255, 132, 0.8)';
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
                await Alpine.store('confirm').alert('Error', 'Error: ' + e.message);
            }
        },

        async removeDevice(simId) {
            try {
                await Alpine.store('site').removeSimulator(simId);
            } catch (e) {
                await Alpine.store('confirm').alert('Error', 'Error: ' + e.message);
            }
        },

        async changeMeter(meterId) {
            if (!meterId) return;
            try {
                await Alpine.store('site').setMeter(meterId);
            } catch (e) {
                await Alpine.store('confirm').alert('Error', 'Error: ' + e.message);
            }
        }
    }));

    // Phase Visualization Component
    Alpine.data('phaseVisualization', () => ({
        showSettingsModal: false,
        localPhaseFactors: { l1: 0.333, l2: 0.333, l3: 0.334 },
        localSiteName: '',
        localFuseSize: 16,
        localFuseProfile: 'manual',
        editingSiteName: false,
        editingPhaseFactors: false,
        changingFuseSize: false,
        changingFuseProfile: false,
        lastSiteId: null,
        fuseBlown: { l1: false, l2: false, l3: false },

        init() {
            // Initialize local settings from store
            this.syncSettingsFromStore(true);

            this.$watch('$store.site.data', () => {
                const currentSiteId = Alpine.store('site').selectedId;
                if (this.lastSiteId !== currentSiteId) {
                    this.lastSiteId = currentSiteId;
                    this.showSettingsModal = false;
                    this.editingSiteName = false;
                    this.editingPhaseFactors = false;
                    this.changingFuseSize = false;
                    this.changingFuseProfile = false;
                    this.syncSettingsFromStore(true);
                    return;
                }

                // Avoid overwriting inputs while editing
                this.syncSettingsFromStore(false);
            });

            // Load fuse state from localStorage
            this.loadFuseState();
        },

        loadFuseState() {
            const siteId = Alpine.store('site').selectedId;
            if (!siteId) return;
            try {
                const saved = localStorage.getItem(`fuse-state-${siteId}`);
                if (saved) {
                    const state = JSON.parse(saved);
                    this.fuseBlown = {
                        l1: state.l1 || false,
                        l2: state.l2 || false,
                        l3: state.l3 || false
                    };
                }
            } catch (e) {
                console.error('Load fuse state error:', e);
            }
        },

        saveFuseState() {
            const siteId = Alpine.store('site').selectedId;
            if (!siteId) return;
            try {
                localStorage.setItem(`fuse-state-${siteId}`, JSON.stringify(this.fuseBlown));
            } catch (e) {
                console.error('Save fuse state error:', e);
            }
        },

        syncSettingsFromStore(force) {
            const data = Alpine.store('site').data;
            if (!data) return;

            if (force || !this.editingSiteName) {
                this.localSiteName = data.name || '';
            }
            if (force || !this.changingFuseSize) {
                this.localFuseSize = data.fuse_size || 16;
            }
            if (force || !this.changingFuseProfile) {
                this.localFuseProfile = data.fuse_profile || 'manual';
            }
            if (force || !this.editingPhaseFactors) {
                const factors = data.phase_factors;
                if (factors) {
                    this.localPhaseFactors = {
                        l1: factors.l1 || 0.333,
                        l2: factors.l2 || 0.333,
                        l3: factors.l3 || 0.334
                    };
                }
            }
            if (data.fuse_blown) {
                this.fuseBlown = {
                    l1: !!data.fuse_blown.l1,
                    l2: !!data.fuse_blown.l2,
                    l3: !!data.fuse_blown.l3
                };
            }
        },

        getFuseProfileLabel() {
            const profile = this.$store.site.data?.fuse_profile;
            const labels = {
                'iec_gg': 'gG/gL curve',
                'iec_gm': 'gM curve',
                'iec_am': 'aM curve',
                'iec_gr': 'gR curve',
                'iec_gs': 'gS curve',
            };
            return labels[profile] || 'no auto tripping';
        },

        openSettingsModal() {
            this.syncSettingsFromStore(true);
            this.showSettingsModal = true;
        },

        closeSettingsModal() {
            this.showSettingsModal = false;
            this.editingSiteName = false;
            this.editingPhaseFactors = false;
            this.changingFuseSize = false;
            this.changingFuseProfile = false;
        },

        onSiteNameFocus() {
            this.editingSiteName = true;
        },

        async onSiteNameBlur() {
            this.editingSiteName = false;
            const trimmed = this.localSiteName.trim();
            const current = Alpine.store('site').data?.name || '';
            if (trimmed && trimmed !== current) {
                await Alpine.store('site').updateConfig({ name: trimmed });
            }
        },

        onPhaseFactorFocus() {
            this.editingPhaseFactors = true;
        },

        onPhaseFactorBlur() {
            this.editingPhaseFactors = false;
        },

        async onFuseSizeChange(value) {
            const next = parseInt(value, 10) || 16;
            const current = Alpine.store('site').data?.fuse_size || 16;
            if (next === current) return;

            this.changingFuseSize = true;
            this.localFuseSize = next;
            try {
                await Alpine.store('site').updateConfig({ fuse_size: next });
            } finally {
                this.changingFuseSize = false;
            }
        },

        async onFuseProfileChange(value) {
            const next = value || 'manual';
            const current = Alpine.store('site').data?.fuse_profile || 'manual';
            if (next === current) return;

            this.changingFuseProfile = true;
            this.localFuseProfile = next;
            try {
                await Alpine.store('site').updateConfig({ fuse_profile: next });
            } finally {
                this.changingFuseProfile = false;
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
            this.saveFuseState();
            Alpine.store('site').updateConfig({ fuse_blown: this.fuseBlown });
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
                    const r = 255;
                    const g = Math.round(213 - (t * 106)); // 213 -> 107
                    const b = Math.round(0 + (t * 0)); // 0 -> 0
                    color = `rgb(${r}, ${g}, ${b})`;
                } else {
                    // Orange to red
                    const t = (overloadRatio - 0.5) * 2;
                    const r = 255;
                    const g = Math.round(107 - (t * 94)); // 107 -> 13
                    const b = Math.round(0 + (t * 13)); // 0 -> 13
                    color = `rgb(${r}, ${g}, ${b})`;
                }
            }

            return `width: ${percentage}%; background: ${color};`;
        },

        getPhasePowers() {
            const powers = Alpine.store('site').data?.phase_powers || { l1: 0, l2: 0, l3: 0 };
            return {
                l1: powers.l1 || 0,
                l2: powers.l2 || 0,
                l3: powers.l3 || 0
            };
        },

        getFuseOverload(phase) {
            const overload = Alpine.store('site').data?.fuse_overload || {};
            return overload[phase] || null;
        },

        hasFuseCountdown(phase) {
            const info = this.getFuseOverload(phase);
            if (!info || !info.active) return false;
            if (this.fuseBlown[phase]) return false;
            return (info.remaining_s || 0) > 0;
        },

        getFuseCountdownLabel(phase) {
            const info = this.getFuseOverload(phase);
            if (!info) return '';
            const remaining = info.remaining_s || 0;
            if (remaining <= 0) return '';
            if (remaining < 1) return 'Trip now';
            if (remaining < 60) return `Trip in ${Math.ceil(remaining)}s`;
            if (remaining < 3600) return `Trip in ${Math.ceil(remaining / 60)}m`;
            return `Trip in ${Math.ceil(remaining / 3600)}h`;
        },

        getFuseCountdownTitle(phase) {
            const info = this.getFuseOverload(phase);
            if (!info || !info.active) return '';
            if (!info.ratio) return '';
            return `${info.ratio.toFixed(2)}x In`;
        },

        getFuseCountdownClass(phase) {
            const info = this.getFuseOverload(phase);
            if (!info || !info.active) return '';
            const remaining = info.remaining_s || 0;
            if (remaining <= 5) return 'danger';
            if (remaining <= 15) return 'warning';
            return '';
        },

        // Bidirectional bar: center = 0, left = export (negative), right = import (positive)
        // Scale: 0-35% each side = normal (0 to fuse limit), 35-50% = overload (fuse to 3x)
        getBidirectionalBarStyle(phasePower, isBlown) {
            if (isBlown) {
                return 'width: 0%; left: 50%; background: transparent;';
            }

            const fuseSize = this.getFuseSize();
            const fuseWatts = fuseSize * 230;  // Voltage assumption
            const maxWatts = fuseWatts * 3;    // 3x overload

            const absPower = Math.abs(phasePower);
            const isExport = phasePower < 0;

            let barWidth, color;

            if (absPower <= fuseWatts) {
                // Normal range: 0 to fuseWatts maps to 0-35% of half-bar
                barWidth = (absPower / fuseWatts) * 35;
                color = isExport ? 'var(--color-export)' : 'var(--color-import)';
            } else {
                // Overload range: fuseWatts to maxWatts maps to 35-50%
                const overloadRatio = Math.min((absPower - fuseWatts) / (maxWatts - fuseWatts), 1);
                barWidth = 35 + (overloadRatio * 15);

                // Blend base color towards red as overload increases
                const base = isExport ? [33, 150, 243] : [255, 107, 0];  // blue : orange
                const red = [255, 13, 13];
                const r = Math.round(base[0] + (red[0] - base[0]) * overloadRatio);
                const g = Math.round(base[1] + (red[1] - base[1]) * overloadRatio);
                const b = Math.round(base[2] + (red[2] - base[2]) * overloadRatio);
                color = `rgb(${r}, ${g}, ${b})`;
            }

            // Position: export grows left from center, import grows right
            const left = isExport ? (50 - barWidth) : 50;
            return `width: ${barWidth}%; left: ${left}%; background: ${color};`;
        },

        formatPhasePower(power) {
            const abs = Math.abs(power);
            if (abs >= 1000) {
                return `${(power / 1000).toFixed(2)} kW`;
            }
            return `${power.toFixed(0)} W`;
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
                this.closeSettingsModal();
            } catch (e) {
                await Alpine.store('confirm').alert('Error', 'Error: ' + e.message);
            }
        }
    }));

    // Confirmation modal component (just a wrapper, logic is in store)
    Alpine.data('confirmModal', () => ({}));

    // Settings Panel Component
    Alpine.data('settingsPanel', () => ({
        modbusBasePort: 5000,
        mqttBasePort: 1883,
        ocppBasePort: 9000,
        modbusDisplayOffset: 0,
        siteTickMs: 500,
        originalMqttBasePort: 1883,
        originalModbusBasePort: 5000,
        originalOcppBasePort: 9000,
        originalModbusDisplayOffset: 0,
        mqttDisplayOffset: 0,
        ocppDisplayOffset: 0,
        originalMqttDisplayOffset: 0,
        originalOcppDisplayOffset: 0,
        originalSiteTickMs: 500,
        saving: false,
        needsRestart: false,
        loaded: false,

        get hasChanges() {
            return this.modbusBasePort !== this.originalModbusBasePort ||
                this.mqttBasePort !== this.originalMqttBasePort ||
                this.ocppBasePort !== this.originalOcppBasePort ||
                this.modbusDisplayOffset !== this.originalModbusDisplayOffset ||
                this.mqttDisplayOffset !== this.originalMqttDisplayOffset ||
                this.ocppDisplayOffset !== this.originalOcppDisplayOffset ||
                this.siteTickMs !== this.originalSiteTickMs;
        },

        get isDockerMode() {
            return this.mqttDisplayOffset > 0 || this.modbusDisplayOffset > 0 || this.ocppDisplayOffset > 0;
        },

        get mqttDisplayPort() {
            return this.mqttBasePort + this.mqttDisplayOffset;
        },

        get modbusDisplayPort() {
            return this.modbusBasePort + this.modbusDisplayOffset;
        },

        get ocppDisplayPort() {
            return this.ocppBasePort + this.ocppDisplayOffset;
        },

        async init() {
            try {
                const settings = await api.getSettings();
                this.modbusBasePort = settings.modbusBasePort || 5000;
                this.mqttBasePort = settings.mqttBasePort || 1883;
                this.ocppBasePort = settings.ocppBasePort || 9000;
                this.modbusDisplayOffset = settings.modbusDisplayOffset || 0;
                this.mqttDisplayOffset = settings.mqttDisplayOffset || 0;
                this.ocppDisplayOffset = settings.ocppDisplayOffset || 0;
                this.siteTickMs = settings.siteTickMs || 500;
                this.originalModbusBasePort = this.modbusBasePort;
                this.originalMqttBasePort = this.mqttBasePort;
                this.originalOcppBasePort = this.ocppBasePort;
                this.originalModbusDisplayOffset = this.modbusDisplayOffset;
                this.originalMqttDisplayOffset = this.mqttDisplayOffset;
                this.originalOcppDisplayOffset = this.ocppDisplayOffset;
                this.originalSiteTickMs = this.siteTickMs;
                this.loaded = true;
            } catch (e) {
                console.error('Failed to load settings:', e);
                this.loaded = true;
            }
        },

        async save() {
            this.saving = true;
            try {
                await api.saveSettings({
                    modbusBasePort: this.modbusBasePort,
                    mqttBasePort: this.mqttBasePort,
                    ocppBasePort: this.ocppBasePort,
                    modbusDisplayOffset: this.modbusDisplayOffset,
                    mqttDisplayOffset: this.mqttDisplayOffset,
                    ocppDisplayOffset: this.ocppDisplayOffset,
                    siteTickMs: this.siteTickMs
                });
                this.originalModbusBasePort = this.modbusBasePort;
                this.originalMqttBasePort = this.mqttBasePort;
                this.originalOcppBasePort = this.ocppBasePort;
                this.originalModbusDisplayOffset = this.modbusDisplayOffset;
                this.originalMqttDisplayOffset = this.mqttDisplayOffset;
                this.originalOcppDisplayOffset = this.ocppDisplayOffset;
                this.originalSiteTickMs = this.siteTickMs;
                this.needsRestart = true;
            } catch (e) {
                console.error('Failed to save settings:', e);
                await Alpine.store('confirm').alert(
                    'Error',
                    'Failed to save settings: ' + e.message
                );
            }
            this.saving = false;
        },

        reset() {
            this.modbusBasePort = this.originalModbusBasePort;
            this.mqttBasePort = this.originalMqttBasePort;
            this.ocppBasePort = this.originalOcppBasePort;
            this.modbusDisplayOffset = this.originalModbusDisplayOffset;
            this.mqttDisplayOffset = this.originalMqttDisplayOffset;
            this.ocppDisplayOffset = this.originalOcppDisplayOffset;
            this.siteTickMs = this.originalSiteTickMs;
        }
    }));

    // Site Load Controls Component
    Alpine.data('siteLoadControls', () => ({
        scenarios: [],
        localLoads: { l1: 333, l2: 333, l3: 334 },
        loadLinked: true,
        activeScenario: '',
        loading: false,
        sliderDebounce: null,

        async init() {
            // Load scenarios
            try {
                this.scenarios = await siteApi.getLoadScenarios();
                const current = Alpine.store('site').data?.load_scenario;
                if (Alpine.store('site').data && !current && this.scenarios.length > 0) {
                    await this.onScenarioChange(this.scenarios[0].key);
                }
            } catch (e) {
                console.error('Failed to load scenarios:', e);
            }

            // Sync from site data
            this.syncFromSite();

            // Watch for site changes
            this.$watch('$store.site.data', () => {
                this.syncFromSite();
            });
        },

        syncFromSite() {
            const data = Alpine.store('site').data;
            if (!data) return;

            this.activeScenario = data.load_scenario || '';
            this.loadLinked = data.load_linked ?? true;

            if (!this.activeScenario && this.scenarios.length > 0 && !this.loading) {
                this.onScenarioChange(this.scenarios[0].key);
            }

            // Only sync loads if not currently dragging a slider
            if (!this.loading || this.isSimulationRunning()) {
                const loads = data.phase_loads || { l1: 333, l2: 333, l3: 334 };
                this.localLoads = { l1: loads.l1, l2: loads.l2, l3: loads.l3 };
            }
        },

        isSimulationRunning() {
            return Alpine.store('site').data?.sim_running || false;
        },

        shouldDisableSliders() {
            return this.isSimulationRunning() && this.activeScenario !== 'static';
        },

        getScenarioName() {
            if (!this.activeScenario) return 'Profile';
            const s = this.scenarios.find(s => s.key === this.activeScenario);
            return s ? s.name : this.activeScenario;
        },

        getScenarioDescription() {
            if (!this.activeScenario) return '';
            const s = this.scenarios.find(s => s.key === this.activeScenario);
            return s ? s.description : '';
        },

        getScenarioIcon(key) {
            const icons = {
                static: '📌',
                gaming_family: '🎮',
                old_couple: '👴',
                always_on: '🏭',
                work_from_home: '💻',
                night_owl: '🦉'
            };
            return icons[key] || '📊';
        },

        getTotalPower() {
            return this.localLoads.l1 + this.localLoads.l2 + this.localLoads.l3;
        },

        formatPower(watts) {
            if (Math.abs(watts) >= 1000) {
                return (watts / 1000).toFixed(1) + ' kW';
            }
            return Math.round(watts) + ' W';
        },

        async onScenarioChange(scenario) {
            this.loading = true;
            this.activeScenario = scenario;
            try {
                const siteId = Alpine.store('site').selectedId;
                if (!siteId) {
                    this.loading = false;
                    return;
                }
                const result = await siteApi.setSiteLoadScenario(siteId, scenario);
                if (result && result.phase_loads) {
                    this.localLoads = {
                        l1: result.phase_loads.l1,
                        l2: result.phase_loads.l2,
                        l3: result.phase_loads.l3
                    };
                }
            } catch (e) {
                console.error('Failed to set scenario:', e);
            }
            this.loading = false;
        },

        toggleLinked() {
            if (this.shouldDisableSliders()) return;
            this.loadLinked = !this.loadLinked;
            this.sendPhaseLoads();
        },

        onPhaseSliderInput(phase, value) {
            if (this.shouldDisableSliders()) return;
            const newValue = parseFloat(value);

            if (this.loadLinked) {
                // Calculate proportional change for all phases
                const oldValue = this.localLoads[phase];
                const delta = newValue - oldValue;

                // Apply same delta to all phases
                this.localLoads.l1 = Math.max(0, Math.min(5000, this.localLoads.l1 + delta));
                this.localLoads.l2 = Math.max(0, Math.min(5000, this.localLoads.l2 + delta));
                this.localLoads.l3 = Math.max(0, Math.min(5000, this.localLoads.l3 + delta));
            } else {
                this.localLoads[phase] = newValue;
            }

            // Debounce the API call
            if (this.sliderDebounce) {
                clearTimeout(this.sliderDebounce);
            }
            this.sliderDebounce = setTimeout(() => {
                this.sendPhaseLoads();
            }, 100);
        },

        async sendPhaseLoads() {
            try {
                const siteId = Alpine.store('site').selectedId;
                await siteApi.setSitePhaseLoads(
                    siteId,
                    this.localLoads.l1,
                    this.localLoads.l2,
                    this.localLoads.l3,
                    this.loadLinked
                );
            } catch (e) {
                console.error('Failed to set phase loads:', e);
            }
        }
    }));
});
