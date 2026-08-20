// Modbus Protocol Module
// Handles Modbus-specific UI and logic

export function modbusControls() {
    return {
        scenario: 'sunny',
        timeMultiplier: 1,

        scenarios: [
            { value: 'sunny', label: '☀️ Sunny Day' },
            { value: 'cloudy', label: '⛅ Cloudy Day' },
            { value: 'overcast', label: '☁️ Overcast' },
            { value: 'rainy', label: '🌧️ Rainy Day' },
            { value: 'night', label: '🌙 Night Mode' },
            { value: 'peak_load', label: '⚡ Peak Load' },
            { value: 'battery_test', label: '🔋 Battery Cycle' },
            { value: 'physical', label: '🌿 Physical plant' }
        ],

        speeds: [
            { value: 1, label: '1× Real-time' },
            { value: 60, label: '60×' },
            { value: 360, label: '360×' },
            { value: 3600, label: '3600×' }
        ],

        init() {
            this.$watch('$store.simulator.automation', (automation) => {
                if (automation) {
                    this.scenario = automation.scenario || 'sunny';
                    this.timeMultiplier = automation.time_multiplier || 1;
                }
            });
        },

        async updateConfig() {
            await Alpine.store('simulator').updateAutomationConfig(
                parseInt(this.timeMultiplier),
                this.scenario
            );
        },

        get simulatedTime() {
            const automation = Alpine.store('simulator').automation;
            if (automation?.simulated_time) {
                return new Date(automation.simulated_time).toLocaleString();
            }
            return '';
        }
    };
}

export function modbusRegisters() {
    return {
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
    };
}
