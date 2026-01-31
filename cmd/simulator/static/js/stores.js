// Alpine Stores - Must load before Alpine.js (not a module)

// Wails detection - window.go is set when running in Wails desktop mode
const isWails = typeof window.go !== 'undefined';

// Wails event listeners for real-time updates (replaces polling in desktop mode)
if (isWails && window.runtime) {
    // Listen for simulator updates
    window.runtime.EventsOn('simulator:updated', () => {
        if (typeof Alpine !== 'undefined' && Alpine.store('app')) {
            Alpine.store('app').loadSimulators();
        }
    });

    // Listen for site updates
    window.runtime.EventsOn('site:updated', () => {
        if (typeof Alpine !== 'undefined' && Alpine.store('site')) {
            Alpine.store('site').loadSites();
            if (Alpine.store('site').selectedId) {
                Alpine.store('site').loadSiteData();
            }
        }
    });
}

// Wails API wrapper - uses bindings when available, falls back to fetch
const api = {
    async getSimulators() {
        if (isWails) {
            const data = await window.go.main.App.GetSimulators({});
            return Array.isArray(data) ? data : [];
        }
        const resp = await fetch('/api/simulators');
        const data = await resp.json();
        return Array.isArray(data) ? data : [];
    },

    async createSimulator(type) {
        if (isWails) {
            return await window.go.main.App.CreateSimulator(type);
        }
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
        if (isWails) {
            await window.go.main.App.DeleteSimulator(id);
            return;
        }
        const resp = await fetch(`/api/simulators/${id}`, { method: 'DELETE' });
        if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text);
        }
    },

    async getSimulator(id) {
        if (isWails) {
            return await window.go.main.App.GetSimulator(id);
        }
        const resp = await fetch(`/api/simulators/${id}`);
        return resp.json();
    },

    async updateConfig(id, config) {
        if (isWails) {
            await window.go.main.App.UpdateConfig(id, config);
            return;
        }
        await fetch(`/api/simulators/${id}/config`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(config)
        });
    },

    async getRegisters(id) {
        if (isWails) {
            return await window.go.main.App.GetRegisters(id);
        }
        const resp = await fetch(`/api/simulators/${id}/registers`);
        return resp.json();
    },

    async writeRegister(id, address, value) {
        if (isWails) {
            await window.go.main.App.WriteRegister(id, address, value);
            return;
        }
        await fetch(`/api/simulators/${id}/registers/${address}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ value })
        });
    },

    async getLogs(id, limit = 50) {
        if (isWails) {
            return await window.go.main.App.GetLogs(id, limit);
        }
        const resp = await fetch(`/api/simulators/${id}/logs?limit=${limit}`);
        return resp.json();
    },

    async getProfiles(id) {
        if (isWails) {
            return await window.go.main.App.GetProfiles(id);
        }
        const resp = await fetch(`/api/simulators/${id}/profiles`);
        return resp.json();
    },

    async applyProfile(id, name) {
        if (isWails) {
            await window.go.main.App.ApplyProfile(id, name);
            return;
        }
        await fetch(`/api/simulators/${id}/profiles/${name}/apply`, { method: 'POST' });
    },

    async getAutomation(id) {
        if (isWails) {
            return await window.go.main.App.GetAutomation(id);
        }
        const resp = await fetch(`/api/simulators/${id}/automation`);
        return resp.json();
    },

    async startAutomation(id) {
        if (isWails) {
            await window.go.main.App.StartAutomation(id);
            return;
        }
        await fetch(`/api/simulators/${id}/automation/start`, { method: 'POST' });
    },

    async stopAutomation(id) {
        if (isWails) {
            await window.go.main.App.StopAutomation(id);
            return;
        }
        await fetch(`/api/simulators/${id}/automation/stop`, { method: 'POST' });
    },

    async updateAutomationConfig(id, config) {
        if (isWails) {
            await window.go.main.App.UpdateAutomationConfig(id, config);
            return;
        }
        await fetch(`/api/simulators/${id}/automation/config`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(config)
        });
    },

    // New emulator/simulation endpoints
    async startEmulator(id) {
        if (isWails) {
            await window.go.main.App.StartEmulator(id);
            return;
        }
        await fetch(`/api/simulators/${id}/start`, { method: 'POST' });
    },

    async stopEmulator(id) {
        if (isWails) {
            await window.go.main.App.StopEmulator(id);
            return;
        }
        await fetch(`/api/simulators/${id}/stop`, { method: 'POST' });
    },

    async startSimulation(id) {
        if (isWails) {
            await window.go.main.App.StartSimulation(id);
            return;
        }
        const resp = await fetch(`/api/simulators/${id}/simulation/start`, { method: 'POST' });
        if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text);
        }
    },

    async stopSimulation(id) {
        if (isWails) {
            await window.go.main.App.StopSimulation(id);
            return;
        }
        await fetch(`/api/simulators/${id}/simulation/stop`, { method: 'POST' });
    },

    async setState(id, key, value) {
        if (isWails) {
            await window.go.main.App.SetState(id, key, value);
            return;
        }
        await fetch(`/api/simulators/${id}/state`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ key, value })
        });
    },

    async getClients(id) {
        if (isWails) {
            return await window.go.main.App.GetClients(id);
        }
        const resp = await fetch(`/api/simulators/${id}/clients`);
        return resp.json();
    },

    async wakeUp(id, wake) {
        if (isWails) {
            await window.go.main.App.WakeUp(id, wake);
            return;
        }
        await fetch(`/api/simulators/${id}/wakeup`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ wake })
        });
    },

    async setPower(id, power) {
        if (isWails) {
            await window.go.main.App.SetPower(id, power);
            return;
        }
        await fetch(`/api/simulators/${id}/power`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ power })
        });
    },

    async getDebugMeterValues(id) {
        if (isWails) {
            return await window.go.main.App.GetDebugMeterValues(id);
        }
        const resp = await fetch(`/api/simulators/${id}/debug/meter-values`);
        return resp.json();
    },

    async reset(id) {
        if (isWails) {
            await window.go.main.App.Reset(id);
            return;
        }
        const resp = await fetch(`/api/simulators/${id}/reset`, { method: 'POST' });
        if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text);
        }
    },

    async getSystemInfo() {
        if (isWails) {
            return await window.go.main.App.GetSystemInfo();
        }
        const resp = await fetch('/api/system');
        return resp.json();
    },

    async getVersionInfo() {
        if (isWails) {
            return await window.go.main.App.GetVersionInfo();
        }
        const resp = await fetch('/api/version');
        return resp.json();
    },

    async checkForUpdate() {
        if (isWails) {
            return await window.go.main.App.CheckForUpdate();
        }
        const resp = await fetch('/api/update/check');
        return resp.json();
    },

    async applyUpdate() {
        if (isWails) {
            return await window.go.main.App.ApplyUpdate();
        }
        const resp = await fetch('/api/update/apply', { method: 'POST' });
        return resp.json();
    }
};

// Site API Layer
const siteApi = {
    async getSites() {
        if (isWails) {
            const data = await window.go.main.App.GetSites();
            return Array.isArray(data) ? data : [];
        }
        const resp = await fetch('/api/sites');
        const data = await resp.json();
        return Array.isArray(data) ? data : [];
    },

    async createSite(name, meterId, simulatorIds, baseLoadW) {
        if (isWails) {
            return await window.go.main.App.CreateSite(name, meterId, simulatorIds || [], baseLoadW);
        }
        const resp = await fetch('/api/sites', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                name,
                meter_id: meterId,
                simulator_ids: simulatorIds,
                base_load_w: baseLoadW
            })
        });
        if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text);
        }
        return resp.json();
    },

    async deleteSite(id) {
        if (isWails) {
            await window.go.main.App.DeleteSite(id);
            return;
        }
        const resp = await fetch(`/api/sites/${id}`, { method: 'DELETE' });
        if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text);
        }
    },

    async getSite(id) {
        if (isWails) {
            return await window.go.main.App.GetSite(id);
        }
        const resp = await fetch(`/api/sites/${id}`);
        return resp.json();
    },

    async updateSiteConfig(id, config) {
        if (isWails) {
            await window.go.main.App.UpdateSiteConfig(id, config);
            return;
        }
        await fetch(`/api/sites/${id}/config`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(config)
        });
    },

    async setSiteMeter(id, meterId) {
        if (isWails) {
            await window.go.main.App.SetSiteMeter(id, meterId);
            return;
        }
        await fetch(`/api/sites/${id}/meter`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ meter_id: meterId })
        });
    },

    async addSimulatorToSite(siteId, simulatorId) {
        if (isWails) {
            await window.go.main.App.AddSimulatorToSite(siteId, simulatorId);
            return;
        }
        const resp = await fetch(`/api/sites/${siteId}/simulators`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ simulator_id: simulatorId })
        });
        if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text);
        }
    },

    async removeSimulatorFromSite(siteId, simulatorId) {
        if (isWails) {
            await window.go.main.App.RemoveSimulatorFromSite(siteId, simulatorId);
            return;
        }
        const resp = await fetch(`/api/sites/${siteId}/simulators/${simulatorId}`, {
            method: 'DELETE'
        });
        if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text);
        }
    },

    async enableSite(id) {
        if (isWails) {
            await window.go.main.App.EnableSite(id);
            return;
        }
        const resp = await fetch(`/api/sites/${id}/enable`, { method: 'POST' });
        if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text);
        }
    },

    async disableSite(id) {
        if (isWails) {
            await window.go.main.App.DisableSite(id);
            return;
        }
        await fetch(`/api/sites/${id}/disable`, { method: 'POST' });
    },

    async setP1Mode(id, mode) {
        if (isWails) {
            return await window.go.main.App.SetP1Mode(id, mode);
        }
        const resp = await fetch(`/api/sites/${id}/p1-mode`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ mode })
        });
        if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text);
        }
        return resp.json();
    },

    async getVirtualDevices(siteId) {
        if (isWails) {
            const data = await window.go.main.App.GetVirtualDevices(siteId);
            return data.virtual_devices || [];
        }
        const resp = await fetch(`/api/sites/${siteId}/virtual-devices`);
        const data = await resp.json();
        return data.virtual_devices || [];
    },

    async writeVirtualDevice(siteId, deviceType, data) {
        if (isWails) {
            return await window.go.main.App.WriteVirtualDevice(siteId, deviceType, data);
        }
        const resp = await fetch(`/api/sites/${siteId}/virtual-devices/${deviceType}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(data)
        });
        if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text);
        }
        return resp.json();
    }
};

