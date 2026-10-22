import { test } from 'node:test';
import assert from 'node:assert/strict';
import { resumeAt, loadPosition, savePosition, clock } from './play.js';

function mem() {
    const m = new Map();
    return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, v), removeItem: (k) => m.delete(k), m };
}

test('resumeAt skips the first half minute and the last minute', () => {
    assert.equal(resumeAt(10, 1500), 0);
    assert.equal(resumeAt(600, 1500), 600);
    assert.equal(resumeAt(1450, 1500), 0);
    assert.equal(resumeAt(600, NaN), 600, 'unknown length still resumes');
    assert.equal(resumeAt('x', 1500), 0);
});

test('a position is kept per item and forgotten near the end', () => {
    const s = mem();
    savePosition('a', 612.7, 1500, s);
    assert.equal(loadPosition('a', s), 612);
    assert.equal(loadPosition('b', s), 0);
    savePosition('a', 1490, 1500, s);
    assert.equal(loadPosition('a', s), 0);
    assert.equal(s.m.size, 0);
});

test('clock reads m:ss and h:mm:ss', () => {
    assert.equal(clock(65), '1:05');
    assert.equal(clock(3725), '1:02:05');
});

test('audioStreams keeps good guesses and falls back to file order', async () => {
    const { audioStreams } = await import('./play.js');
    const t = (...ids) => ids.map((id) => ({ id, codec: 'eac3' }));
    assert.deepEqual(audioStreams(['1', '2'], t(1, 2)), [1, 2]);
    // Off by the external subtitles: take the player's ids in order.
    assert.deepEqual(audioStreams(['3', '4'], t(1, 2)), [1, 2]);
    // Counts disagree: keep the server's guess.
    assert.deepEqual(audioStreams(['3', '4'], t(1)), [3, 4]);
    assert.deepEqual(audioStreams(['1'], []), [1]);
});

test('wireAudio re-maps chips and flags a track the player refuses', async () => {
    const { JSDOM } = await import('jsdom');
    const { wireAudio } = await import('./play.js');
    const dom = new JSDOM(`<div id="r"><div id="audio-tracks">
        <button class="audio track-chip track-chip-active" data-stream="3" data-codec="eac3"></button>
        <button class="audio track-chip" data-stream="4" data-codec="wmapro"></button></div></div>`);
    const root = dom.window.document.getElementById('r');
    const picked = [];
    const player = {
        getAudioTracks: () => [{ id: 1 }, { id: 2 }],
        selectAudioTrack(id) { picked.push(id); return id !== 2; },
    };
    const video = new dom.window.EventTarget();
    video.readyState = 1;
    video.player = player;
    const undo = wireAudio(root, video);
    const chips = root.querySelectorAll('.audio');
    assert.deepEqual([...chips].map((c) => c.getAttribute('data-stream')), ['1', '2']);
    assert.deepEqual(picked, [1]); // the active chip moved: re-selected
    assert.equal(player.selectAudioTrack(2), false);
    assert.equal(chips[1].getAttribute('data-unplayable'), 'true');
    assert.match(chips[1].textContent, /wmapro · can.t play here/);
    undo();
    player.selectAudioTrack(2);
    assert.equal(chips[1].querySelectorAll('.badge').length, 1);
});
