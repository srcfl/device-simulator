// MQTT Protocol Module
// Handles MQTT V2X charger-specific UI and logic

export function mqttControls() {
    return {
        powerSliderValue: 0,
        socSliderValue: 50,
        powerDragging: false,
        socDragging: false,
        controlMode: 'CONTROLLABLE',
        SNAP_THRESHOLD: 500,

        init() {
            this.$watch('$store.simulator.data', (data) => {
                if (data?.state && !this.powerDragging) {
                    const state = data.state;
                    this.powerSliderValue = state.requestedTargetPower || 0;
                    if (!this.socDragging) {
                        this.socSliderValue = state.soc ?? 50;
                    }
                    this.controlMode = state.controlMode || 'CONTROLLABLE';
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
            await Alpine.store('simulator').setState('controlMode', this.controlMode);
        }
    };
}

export function mqttState() {
    return {
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
    };
}

export function mqttClients() {
    return {
        init() {
            // Clients are loaded by the simulator store
        },

        get clients() {
            const store = Alpine.store('simulator');
            return store.clients || [];
        },

        get count() {
            return this.clients.length;
        }
    };
}
