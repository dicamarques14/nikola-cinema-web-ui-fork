// The picker's two answers -- which subtitle, which audio -- put on screen by
// <movi-player> (docs/superpowers/specs/2026-10-15-movi-subtitle-bridge.md).
//
// The picker (track-dialog.js) is the one place a viewer chooses; movi-player's
// own captions and audio menus are switched off. What it offers movi-player
// draws through its own API, which is not <track>:
//
//   embedded   selectSubtitleTrack(streamIndex) / selectAudioTrack(streamIndex)
//              -- the file's stream index, which is what the server renders as
//              data-stream on the chip.
//   side-loaded addSubtitleTrack({url, lang, label, format}) then
//              selectSubtitleLang(lang) -- fetched on selection, not on add.
//              `lang` is the KEY (an entry with the same lang replaces the
//              old one), so it is the chip's id here, never a language:
//              webtor offers several tracks in one.
//   generated  appendSubtitleCues(key, label, cues) -- the AI translation: the
//              growing file is fetched here, parsed, and the cues put in
//              as they come, so nothing blanks while a revision is read.
//
// One bridge per player element (`el.__wtTracks`, set by Player.jsx): module
// state is per bundle, not per page (web-ui CLAUDE.md, splitChunks).

// parseVttCues reads a WebVTT (or SRT) text into {start, end, text} cues.
// Settings after the timing, NOTE/STYLE/REGION blocks, cue identifiers and
// markup tags are dropped; a cue with no text is not a cue.
const TIME = /(?:(\d+):)?(\d{1,2}):(\d{2})[.,](\d{1,3})/;
function seconds(m) {
    return (m[1] ? Number(m[1]) * 3600 : 0) + Number(m[2]) * 60 + Number(m[3]) + Number(m[4].padEnd(3, '0')) / 1000;
}
export function parseVttCues(text) {
    const out = [];
    if (typeof text !== 'string') return out;
    const blocks = text.replace(/^﻿/, '').split(/\r?\n\r?\n+/);
    for (const block of blocks) {
        const lines = block.split(/\r?\n/);
        const at = lines.findIndex((l) => l.includes('-->'));
        if (at < 0) continue;
        const arrow = lines[at].indexOf('-->');
        const a = TIME.exec(lines[at].slice(0, arrow));
        const b = TIME.exec(lines[at].slice(arrow + 3));
        if (!a || !b) continue;
        const start = seconds(a);
        const end = seconds(b);
        const body = lines.slice(at + 1).join('\n').replace(/<[^>]+>/g, '').trim();
        if (end > start && body) out.push({ start, end, text: body });
    }
    return out;
}

