import test from 'node:test';
import assert from 'node:assert/strict';
import { addRecent, isResourceQuery, loadRecent, saveRecent, RECENT_KEY, RECENT_MAX } from './recentSearches.js';

function memStorage(init = {}) {
    const m = new Map(Object.entries(init));
    return {
        getItem: (k) => (m.has(k) ? m.get(k) : null),
        setItem: (k, v) => m.set(k, String(v)),
        removeItem: (k) => m.delete(k),
        _m: m,
    };
}

test('recent searches never store magnets, infohashes or URLs', () => {
    for (const q of [
        'magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10',
        '08ada5a7a6183aae1e09d831df6748d566095a10',
        'https://example.com/file.torrent',
        'www.example.com',
    ]) {
        assert.equal(isResourceQuery(q), true, q);
        assert.deepEqual(addRecent([], q), []);
    }
    assert.equal(isResourceQuery('The Matrix'), false);
});

test('recent searches put the newest first, de-dupe case-insensitively and cap', () => {
    let list = [];
    for (let i = 0; i < RECENT_MAX + 3; i++) list = addRecent(list, `title ${i}`);
    assert.equal(list.length, RECENT_MAX);
    assert.equal(list[0], `title ${RECENT_MAX + 2}`);
    list = addRecent(list, '  TITLE   5 ');
    assert.equal(list[0], 'TITLE 5');
    assert.equal(list.filter((s) => s.toLowerCase() === 'title 5').length, 1);
    assert.deepEqual(addRecent(['a'], '   '), ['a']);
});

test('recent searches survive a corrupt or foreign storage value', () => {
    assert.deepEqual(loadRecent(memStorage({ [RECENT_KEY]: '{nope' })), []);
    assert.deepEqual(loadRecent(memStorage({ [RECENT_KEY]: '{"a":1}' })), []);
    assert.deepEqual(loadRecent(memStorage({ [RECENT_KEY]: '["Dune", 3, ""]' })), ['Dune']);
});

test('recent searches round-trip and clear', () => {
    const s = memStorage();
    saveRecent(s, ['Dune', 'Alien']);
    assert.deepEqual(loadRecent(s), ['Dune', 'Alien']);
    saveRecent(s, []);
    assert.equal(s._m.has(RECENT_KEY), false);
});
