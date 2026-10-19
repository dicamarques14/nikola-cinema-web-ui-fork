import test from 'node:test';
import assert from 'node:assert/strict';
import { createTrackBridge, parseVttCues, formatOf } from './movi-tracks.js';

// A <movi-player> as far as the bridge sees it: an event target with a
// `player` that records what it was asked and answers like the real one.
function fakeElement({ ready = true } = {}) {
    const calls = [];
    const gate = { resolve: null };
    const player = {
        calls,
        addSubtitleTrack: (e) => calls.push(['add', e]),
        appendSubtitleCues: (k, l, c, p) => calls.push(['append', k, l, c.length, !!p]),
        removeSubtitleCues: (k) => { calls.push(['remove', k]); return true; },
        selectSubtitleLang: async (k) => { calls.push(['lang', k]); if (gate.resolve && k === 'slow') await new Promise((r) => { gate.resolve = r; }); return true; },
        selectSubtitleTrack: async (n) => { calls.push(['track', n]); return true; },
        selectAudioTrack: async (n) => { calls.push(['audio', n]); return true; },
    };
    const el = new EventTarget();
    el.player = player;
    el.readyState = ready ? 4 : 0;
    return { el, player, calls, gate };
}

const SAMPLE = 'WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nx\n';
const fetchSample = async () => SAMPLE;

test('parseVttCues: timings, multi-line text, tags and settings dropped', () => {
    const cues = parseVttCues('﻿WEBVTT\n\nNOTE a comment\n\n1\n00:00:01.000 --> 00:00:03.500 align:start\nHello <i>there</i>\nsecond line\n\n01:02:03,250 --> 01:02:04,000\nSRT style\n\n00:00:09.000 --> 00:00:08.000\nbackwards\n\n00:00:10.000 --> 00:00:11.000\n\n');
    assert.deepEqual(cues, [
        { start: 1, end: 3.5, text: 'Hello there\nsecond line' },
        { start: 3723.25, end: 3724, text: 'SRT style' },
    ]);
    assert.deepEqual(parseVttCues(null), []);
});

test('formatOf: the proxy\'s ~vtt is WebVTT whatever the name says', () => {
    assert.equal(formatOf('https://h/a/file.srt~vtt?token=x'), 'vtt');
    assert.equal(formatOf('https://h/a/file.srt?token=x.vtt'), 'srt');
    assert.equal(formatOf('https://h/a/file.vtt'), 'vtt');
    assert.equal(formatOf('https://h/ext/abc/name.ttml'), 'ttml');
    assert.equal(formatOf('https://h/x~tr:pt/s0.vtt'), 'vtt');
    assert.equal(formatOf('https://h/ext/abc/name'), undefined);
});

test('a side-loaded track is fetched, parsed and selected by its chip id', async () => {
    const { el, calls } = fakeElement();
    const b = createTrackBridge(el, { fetchText: fetchSample });
    assert.equal(await b.showSubtitle({ id: 'os-7', provider: 'OpenSubtitles', src: 'https://h/os7.srt~vtt', label: 'English' }), true);
    assert.deepEqual(calls, [
        ['append', 'os-7', 'English', 1, false],
        ['lang', 'os-7'],
    ]);
    assert.equal(b.activeId(), 'os-7');
    assert.equal(b.activeStream(), null);
    assert.equal(b.cuesOf('os-7').length, 1);
});

test('two tracks in one language do not replace each other: the key is the id', async () => {
    const { el, calls } = fakeElement();
    const b = createTrackBridge(el, { fetchText: fetchSample });
    await b.showSubtitle({ id: 'os-1', provider: 'OpenSubtitles', src: 'https://h/1.vtt', label: 'English' });
    await b.showSubtitle({ id: 'os-2', provider: 'OpenSubtitles', src: 'https://h/2.vtt', label: 'English (SDH)' });
    const keys = calls.filter((c) => c[0] === 'append').map((c) => c[1]);
    assert.deepEqual(keys, ['os-1', 'os-2']);
});

