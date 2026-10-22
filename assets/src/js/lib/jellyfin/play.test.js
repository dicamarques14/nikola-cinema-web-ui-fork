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
