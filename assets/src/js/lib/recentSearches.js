// NH-3: recent title searches from the nav search box, kept in this
// browser only. Magnets, infohashes and URLs are never stored: they are
// one-off resource pastes, not searches you would repeat, and a magnet
// link can be long and private.

export const RECENT_KEY = 'nc.recentSearches';
export const RECENT_MAX = 8;

const RESOURCE_RE = [
    /^magnet:/i,
    /^[0-9a-f]{40}$/i,
    /^[0-9a-f]{64}$/i,
    /^[a-z2-7]{32}$/i,
    /^[a-z][a-z0-9+.-]*:\/\//i,
    /^www\./i,
];

export function isResourceQuery(q) {
    const s = String(q || '').trim();
    return RESOURCE_RE.some((re) => re.test(s));
}

export function loadRecent(storage) {
    try {
        const list = JSON.parse(storage.getItem(RECENT_KEY) || '[]');
        return Array.isArray(list) ? list.filter((s) => typeof s === 'string' && s.trim()).slice(0, RECENT_MAX) : [];
    } catch (e) {
        return [];
    }
}

// addRecent puts q first (case-insensitive de-dupe) and returns the new
// list; resource pastes and blanks leave the list as it was.
export function addRecent(list, q) {
    const s = String(q || '').trim().replace(/\s+/g, ' ');
    if (!s || s.length > 120 || isResourceQuery(s)) return list;
    const lower = s.toLowerCase();
    return [s, ...list.filter((x) => x.toLowerCase() !== lower)].slice(0, RECENT_MAX);
}

export function saveRecent(storage, list) {
    try {
        if (list.length) storage.setItem(RECENT_KEY, JSON.stringify(list));
        else storage.removeItem(RECENT_KEY);
    } catch (e) {
        // storage full or disabled: recents are a nicety
    }
}
