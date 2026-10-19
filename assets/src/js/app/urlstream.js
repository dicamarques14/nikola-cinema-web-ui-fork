// The direct-URL player page (templates/views/urlstream/play.html,
// docs/url_streams.md): movi-player on /url/:id/stream, and the import's
// status under it. Once the import has made a torrent of the file, the page
// moves to the ordinary resource page -- with the position carried over
// through the same PUT /watch/position the player uses.
import av from '../lib/av';

const POLL_MS = 3000;
const SWITCH_IN_S = 5;
const MIN_CARRY_S = 30;

function fill(text, n) {
    return (text || '').replace('{n}', String(n));
}

function posKey(id) {
    return `wt-url-pos:${id}`;
}

function mediaOf(root) {
    return root.querySelector('movi-player') || root.querySelector('video');
}

async function carryPosition(media, status) {
    const pos = media ? Number(media.currentTime) || 0 : 0;
    const dur = media ? Number(media.duration) || 0 : 0;
    if (pos < MIN_CARRY_S || !status.info_hash || !status.path) return;
    try {
        await fetch('/watch/position', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json', 'X-CSRF-TOKEN': window._CSRF || '' },
            body: JSON.stringify({ resource_id: status.info_hash, path: status.path, position: pos, duration: dur > 0 ? dur : 0 }),
            keepalive: true,
        });
    } catch (e) { /* the switch goes on without it */ }
}

function watchImport(root, id) {
    const box = root.querySelector('#url-import-status');
    if (!box) return () => {};
    const text = box.querySelector('.url-import-text');
    const go = box.querySelector('.url-import-go');
    const stay = box.querySelector('.url-import-stay');
    const l = root.dataset;
    let timer = null;
    let countdown = null;
    let stopped = false;
    let stayed = false;
    let status = null;

    const show = (msg) => { box.hidden = false; text.textContent = msg; };
    const switchNow = async () => {
        if (!status || !status.resource_url) return;
        clearInterval(countdown);
        await carryPosition(mediaOf(root), status);
        try { localStorage.removeItem(posKey(id)); } catch (e) { /* private mode */ }
        window.location.assign(status.resource_url);
    };
    go.addEventListener('click', switchNow);
    stay.addEventListener('click', () => {
        stayed = true;
        clearInterval(countdown);
        stay.hidden = true;
        // The switch stays offered; only the countdown goes.
        text.textContent = '';
    });

    const ready = () => {
        go.hidden = false;
        stay.hidden = false;
        let left = SWITCH_IN_S;
        show(fill(l.lReady, left));
        countdown = setInterval(() => {
            left -= 1;
            if (stayed) return;
            if (left <= 0) { switchNow(); return; }
            show(fill(l.lReady, left));
        }, 1000);
    };

    const poll = async () => {
        if (stopped) return;
        try {
            const res = await fetch(root.dataset.statusUrl, { headers: { Accept: 'application/json' } });
            if (res.ok) status = await res.json();
        } catch (e) { /* try again */ }
        if (stopped) return;
        if (status) {
            const pct = status.total > 0 ? Math.min(100, Math.floor(status.done * 100 / status.total)) : 0;
            if (status.status === 'imported' && status.resource_url) { ready(); return; }
            if (status.status === 'failed') { show(l.lFailed); return; }
            if (!status.import_enabled) { show(l.lProxyOnly); return; }
            if (status.status === 'importing') {
                if (status.phase === 'downloading') show(fill(l.lDownloading, pct));
                else if (status.phase === 'hashing') show(fill(l.lHashing, pct));
                else if (status.phase === 'storing') show(l.lStoring);
                else show(l.lQueued);
            }
        }
        timer = setTimeout(poll, POLL_MS);
    };
    poll();
    return () => { stopped = true; clearTimeout(timer); clearInterval(countdown); };
}

// The page's own resume, for a viewer who comes back before the import is
// done: kept locally, as the file has no resource id yet.
function rememberPosition(root, id) {
    const media = mediaOf(root);
    if (!media) return () => {};
    let restored = false;
    const restore = () => {
        if (restored) return;
        restored = true;
        let saved = 0;
        try { saved = Number(localStorage.getItem(posKey(id))) || 0; } catch (e) { /* private mode */ }
        const dur = Number(media.duration) || 0;
        if (saved >= MIN_CARRY_S && (!dur || saved < dur - 60)) media.currentTime = saved;
    };
    let last = 0;
    const save = () => {
        const now = Date.now();
        if (now - last < 5000) return;
        last = now;
        try { localStorage.setItem(posKey(id), String(Math.floor(media.currentTime || 0))); } catch (e) { /* private mode */ }
    };
    media.addEventListener('loadedmetadata', restore);
    media.addEventListener('timeupdate', save);
    return () => {
        media.removeEventListener('loadedmetadata', restore);
        media.removeEventListener('timeupdate', save);
    };
}

let cleanup = [];

av(async function () {
    const root = this.closest('#url-player') || this;
    const video = root.querySelector('video.player');
    const id = video ? video.dataset.urlSourceId : '';
    const { initPlayer } = await import('../lib/player/Player');
    await initPlayer(root);
    cleanup.push(rememberPosition(root, id));
    if (root.dataset.import === 'true') cleanup.push(watchImport(root, id));
}, async function () {
    for (const f of cleanup) f();
    cleanup = [];
    const { destroyPlayer } = await import('../lib/player/Player');
    destroyPlayer();
});

export {}
