// The Jellyfin player page's own extras (templates/views/jellyfin/play.html):
// the "up next" prompt at the end of an episode and the addon subtitles. The
// player itself is lib/player (initPlayer); the resume point is the player's
// own (useWatchHistory: the <video>'s data-resource-id="jellyfin" and
// data-path = the item id, stored server-side for Continue watching).

export const UP_NEXT_SECONDS = 10;

export function clock(sec) {
    const t = Math.max(0, Math.floor(sec));
    const h = Math.floor(t / 3600);
    const m = Math.floor((t % 3600) / 60);
    const s = String(t % 60).padStart(2, '0');
    return h ? `${h}:${String(m).padStart(2, '0')}:${s}` : `${m}:${s}`;
}

// wirePlayPage hooks the extras onto the mounted player `video` inside
// `root`. Returns an undo for destroy.
export function wirePlayPage(root, video, { go = (href) => window.location.assign(href) } = {}) {
    const nextHref = root.getAttribute('data-next-href') || '';
    const offs = [];
    const on = (el, ev, fn) => { el.addEventListener(ev, fn); offs.push(() => el.removeEventListener(ev, fn)); };

    // ---- up next -----------------------------------------------------
    const card = root.querySelector('#jellyfin-up-next');
    let timer = null;
    const stop = () => { if (timer) clearInterval(timer); timer = null; };
    offs.push(stop);
    const hide = () => { stop(); if (card) card.hidden = true; };
    on(video, 'ended', () => {
        if (!card || !nextHref) return;
        let left = UP_NEXT_SECONDS;
        const n = card.querySelector('.jellyfin-countdown');
        if (n) n.textContent = String(left);
        card.hidden = false;
        stop();
        timer = setInterval(() => {
            left -= 1;
            if (n) n.textContent = String(Math.max(left, 0));
            if (left <= 0) { stop(); go(nextHref); }
        }, 1000);
    });
    // Watching again (a seek back, play) is a no to the prompt.
    on(video, 'play', hide);
    on(video, 'seeking', hide);
    if (card) {
        const cancel = card.querySelector('.jellyfin-up-next-cancel');
        if (cancel) on(cancel, 'click', hide);
    }
    return () => offs.forEach((f) => f());
}

// audioStreams lines the picker's audio chips up with the tracks movi-player
// demuxed. A chip's data-stream is the server's guess at the track's index in
// the file (Jellyfin's index less the external subtitles it counts first). It
// stands when every guess is a track movi-player has; otherwise, with as many
// chips as tracks, the chips take the track ids in order (both lists are in
// file order). Returns the stream per chip.
export function audioStreams(streams, tracks) {
    const ids = (tracks || []).map((t) => Number(t.id));
    const have = new Set(ids);
    const nums = streams.map(Number);
    if (nums.every((n) => have.has(n))) return nums;
    if (ids.length === nums.length) return ids;
    return nums;
}

// markUnplayable flags a chip whose track the player refused, so the choice
// says why nothing changed instead of failing in silence.
export function markUnplayable(chip) {
    if (!chip || chip.getAttribute('data-unplayable') === 'true') return;
    chip.setAttribute('data-unplayable', 'true');
    const b = chip.ownerDocument.createElement('span');
    b.className = 'badge badge-xs badge-warning uppercase tracking-wider';
    b.textContent = (chip.getAttribute('data-codec') || 'codec') + ' · can\u2019t play here';
    chip.appendChild(b);
}

// wireAudio reconciles the chips once the player knows the file's tracks and
// watches the switches for a refusal. Returns the undo.
export function wireAudio(root, video) {
    const chips = () => Array.from(root.querySelectorAll('#audio-tracks .audio'));
    if (!chips().length) return () => {};
    let wrapped = null;
    const sync = () => {
        const p = video.player;
        if (!p || typeof p.getAudioTracks !== 'function') return;
        const list = chips();
        const next = audioStreams(list.map((c) => c.getAttribute('data-stream')), p.getAudioTracks());
        let moved = null;
        list.forEach((c, i) => {
            if (Number(c.getAttribute('data-stream')) !== next[i] && c.classList.contains('track-chip-active')) moved = next[i];
            c.setAttribute('data-stream', String(next[i]));
        });
        // The picker may already have asked for the active chip's old guess.
        if (moved !== null && typeof p.selectAudioTrack === 'function') p.selectAudioTrack(moved);
        if (wrapped || typeof p.selectAudioTrack !== 'function') return;
        const orig = p.selectAudioTrack;
        p.selectAudioTrack = function (id) {
            const ok = orig.call(this, id);
            if (ok === false) {
                markUnplayable(chips().find((c) => Number(c.getAttribute('data-stream')) === Number(id)));
            }
            return ok;
        };
        wrapped = { p, orig };
    };
    if (video.readyState >= 1) sync();
    video.addEventListener('loadedmetadata', sync);
    return () => {
        video.removeEventListener('loadedmetadata', sync);
        if (wrapped) wrapped.p.selectAudioTrack = wrapped.orig;
        wrapped = null;
    };
}

// addonChip builds a subtitle chip for one addon subtitle, like the
// server-rendered Jellyfin ones: side-loaded from its (same-origin) src.
export function addonChip(doc, sub) {
    const b = doc.createElement('button');
    b.type = 'button';
    b.setAttribute('role', 'radio');
    b.setAttribute('aria-checked', 'false');
    b.setAttribute('data-id', String(sub.id));
    b.setAttribute('data-provider', 'Addon');
    b.setAttribute('data-src', String(sub.src));
    b.setAttribute('data-label', `${sub.label} · ${sub.source}`);
    b.setAttribute('data-srclang', String(sub.lang || 'und'));
    b.setAttribute('data-lang', String(sub.lang || 'und'));
    b.className = 'subtitle track-chip min-h-12 px-4';
    b.innerHTML = '<svg class="chip-check w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" aria-hidden="true" hidden><polyline points="20 6 9 17 4 12"/></svg>';
    const badge = doc.createElement('span');
    badge.className = 'chip-origin badge badge-xs font-mono font-semibold tracking-wider bg-base-300/80 border-w-line/50 text-w-sub';
    badge.title = String(sub.source || 'Addon');
    badge.textContent = 'ADDON';
    const label = doc.createElement('span');
    label.className = 'max-w-[14rem] truncate';
    label.textContent = `${sub.label} · ${sub.source}`;
    label.title = label.textContent;
    b.append(badge, label);
    return b;
}

// loadAddonSubtitles asks the server for the item's subtitles from the
// viewer's Stremio subtitle addons and adds them after Jellyfin's own.
// Returns how many were added; a failure adds none.
export async function loadAddonSubtitles(root, { fetchJSON = defaultFetchJSON } = {}) {
    const url = root.getAttribute('data-addon-subtitles');
    const box = root.querySelector('#subtitle-tracks');
    if (!url || !box) return 0;
    let subs;
    try { subs = await fetchJSON(url); } catch (e) { return 0; }
    if (!Array.isArray(subs) || !subs.length || !root.isConnected) return 0;
    const doc = root.ownerDocument;
    for (const sub of subs) {
        if (!sub || !sub.id || !sub.src) continue;
        box.appendChild(addonChip(doc, sub));
    }
    const none = root.querySelector('#jellyfin-no-subtitles');
    if (none) none.hidden = true;
    return subs.length;
}

async function defaultFetchJSON(url) {
    const r = await fetch(url, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
    if (!r.ok) throw new Error(`status ${r.status}`);
    return r.json();
}
