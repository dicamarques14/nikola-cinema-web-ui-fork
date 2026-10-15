// Phase 1 spike (dev builds only: webpack.config.js adds assets/src/js/dev
// entries when mode != production). Not shipped.
//
// Mounts a full-window <movi-player> on whatever page loads this bundle and
// plays the URL given as ?movi=<url>. Use a webtor download URL built
// WITHOUT download=true (that adds Content-Disposition: attachment).
//
// Run with `npm start`, open any page of the dev server, then in the console:
//   import('/assets/dev/movi-spike.js')
// or add ?movi=<url> to the page and load the bundle with a <script>.
//
// What to check: the file plays, seeking works (a ranged GET per seek in the
// network tab, no `linearmode` event), and movi.wasm is fetched as
// application/wasm.

// Everything after `movi=` is the URL, taken raw: the download URL has its own
// ?api-key=...&token=..., which URLSearchParams would split into separate
// params and drop the token.
const rawAt = location.href.indexOf('movi=');
const src = rawAt >= 0 ? location.href.slice(rawAt + 5) : window.MOVI_SRC;

async function main() {
    if (!src) {
        console.error('movi-spike: pass ?movi=<url> or set window.MOVI_SRC');
        return;
    }
    // Slim build: the 5.6 MB movi.wasm is emitted by webpack as an asset
    // (new URL('movi.wasm', import.meta.url)) next to this chunk.
    await import('movi-player/element/slim');

    const el = document.createElement('movi-player');
    el.setAttribute('controls', '');
    // Without this the slim build looks for movi.wasm at a file:// path (webpack
    // bakes import.meta.url) and silently falls back to the native <video>,
    // which cannot play 10-bit HEVC MKV. Setting wasmurl also turns that
    // fallback off, so a broken engine shows up as an error instead.
    el.setAttribute('wasmurl', '/assets/movi.wasm');
    el.style.cssText = 'position:fixed;inset:0;z-index:99999;background:#000';
    for (const ev of ['linearmode', 'errordisplay', 'error', 'loadedmetadata', 'seeked', 'nativefallback']) {
        el.addEventListener(ev, (e) => console.log('movi-spike:', ev, e.detail));
    }
    el.src = src;
    document.body.appendChild(el);
    window.moviSpike = el;
}

main();
