import av from '../lib/av';
import { rebindAsync } from '../lib/async';
import { CINEMETA_BASE } from '../lib/discover/client';
import { langPath } from '../lib/i18n';

// NH-2: the Home "Popular movies" / "Popular shows" rails. Cinemeta's top
// catalogs are what Discover's landing shows too; the server has no copy,
// so these two rails are filled here and the section is removed when the
// fetch fails or comes back empty (an empty rail is never shown).
const CARD_COUNT = 20;

function renderCard(item, type) {
    const name = item.name || 'Unknown';
    const year = item.releaseInfo || item.year || '';
    const a = document.createElement('a');
    a.href = langPath('/discover') + '?id=' + encodeURIComponent(item.id) + '&type=' + encodeURIComponent(item.type || type);
    a.setAttribute('data-async-target', 'main');
    a.setAttribute('data-umami-event', 'home-trending-click');
    a.className = 'shrink-0 snap-start w-[220px] group cursor-pointer';

    const frame = document.createElement('div');
    frame.className = 'relative aspect-[2/3] rounded-xl overflow-hidden border border-w-line group-hover:border-w-cyan/40 transition-all duration-300';
    const ph = document.createElement('div');
    ph.className = 'absolute inset-0 bg-gradient-to-br from-w-purple/20 via-w-pink/10 to-w-cyan/15 flex items-center justify-center';
    const phText = document.createElement('div');
    phText.className = 'text-center font-bold text-lg p-3 text-w-purpleL/60 line-clamp-4';
    phText.textContent = name;
    ph.appendChild(phText);
    frame.appendChild(ph);
    if (item.poster) {
        const img = document.createElement('img');
        img.className = 'absolute inset-0 w-full h-full object-cover group-hover:scale-105 transition-transform duration-300';
        img.src = item.poster;
        img.alt = name;
        img.loading = 'lazy';
        img.addEventListener('error', () => img.remove());
        frame.appendChild(img);
    }
    a.appendChild(frame);

    const title = document.createElement('p');
    title.className = 'mt-3 text-base font-semibold text-w-text truncate group-hover:text-w-cyan transition-colors';
    title.textContent = name;
    a.appendChild(title);
    if (year) {
        const y = document.createElement('p');
        y.className = 'text-sm text-w-muted';
        y.textContent = year;
        a.appendChild(y);
    }
    return a;
}

async function fillRail(section) {
    const type = section.getAttribute('data-home-trending');
    const cards = section.querySelector('[data-home-trending-cards]');
    if (!cards || (type !== 'movie' && type !== 'series')) {
        section.remove();
        return;
    }
    try {
        const res = await fetch(`${CINEMETA_BASE}/catalog/${type}/top.json`);
        if (!res.ok) throw new Error(`cinemeta ${res.status}`);
        const data = await res.json();
        const items = (data.metas || []).filter((m) => m && m.id).slice(0, CARD_COUNT);
        if (!items.length) throw new Error('empty catalog');
        cards.replaceChildren(...items.map((m) => renderCard(m, type)));
        rebindAsync(cards);
    } catch (e) {
        section.remove();
    }
}

av(async function () {
    await Promise.all([...this.querySelectorAll('[data-home-trending]')].map(fillRail));
});

export {};
