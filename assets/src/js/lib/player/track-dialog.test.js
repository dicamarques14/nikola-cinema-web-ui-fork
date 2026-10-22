// markTrack's write-back, against a dialog shaped like the Jellyfin player's
// (templates/views/jellyfin/play.html): chips side-loaded from a proxy
// route, and data-persist="false" because there is no session to PUT to.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'https://webtor.test/jellyfin/x' });
globalThis.window = dom.window;
globalThis.document = dom.window.document;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.Event = dom.window.Event;
globalThis.__SUPPORTED_LOCALES__ = ['en'];

const { markTrack } = await import('./track-dialog.js');

function mount(persistAttr) {
    document.body.innerHTML = `
        <section id="jellyfin-player">
          <dialog id="subtitles" ${persistAttr} data-subtitles-off="true" data-resource-id="r" data-item-id="i">
            <div id="subtitle-tracks">
              <button class="subtitle" id="subtitle-none" data-id="none" data-default="true" hidden></button>
              <button class="subtitle track-chip" data-id="jf-0" data-provider="Jellyfin" data-src="/jellyfin/a/subtitles/0.vtt">PT</button>
            </div>
          </dialog>
        </section>`;
    const calls = [];
    globalThis.fetch = (url, params) => { calls.push({ url, params }); return Promise.resolve({ status: 200 }); };
    window.fetch = globalThis.fetch;
    const root = document.getElementById('jellyfin-player');
    return { root, chip: root.querySelector('[data-id="jf-0"]'), calls };
}

test('data-persist="false": the chip is marked and nothing is PUT', async () => {
    const p = mount('data-persist="false"');
    await markTrack(p.root, p.chip, 'subtitle');
    assert.equal(p.chip.getAttribute('data-default'), 'true');
    assert.ok(p.chip.classList.contains('track-chip-active'));
    assert.equal(p.root.querySelector('#subtitle-none').getAttribute('data-default'), null);
    assert.equal(p.calls.length, 0);
});

test('without it the pick is written back to the session', async () => {
    const p = mount('');
    await markTrack(p.root, p.chip, 'subtitle');
    assert.equal(p.calls.length, 1);
    assert.equal(p.calls[0].url, '/stream-video/subtitle');
    assert.equal(JSON.parse(p.calls[0].params.body).id, 'jf-0');
});