test('the same track picked again re-fetches and appends cues again', async () => {
    const { el, calls } = fakeElement();
    const b = createTrackBridge(el, { fetchText: fetchSample });
    const sel = { id: 'us-1', provider: 'UserSubtitle', src: 'https://h/u.vtt', label: 'Mine' };
    await b.showSubtitle(sel);
    await b.showSubtitle(sel);
    assert.equal(calls.filter((c) => c[0] === 'append').length, 2);
    await b.showSubtitle({ ...sel, src: 'https://h/u.vtt?token=new' });
    assert.equal(calls.filter((c) => c[0] === 'append').length, 3);
});

test('an embedded track is selected by the file\'s stream index, the external one switched off first', async () => {
    const { el, calls } = fakeElement();
    const b = createTrackBridge(el, { fetchText: fetchSample });
    await b.showSubtitle({ id: 'os-1', provider: 'OpenSubtitles', src: 'https://h/1.vtt', label: 'English' });
    calls.length = 0;
    assert.equal(await b.showSubtitle({ id: 'mp-0', provider: 'MediaProbe', stream: '5' }), true);
    assert.deepEqual(calls, [['lang', null], ['track', null], ['track', 5]]);
    assert.equal(b.activeId(), '');
    assert.equal(b.activeStream(), 5);
});

test('None turns both renderers off', async () => {
    const { el, calls } = fakeElement();
    const b = createTrackBridge(el);
    await b.showSubtitle({ id: 'none', provider: '' });
    assert.deepEqual(calls, [['lang', null], ['track', null]]);
    await b.showSubtitle(null);
    assert.equal(b.activeId(), '');
});

test('audio is selected by stream index', async () => {
    const { el, calls } = fakeElement();
    const b = createTrackBridge(el);
    assert.equal(await b.showAudio('3'), true);
    assert.deepEqual(calls, [['audio', 3]]);
    assert.equal(await b.showAudio('nope'), false);
});

test('nothing is asked of the player before its metadata is in', async () => {
    const { el, calls } = fakeElement({ ready: false });
    const b = createTrackBridge(el);
    const p = b.showAudio(1);
    await new Promise((r) => setTimeout(r, 10));
    assert.deepEqual(calls, [], 'held until loadedmetadata');
    el.dispatchEvent(new Event('loadedmetadata'));
    assert.equal(await p, true);
    assert.deepEqual(calls, [['audio', 1]]);
});

test('a new source arms the wait again', async () => {
    const { el, calls } = fakeElement();
    const b = createTrackBridge(el);
    await b.showAudio(1);
    el.readyState = 0;
    el.dispatchEvent(new Event('emptied'));
    const p = b.showAudio(2);
    await new Promise((r) => setTimeout(r, 10));
    assert.equal(calls.filter((c) => c[0] === 'audio').length, 1);
    el.readyState = 4;
    el.dispatchEvent(new Event('loadedmetadata'));
    await p;
    assert.equal(calls.filter((c) => c[0] === 'audio').length, 2);
});

test('the last selection asked for wins: an earlier one in flight does not land', async () => {
    const { el, calls } = fakeElement();
    let release;
    const slow = new Promise((r) => { release = r; });
    el.player.selectSubtitleLang = async (k) => { calls.push(['lang', k]); if (k === 'a') await slow; return true; };
    const b = createTrackBridge(el, { fetchText: fetchSample });
    const first = b.showSubtitle({ id: 'a', provider: 'ExportTag', src: 'https://h/a.vtt', label: 'A' });
    await new Promise((r) => setTimeout(r, 5));
    const second = b.showSubtitle({ id: 'b', provider: 'ExportTag', src: 'https://h/b.vtt', label: 'B' });
    await second;
    release();
    assert.equal(await first, false);
    assert.equal(b.activeId(), 'b');
});

