import { render } from 'preact';
import { useRef, useState, useEffect, useCallback } from 'preact/hooks';
import { usePlayerState } from './hooks/usePlayerState';
import { useWatchHistory } from './hooks/useWatchHistory';
import { useSubtitleTranslation } from './hooks/useSubtitleTranslation';
import { createGraceHold } from './grace-hold';
import { loadPrefs, savePrefs, loadSubtitleDelay, saveSubtitleDelay } from './player-prefs';
import { normalizeDelay, SUBTITLE_DELAY_STEP } from './cue-offset';
import { createTrackBridge } from './movi-tracks';
import { readNext, advancePlan, atEnd, resumeAt, readStreak, writeStreak, countdown } from './next-item';
import { createNextItemGo, canMoveOn, takeFallbackNote } from './next-item-go';
import { HAS_POPOVER, useDockedPopover } from './useAnchoredPopover';
import { creditsStart, parseVttTimings, timingSourceURL, creditsFromElement } from './credits';
import { track, settled } from './player-telemetry';
import { readTracks, resolveSubtitleLevel } from './subtitle-telemetry.js';
import { offerTiming, offerVisible, pickOffer, suppressUpsell, upsellSuppressed } from './subtitle-offer.js';
import { readChips } from './track-picker.js';
import { markAutoResume, takeAutoResume } from './preferred-lang.js';
import {
    activateSubtitle, findSubtitleItem, findSubtitlesModal, offerStorage, readUpsellCard, releaseTrackDialog, toggleDialog, wireTrackHandlers,
} from './track-dialog.js';
import { LoadingSpinner } from './icons';
import { BufferingPill, capCardOpen, closeCapCard, onCapCard, openCapCard } from './BufferingLabel';
import { answerLabel, capLock, lockKey, statusSpoke } from './buffering-label';
import { currentPlayerCause, currentPlayerLabel, onPlayerLabel } from '../playerLabel';
import { init as initI18n, t, tf } from './i18n';
import { getLang } from '../i18n';
import { shareResource } from '../share/share';
import '../../../styles/player.css';

// The player is <movi-player> (movi-player, Apache-2.0): it opens the file
// itself -- the download URL the stream job renders as the <source> -- and
// demuxes and decodes it in the browser (WebCodecs, FFmpeg compiled to
// WebAssembly). Nothing is transcoded. It draws its own controls, keyboard,
// gestures, captions menu and the lock screen's media session; this module
// mounts it in place of the server-rendered <video>/<audio> and lays webtor's
// own parts over it: the resume prompt and the watch history, the grace
// popup, the buffering label with the plan's lock, the next file, share and
// embed. docs/player.md.

let _currentPlayer = null;

// ENGAGEMENT_SECONDS is the playback time (not wall clock) after which a
// session counts as real viewing, so press-play-and-bounce does not skew
// the denominator of the stream-start event.
const ENGAGEMENT_SECONDS = 5;

// The engine, loaded with the first player and shared by every one after it.
// The slim build: the 5.6 MB movi.wasm is a file of its own, copied next to
// the bundles by webpack.config.js -- webpack bakes the build machine's
// file:// path into the build's own import.meta.url lookup, so the element is
// told where the file is (wasmURL). It also turns off the slim build's silent
// fall back to the browser's own <video>, which cannot play what this player
// is for: a broken engine shows its error instead.
let enginePromise = null;
function loadEngine() {
    if (!enginePromise) enginePromise = import('movi-player/element/slim');
    return enginePromise;
}

// wasmURL is movi.wasm next to the bundle this module was loaded from: the
// runtime public path webpack resolves (`auto`), so a CDN asset host
// (WEB_ASSETS_HOST) is followed too.
function wasmURL() {
    // eslint-disable-next-line no-undef
    const base = typeof __webpack_public_path__ === 'string' && __webpack_public_path__ ? __webpack_public_path__ : '/assets/';
    return base + 'movi.wasm';
}

// Icons for the controls this page adds to movi-player's bar and menu.
const ICON_SHARE = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><path d="M8.6 13.5l6.8 4M15.4 6.5l-6.8 4"/></svg>';
const ICON_EMBED = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M16 18l6-6-6-6M8 6l-6 6 6 6"/></svg>';
const ICON_NEXT = '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M5 5.5v13a1 1 0 0 0 1.5.86L16 13.6V18a1 1 0 0 0 2 0V6a1 1 0 0 0-2 0v4.4L6.5 4.64A1 1 0 0 0 5 5.5z"/></svg>';
const ICON_TRACKS = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="5" width="18" height="14" rx="2"/><path d="M7 15h4M13 15h4M7 11h10"/></svg>';

/**
 * Main Player Preact component: webtor's layer over <movi-player>.
 */
