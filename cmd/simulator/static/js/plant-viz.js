document.addEventListener('alpine:init', () => {
    Alpine.data('sitePlantViz', () => ({
        facilities: [],
        selected: '',
        applying: false,

        async init() {
            try {
                this.facilities = await siteApi.getFacilities();
            } catch (e) {
                console.error('facilities', e);
                this.facilities = [];
            }
            this.syncSelected();
            this.$watch('$store.site.data', () => this.syncSelected());
        },

        syncSelected() {
            const id = Alpine.store('site').data?.facility_id;
            if (id) this.selected = id;
        },

        plant() {
            return Alpine.store('site').data?.plant || {};
        },

        el() {
            return this.plant().electrical || {};
        },

        th() {
            return this.plant().thermal || {};
        },

        preset() {
            return this.facilities.find(f => f.id === this.selected) || null;
        },

        hint() {
            const p = this.plant();
            if (p.facility_name) {
                return p.facility_name + ' · ' + (p.backend || 'native');
            }
            return 'Physics behind the devices. EMS talks Modbus as if this were a real site.';
        },

        fmtW(w) {
            const n = Number(w) || 0;
            const a = Math.abs(n);
            if (a >= 1000) return (n / 1000).toFixed(2) + ' kW';
            return Math.round(n) + ' W';
        },

        fmtC(c) {
            if (c === undefined || c === null || c === '') return '—';
            const n = Number(c);
            if (!Number.isFinite(n)) return '—';
            if (n === 0 && this.th().indoor_c === undefined) return '—';
            return n.toFixed(1) + ' °C';
        },

        flowW(from, to) {
            const flows = this.plant().flows || [];
            const hit = flows.find(f => f.from === from && f.to === to);
            return hit ? Math.abs(hit.w) : 0;
        },

        stroke(from, to, kind) {
            const w = this.flowW(from, to);
            if (w < 8) return 'stroke:transparent;stroke-width:0';
            const width = Math.max(1.6, Math.min(8, w / 800));
            const color = kind === 'heat' ? '#ff9f43' : '#00FF84';
            return `stroke:${color};stroke-width:${width};opacity:0.9`;
        },

        nodeClass(key) {
            const live = {
                pv: Math.abs(this.el().pv_w) > 10,
                batt: Math.abs(this.el().battery_w) > 10,
                hp: (this.el().hp_w || 0) > 10,
                house: (this.el().house_w || 0) > 10,
                grid: Math.abs(this.el().grid_w) > 10,
                building: (this.th().q_space_w || 0) > 10,
                dhw: (this.th().q_dhw_w || 0) > 10 || (this.th().q_draw_w || 0) > 400,
                outdoor: (this.th().q_loss_w || 0) > 10
            };
            const heat = key === 'hp' || key === 'building' || key === 'dhw' || key === 'outdoor';
            let cls = 'site-plant-node';
            if (live[key]) cls += ' active';
            if (heat) cls += ' heat';
            return cls;
        },

        gridLabel() {
            const g = this.el().grid_w || 0;
            if (g > 20) return 'import';
            if (g < -20) return 'export';
            return 'idle';
        },

        copLabel() {
            const cop = Number(this.th().cop);
            if (!cop) return '';
            return ' · COP ' + cop.toFixed(1);
        },

        sgLabel() {
            const n = Number(this.th().sg_ready) || 0;
            return ['normal', 'block', 'rec', 'boost'][n] || String(n);
        },

        async applyFacility() {
            const siteId = Alpine.store('site').selectedId;
            if (!siteId || !this.selected) return;
            this.applying = true;
            try {
                await siteApi.applyFacility(siteId, this.selected);
                await Promise.all([
                    Alpine.store('site').loadSiteData(),
                    Alpine.store('app').loadSimulators()
                ]);
            } catch (e) {
                await Alpine.store('confirm').alert('Facility', e.message || String(e));
            }
            this.applying = false;
        }
    }));
});
