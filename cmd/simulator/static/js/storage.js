// localStorage Persistence Layer

const STORAGE_KEY = 'device-simulator-configs';

export const storage = {
    load() {
        try {
            const data = localStorage.getItem(STORAGE_KEY);
            if (!data) return { version: 1, simulators: [] };
            return JSON.parse(data);
        } catch (e) {
            console.error('Invalid localStorage data:', e);
            return { version: 1, simulators: [] };
        }
    },

    save(configs) {
        try {
            localStorage.setItem(STORAGE_KEY, JSON.stringify(configs));
        } catch (e) {
            console.error('Failed to save to localStorage:', e);
        }
    },

    generateClientId() {
        return 'sim-' + crypto.randomUUID();
    },

    saveSimulatorConfig(config) {
        const saved = this.load();
        config.lastModified = new Date().toISOString();
        if (!config.createdAt) config.createdAt = config.lastModified;
        saved.simulators.push(config);
        this.save(saved);
    },

    updateConfig(serial, updates) {
        const saved = this.load();
        const config = saved.simulators.find(s => s.serial === serial);
        if (config) {
            Object.assign(config, updates);
            config.lastModified = new Date().toISOString();
            this.save(saved);
        }
    },

    updateConfigByOldSerial(oldSerial, newSerial, updates) {
        const saved = this.load();
        const config = saved.simulators.find(s => s.serial === oldSerial);
        if (config) {
            config.serial = newSerial;
            Object.assign(config, updates);
            config.lastModified = new Date().toISOString();
            this.save(saved);
        }
    },

    remove(serial) {
        const saved = this.load();
        saved.simulators = saved.simulators.filter(s => s.serial !== serial);
        this.save(saved);
    },

    findBySerial(serial) {
        const saved = this.load();
        return saved.simulators.find(s => s.serial === serial);
    },

    getAll() {
        return this.load().simulators;
    },

    syncWithServer(serverSims) {
        const saved = this.load();
        const serverBySerial = new Map(serverSims.map(s => [s.serial, s]));
        const savedBySerial = new Map(saved.simulators.map(s => [s.serial, s]));

        // Add server simulators not in localStorage
        for (const sim of serverSims) {
            if (!savedBySerial.has(sim.serial)) {
                saved.simulators.push({
                    clientId: this.generateClientId(),
                    type: sim.category,
                    serial: sim.serial,
                    slaveId: sim.slave_id || 1,
                    profile: sim.profile || null,
                    automation: {
                        wasRunning: sim.running || false,
                        timeMultiplier: 1,
                        scenario: 'sunny'
                    },
                    createdAt: new Date().toISOString(),
                    lastModified: new Date().toISOString()
                });
            }
        }

        // Remove localStorage entries for simulators no longer on server
        if (serverSims.length > 0) {
            saved.simulators = saved.simulators.filter(s => serverBySerial.has(s.serial));
        }

        this.save(saved);
    }
};