// formatOf says how movi-player should read a subtitle URL. Its own guess
// takes any ".srt" in the URL for SRT, which is wrong for the proxy's
// "file.srt~vtt" (already WebVTT).
export function formatOf(url) {
    let path = String(url || '');
    const q = path.search(/[?#]/);
    if (q >= 0) path = path.slice(0, q);
    if (path.includes('~vtt') || /\.vtt$/i.test(path)) return 'vtt';
    if (/\.srt$/i.test(path)) return 'srt';
    if (/\.(ttml|dfxp)$/i.test(path)) return 'ttml';
    return undefined;
}

export function createTrackBridge(el, { fetchText = defaultFetchText, onError = () => {} } = {}) {
    // The element's metadata is in: embedded tracks are known and the engine
    // takes selections. A new source (the player loads another file) is a
    // new `ready`, see `rearm`.
    let ready = null;
    let disposed = false;
    // The loadedmetadata listener still waiting, if any: dispose takes it off.
    let waiting = null;
    const unwait = () => {
        if (waiting) el.removeEventListener('loadedmetadata', waiting);
        waiting = null;
    };
    const arm = () => {
        unwait();
        ready = new Promise((resolve) => {
            if (el.player && el.readyState >= 1) {
                resolve();
                return;
            }
            const done = () => { unwait(); resolve(); };
            waiting = done;
            el.addEventListener('loadedmetadata', done);
        });
    };
    arm();
    const rearm = () => arm();
    el.addEventListener('emptied', rearm);

    // The last selection asked for wins: an async select that lands after a
    // later one is dropped (persistTrackChoice's sequence, same reason).
    let seq = 0;
    const next = () => ++seq;
    // The chip the latest showSubtitle is for, while it is in flight.
    let wanted = '';

    // What this bridge put in the player, keyed by chip id: the key of an
    // external track and of a generated one.
    const registered = new Map(); // id -> { generated: bool, cues: [] }
    let activeKey = '';
    let activeEmbedded = null;

    const player = () => (disposed ? null : el.player || null);

    async function off(p) {
        try { await p.selectSubtitleLang(null); } catch (e) { /* nothing was showing */ }
        try { await p.selectSubtitleTrack(null); } catch (e) { /* nothing was showing */ }
        activeKey = '';
        activeEmbedded = null;
    }

    // register puts a side-loaded track in the player once, under the chip's
    // id. addSubtitleTrack replaces an entry with the same key, so repeated
    // calls are safe, and a changed URL (a rotated token) is picked up.
    function register(p, sel) {
        const known = registered.get(sel.id);
        if (known && known.src === sel.src && known.label === sel.label) return;
        p.addSubtitleTrack({ url: sel.src, lang: sel.id, label: sel.label || 'Subtitle', format: formatOf(sel.src) });
        registered.set(sel.id, { generated: false, src: sel.src, label: sel.label, cues: [] });
    }

    // reloadTranslation: the translation as it stands now, into the player's
    // generated track. The whole file is read each time (a revision is the
    // partial document so far) and appendSubtitleCues keeps what it has and
    // adds the rest: duplicates are dropped by text and start, so a re-read
    // costs nothing on screen. Returns the cue count and where they end.
    //
    // `pending` is the caller's word on whether the run is still writing
    // (useSubtitleTranslation: true until onDone). Left out, the track keeps
    // what it was last told -- a pick of a chip whose run is unknown here --
    // and a track never told is pending only while it has no cue at all.
    async function reloadTranslation(id, src, { label = '', pending } = {}) {
        const text = await fetchText(src);
        const p = player();
        if (!p || text === null) return { cues: 0, lastEnd: null, ok: false };
        const cues = parseVttCues(text);
        let rec = registered.get(id);
        if (!rec || !rec.generated) {
            rec = { generated: true, src, label, cues: [], pending: undefined };
            registered.set(id, rec);
        }
        rec.label = label || rec.label;
        rec.cues = cues;
        if (typeof pending === 'boolean') rec.pending = pending;
        const isPending = typeof rec.pending === 'boolean' ? rec.pending : cues.length === 0;
        p.appendSubtitleCues(id, rec.label || 'Subtitle', cues, isPending);
        return { cues: cues.length, lastEnd: cues.length ? cues[cues.length - 1].end : null, ok: true };
    }

    // failed: a side-loaded or generated pick that did not load. movi-player
    // has already stopped the external track it was drawing (its
    // selectSubtitleLang stops first, fetches second), so what is on screen
    // is nothing; the bridge says so, and the caller hears about it.
    async function failed(p, id) {
        try { await p.selectSubtitleLang(null); } catch (e) { /* nothing showing */ }
        activeKey = '';
        onError(id);
    }

    // sel: { id, provider, src, label, stream } -- null, or id 'none': off.
    async function showSubtitle(sel) {
        const mine = next();
        wanted = (sel && sel.provider !== 'MediaProbe' && sel.id) || '';
        await ready;
        const p = player();
        if (!p || mine !== seq) return false;
        if (!sel || !sel.id || sel.id === 'none') {
            await off(p);
            return true;
        }
        if (sel.provider === 'MediaProbe') {
            const stream = Number(sel.stream);
            if (!Number.isFinite(stream)) return false;
            await off(p);
            if (mine !== seq) return false;
            const ok = await p.selectSubtitleTrack(stream);
            if (mine !== seq) return false;
            if (ok === false) { onError(sel.id); return false; }
            activeEmbedded = stream;
            return true;
        }
        if (!sel.src) return false;
        // An embedded track on screen goes first: selectSubtitleLang drops it
        // only once its own fetch has worked, so a failed or slow one would
        // leave it drawn -- and, between the two, both would be. Nothing is
        // drawn from here until the new track is in.
        if (activeEmbedded !== null) {
            try { await p.selectSubtitleTrack(null); } catch (e) { /* already off */ }
            activeEmbedded = null;
            if (mine !== seq) return false;
        }
        let ok;
        if (sel.provider === 'Translated') {
            const r = await reloadTranslation(sel.id, sel.src, { label: sel.label });
            if (mine !== seq) return false;
            if (!r.ok) { await failed(p, sel.id); return false; }
            ok = await p.selectSubtitleLang(sel.id);
        } else {
            register(p, sel);
            ok = await p.selectSubtitleLang(sel.id);
        }
        if (mine !== seq) return false;
        if (ok === false) { await failed(p, sel.id); return false; }
        activeKey = sel.id;
        activeEmbedded = null;
        return true;
    }

    // stream: the file's stream index of an embedded audio track.
    async function showAudio(stream) {
        await ready;
        const p = player();
        const n = Number(stream);
        if (!p || !Number.isFinite(n)) return false;
        const ok = await p.selectAudioTrack(n);
        return ok !== false;
    }

    // dropMissing takes out the side-loaded and generated tracks whose chip is
    // gone (a deleted upload, the AI item of the old language). Reports
    // whether the one showing was among them.
    //
    // It also ends a selection still in flight for a chip that is gone:
    // landing after this it would put the deleted track back. One for a chip
    // that stays is left to land.
    //
    // A generated track leaves movi-player entirely (removeSubtitleCues). A
    // side-loaded one cannot -- movi-player 0.4.1 has no call that takes an
    // external entry out -- so it is switched off if it was showing and
    // forgotten here; its menu is hidden (controlslist nocc), and a chip that
    // comes back under the same id replaces the entry (addSubtitleTrack).
    function dropMissing(keepIds) {
        const keep = new Set(keepIds);
        if (wanted && wanted !== 'none' && !keep.has(wanted)) {
            seq++;
            wanted = '';
        }
        let wasShowing = false;
        for (const id of Array.from(registered.keys())) {
            if (keep.has(id)) continue;
            const rec = registered.get(id);
            const p = player();
            if (p) {
                if (rec.generated) p.removeSubtitleCues(id);
                else if (id === activeKey) p.selectSubtitleLang(null);
            }
            if (id === activeKey) {
                wasShowing = true;
                activeKey = '';
            }
            registered.delete(id);
        }
        return wasShowing;
    }

    return {
        get ready() { return ready; },
        showSubtitle,
        showAudio,
        reloadTranslation,
        dropMissing,
        // The chip id of the side-loaded or generated track on screen, ''
        // for none or an embedded one (activeStream says which).
        activeId: () => activeKey,
        activeStream: () => activeEmbedded,
        // The cues the bridge holds for a generated track (what the credits
        // guess and the translation's "where do the cues end" read).
        cuesOf: (id) => (registered.has(id) ? registered.get(id).cues : []),
        dispose() {
            disposed = true;
            seq++;
            unwait();
            el.removeEventListener('emptied', rearm);
            registered.clear();
        },
    };
}

async function defaultFetchText(url) {
    try {
        const res = await fetch(url, { credentials: 'same-origin' });
        if (!res.ok) return null;
        return await res.text();
    } catch (e) {
        return null;
    }
}
