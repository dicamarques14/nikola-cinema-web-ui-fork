// Searching from the Watchlist, end to end in jsdom. The page is opened on
// /discover?watchlist=1 with only series saved, while the catalog behind the
// watchlist (Cinemeta's movie top list) is still loading: then the search
// box is used. The search must replace the watchlist with its results and
// the "Loading catalogs" spinner must go away -- it used to stay forever,
// because the search aborted that catalog load and an aborted load never
// cleared its loading flag.
//
// Real: DiscoverApp, its reducer, the Stremio client, preact, the DOM.
// Faked: `fetch` (Cinemeta, the watchlist endpoints), window.umami.

import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

const CINEMETA = 'https://v3-cinemeta.strem.io';

const dom = new JSDOM('<!doctype html><html lang="en"><body><main><div id="root"></div></main></body></html>', {
    url: 'https://webtor.io/discover?watchlist=1',
    pretendToBeVisual: true,
});
const w = dom.window;
globalThis.window = w;
globalThis.document = w.document;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: w.navigator });
for (const k of ['HTMLElement', 'Event', 'CustomEvent', 'MouseEvent', 'PopStateEvent', 'localStorage', 'sessionStorage']) {
    globalThis[k] = w[k];
}
globalThis.requestAnimationFrame = w.requestAnimationFrame.bind(w);
globalThis.cancelAnimationFrame = w.cancelAnimationFrame.bind(w);
globalThis.__SUPPORTED_LOCALES__ = ['en'];
w.HTMLDialogElement.prototype.showModal = function showModal() { this.open = true; };
w.HTMLDialogElement.prototype.close = function close() { this.open = false; };
w.HTMLCanvasElement.prototype.getContext = () => null;
w.scrollTo = () => {};
class NoEventSource {
    static CLOSED = 2;
    constructor() { this.readyState = 0; }
    addEventListener() {}
    close() { this.readyState = NoEventSource.CLOSED; }
}
globalThis.EventSource = NoEventSource;
w.EventSource = NoEventSource;
w.umami = { track: () => {} };

const SERIES = ['tt0903747', 'tt0944947', 'tt0475784', 'tt2861424', 'tt4574334'];
const respond = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const abortable = (signal) => new Promise((_, reject) => {
    const fail = () => reject(new DOMException('aborted', 'AbortError'));
    if (signal?.aborted) fail();
    signal?.addEventListener('abort', fail, { once: true });
});
const requested = [];
globalThis.fetch = async (url, opts = {}) => {
    const u = String(url);
    requested.push(u);
    if (u === `${CINEMETA}/manifest.json`) {
        return respond({
            id: 'com.linvo.cinemeta', name: 'Cinemeta', version: '1.0.0', resources: ['catalog', 'meta'], types: ['movie', 'series'],
            catalogs: [
                { type: 'movie', id: 'top', name: 'Popular', extra: [{ name: 'search' }, { name: 'skip' }] },
                { type: 'series', id: 'top', name: 'Popular', extra: [{ name: 'search' }, { name: 'skip' }] },
            ],
        });
    }
    // The catalog behind the watchlist: slow, still loading when the user searches.
    if (u.startsWith(`${CINEMETA}/catalog/movie/top.json`) || u.startsWith(`${CINEMETA}/catalog/movie/top/skip=`)) return abortable(opts.signal);
    if (u === `${CINEMETA}/catalog/movie/top/search=Inception.json`) {
        return respond({ metas: [{ id: 'tt1375666', type: 'movie', name: 'Inception' }] });
    }
    if (u === `${CINEMETA}/catalog/series/top/search=Inception.json`) return respond({ metas: [] });
    if (u.endsWith('/discover/watchlist')) {
        return respond({
            items: SERIES.map((id, i) => ({ video_id: id, type: 'series', title: `Saved Series ${i + 1}` })),
            video_ids: SERIES,
        });
    }
    if (u.endsWith('/discover/watchlist/ids')) return respond({ video_ids: SERIES });
    return respond({}, 404);
};

after(() => w.close());

const { h, render } = await import('preact');
const { init } = await import('../i18n.js');
const { CINEMETA_BASE } = await import('../client.js');
const { DiscoverApp } = await import('./DiscoverApp.jsx');
await init();

const until = async (cond, what, ms = 3000) => {
    const t0 = Date.now();
    while (!cond()) {
        if (Date.now() - t0 > ms) throw new Error(`timed out waiting for ${what}`);
        await new Promise((r) => setTimeout(r, 10));
    }
};

test('a search from the Watchlist shows its results and does not hang on "Loading catalogs"', async () => {
    assert.equal(CINEMETA_BASE, CINEMETA);
    const root = document.getElementById('root');
    render(h(DiscoverApp, {
        addonUrls: [CINEMETA],
        addonSeeds: [{ id: '', url: CINEMETA, name: 'Cinemeta', resources: ['catalog', 'meta'], types: ['movie', 'series'], fetchedAt: null }],
        hasCustomAddons: false,
    }), root);
    await until(() => root.textContent.includes('Saved Series 5'), 'the watchlist with its five series');

    const input = root.querySelector('input[placeholder="discover.search"]');
    assert.ok(input, 'the search box');
    input.value = 'Inception';
    input.dispatchEvent(new w.Event('input', { bubbles: true }));

    await until(() => requested.includes(`${CINEMETA}/catalog/movie/top/search=Inception.json`), 'the movie search');
    await until(() => root.textContent.includes('Inception') && !root.textContent.includes('discover.loadingCatalogs'),
        'the results, without the catalog spinner', 3000).catch(() => {});
    assert.ok(root.textContent.includes('Inception'), 'the movie found by the search is shown');
    assert.ok(!root.textContent.includes('discover.loadingCatalogs'), 'no "Loading catalogs" spinner after the search answered');
    assert.ok(!root.textContent.includes('Saved Series'), 'the watchlist gave way to the results');
    assert.equal(w.location.search, '?search=Inception');
    render(null, root);
});
