// How much of the film <movi-player> may hold in memory: its `buffersize`
// attribute, in MB.
//
// Stock movi-player 0.4.1 (the bundled dist/element.slim.js) caches a file no
// larger than the cap WHOLE and downloads it to the end, playing or paused.
// A larger file got a window of only 8% of the file, each stream at most
// 250 MB, compacted to nothing behind the playhead, and the download stopped
// once that small window was full: minutes ahead, so a slow link stutters.
//
// patches/movi-player+0.4.1.patch (applied by `postinstall`) makes the window
// the whole cap instead: it fills to 90% of it, playing or paused, keeps 10%
// behind the playhead for short seeks back, and refills as playback eats into
// it. The source is sized at open from this attribute (stock re-sized it after
// load into a fresh, empty buffer), and if the one big allocation fails it
// halves until one fits. So the cap is how far ahead we buffer: it follows
// navigator.deviceMemory (Chromium only, in GB, rounded down and capped at 8)
// and stays small on phones, whose tabs are killed long before a desktop's.
// Without deviceMemory (Firefox, Safari) the element's default stands.
// 2000 MB keeps the one buffer under 2 GiB, the most a typed array may hold
// on older V8.
export const DEFAULT_BUFFER_MB = 520;

const BUDGETS = [
    // [at least this many GB of device memory, MB on desktop, MB on mobile]
    [8, 2000, 1024],
    [4, 1024, 520],
];

// isMobile is a phone or tablet, by Client Hints where the browser has them
// and by user agent otherwise. A Tesla says "X11; GNU/Linux": desktop.
export function isMobile(nav) {
    if (!nav) return false;
    if (nav.userAgentData && typeof nav.userAgentData.mobile === 'boolean') {
        return nav.userAgentData.mobile;
    }
    return /Android|iPhone|iPad|iPod|Mobile/i.test(nav.userAgent || '');
}

// bufferSizeMB is the `buffersize` for this device, or 0 to leave the
// element's default (DEFAULT_BUFFER_MB) alone.
export function bufferSizeMB(nav) {
    const gb = nav && Number(nav.deviceMemory);
    if (!(gb > 0)) return 0;
    const mobile = isMobile(nav);
    for (const [minGB, desktopMB, mobileMB] of BUDGETS) {
        if (gb >= minGB) {
            const mb = mobile ? mobileMB : desktopMB;
            return mb > DEFAULT_BUFFER_MB ? mb : 0;
        }
    }
    return 0;
}

// applyBufferSize sets `buffersize` on a <movi-player> before it loads,
// unless the page already chose one.
export function applyBufferSize(el, nav) {
    if (!el || el.hasAttribute('buffersize')) return;
    const mb = bufferSizeMB(nav);
    if (mb > 0) el.setAttribute('buffersize', String(mb));
}
