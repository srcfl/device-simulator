// Logs Viewer Component
// Displays protocol logs with formatting

export function logsViewer() {
    return {
        copySuccess: false,

        getOpClass(log) {
            if (log.operation?.includes('read')) return 'log-op read';
            if (log.operation?.includes('write')) return 'log-op write';
            if (log.operation?.includes('connect')) return 'log-op connect';
            return 'log-op message';
        },

        formatTime(timestamp) {
            return new Date(timestamp).toLocaleTimeString();
        },

        formatDetails(log) {
            if (log.values) {
                return log.values.join(', ');
            }
            if (log.details) {
                const d = log.details;
                if (d.topic) return `${d.topic}: ${d.payload}`;
                if (d.action) {
                    let payloadStr = '';
                    if (d.payload && d.payload !== '{}' && d.payload !== 'null') {
                        try {
                            const parsed = typeof d.payload === 'string' ? JSON.parse(d.payload) : d.payload;
                            payloadStr = Object.keys(parsed).length > 0 ? ` ${JSON.stringify(parsed)}` : '';
                        } catch { payloadStr = ` ${d.payload}`; }
                    }
                    return `${d.action}${payloadStr}`;
                }
                return JSON.stringify(d);
            }
            if (log.count) {
                return `x${log.count}`;
            }
            return '';
        },

        copyLogs() {
            const store = Alpine.store('simulator');
            const logText = store.logs.map(log => {
                const time = this.formatTime(log.timestamp);
                const op = log.operation || '';
                const addr = log.address !== undefined ? log.address : '';
                const details = this.formatDetails(log);
                return `${time} ${op} ${addr} ${details}`.trim().replace(/\s+/g, ' ');
            }).join('\n');

            const textarea = document.createElement('textarea');
            textarea.value = logText;
            textarea.style.position = 'fixed';
            textarea.style.opacity = '0';
            document.body.appendChild(textarea);
            textarea.select();
            document.execCommand('copy');
            document.body.removeChild(textarea);

            this.copySuccess = true;
            setTimeout(() => this.copySuccess = false, 1500);
        }
    };
}
