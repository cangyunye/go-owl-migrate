const api = {
    async get(path) {
        const resp = await fetch(path);
        if (!resp.ok) throw new Error(`${resp.status} ${resp.statusText}`);
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
