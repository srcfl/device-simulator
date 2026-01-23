// Power Slider Component
// Reusable bi-directional power slider for MQTT and OCPP

export function powerSlider() {
    return {
        value: 0,
        min: -11000,
        max: 11000,
        dragging: false,
        disabled: false,
        snapThreshold: 500,

        init() {
            // Initialize from props if available
        },

        get displayValue() {
            const absValue = Math.abs(this.value);
            if (this.value === 0) return '0 W';
            if (this.value > 0) {
                return absValue >= 1000
                    ? `Discharging +${(this.value / 1000).toFixed(1)} kW`
                    : `Discharging +${this.value} W`;
            } else {
                return absValue >= 1000
                    ? `Charging ${(this.value / 1000).toFixed(1)} kW`
                    : `Charging ${this.value} W`;
            }
        },

        get displayClass() {
            if (this.disabled) return 'power-slider-value disabled';
            if (this.value === 0) return 'power-slider-value stopped';
            if (this.value > 0) return 'power-slider-value discharging';
            return 'power-slider-value charging';
        },

        onInput(event) {
            this.dragging = true;
            let val = parseInt(event.target.value);

            // Snap to zero if within threshold
            if (Math.abs(val) < this.snapThreshold) {
                val = 0;
                event.target.value = 0;
            }

            this.value = val;
        },

        async onChange(event) {
            this.dragging = false;
            let val = parseInt(event.target.value);

            // Snap to zero if within threshold
            if (Math.abs(val) < this.snapThreshold) {
                val = 0;
                event.target.value = 0;
            }

            this.value = val;

            // Emit change event for parent to handle
            this.$dispatch('power-change', { value: val });
        }
    };
}

// SoC Slider Component
export function socSlider() {
    return {
        value: 50,
        dragging: false,

        get displayValue() {
            return `${Math.round(this.value)}%`;
        },

        get displayClass() {
            if (this.value <= 20) return 'soc-slider-value low';
            if (this.value <= 40) return 'soc-slider-value medium';
            return 'soc-slider-value high';
        },

        onInput(event) {
            this.dragging = true;
            this.value = parseInt(event.target.value);
        },

        async onChange(event) {
            this.dragging = false;
            this.value = parseInt(event.target.value);
            this.$dispatch('soc-change', { value: this.value });
        }
    };
}

// OCPP Power Slider (0 to max, charge only)
export function ocppPowerSlider() {
    return {
        value: 0,
        max: 7700,
        dragging: false,
        disabled: false,
        evConnected: false,
        wasCharging: false,

        get displayValue() {
            if (!this.evConnected) return 'EV not connected';
            if (this.value === 0) return 'Not charging';
            return this.value >= 1000
                ? `Charging at ${(this.value / 1000).toFixed(1)} kW`
                : `Charging at ${this.value} W`;
        },

        get displayClass() {
            if (!this.evConnected) return 'power-slider-value disabled';
            if (this.value === 0) return 'power-slider-value stopped';
            return 'power-slider-value charging';
        },

        onInput(event) {
            this.dragging = true;
            this.value = parseInt(event.target.value);
        },

        async onChange(event) {
            this.dragging = false;
            const powerW = parseInt(event.target.value);
            this.value = powerW;

            this.$dispatch('ocpp-power-change', {
                value: powerW,
                wasCharging: this.wasCharging
            });

            if (powerW > 0) {
                this.wasCharging = true;
            } else {
                this.wasCharging = false;
            }
        }
    };
}