function PlayerComponent({ videoEl, isVideo, settings, containerEl, showControls, fixedSize, trackContainer, trackHooks, awaitStart = false }) {
    const containerRef = useRef(containerEl);
    const videoRef = useRef(videoEl);

    const duration = videoEl.getAttribute('data-duration') ? parseFloat(videoEl.getAttribute('data-duration')) : -1;
    const graceDurationSec = videoEl.dataset.graceDurationSec ? parseInt(videoEl.dataset.graceDurationSec, 10) : 0;
    const graceShownRef = useRef(false);
    // The grace popup holds the film until the viewer answers it
    // (grace-hold.js), and the answer while it is up goes through
    // graceAnswerRef.
    const graceHoldRef = useRef(null);
    if (!graceHoldRef.current) graceHoldRef.current = createGraceHold(videoEl);
    const graceAnswerRef = useRef(null);
    // The popup on screen, for the render: no lock on the buffering label
    // while it is (buffering-label.js capLock).
    const [graceOpen, setGraceOpen] = useState(false);
    useEffect(() => () => graceHoldRef.current.dispose(), []);
    const streamStartFiredRef = useRef(false);
    const resourceID = videoEl.dataset.resourceId;
    const path = videoEl.dataset.path;

    const features = parseFeatures(settings, isVideo);

    // The film's clock and state, from the element's own events. movi-player
    // answers the HTMLMediaElement surface, so this is the hook a <video>
    // had; its own controls are not drawn (movi-player draws them).
    const state = usePlayerState(videoRef, containerRef, { duration, seekOffset: 0, seeking: false });

    // ---- what the picker chose, on screen ---------------------------------
    // Once the file's metadata is in (embedded tracks are known only then) the
    // dialog's default chips -- the server's ladder, a saved choice, a carried
    // one -- are put on screen the way a click would, minus the PUT: nothing
    // was chosen here.
    useEffect(() => {
        const bridge = videoEl.__wtTracks;
        if (!bridge) return undefined;
        let cancelled = false;
        bridge.ready.then(() => {
            if (cancelled) return;
            const modal = document.getElementById('subtitles');
            if (!modal) return;
            const sub = modal.querySelector('.subtitle[data-default="true"]');
            if (sub && sub.getAttribute('data-id') !== 'none') activateSubtitle(trackContainer, sub, { persist: false });
            const audio = modal.querySelector('.audio[data-default="true"]');
            if (audio && audio.getAttribute('data-provider') === 'MediaProbe') bridge.showAudio(audio.getAttribute('data-stream'));
        });
        return () => { cancelled = true; };
    }, []);

    // ---- the subtitle delay -----------------------------------------------
    // The viewer's delay for this file (seconds, positive = later), remembered
    // per file (player-prefs.js). movi-player has one delay for whatever it
    // draws -- embedded tracks too -- and tells the page when it changes, by
    // the dialog's row or its own keys. The row in the #subtitles dialog is
    // server markup, painted and driven from here.
    const delayKey = resourceID && path ? `${resourceID}:${path}` : '';
    const subDelayRef = useRef(0);
    const delayEventRef = useRef(null);
    if (!delayEventRef.current) delayEventRef.current = settled((v) => track('subtitle-delay', v), 2000);
    const delaySourceRef = useRef('dialog');
    useEffect(() => () => delayEventRef.current.flush(), []);
    const formatDelay = (d) => `${d > 0 ? '+' : d < 0 ? '\u2212' : ''}${Math.abs(d).toFixed(2)}`;
    const paintSubDelay = useCallback(() => {
        const modal = document.getElementById('subtitles');
        const row = modal && modal.querySelector('#subtitle-delay');
        if (!row) return;
        const d = subDelayRef.current;
        const value = row.querySelector('#subtitle-delay-value');
        if (value) value.textContent = tf('player.subtitleDelayValue', formatDelay(d));
        const reset = row.querySelector('[data-sub-delay="reset"]');
        if (reset) {
            reset.hidden = false;
            reset.classList.remove('invisible');
            reset.removeAttribute('tabindex');
        }
        // The switch is the one live statement of on/off (markTrack moves it).
        const toggle = modal.querySelector('#subtitles-toggle');
        const off = toggle ? !toggle.checked : false;
        for (const b of row.querySelectorAll('button[data-sub-delay]')) {
            b.disabled = off || (b.dataset.subDelay === 'reset' && d === 0);
        }
        row.classList.toggle('opacity-50', off);
        row.title = '';
    }, []);
    const changeSubDelay = useCallback((next, source) => {
        delaySourceRef.current = source;
        videoEl.subtitleDelay = normalizeDelay(next);
    }, []);
    useEffect(() => {
        const start = normalizeDelay(loadSubtitleDelay(delayKey));
        subDelayRef.current = start;
        if (start !== 0) videoEl.subtitleDelay = start;
        const onChange = (e) => {
            const d = normalizeDelay(e.detail && e.detail.subtitleDelay);
            subDelayRef.current = d;
            saveSubtitleDelay(delayKey, d);
            delayEventRef.current.push({ delay: d, source: delaySourceRef.current });
            delaySourceRef.current = 'key';
            paintSubDelay();
        };
        videoEl.addEventListener('subtitledelaychange', onChange);
        paintSubDelay();
        // Delegated on the document: the dialog is swapped whole on a
        // preferred-language change, and repainted after ANY click inside it
        // -- a track pick or the on/off switch changes whether there is
        // anything to shift.
        const onClick = (e) => {
            const modal = e.target.closest && e.target.closest('#subtitles');
            if (!modal) return;
            const btn = e.target.closest('[data-sub-delay]');
            if (btn && !btn.disabled) {
                const op = btn.dataset.subDelay;
                const cur = subDelayRef.current;
                changeSubDelay(op === 'reset' ? 0 : cur + (op === 'later' ? SUBTITLE_DELAY_STEP : -SUBTITLE_DELAY_STEP), 'dialog');
                return;
            }
            setTimeout(paintSubDelay, 0);
        };
        // VLC's keys: g earlier, h later.
        const onKey = (e) => {
            if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA' || e.ctrlKey || e.metaKey || e.altKey) return;
            if (e.key !== 'g' && e.key !== 'h') return;
            e.preventDefault();
            changeSubDelay(subDelayRef.current + (e.key === 'h' ? SUBTITLE_DELAY_STEP : -SUBTITLE_DELAY_STEP), 'key');
        };
        document.addEventListener('click', onClick);
        document.addEventListener('keydown', onKey);
        return () => {
            videoEl.removeEventListener('subtitledelaychange', onChange);
            document.removeEventListener('click', onClick);
            document.removeEventListener('keydown', onKey);
        };
    }, []);

    // ---- the AI translation (hooks/useSubtitleTranslation.js) --------------
    // The run, its progress poll, the catch-up hold and everything that
    // decides them live in the hook. `late` carries what is only declared
    // further down (the on-screen offer): the hook reads it when it acts,
    // never while rendering. A direct file has one timeline and one run, so
    // the session machinery the hook still carries sees offset 0 and no seek
    // in flight.
    const seekOffsetRef = useRef(0);
    const sessionSeekingRef = useRef(false);
    const late = useRef({ withdrawOffer: () => {}, redecideOffer: () => {} });
    const translation = useSubtitleTranslation({
        videoRef, videoEl, trackContainer, trackHooks, seekOffset: 0, seekOffsetRef, sessionSeekingRef, late,
    });
    const { catchUp, preHolding, onDirectSeek, cancelPreHold, handleWait, handleKeepWatching, handleDismissCatchUp } = translation;
    // movi-player's controls seek and play on their own: the hook learns of
    // it from the element. A seek by the viewer opens the translation's hold
    // window; a Play while the silent hold is on means "pause" (the picture
    // reads as a seek in progress, not a paused film), so the hold ends and
    // the film stays where it is.
    useEffect(() => {
        const onSeeked = () => onDirectSeek();
        const onPlay = () => { if (cancelPreHold()) videoEl.pause(); };
        videoEl.addEventListener('seeked', onSeeked);
        videoEl.addEventListener('play', onPlay);
        return () => {
            videoEl.removeEventListener('seeked', onSeeked);
            videoEl.removeEventListener('play', onPlay);
        };
    }, [onDirectSeek, cancelPreHold]);

    // Resume prompt state -- must be declared before useWatchHistory which reads it.
    const [showResumePrompt, setShowResumePrompt] = useState(false);
    // The viewer has answered the resume prompt (either way). Until then a
    // film with a saved position is held: see the hold effect below.
    const [resumeAnswered, setResumeAnswered] = useState(false);
    // The same fact for the hold's 'play' listener: the answer calls play()
    // at once, and that event lands before the effect cleanup that would
    // take the listener off.
    const resumeAnsweredRef = useRef(false);
    const autoResumeRef = useRef(false);

    // Where the credits begin (credits.js), filled in further down. Declared
    // here because the watch history reports it with every position.
    const creditsAtRef = useRef(null);
    const { resumePosition, resumeReady, forceSendPosition } = useWatchHistory(videoRef, {
        resourceID, path, creditsAtRef,
        currentTime: state.currentTime,
        duration: state.duration,
        playing: state.playing,
        paused: showResumePrompt,
    });

    // A settings change that re-renders the player (the preferred language,
    // preferred-lang.js) calls this first: the position is saved now, and the
    // player that comes back continues from it without asking.
    useEffect(() => {
        if (!trackHooks) return undefined;
        trackHooks.beforeRestart = () => {
            const video = videoRef.current;
            if (!video) return;
            const at = video.currentTime || 0;
            const dur = duration > 0 ? duration : (video.duration || 0);
            if (!(at > 0) || !(dur > 0)) return;
            forceSendPosition(at, dur);
            markAutoResume(resourceID, path);
        };
        return () => { trackHooks.beforeRestart = null; };
    }, [trackHooks, duration, forceSendPosition, resourceID, path]);

    // Stream-start Umami event -- once per player, when playback passes
    // ENGAGEMENT_SECONDS. The denominator of share-rate analysis.
    useEffect(() => {
        if (streamStartFiredRef.current) return;
        if (state.currentTime < ENGAGEMENT_SECONDS) return;
        streamStartFiredRef.current = true;
        if (window.umami) window.umami.track('stream-start', {
            isVideo,
            resourceID: resourceID || '',
            player: 'movi',
            rate: videoRef.current ? videoRef.current.playbackRate : 1,
        });
        const modal = document.getElementById('subtitles');
        // getLang(), not document.documentElement.lang: the embed layouts
        // render <html> without a lang attribute.
        const uiLang = getLang();
        if (!modal) return;
        const audioEl = modal.querySelector('.audio[data-default="true"]');
        const audioLang = audioEl ? (audioEl.getAttribute('data-srclang') || '') : '';
        const preferredLang = modal.getAttribute('data-preferred-lang') || '';
        if (window.umami) {
            window.umami.track('subtitle-resolved', {
                ...resolveSubtitleLevel(readTracks(modal), uiLang, { audioLang, preferredLang }),
                uiLang,
                audioLang,
                notReady: modal.getAttribute('data-subtitles-not-ready') === 'true',
            });
        }
    }, [state.currentTime, isVideo, resourceID]);

    // Grace soft CTA -- once, when the film crosses the grace window. A native
    // <dialog> rendered by the action template next to the player and opened
    // with showModal(): the top layer shows it over a fullscreen player too.
    // The film stops while it is up and goes on with the answer, only if it
    // was the popup that stopped it (grace-hold.js).
    useEffect(() => {
        if (!graceDurationSec || graceShownRef.current) return;
        if (state.currentTime < graceDurationSec) return;
        const el = document.querySelector('#grace-cta');
        if (!el) return;
        graceShownRef.current = true;
        // Up from here on: the transfer status reads the popup itself
        // (lib/playerActivity.js graceOfferDue).
        videoEl.dataset.graceCtaShown = '';
        const hold = graceHoldRef.current;
        hold.start();
        const modal = typeof el.showModal === 'function';
        if (modal) {
            if (!el.open) el.showModal();
        } else {
            if (document.fullscreenElement) {
                document.exitFullscreen().catch(() => {});
            }
            el.classList.remove('hidden');
        }
        setGraceOpen(true);
        if (window.umami) window.umami.track('grace-soft-cta-shown', { paused: hold.held() });
        // `via`: the popup's own button, or Play -- the answer "continue".
        const hide = (action, via = 'button') => {
            if (graceAnswerRef.current !== hide) return;
            graceAnswerRef.current = null;
            videoEl.removeEventListener('play', onPlayAnswer);
            // The viewer's answer, on the element: the transfer status sells
            // the way out again only at this player's first real stall
            // (lib/playerActivity.js offerAnswered), and the buffering label
            // shows the lock from here on (buffering-label.js capLock).
            videoEl.dataset.graceCtaAnswered = action;
            if (modal) {
                if (el.open) el.close();
            } else {
                el.classList.add('hidden');
            }
            setGraceOpen(false);
            const paused = hold.held();
            hold.release({ play: via === 'play' });
            if (window.umami) window.umami.track('grace-soft-cta-click', { action, via, paused });
        };
        graceAnswerRef.current = hide;
        // Play while the popup holds the film is the answer "continue"
        // (grace-hold.js pauses it back; this answers for the viewer).
        const onPlayAnswer = () => {
            if (graceAnswerRef.current === hide && hold.isActive()) hide('continue', 'play');
        };
        videoEl.addEventListener('play', onPlayAnswer);
        const closeBtn = el.querySelector('.grace-cta-close');
        if (closeBtn) closeBtn.addEventListener('click', () => hide('dismiss'), { once: true });
        const contBtn = el.querySelector('.grace-cta-continue');
        if (contBtn) contBtn.addEventListener('click', () => hide('continue'), { once: true });
        // Esc closes a modal dialog on its own; here it is the close button.
        if (modal) {
            el.addEventListener('cancel', (e) => {
                e.preventDefault();
                hide('dismiss');
            }, { once: true });
        }
    }, [state.currentTime, graceDurationSec]);

    // player_play / player_paused custom events (the embed page's ads).
    useEffect(() => {
        let forcePaused = false;
        function onPlayerPaused() {
            videoRef.current?.pause();
            forcePaused = true;
        }
        function onPlayerPlay() {
            forcePaused = false;
            const r = videoRef.current?.play();
            if (r && r.catch) r.catch(() => {});
        }
        function onPlaying() {
            if (forcePaused) videoRef.current?.pause();
        }
        window.addEventListener('player_paused', onPlayerPaused);
        window.addEventListener('player_play', onPlayerPlay);
        videoEl.addEventListener('playing', onPlaying);
        return () => {
            window.removeEventListener('player_paused', onPlayerPaused);
            window.removeEventListener('player_play', onPlayerPlay);
            videoEl.removeEventListener('playing', onPlaying);
        };
    }, []);

    // player_ready on the first canplay, with the container's shape from the
    // picture. A file movi-player cannot play never reaches canplay: its
    // error screen (errordisplay) is what there is to see, so it reveals the
    // player the same way -- the page keeps the rendered player hidden until
    // one of the two (app/action.js, app/embed/index.js).
    useEffect(() => {
        let dispatched = false;
        const reveal = () => {
            if (dispatched) return;
            dispatched = true;
            window.dispatchEvent(new CustomEvent('player_ready'));
        };
        function onCanPlay() {
            if (videoEl.videoWidth && videoEl.videoHeight) {
                containerEl.style.aspectRatio = `${videoEl.videoWidth} / ${videoEl.videoHeight}`;
            }
            reveal();
        }
        function onErrorDisplay(e) {
            const d = (e && e.detail) || {};
            track('player-error', { title: String(d.title || '').slice(0, 120), retry: !!d.canRetry, software: !!d.canTrySoftware });
            window.dispatchEvent(new CustomEvent('player_show'));
            reveal();
        }
        videoEl.addEventListener('canplay', onCanPlay);
        videoEl.addEventListener('errordisplay', onErrorDisplay);
        return () => {
            videoEl.removeEventListener('canplay', onCanPlay);
            videoEl.removeEventListener('errordisplay', onErrorDisplay);
        };
    }, []);

    // A server that ignored Range: forward-only playback, no seeking. Counted;
    // movi-player hides its own seek UI.
    useEffect(() => {
        const onLinear = () => track('player-linear-mode', {});
        videoEl.addEventListener('linearmode', onLinear);
        return () => videoEl.removeEventListener('linearmode', onLinear);
    }, []);

    // Once the resume check completes: the prompt if there's a saved position.
    useEffect(() => {
        if (!resumeReady) return;
        // Video only, like the prompt's markup and the hold below.
        if (isVideo && resumePosition && resumePosition > 0) {
            // A player that came back from a settings change (the preferred
            // language, preferred-lang.js) continues without asking.
            autoResumeRef.current = takeAutoResume(resourceID, path);
            setShowResumePrompt(true);
        }
    }, [resumeReady]);

    // A film with a saved position does not start by itself: autoplay would
    // play it from the beginning behind the prompt, sound included. Held from
    // the moment the saved position is known until the prompt is answered.
    useEffect(() => {
        if (!isVideo || !resumeReady || !(resumePosition > 0) || resumeAnswered) return;
        const video = videoRef.current;
        if (!video) return;
        const hold = () => { if (!resumeAnsweredRef.current && !video.paused) video.pause(); };
        hold();
        video.addEventListener('play', hold);
        return () => video.removeEventListener('play', hold);
    }, [isVideo, resumeReady, resumePosition, resumeAnswered]);

    const playAfterPrompt = useCallback(() => {
        const r = videoRef.current ? videoRef.current.play() : null;
        if (r && r.catch) r.catch(() => {});
    }, []);

    const handleResume = useCallback(() => {
        setShowResumePrompt(false);
        resumeAnsweredRef.current = true;
        setResumeAnswered(true);
        const video = videoRef.current;
        if (!video) return;
        video.currentTime = resumePosition;
        state.setCurrentTime(resumePosition);
        playAfterPrompt();
        const dur = duration > 0 ? duration : (video.duration || 0);
        if (dur > 0) forceSendPosition(resumePosition, dur);
    }, [resumePosition, duration, forceSendPosition, playAfterPrompt]);

    const handleStartOver = useCallback(() => {
        setShowResumePrompt(false);
        resumeAnsweredRef.current = true;
        setResumeAnswered(true);
        const video = videoRef.current;
        if (video && video.currentTime > 0) video.currentTime = 0;
        playAfterPrompt();
        const dur = duration > 0 ? duration : (video?.duration || 0);
        if (dur > 0) forceSendPosition(0, dur);
    }, [duration, forceSendPosition, playAfterPrompt]);

    // A player back from a settings change answers the prompt itself. A file
    // the viewer had all but finished starts from the top (next-item.js
    // resumeAt): an automatic move that resumed at 98% would chain on.
    useEffect(() => {
        if (!showResumePrompt || !autoResumeRef.current) return;
        autoResumeRef.current = false;
        const dur = duration > 0 ? duration : ((videoRef.current && videoRef.current.duration) || 0);
        if (resumeAt(resumePosition, dur) > 0) handleResume();
        else handleStartOver();
    }, [showResumePrompt, handleResume, handleStartOver]);

    // ---- the on-screen translation offer (subtitle-offer.js) ----------
    //
    // Decided once, on the first play: the pill is about "this film has no
    // subtitles in your language", a property of the page the server rendered,
    // not something to re-ask on every resume. Read at first play rather than
    // at mount: it stays off a page that never plays (autoplay blocked, resume
    // prompt up) and gives the mount-time restore of a saved translation time
    // to make the chip the default -- which withdraws the offer by pickOffer's
    // own rule.
    const [controlsVisible, setControlsVisible] = useState(true);
    const [offer, setOffer] = useState(null);
    const [offerLingering, setOfferLingering] = useState(false);
    const [offerCard, setOfferCard] = useState(false);
    const offerArmedRef = useRef(false);
    // Bumped when the dialog is re-rendered for another language, so the
    // offer below is decided again.
    const [dialogVersion, setDialogVersion] = useState(0);
    const offerTimerRef = useRef(null);
    late.current.withdrawOffer = () => { setOffer(null); setOfferCard(false); };
    late.current.redecideOffer = () => { offerArmedRef.current = false; setDialogVersion((v) => v + 1); };
    // movi-player hides its bar when the film plays; the pill goes with it.
    useEffect(() => {
        const show = () => setControlsVisible(true);
        const hide = () => { if (!videoEl.paused) setControlsVisible(false); };
        let timer = null;
        const onMove = () => {
            show();
            if (timer) clearTimeout(timer);
            timer = setTimeout(hide, 3000);
        };
        containerEl.addEventListener('mousemove', onMove);
        containerEl.addEventListener('touchstart', onMove);
        videoEl.addEventListener('pause', show);
        onMove();
        return () => {
            if (timer) clearTimeout(timer);
            containerEl.removeEventListener('mousemove', onMove);
            containerEl.removeEventListener('touchstart', onMove);
            videoEl.removeEventListener('pause', show);
        };
    }, []);

    const trackOffer = useCallback((name, o, extra) => {
        if (window.umami) window.umami.track(name, { lang: o.lang, kind: o.kind, ...(extra || {}) });
    }, []);

    useEffect(() => {
        if (!isVideo || !state.playing || offerArmedRef.current) return;
        // Not under the resume prompt: autoplay can get a moment of playback
        // in before the saved position arrives, and a pill decided then would
        // spend its ten seconds behind the prompt's overlay.
        if (!resumeReady || (resumePosition > 0 && !resumeAnswered)) return;
        offerArmedRef.current = true;
        const modal = findSubtitlesModal(trackContainer);
        if (!modal) return;
        const next = pickOffer(readChips(modal));
        if (!next || !next.label) return;
        if (next.kind === 'upsell' && upsellSuppressed(offerStorage(), Date.now())) return;
        setOffer(next);
        setOfferLingering(true);
        offerTimerRef.current = setTimeout(() => setOfferLingering(false), offerTiming.lingerMs);
        trackOffer('subtitle-offer-shown', next);
    }, [state.playing, isVideo, trackContainer, trackOffer, resumeReady, resumePosition, resumeAnswered, dialogVersion]);

    useEffect(() => () => { if (offerTimerRef.current) clearTimeout(offerTimerRef.current); }, []);

    const handleOfferClick = useCallback(() => {
        if (!offer) return;
        trackOffer('subtitle-offer-click', offer);
        if (offer.kind === 'upsell') {
            setOfferCard((open) => !open);
            return;
        }
        // Taking the offer IS pressing the chip: the same delegated handler,
        // so the same PUT, the same marks, the same poll. The chip is asked
        // again whether it is still on offer -- an audio switch or a manual
        // pick may have spent it since the pill was decided.
        const modal = findSubtitlesModal(trackContainer);
        const el = modal ? findSubtitleItem(modal, offer.id) : null;
        setOffer(null);
        if (el && el.getAttribute('data-offered') === 'true') el.click();
    }, [offer, trackContainer, trackOffer]);

    // how: 'close' (the x), 'later' (Not now), 'never' (Don't offer).
    // Only the upsell is remembered; see subtitle-offer.js.
    const handleOfferDismiss = useCallback((how) => {
        if (!offer) return;
        if (offer.kind === 'upsell') suppressUpsell(offerStorage(), Date.now(), { never: how === 'never' });
        trackOffer('subtitle-offer-dismiss', offer, { how });
        setOffer(null);
        setOfferCard(false);
    }, [offer, trackOffer]);

    // A player mounted by a move to the next file (next-item-go.js) is about
    // to play by itself. Until it does, it is LOADING, not paused.
    const [awaitingStart, setAwaitingStart] = useState(awaitStart);
    useEffect(() => {
        if (!awaitingStart) return undefined;
        const video = videoRef.current;
        const done = () => setAwaitingStart(false);
        const timer = setTimeout(done, 10000);
        if (video) video.addEventListener('playing', done, { once: true });
        return () => {
            clearTimeout(timer);
            if (video) video.removeEventListener('playing', done);
        };
    }, [awaitingStart]);
    useEffect(() => { if (showResumePrompt) setAwaitingStart(false); }, [showResumePrompt]);

    // ---- the next file (next-item.js decides, next-item-go.js moves) ----
    // `next` is the server's answer on the player element; without it none
    // of this exists -- a film, the last file, an embed.
    const next = useRef(canMoveOn(document) ? readNext(videoEl) : null).current;
    useEffect(() => {
        let store = null;
        try { store = window.sessionStorage; } catch (e) { return; }
        const note = takeFallbackNote(store);
        if (!note) return;
        console.warn('next item: the previous page fell back to a reload --', note.reason, note);
        track('next-item-fallback', { reason: String(note.reason).slice(0, 120) });
    }, []);
    const [autoplayNext, setAutoplayNext] = useState(() => loadPrefs().autoplayNext);
    const autoplayNextRef = useRef(autoplayNext);
    autoplayNextRef.current = autoplayNext;
    const [nextCard, setNextCard] = useState(null); // null | 'soon' | 'offer' | 'ask'
    const nextCancelledRef = useRef(false);
    const [nextLoading, setNextLoading] = useState(false);
    const [nextProgress, setNextProgress] = useState('');
    const nextLoadingRef = useRef(false);
    nextLoadingRef.current = nextLoading;
    const nextGoRef = useRef(null);
    useEffect(() => () => nextGoRef.current?.dispose(), []);
    const earlyGoneRef = useRef(false);
    const cardShownAtRef = useRef(null);
    if (next && !nextGoRef.current) {
        nextGoRef.current = createNextItemGo({
            next, resourceID, root: trackContainer,
            getStage: currentStage, getAspectRatio: currentAspectRatio, initPlayer, destroyPlayer,
            onEvent: (name, data) => {
                if (name === 'go') track('next-item-go', { kind: next.kind, ...data });
                if (name === 'prepared') track('next-item-prepared', { kind: next.kind, ...data });
                if (name === 'loading') setNextLoading(!!data.on);
                if (name === 'progress') setNextProgress(data.text || '');
            },
        });
    }
    const safeSession = () => { try { return window.sessionStorage; } catch (e) { return null; } };
    const goNext = useCallback((how) => {
        if (!nextGoRef.current) return;
        if (how === 'auto') writeStreak(safeSession(), readStreak(safeSession()) + 1);
        else writeStreak(safeSession(), 0);
        setNextCard((c) => c || 'offer');
        // The film being left stops here; the pause also saves its position
        // (useWatchHistory).
        const video = videoRef.current;
        if (video && !video.paused) video.pause();
        graceHoldRef.current.dispose();
        nextGoRef.current.go(how);
    }, []);
    // Any sign of a viewer ends the "is anyone there" streak.
    useEffect(() => {
        if (!next) return undefined;
        const alive = () => writeStreak(safeSession(), 0);
        document.addEventListener('pointerdown', alive, true);
        document.addEventListener('keydown', alive, true);
        return () => {
            document.removeEventListener('pointerdown', alive, true);
            document.removeEventListener('keydown', alive, true);
        };
    }, []);
    // Where the credits begin: the container's chapters (the stream job's
    // data-credits-at) first, else subtitle timings past the middle
    // (credits.js) -- ONE request for a whole-file track, timings only.
    const creditsTriedRef = useRef(false);
    useEffect(() => {
        if (!isVideo || creditsTriedRef.current || !(next ? next.kind !== 'track' : !!window._userId)) return;
        if (state.duration > 0) {
            const fromChapters = creditsFromElement(videoEl, state.duration);
            if (fromChapters !== null) {
                creditsTriedRef.current = true;
                creditsAtRef.current = fromChapters;
                track('next-item-credits', { source: 'chapters', found: true, lead_s: Math.round(state.duration - fromChapters), next: !!next });
                return;
            }
        }
        if (!(state.duration > 0) || state.currentTime < state.duration * 0.6) return;
        creditsTriedRef.current = true;
        const dur = state.duration;
        const settle = (cues, source) => {
            const at = creditsStart(cues, dur);
            creditsAtRef.current = at;
            track('next-item-credits', { source, found: at !== null, lead_s: at !== null ? Math.round(dur - at) : 0, next: !!next });
        };
        // The cues the bridge holds for the track on screen (a translation's);
        // any other track's are read from its file below, once.
        const bridge = videoEl.__wtTracks;
        const loaded = (bridge ? bridge.cuesOf(bridge.activeId()) : []).map((c) => ({ start: c.start, end: c.end }));
        if (loaded.length && creditsStart(loaded, dur) !== null) { settle(loaded, 'loaded'); return; }
        const src = timingSourceURL(document.getElementById('subtitles'));
        if (!src) {
            if (loaded.length) settle(loaded, 'loaded');
            else track('next-item-credits', { source: 'none', found: false, lead_s: 0, next: !!next });
            return;
        }
        fetch(src).then((r) => (r.ok ? r.text() : '')).then((text) => settle(parseVttTimings(text), 'fetched')).catch(() => {});
    }, [state.currentTime, state.duration]);

    // The "coming up" card follows the clock.
    useEffect(() => {
        if (!next || !nextGoRef.current) return;
        const plan = advancePlan({
            currentTime: state.currentTime, duration: state.duration, playing: state.playing,
            prewarmed: nextGoRef.current.isPrepared(), kind: next.kind, creditsAt: creditsAtRef.current,
        });
        if (plan.card && !nextCancelledRef.current && nextCard === null) {
            cardShownAtRef.current = state.currentTime;
            setNextCard('soon');
            track('next-item-shown', { kind: next.kind, autoplay: autoplayNext });
        }
        if (!plan.card && nextCard === 'soon') { setNextCard(null); cardShownAtRef.current = null; }
        // The countdown ran out inside the credits: the verdict `ended` gets,
        // a little earlier. Only while actually playing.
        if (plan.card && nextCard === 'soon' && state.playing && !nextCancelledRef.current && !earlyGoneRef.current) {
            const cd = countdown({ currentTime: state.currentTime, duration: state.duration, creditsAt: creditsAtRef.current, shownAt: cardShownAtRef.current });
            if (cd.early && cd.left === 0) {
                earlyGoneRef.current = true;
                const verdict = atEnd({ autoplay: autoplayNext, autoStreak: readStreak(safeSession()), cancelled: false, kind: next.kind });
                if (verdict === 'go') goNext('auto');
                else if (verdict === 'ask') { setNextCard('ask'); track('next-item-still-watching', { kind: next.kind }); }
            }
        }
    }, [state.currentTime, state.duration, state.playing]);
    // The prewarm follows the element's own `timeupdate`: state.currentTime
    // is fed by requestAnimationFrame, which a background tab does not run.
    useEffect(() => {
        const video = videoRef.current;
        if (!next || !video || !nextGoRef.current) return undefined;
        const onTime = () => {
            const plan = advancePlan({
                currentTime: video.currentTime || 0, duration: video.duration || 0,
                playing: !video.paused, prewarmed: nextGoRef.current.isPrepared(),
                kind: next.kind, creditsAt: creditsAtRef.current,
            });
            if (plan.prewarm) nextGoRef.current.prepare();
        };
        video.addEventListener('timeupdate', onTime);
        return () => video.removeEventListener('timeupdate', onTime);
    }, []);
    // The end of the file.
    useEffect(() => {
        const video = videoRef.current;
        if (!next || !video) return undefined;
        const onEnded = () => {
            const verdict = atEnd({ autoplay: autoplayNext, autoStreak: readStreak(safeSession()), cancelled: nextCancelledRef.current, kind: next.kind });
            if (verdict === 'go') goNext('auto');
            else if (verdict === 'offer' || verdict === 'ask') {
                setNextCard(verdict);
                if (verdict === 'ask') track('next-item-still-watching', { kind: next.kind });
            }
        };
        video.addEventListener('ended', onEnded);
        return () => video.removeEventListener('ended', onEnded);
    }, [autoplayNext, goNext]);
    const nextCardRef = useRef(null);
    const nextCardVisible = !!(next && next.kind !== 'track' && nextCard);
    useDockedPopover(nextCardVisible, containerEl, nextCardRef, `${nextLoading}|${nextProgress ? 1 : 0}|${nextCard}`);

    const cancelNext = useCallback(() => {
        nextCancelledRef.current = true;
        setNextCard(null);
        track('next-item-cancel', { kind: next ? next.kind : '' });
    }, []);
    const toggleAutoplayNext = useCallback(() => {
        setAutoplayNext((v) => {
            savePrefs({ autoplayNext: !v });
            track('next-item-autoplay', { on: !v });
            return !v;
        });
    }, []);

    // ---- webtor's controls in movi-player's own chrome ----------------
    // Share (the header's handler, tagged location:'player'), the embed
    // dialog, the track picker dialog, Next and its autoplay switch. Added
    // once; addControl puts them on the bar and in the right-click menu,
    // fullscreen included.
    const goNextRef = useRef(goNext);
    goNextRef.current = goNext;
    const toggleAutoplayRef = useRef(toggleAutoplayNext);
    toggleAutoplayRef.current = toggleAutoplayNext;
    useEffect(() => {
        const el = videoEl;
        if (!showControls || typeof el.addControl !== 'function') return undefined;
        const added = [];
        const add = (spec) => {
            try {
                el.addControl(spec);
                added.push(spec.id);
            } catch (e) {
                console.warn('player: addControl failed --', spec.id, e);
            }
        };
        if (isVideo && features.share) {
            add({
                id: 'wt-share', label: t('player.share'), icon: ICON_SHARE, placement: ['top', 'menu'],
                onSelect: () => shareResource({ location: 'player', url: videoEl.dataset.shareUrl }),
            });
        }
        if (features.embed) {
            add({ id: 'wt-embed', label: t('player.embed'), icon: ICON_EMBED, placement: 'menu', onSelect: () => toggleDialog('embed') });
        }
        if (isVideo && features.advancedtracks && document.getElementById('subtitles')) {
            // On the bar where movi-player's own CC and audio buttons were
            // (controlsListFor switches them off: this picker is the one).
            add({
                id: 'wt-tracks', label: t('player.subtitlesAndAudio'), icon: ICON_TRACKS, placement: ['bar', 'menu'],
                before: 'settings', onSelect: () => {
                    toggleDialog('subtitles');
                    paintSubDelay();
                },
            });
        }
        if (next) {
            add({
                id: 'wt-next', label: t('player.next'), title: next.label ? `${t('player.next')}: ${next.label}` : null,
                icon: ICON_NEXT, after: 'play', side: 'left', hotkey: 'shift+n',
                // Not twice: the next file is already loading.
                onSelect: () => { if (!nextLoadingRef.current) goNextRef.current('button'); },
            });
            add({
                id: 'wt-autoplay-next', label: t('player.autoplayNext'), placement: 'menu', toggle: true,
                active: autoplayNextRef.current, onSelect: () => toggleAutoplayRef.current(),
            });
        }
        return () => {
            for (const id of added) {
                try { el.removeControl(id); } catch (e) { /* the element is gone */ }
            }
        };
    }, []);
    // The menu's autoplay switch follows the card's.
    useEffect(() => {
        if (!next || typeof videoEl.updateControl !== 'function') return;
        try { videoEl.updateControl('wt-autoplay-next', { active: autoplayNext }); } catch (e) { /* not added */ }
    }, [autoplayNext]);

    // ---- fullscreen on the stage -----------------------------------------
    // movi-player asks before it goes fullscreen (movi-fullscreen-request,
    // cancelable): the page takes it onto the STAGE, the element that outlives
    // a player (initPlayer), so the move to the next file stays fullscreen and
    // this layer's overlays -- the resume prompt, the buffering label, the
    // next file's card -- are inside it. Where the stage cannot go fullscreen
    // (iOS) movi-player keeps its own route.
    useEffect(() => {
        const el = videoEl;
        const stage = (containerEl.closest && containerEl.closest('.wt-player-stage')) || containerEl;
        const enter = stage.requestFullscreen || stage.webkitRequestFullscreen;
        if (!enter) return undefined;
        const fsElement = () => document.fullscreenElement || document.webkitFullscreenElement;
        const onRequest = (e) => {
            e.preventDefault();
            if (e.detail && e.detail.active) {
                const exit = document.exitFullscreen || document.webkitExitFullscreen;
                if (fsElement() && exit) Promise.resolve(exit.call(document)).catch(() => {});
                if (typeof el.setHostFullscreen === 'function') el.setHostFullscreen(false);
                return;
            }
            Promise.resolve(enter.call(stage))
                .then(() => { if (typeof el.setHostFullscreen === 'function') el.setHostFullscreen(true); })
                .catch(() => {});
        };
        // Left by Esc or the browser's own way out: the element's UI follows.
        const onChange = () => {
            if (!fsElement() && typeof el.setHostFullscreen === 'function') el.setHostFullscreen(false);
        };
        el.addEventListener('movi-fullscreen-request', onRequest);
        document.addEventListener('fullscreenchange', onChange);
        document.addEventListener('webkitfullscreenchange', onChange);
        return () => {
            el.removeEventListener('movi-fullscreen-request', onRequest);
            document.removeEventListener('fullscreenchange', onChange);
            document.removeEventListener('webkitfullscreenchange', onChange);
        };
    }, []);

    // ---- the buffering label (BufferingLabel.jsx, buffering-label.js) ----
    // Where movi-player's spinner was (controlslist nospinner): "Buffering",
    // and at the plan's cap the lock that opens the status's stream plan
    // card. Whether the viewer is held at the cap is the transfer status's
    // word (lib/playerLabel.js); capLock adds what only the player knows.
    const [capLabel, setCapLabel] = useState(() => currentPlayerLabel());
    const [capCause, setCapCause] = useState(() => currentPlayerCause());
    const answerRef = useRef(undefined);
    if (answerRef.current === undefined) answerRef.current = answerLabel(videoEl);
    useEffect(() => {
        setCapLabel(currentPlayerLabel());
        setCapCause(currentPlayerCause());
        return onPlayerLabel((label, cause) => {
            setCapLabel(label);
            setCapCause(cause);
        });
    }, []);
    const [capCardUp, setCapCardUp] = useState(() => capCardOpen());
    useEffect(() => {
        setCapCardUp(capCardOpen());
        return onCapCard(setCapCardUp);
    }, []);
    const bufferingShown = showControls && isVideo && !showResumePrompt
        && (preHolding || nextLoading || (awaitingStart && !state.playing) || (state.playing && state.loading));
    const graceUp = graceOpen || (!!graceDurationSec && !graceShownRef.current
        && state.currentTime >= graceDurationSec && !!document.querySelector('#grace-cta'));
    const answered = 'graceCtaAnswered' in videoEl.dataset && !nextLoading;
    const spokeRef = useRef(false);
    spokeRef.current = statusSpoke(spokeRef.current, {
        label: capLabel, answered, graceSec: graceDurationSec, movieTime: state.currentTime,
    });
    const lock = bufferingShown ? capLock({
        label: capLabel, answer: answerRef.current, answered, fitsCap: 'statusFitsCap' in videoEl.dataset,
        cause: capCause, spoke: spokeRef.current, graceSec: graceDurationSec, movieTime: state.currentTime, graceUp,
    }) : null;
    const lockSeenRef = useRef(new Set());
    useEffect(() => {
        if (!lock) return;
        const key = lockKey(lock);
        if (lockSeenRef.current.has(key)) return;
        lockSeenRef.current.add(key);
        track('player-label-lock-shown', lock.props);
    }, [lock]);
    const toggleCapCard = useCallback(() => {
        if (capCardOpen()) {
            closeCapCard();
            return;
        }
        if (!lock) return;
        track('donate-player-label-shown', lock.props);
        openCapCard(lock);
    }, [lock]);

    // The container's classes (managed outside Preact).
    useEffect(() => {
        const el = containerEl;
        if (!el) return;
        el.className = `wt-player wt-player--movi ${isVideo ? 'wt-player--video' : 'wt-player--audio'}${fixedSize ? ' wt-player--fixed' : ''}`;
    }, [containerEl, isVideo]);

    return (
        <>
            {/* Resume prompt -- continue from the saved position or start over */}
            {showResumePrompt && isVideo && (
                <div class="wt-player-overlay wt-resume-prompt" style="background:rgba(0,0,0,0.75);z-index:50;display:flex;align-items:center;justify-content:center"
                     onClick={(e) => e.stopPropagation()} onDblClick={(e) => e.stopPropagation()}>
                    <div style="display:flex;flex-direction:column;gap:10px;align-items:center">
                        <button type="button" onClick={(e) => { e.stopPropagation(); handleResume(); }}
                            class="wt-resume-btn wt-resume-btn--primary">
                            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" width="16" height="16"><path fill-rule="evenodd" d="M4.5 5.653c0-1.427 1.529-2.33 2.779-1.643l11.54 6.347c1.295.712 1.295 2.573 0 3.286L7.28 19.99c-1.25.687-2.779-.217-2.779-1.643V5.653Z" clip-rule="evenodd" /></svg>
                            {tf('player.continueFrom', formatTime(resumePosition))}
                        </button>
                        <button type="button" onClick={(e) => { e.stopPropagation(); handleStartOver(); }}
                            class="wt-resume-btn wt-resume-btn--ghost">
                            {t('player.startOver')}
                        </button>
                    </div>
                </div>
            )}

            {/* The AI translation is behind the playhead -- offer to wait
                for it. A top-centre pill: the subtitles it is about live
                at the bottom of the picture and the controls under them. */}
            {/* The subtitles are back: said once, for a few seconds, so the
                pill that explained their absence does not just vanish. */}
            {catchUp && catchUp.caughtUp && isVideo && (
                <div class="wt-catchup wt-catchup--done" role="status"
                     onClick={(e) => e.stopPropagation()} onDblClick={(e) => e.stopPropagation()}>
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="20 6 9 17 4 12"/></svg>
                    <span class="wt-catchup-text">{t('player.subtitleCaughtUp')}</span>
                </div>
            )}
            {catchUp && !catchUp.caughtUp && isVideo && (
                <div class="wt-catchup" role="status"
                     onClick={(e) => e.stopPropagation()} onDblClick={(e) => e.stopPropagation()}>
                    <LoadingSpinner />
                    <span class="wt-catchup-text">
                        {t(catchUp.waiting ? 'player.subtitleCatchUpWaitingShort' : 'player.subtitleCatchUpShort')}
                    </span>
                    <button type="button" class="wt-catchup-btn"
                        onClick={catchUp.waiting ? handleKeepWatching : handleWait}>
                        {t(catchUp.waiting ? 'player.subtitleCatchUpResume' : 'player.subtitleCatchUpWait')}
                    </button>
                    <button type="button" class="wt-catchup-close"
                        aria-label={t('player.subtitleCatchUpDismiss')} onClick={handleDismissCatchUp}>×</button>
                </div>
            )}

            {/* No subtitles in the viewer's language: the picker's offer,
                brought to the picture. Same slot as the catch-up pill, which
                wins it (offerVisible). */}
            {isVideo && offerVisible({ offer, lingering: offerLingering, controlsVisible, cardOpen: offerCard, catchUp }) && (
                <div class="wt-catchup wt-offer" data-kind={offer.kind}
                     onClick={(e) => e.stopPropagation()} onDblClick={(e) => e.stopPropagation()}>
                    <button type="button" class="wt-offer-main" onClick={handleOfferClick}
                        aria-expanded={offer.kind === 'upsell' ? String(offerCard) : undefined}>
                        <span class="wt-offer-mark" aria-hidden="true">✦ AI</span>
                        <span class="wt-catchup-text">{offer.label}</span>
                        {offer.kind === 'upsell' && (
                            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
                        )}
                    </button>
                    <button type="button" class="wt-catchup-close"
                        aria-label={t('player.subtitleCatchUpDismiss')} onClick={() => handleOfferDismiss('close')}>
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>
                    </button>
                </div>
            )}
            {isVideo && offer && offer.kind === 'upsell' && offerCard && !catchUp && (() => {
                const card = readUpsellCard(findSubtitlesModal(trackContainer));
                return (
                    <div class="wt-offer-card" role="dialog" aria-label={offer.label}
                         onClick={(e) => e.stopPropagation()} onDblClick={(e) => e.stopPropagation()}>
                        <p class="wt-offer-card-text">{card.text}</p>
                        {card.href && (
                            <a class="wt-offer-card-cta" href={card.href} target="_blank" rel="noopener"
                               data-umami-event="donate-subtitle-translate" data-umami-event-tier={card.tier}
                               data-umami-event-source="player">{card.cta}</a>
                        )}
                        <div class="wt-offer-card-row">
                            <button type="button" class="wt-offer-card-link" onClick={() => handleOfferDismiss('later')}>{t('player.subtitleOfferLater')}</button>
                            <button type="button" class="wt-offer-card-link" onClick={() => handleOfferDismiss('never')}>{t('player.subtitleOfferNever')}</button>
                        </div>
                    </div>
                );
            })()}

            {/* The buffering label, said by a live region of its own that is
                there before its words. */}
            <span class="sr-only" role="status" data-buffering-live>
                {bufferingShown ? (lock ? tf('player.bufferingCap', lock.rate) : t('player.buffering')) : ''}
            </span>
            {bufferingShown && (
                <div class={`wt-player-overlay wt-buffering${lock ? ' wt-buffering--cap' : ''}`}>
                    <BufferingPill label={lock} open={!!lock && capCardUp} onToggle={toggleCapCard} />
                </div>
            )}

            {/* What comes after this file. 'soon': the last seconds, the next
                one starts by itself at the end unless cancelled. 'offer':
                autoplay is off, the file has ended. 'ask': several files went
                by with no sign of a viewer. */}
            {nextCardVisible && (
                <div ref={nextCardRef} popover={HAS_POPOVER ? 'manual' : undefined} class="wt-next-card" onClick={(e) => e.stopPropagation()} onDblClick={(e) => e.stopPropagation()}>
                    <div class="wt-next-card-kicker">
                        {nextLoading ? t('player.nextLoading') : (nextCard === 'ask' ? t('player.stillWatching') : (nextCard === 'soon' && autoplayNext ? tf('player.nextIn', countdown({ currentTime: state.currentTime, duration: state.duration, creditsAt: creditsAtRef.current, shownAt: cardShownAtRef.current }).left) : t('player.nextUp')))}
                    </div>
                    <div class="wt-next-card-label" title={next.label}>{next.label}</div>
                    {nextLoading && nextProgress && (
                        <div class="wt-next-card-progress" aria-live="off" title={nextProgress}>{nextProgress}</div>
                    )}
                    <div class="wt-next-card-actions">
                        <button type="button" class="wt-next-card-go" onClick={() => goNext('card')} disabled={nextLoading}>
                            {nextCard === 'ask' ? t('player.continueWatching') : t('player.playNow')}
                        </button>
                        {nextCard === 'soon' && (
                            <button type="button" class="wt-next-card-link" onClick={cancelNext}>{t('player.nextCancel')}</button>
                        )}
                    </div>
                    <label class="wt-next-card-auto">
                        <span>{t('player.autoplayNext')}</span>
                        <input type="checkbox" role="switch" class="toggle toggle-soft toggle-sm" checked={autoplayNext} onChange={toggleAutoplayNext} />
                    </label>
                </div>
            )}
        </>
    );
}

