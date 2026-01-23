// Toggle Switch Component
// Reusable toggle switch for on/off states

export function toggleSwitch() {
    return {
        on: false,
        label: '',

        toggle() {
            this.on = !this.on;
            this.$dispatch('toggle-change', { value: this.on });
        }
    };
}
