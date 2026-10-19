// Polls the device-login status for the "sign in with your phone" panel on
// the password page (templates/views/auth/password.html).
//
// approved -> submit the claim form, which signs this browser in;
// expired  -> reload /login, which shows a fresh code;
// pending  -> keep waiting. A failed poll (network blip, the car's browser
// waking up) is just retried on the next tick.
//
// fetch, the timer and the actions are injected so the loop can be tested
// without a DOM or a network.
export function startDeviceLoginPoll({
    statusUrl,
    fetchImpl = (...a) => fetch(...a),
    setTimer = (fn, ms) => setTimeout(fn, ms),
    clearTimer = (id) => clearTimeout(id),
    onApproved,
    onExpired,
    intervalMs = 2000,
}) {
    let stopped = false;
    let timer = null;
    const schedule = () => {
        if (!stopped) timer = setTimer(tick, intervalMs);
    };
    async function tick() {
        if (stopped) return;
        let status = null;
        try {
            const res = await fetchImpl(statusUrl, {
                credentials: 'same-origin',
                cache: 'no-store',
                headers: { 'X-Requested-With': 'XMLHttpRequest' },
            });
            if (res.ok) status = (await res.json()).status;
        } catch (e) {
            status = null;
        }
        if (stopped) return;
        if (status === 'approved') {
            stopped = true;
            onApproved();
            return;
        }
        if (status === 'expired') {
            stopped = true;
            onExpired();
            return;
        }
        schedule();
    }
    schedule();
    return function stop() {
        stopped = true;
        if (timer !== null) clearTimer(timer);
    };
}