test('a translation goes in as cues, again and again, without being selected over', async () => {
    const { el, calls } = fakeElement();
    let doc = 'WEBVTT\n\n00:00:01.000 --> 00:00:02.000\none\n';
    const b = createTrackBridge(el, { fetchText: async () => doc });
    await b.showSubtitle({ id: 'tr-pt', provider: 'Translated', src: 'https://h/x~tr:pt/s.vtt', label: 'Português (AI)' });
    assert.deepEqual(calls, [['append', 'tr-pt', 'Português (AI)', 1, false], ['lang', 'tr-pt']]);
    doc += '\n00:00:03.000 --> 00:00:04.500\ntwo\n';
    calls.length = 0;
    const r = await b.reloadTranslation('tr-pt', 'https://h/x~tr:pt/s.vtt?rev=2');
    assert.deepEqual(r, { cues: 2, lastEnd: 4.5, ok: true });
    assert.deepEqual(calls, [['append', 'tr-pt', 'Português (AI)', 2, false]], 'no select, no add');
    assert.equal(b.cuesOf('tr-pt').length, 2);
    assert.deepEqual(b.cuesOf('missing'), []);
});

test('a translation that cannot be read is reported, not shown', async () => {
    const { el, calls } = fakeElement();
    const failed = [];
    const b = createTrackBridge(el, { fetchText: async () => null, onError: (id) => failed.push(id) });
    assert.equal(await b.showSubtitle({ id: 'tr-pt', provider: 'Translated', src: 'https://h/s.vtt', label: 'AI' }), false);
    assert.deepEqual(failed, ['tr-pt']);
    assert.deepEqual(calls, [['lang', null]], 'nothing appended or selected; whatever was showing is off');
    assert.equal(b.activeId(), '');
});

test('a track the player could not load is reported', async () => {
    const { el } = fakeElement();
    el.player.selectSubtitleLang = async () => false;
    const failed = [];
    const b = createTrackBridge(el, { fetchText: fetchSample, onError: (id) => failed.push(id) });
    assert.equal(await b.showSubtitle({ id: 'os-1', provider: 'OpenSubtitles', src: 'https://h/1.vtt', label: 'E' }), false);
    assert.deepEqual(failed, ['os-1']);
    assert.equal(b.activeId(), '');
});

test('a side-loaded fetch that fails is reported, not shown', async () => {
    const failed = [];
    const { el, calls } = fakeElement();
    const b = createTrackBridge(el, { fetchText: async () => null, onError: (id) => failed.push(id) });
    assert.equal(await b.showSubtitle({ id: 'us-1', provider: 'UserSubtitle', src: 'https://h/u.vtt', label: 'Mine' }), false);
    assert.deepEqual(failed, ['us-1']);
    assert.deepEqual(calls, [['lang', null]]);
    assert.equal(b.activeId(), '');
});

test('dropMissing takes out the tracks whose chip is gone and says if one was showing', async () => {
    const { el, calls } = fakeElement();
    const b = createTrackBridge(el, { fetchText: fetchSample });
    await b.showSubtitle({ id: 'us-1', provider: 'UserSubtitle', src: 'https://h/u.vtt', label: 'Mine' });
    await b.showSubtitle({ id: 'tr-pt', provider: 'Translated', src: 'https://h/t.vtt', label: 'AI' });
    calls.length = 0;
    // The translation is the one on screen, and its chip is gone.
    assert.equal(b.dropMissing(['us-1']), true);
    assert.deepEqual(calls, [['remove', 'tr-pt']], 'a generated track is removed with its cues');
    assert.equal(b.activeId(), '');
    assert.deepEqual(b.cuesOf('tr-pt'), []);
    // The upload is still there; an unrelated chip set takes nothing.
    calls.length = 0;
    assert.equal(b.dropMissing(['us-1', 'os-9']), false);
    assert.deepEqual(calls, []);
    // Gone while NOT showing: removed, but not reported as the one on screen.
    await b.showSubtitle({ id: 'none' });
    calls.length = 0;
    assert.equal(b.dropMissing([]), false);
    assert.deepEqual(calls, [['remove', 'us-1']]);
});