function formatTime(seconds) {
    const s = Math.floor(seconds);
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const sec = s % 60;
    if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}`;
    return `${m}:${String(sec).padStart(2, '0')}`;
}

// The features a page's settings switch on and off (data-settings: an embed's
// own, or the resource page's defaults). The ones movi-player draws become its
// controlslist (controlsListFor).
export function parseFeatures(settings, isVideo) {
    const adsOn = !!(window._domainSettings && window._domainSettings.ads === true);
    const defaults = {
        playpause: true,
        progress: true,
        duration: true,
        volume: true,
        speed: true,
        advancedtracks: isVideo,
        fullscreen: isVideo,
        // The page renders the Embed dialog only on an instance that serves
        // embeds (stream_video.html); no dialog, no control.
        embed: isVideo && !!document.getElementById('embed'),
        // A growth feature: locked on for ads-enabled (free) embed domains,
        // so settings.features.share is honoured only where the domain has
        // earned NoAds. On the resource page there is no _domainSettings.
        share: true,
        logo: adsOn,
    };
    if (settings && settings.features) {
        for (const name in settings.features) {
            if (name === 'share' && adsOn) continue;
            defaults[name] = settings.features[name];
        }
    }
    return defaults;
}

// controlsListFor maps the page's features onto movi-player's controlslist
// tokens. The spinner is always off: the buffering label (with the plan's
// lock) takes its place. With the picker on the page movi-player's own
// captions and audio controls go too.
export function controlsListFor(features, hasPicker = false) {
    const off = ['nospinner'];
    // The picker dialog is the one place tracks are chosen (movi-tracks.js).
    if (hasPicker) off.push('nocc', 'noaudio');
    if (!features.playpause) off.push('noplay', 'nocenterplay');
    if (!features.progress) off.push('noprogress');
    if (!features.duration) off.push('notime');
    if (!features.volume) off.push('novolume');
    if (!features.speed) off.push('nospeed');
    if (!features.fullscreen) off.push('nofullscreen');
    if (!features.advancedtracks) off.push('noaudio', 'nocc');
    return off.join(' ');
}

// upgradeToMovi puts a <movi-player> where the server-rendered <video> or
// <audio> is: its attributes (data-* included -- the rest of the page reads
// them off the player element: the transfer status, the next file, the
// track picker) and its children (<source>, <track>) move over, `controls`
// and the box size are the caller's. The element is created after the
// engine module has registered it, so it is upgraded on creation.
function upgradeToMovi(media, { controls, features, hasPicker }) {
    const el = document.createElement('movi-player');
    for (const a of Array.from(media.attributes)) {
        if (a.name === 'controls' || a.name === 'width' || a.name === 'height') continue;
        el.setAttribute(a.name, a.value);
    }
    el.setAttribute('wasmurl', wasmURL());
    el.setAttribute('controlslist', controlsListFor(features, hasPicker));
    if (controls) el.setAttribute('controls', '');
    const title = media.dataset.resourceTitle;
    if (title) el.setAttribute('title', title);
    // With the picker on the page it owns the tracks (movi-tracks.js): the
    // <track> children would be registered by movi-player under their
    // language too, and shadow the picker's. Without one (a bare embed) they
    // are all the viewer has, and stay.
    while (media.firstChild) {
        const child = media.firstChild;
        if (hasPicker && child.nodeName === 'TRACK') media.removeChild(child);
        else el.appendChild(child);
    }
    media.replaceWith(el);
    return el;
}

/**
 * Initialize player on a target container that contains a <video> or <audio> with class="player".
 * Called from action/stream.js.
 */
// The STAGE is the element fullscreen is requested on, and the one thing that
// outlives a player: moving to the next file (next-item-go.js) destroys this
// player and mounts another INTO THE SAME STAGE, and a fullscreen element
// stays fullscreen for as long as it stays in the document. `opts.stage` is
// that existing stage; an ordinary start makes its own.
export async function initPlayer(target, opts = {}) {
    const media = target.querySelector('video.player, audio.player');
    if (!media) return;

    // Translations and the engine before rendering (sync t() reads from the
    // cached instance; the element must be defined before it is created).
    await Promise.all([initI18n(), loadEngine()]);
    // A transition may have lost its stage meanwhile. It must not register a
    // detached player over the one opened since.
    if (opts.stage && !opts.stage.isConnected) return;
    if (!media.isConnected) return;

    let settings = {};
    if (media.dataset.settings) {
        settings = JSON.parse(media.dataset.settings);
    }
    const isVideo = media.tagName === 'VIDEO';
    const showControls = media.hasAttribute('controls');
    const features = parseFeatures(settings, isVideo);

    const fixedWidth = media.getAttribute('width');
    const fixedHeight = media.getAttribute('height');

    // Wrap the player in its mount and stage.
    const mountEl = document.createElement('div');
    mountEl.className = 'wt-player-mount';
    if (fixedWidth) mountEl.style.width = fixedWidth;
    if (fixedHeight) mountEl.style.height = fixedHeight;
    let stage = opts.stage || null;
    if (!stage) {
        stage = document.createElement('div');
        stage.className = 'wt-player-stage';
        media.parentNode.insertBefore(stage, media);
    }
    stage.appendChild(mountEl);

    const playerContainer = document.createElement('div');
    // The shape of the picture that was here a moment ago (next-item-go.js),
    // until canplay reports this one's.
    if (opts.aspectRatio) playerContainer.style.aspectRatio = opts.aspectRatio;
    mountEl.appendChild(playerContainer);
    playerContainer.appendChild(media);
    const hasPicker = !!document.getElementById('subtitles');
    const videoEl = upgradeToMovi(media, { controls: showControls, features, hasPicker });
    // What the picker puts on screen goes through this (track-dialog.js
    // bridgeFor): on the element, since module state is per bundle.
    if (hasPicker) {
        videoEl.__wtTracks = createTrackBridge(videoEl, {
            onError: (id) => track('subtitle-load-error', { id: String(id).slice(0, 60) }),
        });
    }

    // The track picker and the embed dialog are wired on their own modals,
    // outside the player; `trackHooks` is the hand-off point the component
    // fills in on mount.
    const trackHooks = {};
    wireTrackHandlers(target, trackHooks);
    wireEmbedCopy(target);

    // The logo (ads download overlay) -- only when ads are on.
    let showLogo = !!(window._domainSettings && window._domainSettings.ads === true);
    if (settings.features && settings.features.logo !== undefined) {
        showLogo = settings.features.logo;
    }
    if (showLogo) wireLogo(target, mountEl);

    // webtor's layer goes after the element, in a host of its own: Preact
    // owns that host's children, never the element's.
    const layer = document.createElement('div');
    layer.className = 'wt-player-layer';
    playerContainer.appendChild(layer);
    render(
        <PlayerComponent videoEl={videoEl} isVideo={isVideo} settings={settings} containerEl={playerContainer} showControls={showControls} fixedSize={!!(fixedWidth || fixedHeight)} trackContainer={target} trackHooks={trackHooks} awaitStart={!!opts.awaitStart} />,
        layer
    );

    _currentPlayer = { stage, mountEl, playerContainer, layer, videoEl };
}

function wireEmbedCopy(container) {
    // type="button" -- deliberately not a method="dialog" submit, so copying
    // leaves the embed dialog open.
    const copy = container.querySelector('#embed .copy');
    if (!copy) return;
    copy.addEventListener('click', () => {
        const textarea = container.querySelector('#embed textarea');
        if (!textarea) return;
        navigator.clipboard.writeText(textarea.value).then(() => {
            if (window.toast) window.toast.success(t('player.copied'));
        });
    });
}

function wireLogo(container, playerContainer) {
    const logo = container.querySelector('#logo');
    if (!logo) return;
    logo.className = 'wt-player-logo';
    playerContainer.appendChild(logo);
}

// currentStage: the stage of the player on screen, for a caller that is about
// to replace that player and wants the next one in the same place.
export function currentStage() {
    return _currentPlayer ? _currentPlayer.stage : null;
}

// currentAspectRatio: the CSS aspect-ratio of the player on screen ('' before
// its first canplay), to hand to the player that replaces it.
export function currentAspectRatio() {
    return _currentPlayer ? (_currentPlayer.playerContainer.style.aspectRatio || '') : '';
}

// `keepStage`: the player goes, its stage stays in the document for the next
// one (see initPlayer).
export function destroyPlayer({ keepStage = false } = {}) {
    // Before the early return: "this page's player is gone" is true whether
    // or not one was mounted (track-dialog.js persistTrackChoice).
    releaseTrackDialog();
    // The card the lock opened is the viewer's to close, not the player's: a
    // move to the next file (keepStage) leaves it open.
    if (!keepStage) closeCapCard();
    if (!_currentPlayer) return;
    const { stage, mountEl, layer, videoEl } = _currentPlayer;

    render(null, layer);
    if (videoEl.__wtTracks) {
        videoEl.__wtTracks.dispose();
        videoEl.__wtTracks = null;
    }
    // movi-player tears itself down when it leaves the document
    // (disconnectedCallback): fetches aborted, engine destroyed.
    videoEl.remove();
    mountEl.remove();
    if (stage && !keepStage) stage.remove();

    _currentPlayer = null;
}

// The dialog's functions live in track-dialog.js; they are re-exported here
// for the callers that have always imported them from the player module.
export {
    activateSubtitle,
    findSubtitleItem,
    firstTrackSrc,
    markTrack,
    PUT_RETRY_DELAY_MS,
    setSubtitlesOff,
    setUploadPanel,
    swapSubtitlesDialog,
    syncUploadMarks,
    wireTrackHandlers,
} from './track-dialog.js';
