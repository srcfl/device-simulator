// OCPP Protocol Module
// Handles OCPP charger-specific UI and logic

export function ocppControls() {
    return {
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
                        this.powerSliderValue = state.targetPowerW ?? this.maxPower;
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

            // Start charging if power > 0 and not currently charging
            if (powerW > 0 && !this.wasCharging) {
                await store.setState('startCharging', true);
                this.wasCharging = true;
            }

            // Stop charging if power is 0
            if (powerW === 0 && this.wasCharging) {
                await store.setState('stopCharging', true);
                this.wasCharging = false;
            }

            // Set the power level
            await store.setState('chargingPower', powerW);
        },

        async toggleEVConnected() {
            await Alpine.store('simulator').setState('evConnected', !this.evConnected);
        },

        onOCPPUrlInput(event) {
            if (this.ocppUrlDebounceTimer) {
                clearTimeout(this.ocppUrlDebounceTimer);
            }
            this.ocppUrlDebounceTimer = setTimeout(() => {
                this.saveOCPPUrl(event.target.value.trim());
            }, 500);
        },

        async saveOCPPUrl(url) {
            await Alpine.store('simulator').updateConfig({ ocpp_url: url });
        }
    };
}

export function ocppState() {
    return {
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
    };
}

export function ocppDebug() {
    return {
        copySuccess: false,

        get debug() {
            return Alpine.store('simulator').debugMeterValues || {};
        },

        get hasDebug() {
            return Object.keys(this.debug).length > 0;
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
    };
}
