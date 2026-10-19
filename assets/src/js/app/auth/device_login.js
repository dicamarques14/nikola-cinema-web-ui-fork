import av from '../../lib/av';
import { startDeviceLoginPoll } from '../../lib/deviceLogin';

// The "sign in with your phone" panel on the password page: wait for the
// phone's approval, then submit the claim form so this browser signs in.
// The form also works by hand, which is the no-JS path.
av(function () {
    const panel = this;
    const form = panel.querySelector('form[data-device-claim]');
    this._stopDeviceLogin = startDeviceLoginPoll({
        statusUrl: panel.dataset.statusUrl,
        onApproved: () => form.submit(),
        onExpired: () => window.location.reload(),
    });
}, function () {
    if (this._stopDeviceLogin) this._stopDeviceLogin();
});

export {}