test('pending is the caller\'s word, and kept until it says otherwise', async () => {
    const { el, calls } = fakeElement();
    const doc = 'WEBVTT\n\n00:00:01.000 --> 00:00:02.000\none\n';
    const b = createTrackBridge(el, { fetchText: async () => doc });
    await b.reloadTranslation('tr-pt', 'https://h/s.vtt', { label: 'AI', pending: true });
    assert.deepEqual(calls.at(-1), ['append', 'tr-pt', 'AI', 1, true], 'a running translation with cues is still pending');
    // A pick of the chip says nothing about the run: the last word stands.
    await b.showSubtitle({ id: 'tr-pt', provider: 'Translated', src: 'https://h/s.vtt', label: 'AI' });
    assert.deepEqual(calls.filter((c) => c[0] === 'append').at(-1), ['append', 'tr-pt', 'AI', 1, true]);
    await b.reloadTranslation('tr-pt', 'https://h/s.vtt', { pending: false });
    assert.deepEqual(calls.at(-1), ['append', 'tr-pt', 'AI', 1, false], 'done is done');
});

test('a translation never told is pending only while it is empty', async () => {
    const { el, calls } = fakeElement();
    let doc = 'WEBVTT\n\n';
    const b = createTrackBridge(el, { fetchText: async () => doc });
    await b.reloadTranslation('tr-pt', 'https://h/s.vtt');
    assert.deepEqual(calls.at(-1), ['append', 'tr-pt', 'Subtitle', 0, true]);
    doc += '00:00:01.000 --> 00:00:02.000\none\n';
    await b.reloadTranslation('tr-pt', 'https://h/s.vtt');
    assert.deepEqual(calls.at(-1), ['append', 'tr-pt', 'Subtitle', 1, false]);
});

test('an embedded track on screen is switched off before a side-loaded one is asked for', async () => {
    const { el, calls } = fakeElement();
    const b = createTrackBridge(el, { fetchText: fetchSample });
    await b.showSubtitle({ id: 'mp-0', provider: 'MediaProbe', stream: '4' });
    calls.length = 0;
    await b.showSubtitle({ id: 'os-1', provider: 'OpenSubtitles', src: 'https://h/1.vtt', label: 'English' });
    assert.deepEqual(calls, [
        ['track', null],
        ['append', 'os-1', 'English', 1, false],
        ['lang', 'os-1'],
    ]);
    assert.equal(b.activeStream(), null);
    assert.equal(b.activeId(), 'os-1');
});

test('...and before a translation, and nothing is left drawn when the new one fails', async () => {
    const { el, calls } = fakeElement();
    const failed = [];
    const b = createTrackBridge(el, { fetchText: async () => null, onError: (id) => failed.push(id) });
    await b.showSubtitle({ id: 'mp-b7', provider: 'MediaProbe', stream: '7' });
    calls.length = 0;
    assert.equal(await b.showSubtitle({ id: 'tr-pt', provider: 'Translated', src: 'https://h/s.vtt', label: 'AI' }), false);
    assert.deepEqual(calls, [['track', null], ['lang', null]]);
    assert.equal(b.activeStream(), null);
    assert.equal(b.activeId(), '');
    assert.deepEqual(failed, ['tr-pt']);
});

test('no side-loaded pick switches an embedded track off when none is showing', async () => {
    const { el, calls } = fakeElement();
    const b = createTrackBridge(el, { fetchText: fetchSample });
    await b.showSubtitle({ id: 'os-1', provider: 'OpenSubtitles', src: 'https://h/1.vtt', label: 'E' });
    assert.equal(calls.filter((c) => c[0] === 'track').length, 0);
});

