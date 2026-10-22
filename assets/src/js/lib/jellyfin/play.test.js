import { test } from 'node:test';
import assert from 'node:assert/strict';
import { clock } from './play.js';

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

test('loadAddonSubtitles adds side-loaded chips and hides the "none" note', async () => {
    const { JSDOM } = await import('jsdom');
    const { loadAddonSubtitles } = await import('./play.js');
    const dom = new JSDOM(`<section id="jellyfin-player" data-addon-subtitles="/jellyfin/abc/addon-subtitles">
        <p id="jellyfin-no-subtitles">none</p><div id="subtitle-tracks"></div></section>`);
    const root = dom.window.document.querySelector('#jellyfin-player');
    let asked = '';
    const n = await loadAddonSubtitles(root, { fetchJSON: async (u) => { asked = u; return [
        { id: 'addon-0', src: '/jellyfin/abc/addon-subtitles/0.srt', label: 'Portuguese', lang: 'pt', source: 'OpenSubtitles v3' },
        { id: 'addon-1', src: '/jellyfin/abc/addon-subtitles/1.srt', label: 'English <b>', lang: 'en', source: 'X' },
    ]; } });
    assert.equal(asked, '/jellyfin/abc/addon-subtitles');
    assert.equal(n, 2);
    const chips = root.querySelectorAll('#subtitle-tracks .subtitle');
    assert.equal(chips.length, 2);
    assert.equal(chips[0].getAttribute('data-provider'), 'Addon');
    assert.equal(chips[0].getAttribute('data-src'), '/jellyfin/abc/addon-subtitles/0.srt');
    assert.equal(chips[0].getAttribute('data-label'), 'Portuguese · OpenSubtitles v3');
    assert.equal(chips[1].querySelector('b'), null, 'labels are text, not markup');
    assert.equal(root.querySelector('#jellyfin-no-subtitles').hidden, true);
});

test('loadAddonSubtitles adds nothing when the server fails', async () => {
    const { JSDOM } = await import('jsdom');
    const { loadAddonSubtitles } = await import('./play.js');
    const dom = new JSDOM(`<section data-addon-subtitles="/x"><div id="subtitle-tracks"></div></section>`);
    const root = dom.window.document.querySelector('section');
    assert.equal(await loadAddonSubtitles(root, { fetchJSON: async () => { throw new Error('502'); } }), 0);
    assert.equal(root.querySelectorAll('.subtitle').length, 0);
});
