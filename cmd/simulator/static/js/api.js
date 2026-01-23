// API Layer - All fetch requests for the simulator

export const api = {
    // System
    async getSystemInfo() {
        const resp = await fetch('/api/system');
        return resp.json();
    },

    // Simulators
    async getSimulators(filters = {}) {
        let url = '/api/simulators';
        const params = new URLSearchParams();
        if (filters.protocol) params.append('protocol', filters.protocol);
        if (filters.category) params.append('category', filters.category);
        if (params.toString()) url += '?' + params.toString();

        const resp = await fetch(url);
        const data = await resp.json();
        return Array.isArray(data) ? data : [];
    },

    async createSimulator(type) {
        const resp = await fetch('/api/simulators', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ type })
        });
        if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text);
        }
        return resp.json();
    },

    async deleteSimulator(id) {
        const resp = await fetch(`/api/simulators/${id}`, { method: 'DELETE' });
        if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text);
        }
        return resp.json();
    },

    // Simulator details
    async getSimulator(id) {
        const resp = await fetch(`/api/simulators/${id}`);
        return resp.json();
    },

    async updateConfig(id, config) {
        const resp = await fetch(`/api/simulators/${id}/config`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(config)
        });
        return resp.json();
    },

    // Registers (Modbus)
    async getRegisters(id) {
        const resp = await fetch(`/api/simulators/${id}/registers`);
        return resp.json();
    },

    async writeRegister(id, address, value) {
        const resp = await fetch(`/api/simulators/${id}/registers/${address}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ value })
        });
        return resp.json();
    },

    // Logs
    async getLogs(id, limit = 50) {
        const resp = await fetch(`/api/simulators/${id}/logs?limit=${limit}`);
        return resp.json();
    },

    // Profiles
    async getProfiles(id) {
        const resp = await fetch(`/api/simulators/${id}/profiles`);
        return resp.json();
    },

    async applyProfile(id, profileName) {
        const resp = await fetch(`/api/simulators/${id}/profiles/${profileName}/apply`, {
            method: 'POST'
        });
        return resp.json();
    },

    // Automation
    async getAutomation(id) {
        const resp = await fetch(`/api/simulators/${id}/automation`);
        return resp.json();
    },

    async startAutomation(id) {
        const resp = await fetch(`/api/simulators/${id}/automation/start`, { method: 'POST' });
        return resp.json();
    },

    async stopAutomation(id) {
        const resp = await fetch(`/api/simulators/${id}/automation/stop`, { method: 'POST' });
        return resp.json();
    },

    async updateAutomationConfig(id, config) {
        const resp = await fetch(`/api/simulators/${id}/automation/config`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(config)
        });
        return resp.json();
    },

    // State (MQTT/OCPP)
    async setState(id, key, value) {
        const resp = await fetch(`/api/simulators/${id}/state`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ key, value })
        });
        return resp.json();
    },

    // MQTT specific
    async getClients(id) {
        const resp = await fetch(`/api/simulators/${id}/clients`);
        return resp.json();
    },

    async wakeUp(id, wake) {
        const resp = await fetch(`/api/simulators/${id}/wakeup`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ wake })
        });
        return resp.json();
    },

    async setPower(id, power) {
        const resp = await fetch(`/api/simulators/${id}/power`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ power })
        });
        return resp.json();
    },

    // OCPP specific
    async getDebugMeterValues(id) {
        const resp = await fetch(`/api/simulators/${id}/debug/meter-values`);
        return resp.json();
    },

    // Reset
    async reset(id) {
        const resp = await fetch(`/api/simulators/${id}/reset`, { method: 'POST' });
        return resp.json();
    }
};
