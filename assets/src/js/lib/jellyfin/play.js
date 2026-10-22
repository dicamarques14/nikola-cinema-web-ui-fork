// The Jellyfin player page's own extras (templates/views/jellyfin/play.html):
// the resume point, kept in this browser only, and the "up next" prompt at
// the end of an episode. The player itself is lib/player (initPlayer).

const KEY = 'jellyfin:pos:';
// Not worth resuming: the first half minute, and the last one (credits --
// a finished episode starts over).
const MIN_RESUME = 30;
const END_MARGIN = 60;
export const UP_NEXT_SECONDS = 10;

function store() {
    try { return window.localStorage; } catch (e) { return null; }
}

// resumeAt is where a file with saved position `pos` and length `duration`
// should open, 0 for the start.
export function resumeAt(pos, duration) {
    const p = Number(pos);
    if (!Number.isFinite(p) || p < MIN_RESUME) return 0;
    const d = Number(duration);
    if (Number.isFinite(d) && d > 0 && p > d - END_MARGIN) return 0;
    return p;
}

export function loadPosition(id, s = store()) {
    if (!s || !id) return 0;
    const v = Number(s.getItem(KEY + id));
    return Number.isFinite(v) ? v : 0;
}

// savePosition remembers `pos` for id, or forgets it near either end.
export function savePosition(id, pos, duration, s = store()) {
    if (!s || !id) return;
    try {
        if (resumeAt(pos, duration) > 0) s.setItem(KEY + id, String(Math.floor(pos)));
        else s.removeItem(KEY + id);
    } catch (e) { /* storage full or off */ }
}

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
    const id = root.getAttribute('data-item-id') || '';
    const nextHref = root.getAttribute('data-next-href') || '';
    const offs = [];
    const on = (el, ev, fn) => { el.addEventListener(ev, fn); offs.push(() => el.removeEventListener(ev, fn)); };

    // ---- resume ------------------------------------------------------
    const note = root.querySelector('.jellyfin-resume-note');
    let resumed = false;
    const tryResume = () => {
        if (resumed) return;
        resumed = true;
        const at = resumeAt(loadPosition(id), video.duration);
        if (!at) return;
        video.currentTime = at;
        if (!note) return;
        note.textContent = `Resumed at ${clock(at)}. `;
        const again = document.createElement('button');
        again.type = 'button';
        again.className = 'btn btn-sm btn-ghost underline';
        again.textContent = 'Start over';
        again.addEventListener('click', () => {
            video.currentTime = 0;
            savePosition(id, 0, video.duration);
            note.hidden = true;
        });
        note.appendChild(again);
        note.hidden = false;
    };
    on(video, 'loadedmetadata', tryResume);
    if (video.readyState >= 1) tryResume();
    let last = 0;
    on(video, 'timeupdate', () => {
        const now = Date.now();
        if (now - last < 5000) return;
        last = now;
        savePosition(id, video.currentTime, video.duration);
    });
    on(video, 'pause', () => savePosition(id, video.currentTime, video.duration));

    // ---- up next -----------------------------------------------------
    const card = root.querySelector('#jellyfin-up-next');
    let timer = null;
    const stop = () => { if (timer) clearInterval(timer); timer = null; };
    offs.push(stop);
    const hide = () => { stop(); if (card) card.hidden = true; };
    on(video, 'ended', () => {
        savePosition(id, 0, video.duration);
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
