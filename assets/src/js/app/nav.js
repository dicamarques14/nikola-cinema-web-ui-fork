import av from '../lib/av';
import { rebindAsync } from '../lib/async';
import { langPath } from '../lib/i18n';
import { addRecent, loadRecent, saveRecent } from '../lib/recentSearches';

// NH-3: the nav search panel (<details data-nav-search>). Opening and
// closing work without JS; this adds focus-on-open, Escape / outside-click
// closing, closing once a search or a link inside it fires, and the
// recent-searches chips. The document listeners are removed on destroy
// because the nav re-renders (and re-inits) on every async navigation.
let docListeners = null;

function storage() {
    try {
        return window.localStorage;
    } catch (e) {
        return null;
    }
}

function renderRecent(panel) {
    const wrap = panel.querySelector('[data-nav-search-recent]');
    const list = wrap && wrap.querySelector('[data-nav-search-recent-list]');
    const store = storage();
    if (!list || !store) return;
    const items = loadRecent(store);
    list.replaceChildren();
    for (const q of items) {
        const a = document.createElement('a');
        a.href = langPath('/discover') + '?search=' + encodeURIComponent(q);
        a.setAttribute('data-async-target', 'main');
        a.setAttribute('data-umami-event', 'nav-search-recent');
        a.className = 'btn btn-ghost h-14 min-h-14 px-5 text-base border border-w-line text-w-sub hover:border-w-cyan hover:text-base-content max-w-[260px]';
        const span = document.createElement('span');
        span.className = 'truncate';
        span.textContent = q;
        a.appendChild(span);
        list.appendChild(a);
    }
    wrap.hidden = items.length === 0;
    if (items.length) rebindAsync(list);
}

function bindSearch(root) {
    const panel = root.querySelector('[data-nav-search]');
    if (!panel) return;
    const input = panel.querySelector('input[type="search"]');
    const form = panel.querySelector('[data-nav-search-form]');
    const close = () => { panel.open = false; };

    panel.addEventListener('toggle', () => {
        if (panel.open) {
            renderRecent(panel);
            if (input) input.focus();
        }
    });
    if (form) {
        form.addEventListener('submit', () => {
            const store = storage();
            if (store && input) saveRecent(store, addRecent(loadRecent(store), input.value));
            close();
        });
    }
    panel.addEventListener('click', (e) => {
        if (e.target.closest('a[href]')) close();
    });
    const clear = panel.querySelector('[data-nav-search-recent-clear]');
    if (clear) {
        clear.addEventListener('click', () => {
            const store = storage();
            if (store) saveRecent(store, []);
            renderRecent(panel);
            if (input) input.focus();
        });
    }
    // The upload label is a role=button: let Enter / Space open the picker.
    const upload = panel.querySelector('label[for="nav-dropzone-input"]');
    if (upload) {
        upload.addEventListener('keydown', (e) => {
            if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                upload.click();
            }
        });
    }

    removeDocListeners();
    docListeners = {
        keydown(e) {
            if (e.key === 'Escape' && panel.open) {
                close();
                const summary = panel.querySelector('summary');
                if (summary) summary.focus();
            }
        },
        click(e) {
            if (panel.open && !panel.contains(e.target)) close();
        },
    };
    document.addEventListener('keydown', docListeners.keydown);
    document.addEventListener('click', docListeners.click);
}

function removeDocListeners() {
    if (!docListeners) return;
    document.removeEventListener('keydown', docListeners.keydown);
    document.removeEventListener('click', docListeners.click);
    docListeners = null;
}

av(async function() {
    if (window.umami) {
        if (window._isNewUser) {
            window.umami.track('signup');
        }
        if (window._tierUpdated && window._tier !== 'free') {
            window.umami.track('subscription-started', {
                tier: window._tier,
            });
        }
    }
    // Nav search-panel dropzone: the label opens the picker natively (for=),
    // JS only submits the form once a file is chosen. Unlike the home-page
    // dropzone (lib/drop.js) there are no document-level drag/drop handlers —
    // hijacking every drop on every page is home-hero behavior, not nav's.
    const dropzone = this.querySelector('.nav-dropzone');
    if (dropzone) {
        dropzone.querySelector('.dropzone-input').addEventListener('change', function() {
            dropzone.requestSubmit();
        });
    }
    bindSearch(this);
    const self = this;
    window.addEventListener('auth', function() {
        // Re-identify so the now-authed tier/is_authed/user_id props refresh
        // in session_data under the same server session id (which stays stable
        // across the anon→authed transition).
        if (window.umami && window._sessionID) {
            window.umami.identify(window._sessionID);
        }
        self.reload();
    }, { once: true });
}, function() {
    removeDocListeners();
});

export {}

