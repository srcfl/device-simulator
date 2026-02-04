// Main Application - Alpine.js initialization and stores
import { api } from './api.js';
import { storage } from './storage.js';

// Categories configuration
const categories = {
    inverter: { name: 'Inverters', icon: '⚡', collapsed: false },
    v2x_charger: { name: 'V2X Chargers', icon: '🔋', collapsed: false },
    ocpp_charger: { name: 'OCPP Chargers', icon: '🔌', collapsed: false }
};

const getConfirmStore = () => window.Alpine?.store?.('confirm');

const showAlert = async (title, message) => {
    const store = getConfirmStore();
    if (store?.alert) {
        await store.alert(title, message);
    }
};

const showConfirm = async (title, message, options = {}) => {
    const store = getConfirmStore();
    if (store?.prompt) {
        return store.prompt(title, message, options);
    }
    return false;
};

// Initialize Alpine stores
document.addEventListener('alpine:init', () => {
    // Main application store
    Alpine.store('app', {
        simulators: [],
        selectedId: null,
        selectedProtocol: null,
        selectedSerial: null,
        localIP: window.location.hostname || '--',
        categories: categories,
        addMenuOpen: false,
        refreshInterval: null,
        cachedProfiles: null,

        async init() {
            // Check if server has simulators
            try {
                const serverSims = await api.getSimulators();
                if (serverSims.length === 0) {
                    await this.restoreSimulators();
                } else {
                    storage.syncWithServer(serverSims);
                }
            } catch (e) {
                console.error('Failed to check server simulators:', e);
            }

            await this.loadSimulators();
            setInterval(() => this.loadSimulators(), 3000);
        },

        async loadSimulators() {
            try {
                this.simulators = await api.getSimulators();
            } catch (e) {
                console.error('Failed to load simulators:', e);
            }
        },

        async restoreSimulators() {
            const saved = storage.getAll();
            if (saved.length === 0) return;

            console.log(`Restoring ${saved.length} simulators from localStorage...`);
            const serverSims = await api.getSimulators();
            const serverBySerial = new Map(serverSims.map(s => [s.serial, s]));

            let restored = 0, created = 0;

            for (const config of saved) {
                const existing = serverBySerial.get(config.serial);

                if (existing) {
                    restored++;
                    if (config.ocppUrl && config.type === 'ocpp_charger') {
                        try {
                            await api.updateConfig(existing.id, { ocpp_url: config.ocppUrl });
                        } catch (e) {
                            console.error(`Failed to apply OCPP URL to ${config.serial}:`, e);
                        }
                    }
                } else {
                    try {
                        const newSim = await api.createSimulator(config.type);
                        created++;

                        if (newSim.serial !== config.serial || config.slaveId) {
                            await api.updateConfig(newSim.id, {
                                serial: config.serial,
                                slave_id: config.slaveId || 1
                            });
                        }

                        if (config.profile) {
                            await api.applyProfile(newSim.id, config.profile);
                        }

                        if (config.ocppUrl && config.type === 'ocpp_charger') {
                            await api.updateConfig(newSim.id, { ocpp_url: config.ocppUrl });
                        }

                        if (config.automation) {
                            await api.updateAutomationConfig(newSim.id, {
                                time_multiplier: config.automation.timeMultiplier || 1,
                                scenario: config.automation.scenario || 'sunny'
                            });
                            if (config.automation.wasRunning) {
                                await api.startAutomation(newSim.id);
                            }
                        }
                    } catch (e) {
                        console.error(`Failed to restore simulator ${config.serial}:`, e);
                    }
                }
            }
            console.log(`Restoration complete: ${restored} matched, ${created} created`);
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
                console.error('Failed to create simulator:', e);
                await showAlert('Error', 'Failed to create simulator: ' + e.message);
            }
        },

        async deleteSimulator(id, serial) {
            const confirmed = await showConfirm(
                'Delete Simulator',
                `Are you sure you want to delete simulator ${serial}?`,
                { okLabel: 'Delete', okVariant: 'danger' }
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
                console.error('Failed to delete simulator:', e);
                await showAlert('Error', 'Failed to delete simulator: ' + e.message);
            }
        },

        selectSimulator(id, protocol) {
            this.selectedId = id;
            this.selectedProtocol = protocol;
            const sim = this.simulators.find(s => s.id === id);
            this.selectedSerial = sim ? sim.serial : null;
            this.cachedProfiles = null;

            // Clear refresh interval and set new one
            if (this.refreshInterval) clearInterval(this.refreshInterval);
            this.refreshInterval = setInterval(() => Alpine.store('simulator').loadAllData(), 2000);

            // Initialize simulator store
            Alpine.store('simulator').init(id, protocol);
        },

        async toggleSimRunning(id, currentState) {
            const endpoint = currentState ? 'stop' : 'start';
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
                    config.lastModified = new Date().toISOString();
                    storage.save(saved);
                }
            }

            await this.loadSimulators();
        },

        async copyIP() {
            try {
                await navigator.clipboard.writeText(this.localIP);
            } catch (e) {
                console.error('Failed to copy IP:', e);
            }
        }
    });

    // Selected simulator store
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
            await Promise.all([
                this.loadConfig(),
                this.loadState(),
                this.loadLogs()
            ]);
            if (this.protocol === 'ocpp') {
                await this.loadDebugMeterValues();
            }
        },

        async loadConfig() {
            try {
                this.data = await api.getSimulator(this.id);
                await this.loadProfiles();
            } catch (e) {
                console.error('Failed to load config:', e);
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
                console.error('Failed to load registers:', e);
            }
        },

        async loadLogs() {
            try {
                const data = await api.getLogs(this.id);
                this.logs = (data.logs || []).reverse();
            } catch (e) {
                console.error('Failed to load logs:', e);
            }
        },

        async loadProfiles() {
            try {
                const data = await api.getProfiles(this.id);
                this.profiles = data.profiles || [];
                this.activeProfile = data.active || (this.profiles[0]?.name || '');
            } catch (e) {
                console.error('Failed to load profiles:', e);
            }
        },

        async loadAutomation() {
            try {
                this.automation = await api.getAutomation(this.id);
            } catch (e) {
                console.error('Failed to load automation:', e);
            }
        },

        async loadDebugMeterValues() {
            if (this.protocol !== 'ocpp') return;
            try {
                this.debugMeterValues = await api.getDebugMeterValues(this.id);
            } catch (e) {
                console.error('Failed to load debug meter values:', e);
            }
        },

        async loadClients() {
            if (this.protocol !== 'mqtt') return;
            try {
                const data = await api.getClients(this.id);
                this.clients = data.clients || [];
            } catch (e) {
                console.error('Failed to load clients:', e);
            }
        },

        async updateConfig(updates) {
            try {
                await api.updateConfig(this.id, updates);
                const app = Alpine.store('app');
                if (updates.serial && app.selectedSerial !== updates.serial) {
                    storage.updateConfigByOldSerial(app.selectedSerial, updates.serial, updates);
                    app.selectedSerial = updates.serial;
                } else if (app.selectedSerial) {
                    storage.updateConfig(app.selectedSerial, updates);
                }
                await app.loadSimulators();
            } catch (e) {
                console.error('Failed to update config:', e);
            }
        },

        async applyProfile(name) {
            try {
                await api.applyProfile(this.id, name);
                this.activeProfile = name;
                storage.updateConfig(Alpine.store('app').selectedSerial, { profile: name });
                await this.loadState();
            } catch (e) {
                console.error('Failed to apply profile:', e);
            }
        },

        async toggleAutomation() {
            const isRunning = this.data?.running;

            if (!isRunning && this.protocol === 'ocpp') {
                // Check OCPP URL requirement
                const ocppUrl = this.data?.ocpp_url || '';
                if (!ocppUrl.trim()) {
                    await showAlert(
                        'Missing OCPP URL',
                        'Please enter an OCPP URL before starting the simulation.'
                    );
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
                    config.lastModified = new Date().toISOString();
                    storage.save(saved);
                }
            }

            await this.loadAllData();
            await app.loadSimulators();
        },

        async updateAutomationConfig(multiplier, scenario) {
            await api.updateAutomationConfig(this.id, {
                time_multiplier: multiplier,
                scenario: scenario
            });

            const app = Alpine.store('app');
            if (app.selectedSerial) {
                const saved = storage.load();
                const config = saved.simulators.find(s => s.serial === app.selectedSerial);
                if (config) {
                    if (!config.automation) config.automation = {};
                    config.automation.timeMultiplier = multiplier;
                    config.automation.scenario = scenario;
                    config.lastModified = new Date().toISOString();
                    storage.save(saved);
                }
            }
        },

        async writeRegister(address, value) {
            try {
                await api.writeRegister(this.id, address, value);
                await this.loadRegisters();
            } catch (e) {
                console.error('Failed to write register:', e);
            }
        },

        async setState(key, value) {
            try {
                await api.setState(this.id, key, value);
                await this.loadAllData();
            } catch (e) {
                console.error('Failed to set state:', e);
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
                console.error('Failed to toggle wake up:', e);
            }
        },

        async setPower(power) {
            try {
                await api.setPower(this.id, power);
            } catch (e) {
                console.error('Failed to set power:', e);
            }
        },

        async startOCPPCharging(powerW) {
            if (powerW > 0) {
                await this.setState('startCharging', true);
            }
            await this.setState('chargingPower', powerW);
        },

        async stopOCPPCharging() {
            await this.setState('stopCharging', true);
            await this.setState('chargingPower', 0);
        }
    });

    // UI helpers store
    Alpine.store('ui', {
        powerSliderDragging: false,
        socSliderDragging: false,
        ocppPowerSliderDragging: false,
        ocppWasCharging: false,

        escapeHtml(text) {
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        },

        formatTime(timestamp) {
            return new Date(timestamp).toLocaleTimeString();
        },

        formatDateTime(timestamp) {
            return new Date(timestamp).toLocaleString();
        },

        copyToClipboard(text) {
            const textarea = document.createElement('textarea');
            textarea.value = text;
            textarea.style.position = 'fixed';
            textarea.style.opacity = '0';
            document.body.appendChild(textarea);
            textarea.select();
            document.execCommand('copy');
            document.body.removeChild(textarea);
        },

        copyLogs() {
            const store = Alpine.store('simulator');
            const logText = store.logs.map(log => {
                const time = this.formatTime(log.timestamp);
                const op = log.operation || '';
                const addr = log.address !== undefined ? log.address : '';
                const values = log.values ? log.values.join(', ') : '';
                return `${time} ${op} ${addr} ${values}`.trim().replace(/\s+/g, ' ');
            }).join('\n');
            this.copyToClipboard(logText);
        }
    });
});

// Initialize the app after Alpine loads
document.addEventListener('DOMContentLoaded', () => {
    // Alpine auto-initializes with x-data, no manual init needed
});