// Storage Layer
const STORAGE_KEY = 'device-simulator-configs';
const STORAGE_VERSION = 3;

const storage = {
    load() {
        try {
            const data = localStorage.getItem(STORAGE_KEY);
            if (!data) return { version: STORAGE_VERSION, simulators: [], sites: [] };
            const parsed = JSON.parse(data);
            // Ensure sites array exists (migration)
            if (!parsed.sites) parsed.sites = [];
            // Migrate energy meter serials from string to numeric (v2)
            if (!parsed.version || parsed.version < 2) {
                parsed.simulators = parsed.simulators.map(sim => {
                    if (sim.category === 'energy_meter' && sim.serial && !/^\d+$/.test(sim.serial)) {
                        // Convert string serial to numeric using hash
                        sim.serial = this.stringToMeterSerial(sim.serial);
                    }
                    return sim;
                });
                parsed.version = 2;
            }
            // Migrate sites to add fuse_size, phase_factors, bound_meter (v3)
            if (parsed.version < 3) {
                parsed.sites = parsed.sites.map(site => {
                    site.fuseSize = site.fuseSize || 16;
                    site.phaseFactors = site.phaseFactors || { l1: 0.333, l2: 0.333, l3: 0.334 };
                    site.boundMeter = site.boundMeter ?? false;
                    return site;
                });
                parsed.version = 3;
            }
            if (parsed.version !== STORAGE_VERSION) {
                this.save(parsed);
            }
            return parsed;
        } catch (e) {
            return { version: STORAGE_VERSION, simulators: [], sites: [] };
        }
    },

    // Convert legacy string serial to numeric for energy meters (U32)
    stringToMeterSerial(str) {
        let hash = 1000000;
        for (let i = 0; i < str.length; i++) {
            hash += str.charCodeAt(i) * (i + 1) * 7;
        }
        return String(hash % 4294967296);
    },

    save(configs) {
        try {
            localStorage.setItem(STORAGE_KEY, JSON.stringify(configs));
        } catch (e) {
            console.error('Failed to save:', e);
        }
    },

    generateClientId() {
        // Fallback for browsers without crypto.randomUUID support
        if (crypto.randomUUID) {
            return 'sim-' + crypto.randomUUID();
        }
        // Generate UUID v4 using crypto.getRandomValues
        const bytes = new Uint8Array(16);
        crypto.getRandomValues(bytes);
        bytes[6] = (bytes[6] & 0x0f) | 0x40; // Version 4
        bytes[8] = (bytes[8] & 0x3f) | 0x80; // Variant 10
        const hex = [...bytes].map(b => b.toString(16).padStart(2, '0')).join('');
        return 'sim-' + hex.slice(0, 8) + '-' + hex.slice(8, 12) + '-' + hex.slice(12, 16) + '-' + hex.slice(16, 20) + '-' + hex.slice(20);
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

    remove(serial) {
        const saved = this.load();
        saved.simulators = saved.simulators.filter(s => s.serial !== serial);
        this.save(saved);
    },

    getAll() {
        return this.load().simulators;
    },

    syncWithServer(serverSims) {
        const saved = this.load();
        const serverBySerial = new Map(serverSims.map(s => [s.serial, s]));
        const savedBySerial = new Map(saved.simulators.map(s => [s.serial, s]));

        for (const sim of serverSims) {
            if (!savedBySerial.has(sim.serial)) {
                saved.simulators.push({
                    clientId: this.generateClientId(),
                    type: sim.category,
                    serial: sim.serial,
                    slaveId: sim.slave_id || 1,
                    profile: sim.profile || null,
                    automation: { wasRunning: sim.running || false, timeMultiplier: 1, scenario: 'sunny' },
                    createdAt: new Date().toISOString(),
                    lastModified: new Date().toISOString()
                });
            }
        }

        if (serverSims.length > 0) {
            saved.simulators = saved.simulators.filter(s => serverBySerial.has(s.serial));
        }

        this.save(saved);
    },

    // Site storage methods
    getAllSites() {
        return this.load().sites || [];
    },

    saveSiteConfig(config) {
        const saved = this.load();
        config.lastModified = new Date().toISOString();
        if (!config.createdAt) config.createdAt = config.lastModified;
        saved.sites.push(config);
        this.save(saved);
    },

    updateSiteConfig(name, updates) {
        const saved = this.load();
        const config = saved.sites.find(s => s.name === name);
        if (config) {
            Object.assign(config, updates);
            config.lastModified = new Date().toISOString();
            this.save(saved);
        }
    },

    removeSite(name) {
        const saved = this.load();
        saved.sites = saved.sites.filter(s => s.name !== name);
        this.save(saved);
    },

    syncSitesWithServer(serverSites) {
        const saved = this.load();
        const serverByName = new Map(serverSites.map(s => [s.name, s]));
        const savedByName = new Map((saved.sites || []).map(s => [s.name, s]));

        for (const site of serverSites) {
            if (!savedByName.has(site.name)) {
                saved.sites.push({
                    name: site.name,
                    meterId: site.meter_id,
                    simulatorIds: site.simulator_ids || [],
                    baseLoadW: site.base_load_w || 1000,
                    enabled: site.enabled || false,
                    fuseSize: site.fuse_size || 16,
                    phaseFactors: site.phase_factors || { l1: 0.333, l2: 0.333, l3: 0.334 },
                    boundMeter: site.bound_meter || false,
                    createdAt: new Date().toISOString(),
                    lastModified: new Date().toISOString()
                });
            } else {
                // Update existing site config with server state
                const existing = savedByName.get(site.name);
                existing.meterId = site.meter_id;
                existing.simulatorIds = site.simulator_ids || [];
                existing.baseLoadW = site.base_load_w;
                existing.enabled = site.enabled;
                existing.fuseSize = site.fuse_size || 16;
                existing.phaseFactors = site.phase_factors || { l1: 0.333, l2: 0.333, l3: 0.334 };
                existing.boundMeter = site.bound_meter || false;
                existing.lastModified = new Date().toISOString();
            }
        }

        // Remove sites that no longer exist on server
        if (serverSites.length > 0) {
            saved.sites = saved.sites.filter(s => serverByName.has(s.name));
        }

        this.save(saved);
    }
};

// Categories
const categories = {
    inverter: { name: 'Inverters', icon: '⚡', collapsed: false },
    energy_meter: { name: 'Energy Meters', icon: '📊', collapsed: false },
    v2x_charger: { name: 'V2X Chargers', icon: '🔋', collapsed: false },
    ocpp_charger: { name: 'OCPP Chargers', icon: '🔌', collapsed: false }
};

// Register Alpine stores on alpine:init
document.addEventListener('alpine:init', () => {
    // Confirmation dialog store
    Alpine.store('confirm', {
        show: false,
        title: '',
        message: '',
        _resolve: null,

        async prompt(title, message) {
            this.title = title;
            this.message = message;
            this.show = true;
            return new Promise(resolve => {
                this._resolve = resolve;
            });
        },

        ok() {
            this.show = false;
            if (this._resolve) this._resolve(true);
        },

        cancel() {
            this.show = false;
            if (this._resolve) this._resolve(false);
        }
    });

    // Main app store
    Alpine.store('app', {
        simulators: [],
        selectedId: null,
        selectedProtocol: null,
        selectedSerial: null,
        localIP: '--',
        categories: categories,
        addMenuOpen: false,
        refreshInterval: null,
        globalRefreshInterval: null,
        // Version and update state
        version: '0.0.0',
        updateAvailable: false,
        updateInfo: null,
        updateChecking: false,
        updateApplying: false,
        updateSuccess: false,
        updateError: null,
        showUpdateModal: false,

        async init() {
            // Fetch version and local IP from backend
            try {
                const [sysInfo, versionInfo] = await Promise.all([
                    api.getSystemInfo(),
                    api.getVersionInfo()
                ]);
                if (sysInfo && (sysInfo.local_ip || sysInfo.localIP)) {
                    this.localIP = sysInfo.local_ip || sysInfo.localIP;
                }
                if (versionInfo && versionInfo.version) {
                    this.version = versionInfo.version;
                }
            } catch (e) {
                console.error('Failed to get system info:', e);
                this.localIP = window.location.hostname || '--';
            }

            // Check for updates (non-blocking)
            this.checkForUpdate();

            try {
                const serverSims = await api.getSimulators();
                if (serverSims.length === 0) {
                    await this.restoreSimulators();
                    // After restore, sync the new server state back to localStorage
                    const newServerSims = await api.getSimulators();
                    if (newServerSims.length > 0) {
                        storage.syncWithServer(newServerSims);
                    }
                } else {
                    storage.syncWithServer(serverSims);
                }
            } catch (e) {
                console.error('Init error:', e);
            }
            await this.loadSimulators();
            await Alpine.store('site').loadSites();

            // Clear any existing global refresh interval before creating new one
            if (this.globalRefreshInterval) {
                clearInterval(this.globalRefreshInterval);
            }
            this.globalRefreshInterval = setInterval(() => {
                this.loadSimulators();
                Alpine.store('site').loadSites();
            }, 3000);
        },

        async loadSimulators() {
            try {
                this.simulators = await api.getSimulators();
            } catch (e) {
                console.error('Load error:', e);
            }
        },

        async restoreSimulators() {
            const saved = storage.getAll();
            if (saved.length === 0) return;

            for (const config of saved) {
                // Check server state BEFORE each creation to handle race conditions
                const currentServerSims = await api.getSimulators();
                const existingSerials = new Set(currentServerSims.map(s => s.serial));

                // Skip if a simulator with this serial already exists
                if (existingSerials.has(config.serial)) {
                    console.log(`Skipping restore of ${config.serial} - already exists on server`);
                    continue;
                }

                try {
                    const newSim = await api.createSimulator(config.type);
                    if (newSim.serial !== config.serial || config.slaveId) {
                        await api.updateConfig(newSim.id, { serial: config.serial, slave_id: config.slaveId || 1 });
                    }
                    if (config.profile) {
                        await api.applyProfile(newSim.id, config.profile);
                    }
                    if (config.ocppUrl && config.type === 'ocpp_charger') {
                        await api.updateConfig(newSim.id, { ocpp_url: config.ocppUrl });
                    }
                    // Restore automation config (time multiplier, scenario)
                    if (config.automation?.timeMultiplier || config.automation?.scenario) {
                        await api.updateAutomationConfig(newSim.id, {
                            time_multiplier: config.automation.timeMultiplier || 1,
                            scenario: config.automation.scenario || 'sunny'
                        });
                    }
                    // Start automation if it was running
                    if (config.automation?.wasRunning) {
                        await api.startAutomation(newSim.id);
                    }
                } catch (e) {
                    console.error('Restore error:', e);
                }
            }
        },

        getGroupedSimulators() {
            const grouped = {};
            for (const cat of Object.keys(categories)) {
                grouped[cat] = this.simulators.filter(s => s.category === cat);
            }
            return grouped;
        },

        toggleCategory(cat) {
            categories[cat].collapsed = !categories[cat].collapsed;
        },

        toggleAddMenu() {
            this.addMenuOpen = !this.addMenuOpen;
        },

        async addSimulator(type) {
            this.addMenuOpen = false;
            try {
                const newSim = await api.createSimulator(type);
                storage.saveSimulatorConfig({
                    clientId: storage.generateClientId(),
                    type: type,
                    serial: newSim.serial,
                    slaveId: newSim.slave_id || 1,
                    profile: newSim.profile || null,
                    automation: { wasRunning: false, timeMultiplier: 1, scenario: 'sunny' }
                });
                await this.loadSimulators();
            } catch (e) {
                console.error('Add error:', e);
                alert('Failed: ' + e.message);
            }
        },

        async deleteSimulator(id, serial) {
            const confirmed = await Alpine.store('confirm').prompt(
                'Delete Simulator',
                `Are you sure you want to delete simulator ${serial}?`
            );
            if (!confirmed) return;

            try {
                await api.deleteSimulator(id);
                storage.remove(serial);
                if (this.selectedId === id) {
                    this.selectedId = null;
                    Alpine.store('simulator').clear();
                }
                await this.loadSimulators();
            } catch (e) {
                console.error('Delete error:', e);
                alert('Failed: ' + e.message);
            }
        },

        selectSimulator(id, protocol) {
            this.selectedId = id;
            this.selectedProtocol = protocol;
            const sim = this.simulators.find(s => s.id === id);
            this.selectedSerial = sim ? sim.serial : null;

            // Don't clear site selection - site view should always be on
            // Just stop the site refresh interval since we'll show simulator view
            const siteStore = Alpine.store('site');
            if (siteStore.refreshInterval) {
                clearInterval(siteStore.refreshInterval);
                siteStore.refreshInterval = null;
            }

            if (this.refreshInterval) clearInterval(this.refreshInterval);
            this.refreshInterval = setInterval(() => Alpine.store('simulator').loadAllData(), 2000);

            Alpine.store('simulator').init(id, protocol);
        },

        async toggleSimRunning(id, currentState) {
            if (currentState) {
                await api.stopAutomation(id);
            } else {
                await api.startAutomation(id);
            }

            const sim = this.simulators.find(s => s.id === id);
            if (sim) {
                const saved = storage.load();
                const config = saved.simulators.find(s => s.serial === sim.serial);
                if (config) {
                    if (!config.automation) config.automation = {};
                    config.automation.wasRunning = !currentState;
                    storage.save(saved);
                }
            }
            await this.loadSimulators();
        },

        // Toggle emulator (server) only - for sidebar toggle
        async toggleEmulatorRunning(id, currentState) {
            if (currentState) {
                await api.stopEmulator(id);
            } else {
                await api.startEmulator(id);
            }

            const sim = this.simulators.find(s => s.id === id);
            if (sim) {
                const saved = storage.load();
                const config = saved.simulators.find(s => s.serial === sim.serial);
                if (config) {
                    if (!config.automation) config.automation = {};
                    config.automation.wasRunning = !currentState;
                    storage.save(saved);
                }
            }
            await this.loadSimulators();
        },

        async copyIP() {
            try {
                await navigator.clipboard.writeText(this.localIP);
            } catch (e) {
                console.error('Copy error:', e);
            }
        },

        async checkForUpdate() {
            if (this.updateChecking) return;
            this.updateChecking = true;
            this.updateSuccess = false;
            this.updateError = null;
            try {
                const info = await api.checkForUpdate();
                this.updateInfo = info;
                this.updateAvailable = info && info.available;
                if (this.updateAvailable) {
                    console.log(`Update available: ${info.current_version} -> ${info.latest_version}`);
                }
            } catch (e) {
                console.error('Update check failed:', e);
            } finally {
                this.updateChecking = false;
            }
        },

        async applyUpdate() {
            if (this.updateApplying || !this.updateAvailable) return;
            this.updateApplying = true;
            this.updateSuccess = false;
            this.updateError = null;
            try {
                await api.applyUpdate();
                this.updateSuccess = true;
                this.updateAvailable = false;
            } catch (e) {
                console.error('Update failed:', e);
                // Extract error message - Wails errors may be strings or have message property
                this.updateError = typeof e === 'string' ? e : (e.message || e.toString() || 'Unknown error occurred');
            } finally {
                this.updateApplying = false;
            }
        }
    });

    // Simulator store
    Alpine.store('simulator', {
        id: null,
        protocol: null,
        data: null,
        registers: [],
        logs: [],
        profiles: [],
        activeProfile: '',
        automation: null,
        clients: [],
        debugMeterValues: null,

        init(id, protocol) {
            this.id = id;
            this.protocol = protocol;
            this.registers = [];
            this.logs = [];
            this.loadAllData();
        },

        clear() {
            this.id = null;
            this.protocol = null;
            this.data = null;
            this.registers = [];
            this.logs = [];
        },

        async loadAllData() {
            if (!this.id) return;
            await Promise.all([this.loadConfig(), this.loadState(), this.loadLogs()]);
            if (this.protocol === 'ocpp') {
                await this.loadDebugMeterValues();
            }
            if (this.protocol === 'mqtt') {
                await this.loadClients();
            }
        },

        async loadConfig() {
            try {
                this.data = await api.getSimulator(this.id);
                await this.loadProfiles();
            } catch (e) {
                console.error('Load config error:', e);
            }
        },

        async loadState() {
            if (this.protocol === 'modbus') {
                await this.loadRegisters();
                await this.loadAutomation();
            }
        },

        async loadRegisters() {
            try {
                this.registers = await api.getRegisters(this.id);
            } catch (e) {
                console.error('Load registers error:', e);
            }
        },

        async loadLogs() {
            try {
                const data = await api.getLogs(this.id);
                this.logs = (data.logs || []).reverse();
            } catch (e) {
                console.error('Load logs error:', e);
            }
        },

        async loadProfiles() {
            try {
                const data = await api.getProfiles(this.id);
                this.profiles = data.profiles || [];
                this.activeProfile = data.active || (this.profiles[0]?.name || '');
            } catch (e) {
                console.error('Load profiles error:', e);
            }
        },

        async loadAutomation() {
            try {
                this.automation = await api.getAutomation(this.id);
            } catch (e) {
                console.error('Load automation error:', e);
            }
        },

        async loadDebugMeterValues() {
            if (this.protocol !== 'ocpp') return;
            try {
                this.debugMeterValues = await api.getDebugMeterValues(this.id);
            } catch (e) {
                console.error('Load debug error:', e);
            }
        },

        async loadClients() {
            if (this.protocol !== 'mqtt' && this.protocol !== 'modbus') return;
            try {
                const data = await api.getClients(this.id);
                this.clients = data.clients || [];
            } catch (e) {
                console.error('Load clients error:', e);
            }
        },

        async updateConfig(updates) {
            try {
                await api.updateConfig(this.id, updates);
                const app = Alpine.store('app');
                if (updates.serial && app.selectedSerial !== updates.serial) {
                    app.selectedSerial = updates.serial;
                }
                // Normalize keys for storage (API uses snake_case, storage uses camelCase)
                const storageUpdates = {};
                if (updates.serial) storageUpdates.serial = updates.serial;
                if (updates.slave_id) storageUpdates.slaveId = updates.slave_id;
                if (updates.ocpp_url !== undefined) storageUpdates.ocppUrl = updates.ocpp_url;
                storage.updateConfig(app.selectedSerial, storageUpdates);
                await app.loadSimulators();
            } catch (e) {
                console.error('Update config error:', e);
            }
        },

        async applyProfile(name) {
            try {
                await api.applyProfile(this.id, name);
                this.activeProfile = name;
                storage.updateConfig(Alpine.store('app').selectedSerial, { profile: name });
                await this.loadState();
            } catch (e) {
                console.error('Apply profile error:', e);
            }
        },

        async toggleAutomation() {
            const isRunning = this.data?.running;

            if (!isRunning && this.protocol === 'ocpp') {
                const ocppUrl = this.data?.ocpp_url || '';
                if (!ocppUrl.trim()) {
                    alert('Please enter an OCPP URL before starting.');
                    return;
                }
            }

            if (isRunning) {
                await api.stopAutomation(this.id);
            } else {
                await api.startAutomation(this.id);
            }

            const app = Alpine.store('app');
            if (app.selectedSerial) {
                const saved = storage.load();
                const config = saved.simulators.find(s => s.serial === app.selectedSerial);
                if (config) {
                    if (!config.automation) config.automation = {};
                    config.automation.wasRunning = !isRunning;
                    storage.save(saved);
                }
            }

            await this.loadAllData();
            await app.loadSimulators();
        },

        // Toggle emulator (server) only
        async toggleEmulator() {
            const isRunning = this.data?.running;

            if (!isRunning && this.protocol === 'ocpp') {
                const ocppUrl = this.data?.ocpp_url || '';
                if (!ocppUrl.trim()) {
                    alert('Please enter an OCPP URL before starting.');
                    return;
                }
            }

            if (isRunning) {
                await api.stopEmulator(this.id);
            } else {
                await api.startEmulator(this.id);
            }

            const app = Alpine.store('app');
            if (app.selectedSerial) {
                const saved = storage.load();
                const config = saved.simulators.find(s => s.serial === app.selectedSerial);
                if (config) {
                    if (!config.automation) config.automation = {};
                    config.automation.wasRunning = !isRunning;
                    storage.save(saved);
                }
            }

            await this.loadAllData();
            await app.loadSimulators();
        },

        // Toggle simulation (data generation) only
        async toggleSimulation() {
            const simulationEnabled = this.automation?.enabled || this.data?.automation?.enabled;

            try {
                if (simulationEnabled) {
                    await api.stopSimulation(this.id);
                } else {
                    await api.startSimulation(this.id);
                }
            } catch (e) {
                alert('Error: ' + e.message);
                return;
            }

            await this.loadAllData();
        },

        async updateAutomationConfig(multiplier, scenario) {
            await api.updateAutomationConfig(this.id, { time_multiplier: multiplier, scenario: scenario });

            // Save to localStorage
            const app = Alpine.store('app');
            if (app.selectedSerial) {
                const saved = storage.load();
                const config = saved.simulators.find(s => s.serial === app.selectedSerial);
                if (config) {
                    if (!config.automation) config.automation = {};
                    config.automation.timeMultiplier = multiplier;
                    config.automation.scenario = scenario;
                    storage.save(saved);
                }
            }
        },

        async writeRegister(address, value) {
            try {
                await api.writeRegister(this.id, address, value);
                await this.loadRegisters();
            } catch (e) {
                console.error('Write register error:', e);
            }
        },

        async setState(key, value) {
            try {
                await api.setState(this.id, key, value);
                await this.loadAllData();
            } catch (e) {
                console.error('Set state error:', e);
            }
        },

        async toggleEVConnected() {
            const current = this.data?.state?.evConnected || false;
            await this.setState('evConnected', !current);
        },

        async toggleWakeUp() {
            const state = this.data?.state || {};
            const isAwake = state.sessionState === 'CHARGE_LOOP' || state.sessionState === 'PAUSED';
            try {
                await api.wakeUp(this.id, !isAwake);
                await this.loadAllData();
            } catch (e) {
                console.error('Toggle wake error:', e);
            }
        },

        async setPower(power) {
            try {
                await api.setPower(this.id, power);
            } catch (e) {
                console.error('Set power error:', e);
            }
        }
    });

    // Site store
    Alpine.store('site', {
        sites: [],
        selectedId: null,
        data: null,
        refreshInterval: null,

        async init() {
            const serverSites = await siteApi.getSites();
            if (serverSites.length === 0) {
                await this.restoreSites();
                const newServerSites = await siteApi.getSites();
                if (newServerSites.length > 0) {
                    storage.syncSitesWithServer(newServerSites);
                } else {
                    // No sites in storage either - create default "My Home" site
                    await this.createDefaultSite();
                }
            } else {
                storage.syncSitesWithServer(serverSites);
            }
            await this.loadSites();

            // Auto-enable all sites (aggregation is always on)
            await this.enableAllSites();

            // Auto-select the first site
            if (this.sites.length > 0 && !this.selectedId) {
                this.selectSite(this.sites[0].id);
            }
        },

        async enableAllSites() {
            for (const site of this.sites) {
                if (!site.enabled) {
                    try {
                        await siteApi.enableSite(site.id);
                    } catch (e) {
                        console.error('Auto-enable site error:', e);
                    }
                }
            }
            await this.loadSites();
        },

        async createDefaultSite() {
            try {
                // Create an empty default site (no meter, no simulators)
                await siteApi.createSite('My Home', 0, [], 1000);

                // Save to localStorage
                storage.saveSiteConfig({
                    name: 'My Home',
                    meterSerial: null,
                    simulatorSerials: [],
                    baseLoadW: 1000,
                    enabled: false
                });

                console.log('Created default site "My Home"');
            } catch (e) {
                console.error('Failed to create default site:', e);
            }
        },

        async loadSites() {
            try {
                this.sites = await siteApi.getSites();
            } catch (e) {
                console.error('Load sites error:', e);
            }
        },

        async restoreSites() {
            const saved = storage.getAllSites();
            if (saved.length === 0) return;

            // Get current simulators to map names back to IDs
            const simulators = await api.getSimulators();
            const simBySerial = new Map(simulators.map(s => [s.serial, s]));

            for (const config of saved) {
                // Check if site already exists
                const currentSites = await siteApi.getSites();
                if (currentSites.some(s => s.name === config.name)) {
                    console.log(`Skipping restore of site ${config.name} - already exists`);
                    continue;
                }

                try {
                    // Find meter by looking up the simulator
                    let meterId = 0;
                    if (config.meterSerial) {
                        const meter = simBySerial.get(config.meterSerial);
                        if (meter) meterId = meter.id;
                    }

                    const newSite = await siteApi.createSite(
                        config.name,
                        meterId,
                        [], // Start with no simulators, add them after
                        config.baseLoadW || 1000
                    );

                    // Add simulators by looking up their serials
                    if (config.simulatorSerials) {
                        for (const serial of config.simulatorSerials) {
                            const sim = simBySerial.get(serial);
                            if (sim) {
                                await siteApi.addSimulatorToSite(newSite.id, sim.id);
                            }
                        }
                    }

                    // Enable if it was enabled
                    if (config.enabled) {
                        await siteApi.enableSite(newSite.id);
                    }
                } catch (e) {
                    console.error('Restore site error:', e);
                }
            }
        },

        async createSite(name, meterId, simulatorIds, baseLoadW) {
            try {
                const newSite = await siteApi.createSite(name, meterId, simulatorIds, baseLoadW);

                // Save to localStorage with serial references
                const simulators = Alpine.store('app').simulators;
                const meter = simulators.find(s => s.id === meterId);
                storage.saveSiteConfig({
                    name: name,
                    meterSerial: meter?.serial || null,
                    simulatorSerials: [],
                    baseLoadW: baseLoadW,
                    enabled: false
                });

                await this.loadSites();
                return newSite;
            } catch (e) {
                console.error('Create site error:', e);
                throw e;
            }
        },

        async deleteSite(id) {
            try {
                const site = this.sites.find(s => s.id === id);
                const siteName = site?.name;

                await siteApi.deleteSite(id);

                if (siteName) {
                    storage.removeSite(siteName);
                }

                if (this.selectedId === id) {
                    this.selectedId = null;
                    this.data = null;
                }
                await this.loadSites();
            } catch (e) {
                console.error('Delete site error:', e);
                throw e;
            }
        },

        selectSite(id) {
            this.selectedId = id;
            // Clear simulator selection when selecting a site
            Alpine.store('app').selectedId = null;
            Alpine.store('simulator').clear();

            if (this.refreshInterval) clearInterval(this.refreshInterval);
            this.refreshInterval = setInterval(() => this.loadSiteData(), 1000);

            this.loadSiteData();

            // Auto-enable aggregation (sites are always aggregating)
            this.ensureEnabled(id);
        },

        async ensureEnabled(id) {
            // Check if site is enabled, if not enable it
            const site = this.sites.find(s => s.id === id);
            if (site && !site.enabled) {
                try {
                    await siteApi.enableSite(id);
                    await this.loadSites();
                } catch (e) {
                    console.error('Auto-enable site error:', e);
                }
            }
        },

        clearSelection() {
            this.selectedId = null;
            this.data = null;
            if (this.refreshInterval) {
                clearInterval(this.refreshInterval);
                this.refreshInterval = null;
            }
        },

        async loadSiteData() {
            if (!this.selectedId) return;
            try {
                this.data = await siteApi.getSite(this.selectedId);
            } catch (e) {
                console.error('Load site data error:', e);
            }
        },

        // Helper to sync current site to localStorage
        syncCurrentSiteToStorage() {
            if (!this.data) return;
            const simulators = Alpine.store('app').simulators;
            const simById = new Map(simulators.map(s => [s.id, s]));

            const meter = simById.get(this.data.meter_id);
            const simulatorSerials = (this.data.simulator_ids || [])
                .map(id => simById.get(id)?.serial)
                .filter(Boolean);

            storage.updateSiteConfig(this.data.name, {
                meterSerial: meter?.serial || null,
                simulatorSerials: simulatorSerials,
                baseLoadW: this.data.base_load_w,
                enabled: this.data.enabled
            });
        },

        async updateConfig(updates) {
            if (!this.selectedId) return;
            try {
                await siteApi.updateSiteConfig(this.selectedId, updates);
                await this.loadSiteData();
                await this.loadSites();
                this.syncCurrentSiteToStorage();
            } catch (e) {
                console.error('Update site config error:', e);
            }
        },

        async setMeter(meterId) {
            if (!this.selectedId) return;
            try {
                await siteApi.setSiteMeter(this.selectedId, meterId);
                await this.loadSiteData();
                this.syncCurrentSiteToStorage();
            } catch (e) {
                console.error('Set meter error:', e);
                throw e;
            }
        },

        async addSimulator(simulatorId) {
            if (!this.selectedId) return;
            try {
                await siteApi.addSimulatorToSite(this.selectedId, simulatorId);
                await this.loadSiteData();
                this.syncCurrentSiteToStorage();
            } catch (e) {
                console.error('Add simulator error:', e);
                throw e;
            }
        },

        async removeSimulator(simulatorId) {
            if (!this.selectedId) return;
            try {
                await siteApi.removeSimulatorFromSite(this.selectedId, simulatorId);
                await this.loadSiteData();
                this.syncCurrentSiteToStorage();
            } catch (e) {
                console.error('Remove simulator error:', e);
                throw e;
            }
        },

        async toggleEnabled() {
            if (!this.selectedId || !this.data) return;
            try {
                if (this.data.enabled) {
                    await siteApi.disableSite(this.selectedId);
                } else {
                    await siteApi.enableSite(this.selectedId);
                }
                await this.loadSiteData();
                await this.loadSites();
                this.syncCurrentSiteToStorage();
            } catch (e) {
                console.error('Toggle enabled error:', e);
                alert('Error: ' + e.message);
            }
        },

        getAvailableSimulators() {
            // Return simulators not in any site, or in current site
            const app = Alpine.store('app');
            const inOtherSites = new Set();

            for (const site of this.sites) {
                if (site.id !== this.selectedId) {
                    for (const simId of site.simulator_ids || []) {
                        inOtherSites.add(simId);
                    }
                }
            }

            return app.simulators.filter(s => !inOtherSites.has(s.id));
        },

        getModbusSimulators() {
            // Return Modbus simulators for meter selection, with energy meters first
            const all = Alpine.store('app').simulators.filter(s => s.protocol === 'modbus');
            // Sort so energy_meter category comes first
            return all.sort((a, b) => {
                if (a.category === 'energy_meter' && b.category !== 'energy_meter') return -1;
                if (a.category !== 'energy_meter' && b.category === 'energy_meter') return 1;
                return 0;
            });
        },

        // Check if a simulator is a bound meter for any site
        isBoundMeter(simId) {
            return this.sites.some(site => site.bound_meter && site.meter_id === simId);
        },

        // Check if current site's meter is an inverter (for virtual device display)
        isMeterInverter() {
            if (!this.data?.meter_id) return false;
            const sim = Alpine.store('app').simulators.find(s => s.id === this.data.meter_id);
            return sim?.category === 'inverter';
        },

        async setP1Mode(mode) {
            if (!this.selectedId) return;
            try {
                await siteApi.setP1Mode(this.selectedId, mode);
                await this.loadSiteData();
            } catch (e) {
                console.error('Set P1 mode error:', e);
                alert('Error: ' + e.message);
            }
        },

        // Get virtual devices from site data
        getVirtualDevices() {
            return this.data?.virtual_devices || [];
        },

        // Write to a virtual device (pv or battery)
        async writeVirtualDevice(deviceType, data) {
            if (!this.selectedId) return;
            try {
                await siteApi.writeVirtualDevice(this.selectedId, deviceType, data);
                await this.loadSiteData();
            } catch (e) {
                console.error('Write virtual device error:', e);
            }
        },

        // Find site that has this simulator as its meter
        getSiteWithMeter(simulatorId) {
            return this.sites.find(s => s.meter_id === simulatorId);
        }
    });
});
