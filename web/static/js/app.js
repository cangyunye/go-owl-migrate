const api = {
    async get(path) {
        const resp = await fetch(path);
        if (!resp.ok) {
            const err = await resp.json().catch(() => ({}));
            throw new Error(err.error || `${resp.status} ${resp.statusText}`);
        }
        return resp.json();
    },
    async post(path, body) {
        const resp = await fetch(path, {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(body || {})
        });
        if (!resp.ok) {
            const err = await resp.json().catch(() => ({}));
            throw new Error(err.error || `${resp.status} ${resp.statusText}`);
        }
        return resp.json();
    },
    async put(path, body) {
        const resp = await fetch(path, {
            method: 'PUT',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(body || {})
        });
        if (!resp.ok) {
            const err = await resp.json().catch(() => ({}));
            throw new Error(err.error || `${resp.status} ${resp.statusText}`);
        }
        return resp.json();
    },
    async del(path) {
        const resp = await fetch(path, {method: 'DELETE'});
        if (!resp.ok) {
            const err = await resp.json().catch(() => ({}));
            throw new Error(err.error || `${resp.status} ${resp.statusText}`);
        }
        return resp.json();
    }
};

// jobUI: shared start/progress/cancel wiring for pages that launch a job
// (migrate / export / import) and stream progress over WebSocket.
const jobUI = {
    jobId: null,
    ws: null,
    logEl: null,

    bind(logSelector) {
        this.logEl = document.querySelector(logSelector);
    },

    async start(endpoint, body) {
        const resp = await api.post(endpoint, body || {});
        this.jobId = resp.job_id;
        const badge = document.getElementById('job-id-badge');
        if (badge) badge.textContent = resp.job_id;
        const panel = document.getElementById('progress-panel');
        if (panel) panel.style.display = 'block';
        const start = document.getElementById('btn-start');
        const cancel = document.getElementById('btn-cancel');
        if (start) start.style.display = 'none';
        if (cancel) cancel.style.display = 'inline-block';
        this.connect(resp.job_id);
        return resp;
    },

    async cancel() {
        if (!this.jobId) return;
        await api.del('/api/v1/jobs/' + this.jobId);
        this.log('⚠️ 已发送取消请求');
    },

    connect(jobId) {
        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        this.ws = new WebSocket(proto + '//' + location.host + '/api/v1/jobs/' + jobId + '/ws');
        this.ws.onmessage = (e) => {
            const m = JSON.parse(e.data);
            if (m.type === 'progress') {
                this.log(`[${m.seq}] ${m.event} ${m.schema || ''}.${m.table || ''} → ${m.rows} rows`);
            } else if (m.type === 'complete') {
                this.log('✅ 完成: ' + (m.status || ''));
                this.finish();
            } else if (m.type === 'cancelled') {
                this.log('🚫 已取消');
                this.finish();
            } else if (m.type === 'error') {
                this.log('❌ ' + (m.error || ''));
                this.finish();
            }
        };
        this.ws.onclose = () => this.log('— 连接关闭 —');
    },

    finish() {
        const start = document.getElementById('btn-start');
        const cancel = document.getElementById('btn-cancel');
        if (start) start.style.display = 'inline-block';
        if (cancel) cancel.style.display = 'none';
        if (this.ws) this.ws.close();
    },

    log(text) {
        if (!this.logEl) return;
        this.logEl.textContent += text + '\n';
        this.logEl.scrollTop = this.logEl.scrollHeight;
    }
};

// renderGenFiles: show generated SQL files as a selectable list + preview.
function renderGenFiles(files, listEl, previewEl) {
    listEl.innerHTML = '';
    if (!files || files.length === 0) {
        previewEl.textContent = '（无输出文件）';
        return;
    }
    files.forEach((f, i) => {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'file-tab' + (i === 0 ? ' active' : '');
        btn.textContent = f.name;
        btn.addEventListener('click', () => {
            listEl.querySelectorAll('.file-tab').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            previewEl.textContent = f.content;
        });
        listEl.appendChild(btn);
    });
    previewEl.textContent = files[0].content;
}
