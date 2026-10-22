import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { DEFAULT_BUFFER_MB, applyBufferSize, bufferSizeMB, isMobile } from './buffer-budget.js';

const TESLA = 'Mozilla/5.0 (X11; GNU/Linux) AppleWebKit/537.36 (KHTML, like Gecko) Chromium/108.0.5359.215 Chrome/108.0.5359.215 Safari/537.36 Tesla/2024.14.9';
const PIXEL = 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Mobile Safari/537.36';

test('bufferSizeMB follows device memory, smaller on phones', () => {
    for (const [nav, want] of [
        [{ deviceMemory: 8, userAgent: TESLA }, 2000],
        [{ deviceMemory: 4, userAgent: TESLA }, 1024],
        [{ deviceMemory: 2, userAgent: TESLA }, 0],
        [{ deviceMemory: 0.5, userAgent: TESLA }, 0],
        [{ deviceMemory: 8, userAgent: PIXEL }, 1024],
        [{ deviceMemory: 4, userAgent: PIXEL }, 0],
        [{ deviceMemory: 8, userAgent: PIXEL, userAgentData: { mobile: false } }, 2000],
        [{ userAgent: TESLA }, 0], // Firefox, Safari: no deviceMemory
        [{ deviceMemory: 'x' }, 0],
        [undefined, 0],
    ]) {
        assert.equal(bufferSizeMB(nav), want, JSON.stringify(nav));
    }
});

test('the budget never shrinks the element default and stays under 2 GiB', () => {
    for (const gb of [0.25, 0.5, 1, 2, 4, 8, 16]) {
        for (const userAgent of [TESLA, PIXEL]) {
            const mb = bufferSizeMB({ deviceMemory: gb, userAgent });
            assert.ok(mb === 0 || mb > DEFAULT_BUFFER_MB, `${gb} GB: ${mb}`);
            assert.ok(mb * 1024 * 1024 < 2 ** 31, `${gb} GB: ${mb} MB`);
        }
    }
});

test('isMobile: Client Hints first, user agent otherwise', () => {
    assert.equal(isMobile({ userAgent: TESLA }), false);
    assert.equal(isMobile({ userAgent: PIXEL }), true);
    assert.equal(isMobile({ userAgent: PIXEL, userAgentData: { mobile: false } }), false);
    assert.equal(isMobile(undefined), false);
});

function fakeEl(attrs = {}) {
    return {
        attrs: { ...attrs },
        hasAttribute(n) { return n in this.attrs; },
        setAttribute(n, v) { this.attrs[n] = v; },
    };
}

test('applyBufferSize sets the attribute, keeps a page-chosen one', () => {
    const el = fakeEl();
    applyBufferSize(el, { deviceMemory: 8, userAgent: TESLA });
    assert.equal(el.attrs.buffersize, '2000');
    const chosen = fakeEl({ buffersize: '300' });
    applyBufferSize(chosen, { deviceMemory: 8, userAgent: TESLA });
    assert.equal(chosen.attrs.buffersize, '300');
    const small = fakeEl();
    applyBufferSize(small, { deviceMemory: 2, userAgent: TESLA });
    assert.equal('buffersize' in small.attrs, false);
});

// Every player (torrent, Jellyfin, URL stream, next item) is a <movi-player>
// made by upgradeToMovi; the budget is set there, before the element loads.
test('upgradeToMovi applies the budget', () => {
    const dir = path.dirname(fileURLToPath(import.meta.url));
    const src = readFileSync(path.join(dir, 'Player.jsx'), 'utf8');
    const body = src.slice(src.indexOf('function upgradeToMovi('), src.indexOf('media.replaceWith(el)'));
    assert.match(body, /applyBufferSize\(el, navigator\)/);
});

// The numbers above rest on our patch to movi-player (patches/, applied by
// postinstall) to the bundle the app imports; if an upgrade drops it, the
// window is back to 8% of the file and this fails.
test('movi-player is patched to buffer the whole budget ahead', () => {
    const dir = path.dirname(fileURLToPath(import.meta.url));
    const src = readFileSync(path.join(dir, '../../../../../node_modules/movi-player/dist/element.slim.js'), 'utf8');
    assert.equal(src.split('webtor: window = whole buffersize').length - 1, 2);
    assert.doesNotMatch(src, /0\.08 \* this\.size/);
    assert.doesNotMatch(src, /Math\.min\(\w+, 0\.9 \* this\.bufferSize\)/);
    assert.match(src, /webtor: keep 10% behind/);
    assert.match(src, /webtor: halve on allocation failure/);
    assert.match(src, new RegExp(`maxSizeMB: this\\._bufferSize > 0 \\? this\\._bufferSize : ${DEFAULT_BUFFER_MB}\\b`));
});