test('dropMissing discards a pick in flight for a chip that is gone', async () => {
    const { el, calls } = fakeElement();
    let release;
    const slow = new Promise((r) => { release = r; });
    el.player.selectSubtitleLang = async (k) => { calls.push(['lang', k]); if (k === 'us-1') await slow; return true; };
    const b = createTrackBridge(el, { fetchText: fetchSample });
    const pick = b.showSubtitle({ id: 'us-1', provider: 'UserSubtitle', src: 'https://h/u.vtt', label: 'Mine' });
    await new Promise((r) => setTimeout(r, 5));
    // The upload is deleted while its file is still being read.
    b.dropMissing(['none', 'os-1']);
    release();
    assert.equal(await pick, false, 'the deleted track does not land');
    assert.equal(b.activeId(), '');
});

test('dropMissing leaves a pick in flight for a chip that stays', async () => {
    const { el, calls } = fakeElement();
    let release;
    const slow = new Promise((r) => { release = r; });
    el.player.selectSubtitleLang = async (k) => { calls.push(['lang', k]); if (k === 'os-1') await slow; return true; };
    const b = createTrackBridge(el, { fetchText: fetchSample });
    const pick = b.showSubtitle({ id: 'os-1', provider: 'OpenSubtitles', src: 'https://h/1.vtt', label: 'E' });
    await new Promise((r) => setTimeout(r, 5));
    b.dropMissing(['none', 'os-1']);
    release();
    assert.equal(await pick, true);
    assert.equal(b.activeId(), 'os-1');
});

test('a side-loaded track that is gone is removed; showing or not', async () => {
    const { el, calls } = fakeElement();
    const b = createTrackBridge(el, { fetchText: fetchSample });
    await b.showSubtitle({ id: 'us-1', provider: 'UserSubtitle', src: 'https://h/u.vtt', label: 'Mine' });
    await b.showSubtitle({ id: 'us-2', provider: 'UserSubtitle', src: 'https://h/v.vtt', label: 'Mine 2' });
    calls.length = 0;
    assert.equal(b.dropMissing(['us-2']), false, 'us-1 was not on screen');
    assert.deepEqual(calls, [['remove', 'us-1']]);
    assert.equal(b.dropMissing([]), true);
    assert.deepEqual(calls.slice(-1), [['remove', 'us-2']]);
    // Back under the same id: cues go in again.
    calls.length = 0;
    await b.showSubtitle({ id: 'us-1', provider: 'UserSubtitle', src: 'https://h/u.vtt', label: 'Mine' });
    assert.equal(calls.filter((c) => c[0] === 'append').length, 1);
});

test('dispose takes off the loadedmetadata listener still waiting', () => {
    const { el } = fakeElement({ ready: false });
    const live = new Set();
    const add = el.addEventListener.bind(el);
    const remove = el.removeEventListener.bind(el);
    el.addEventListener = (type, fn) => { live.add(`${type}`); add(type, fn); };
    el.removeEventListener = (type, fn) => { live.delete(`${type}`); remove(type, fn); };
    const b = createTrackBridge(el);
    assert.ok(live.has('loadedmetadata'));
    b.dispose();
    assert.deepEqual([...live], [], 'nothing left on the element');
});

test('a new source does not stack a second loadedmetadata listener', () => {
    const { el } = fakeElement({ ready: false });
    let n = 0;
    const add = el.addEventListener.bind(el);
    const remove = el.removeEventListener.bind(el);
    el.addEventListener = (type, fn) => { if (type === 'loadedmetadata') n++; add(type, fn); };
    el.removeEventListener = (type, fn) => { if (type === 'loadedmetadata') n--; remove(type, fn); };
    createTrackBridge(el);
    el.dispatchEvent(new Event('emptied'));
    el.dispatchEvent(new Event('emptied'));
    assert.equal(n, 1);
});
