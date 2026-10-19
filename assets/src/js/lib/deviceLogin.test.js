import { test } from 'node:test';
import assert from 'node:assert/strict';
import { startDeviceLoginPoll } from './deviceLogin.js';

// A manual clock: each run() fires the pending timer and lets the async tick
// settle.
function harness(responses) {
    let pending = null;
    const calls = [];
    const events = [];
    const stop = startDeviceLoginPoll({
        statusUrl: '/login/device/status',
        fetchImpl: async (url) => {
            calls.push(url);
            const r = responses.shift();
            if (r instanceof Error) throw r;
            return { ok: r !== 503, json: async () => ({ status: r }) };
        },
        setTimer: (fn) => { pending = fn; return 1; },
        clearTimer: () => { pending = null; },
        onApproved: () => events.push('approved'),
        onExpired: () => events.push('expired'),
    });
    return {
        calls, events, stop,
        async run() {
            const fn = pending;
            pending = null;
            if (fn) await fn();
        },
        get pending() { return pending; },
    };
}

test('keeps polling while pending, submits once approved', async () => {
    const h = harness(['pending', 'pending', 'approved']);
    await h.run();
    await h.run();
    assert.deepEqual(h.events, []);
    await h.run();
    assert.deepEqual(h.events, ['approved']);
    assert.equal(h.calls.length, 3);
    assert.equal(h.pending, null, 'no further polls after approval');
});

test('reloads on expiry', async () => {
    const h = harness(['expired']);
    await h.run();
    assert.deepEqual(h.events, ['expired']);
    assert.equal(h.pending, null);
});

test('retries after a network error or a server error', async () => {
    const h = harness([new Error('offline'), 503, 'approved']);
    await h.run();
    await h.run();
    assert.deepEqual(h.events, []);
    await h.run();
    assert.deepEqual(h.events, ['approved']);
});

test('stop() ends the loop', async () => {
    const h = harness(['pending', 'approved']);
    h.stop();
    assert.equal(h.pending, null);
    assert.deepEqual(h.events, []);
});
