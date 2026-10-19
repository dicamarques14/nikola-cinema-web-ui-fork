# Player (`assets/src/js/lib/player/`)

The player of the stream action (`views/action/stream_video.html`, `stream_audio.html`) and the
embed is **`<movi-player>`** (movi-player 0.4.1, Apache-2.0) with a thin webtor layer on top
(`Player.jsx`). The browser opens the file itself over HTTP range requests and decodes it --
WebCodecs, with FFmpeg in WebAssembly as the software path -- so nothing is transcoded for
playback: no hls.js, no transcoder session, no passthrough route, no stream restart. What the
element needs from the server is a byte-range-capable URL (`/torrent-http-proxy/`, 206 with
`Content-Range`; self-hosted `tests/scenarios/33-range.sh` holds that).

This file covers the layer webtor keeps over movi-player. Subtitles and AI translation live in
`docs/subtitle_translate.md`, the track picker in `docs/uikit.html` §19, the design of the
subtitle/audio bridge in `docs/superpowers/specs/2026-10-15-movi-subtitle-bridge.md`.

## Mounting — `initPlayer`, `upgradeToMovi`

- **The engine** is loaded once per page (`loadEngine`: `import('movi-player/element/slim')`,
  shared by every player after the first). The slim build's `movi.wasm` is a file of its own,
  copied next to the bundles by `webpack.config.js`; the element is told where it is
  (`wasmurl`, from the runtime public path, so a CDN asset host is followed). The slim build's
  silent fallback to the browser's own `<video>` is off: a file the engine cannot play shows its
  error screen rather than a broken native player.
- **The upgrade.** The server renders a plain `<video class="player">` / `<audio>`;
  `upgradeToMovi` puts a `<movi-player>` in its place with all its attributes (`data-*`
  included -- the transfer status, the next file and the picker read them off the player
  element), `controlslist` (below) and its `<source>` children. With the picker on the page the
  `<track>` children are dropped: the bridge owns what is on screen, and movi-player would
  register them under their language and shadow the picker's. A bare embed without a picker
  keeps them, and keeps movi-player's own captions menu.
- **The stage.** The element lives in `.wt-player-mount` inside `.wt-player-stage`, the element
  fullscreen is requested on and the one thing that outlives a player: the next file
  (`next-item-go.js`) destroys this player and mounts the next INTO THE SAME STAGE, so
  fullscreen survives the move. movi-player asks before it goes fullscreen
  (`movi-fullscreen-request`, cancelable); the page takes it onto the stage and tells the
  element (`setHostFullscreen`). Where the stage cannot go fullscreen (iOS) movi-player keeps its
  own route.
- **The layer.** webtor's overlays (resume prompt, buffering label, offer and catch-up pills,
  next card) are rendered by Preact into `.wt-player-layer`, a sibling of the element: Preact
  owns that host's children, never the element's.
- **Reveal.** `player_ready` goes out on the first `canplay` (with the picture's aspect ratio on
  the container), or on movi-player's `errordisplay` -- a file it cannot play never reaches
  `canplay`, and the page keeps the rendered player hidden until one of the two
  (`app/action.js`, `app/embed/index.js`). `player_play` / `player_paused` (the embed page's
  ads) play and pause the element.
- **Teardown** (`destroyPlayer`): the layer is unrendered, the track bridge disposed, the element
  removed -- movi-player tears itself down when it leaves the document (fetches aborted, engine
  destroyed).

## Controls and `features`

`parseFeatures` (`Player.jsx`) builds the defaults; an embed may override them through
`settings.features` (the share lock for ad-supported domains is described in the code).

| feature | default | what |
|---|---|---|
| `playpause`, `progress`, `duration`, `volume` | on | the basics |
| `speed` | on | playback speed |
| `advancedtracks`, `fullscreen`, `chromecast`, `embed` | video only | |
| `share`, `logo`, `availableprogress` | see code | |

movi-player draws the controls. `controlsListFor(features, hasPicker)` maps the features onto its
`controlslist` tokens: a feature that is off is its `no*` token; `nospinner` always (the
buffering label takes its place); `nocc noaudio` when the track picker (`#subtitles`) is on the
page -- the picker is the one place tracks are chosen -- or when `advancedtracks` is off.

What the page adds goes through the element's control API (`addControl`, removed on unmount):

| id | where | what |
|---|---|---|
| `wt-share` | top, menu | share (video, `features.share`) |
| `wt-embed` | menu | the embed dialog |
| `wt-tracks` | bar (before settings), menu | "Subtitles & audio": the picker dialog, where CC sat |
| `wt-next` | bar, after play; `shift+N` | the next file (below) |
| `wt-autoplay-next` | menu, toggle | autoplay of the next file, kept in step with the card's switch |

## Keys

movi-player owns the player's keys (play/pause, seek, volume, fullscreen, mute, speed). The page
adds `g` / `h`: subtitles earlier / later (VLC's keys, below), and `shift+N` through the Next
control. Play while the grace popup is up -- whatever starts it -- is its answer "continue"
(below).

## Subtitles and audio on screen — `movi-tracks.js`, `track-dialog.js`

The picker (`#subtitles`, `track-dialog.js`, `track-picker.js`) decides; the bridge
(`createTrackBridge(el)`, one per element, hung on it as `el.__wtTracks` because module state is
per bundle) puts the answer on screen through movi-player's API, which is not `<track>`:

| chip | how it is drawn |
|---|---|
| embedded (`MediaProbe`) | `selectSubtitleTrack(streamIndex)` / `selectAudioTrack(streamIndex)`: the file's ffprobe stream index, rendered by the server as `data-stream` (`handlers/action/helper.go` `ListItem.Stream`). Text and bitmap (PGS, DVB, DVD) tracks; a PGS chip has no HLS-era number and is named `mp-b<index>`. |
| side-loaded (OpenSubtitles, uploads, export tags) | `addSubtitleTrack({url, lang: chipID, label, format})`, then `selectSubtitleLang(chipID)`. The key is the chip id, never a language: webtor offers several tracks in one language. Fetched by movi-player on selection only. `format` is the bridge's (`formatOf`): the proxy's `file.srt~vtt` is WebVTT, which movi-player would take for SRT. |
| AI translation (`Translated`) | the generated path: the bridge reads the growing VTT itself (`parseVttCues`) and hands the cues over with `appendSubtitleCues(chipID, label, cues, pending)`; a re-read adds what is new without blanking. `pending` is the translation hook's word: true while the run writes, false after `onDone`. |
| None | both off. |

Rules: every call waits for `loadedmetadata` (embedded tracks are known only then; a new source
re-arms the wait); the last selection asked for wins (a sequence number, so a slow fetch cannot
land over a later pick); an embedded track on screen is switched off before a side-loaded or
generated one is asked for, so two never draw at once, and a pick that fails to load leaves
nothing drawn and is reported (`subtitle-load-error`); `dropMissing(chipIDs)` -- a deleted
upload, the AI chip of a language that was left -- takes generated tracks out of the engine,
switches a showing side-loaded one off (movi-player 0.4.1 has no call that removes an external
entry; its menu is hidden, and a chip that comes back under the same id replaces the entry), and
discards a selection still in flight for a chip that is gone.

At `loadedmetadata` the dialog's defaults -- the server's ladder, a saved choice, a carried one --
go on screen the way a click would, minus the PUT. "What is playing" (the upload marks, the
credits guess) is the bridge's answer (`activeId()`, `activeStream()`, `cuesOf(id)`), never
`textTracks`: movi-player mirrors one synthetic cue there.

## Subtitle delay — `player-prefs.js`

One value, seconds, positive = later (VLC's sign), for whatever movi-player draws -- embedded
tracks included: the element's `subtitleDelay`, which tells the page when it changes
(`subtitledelaychange`, movi-player's own keys too). Remembered **per file**
(`resourceID:path`, `loadSubtitleDelay` / `saveSubtitleDelay`), set on the element at mount,
saved on every change. The dialog's row (`#subtitle-delay`: earlier / later / reset, step
`SUBTITLE_DELAY_STEP` = 0.25 s, clamped to ±60 s) and `g` / `h` drive the element; the row is
repainted whenever the dialog opens or a pick changes whether there is anything to shift. The
delays the HLS player stored are not migrated.

> The sections from here on were carried over from the HLS player and still describe what
> `Player.jsx` does on top of movi-player. Where they still mention hls.js, transcoder runs or
> session seeks, that is the old engine: there is one direct file now, and every seek is a
> `currentTime` write movi-player serves by range requests. Those mentions are to be pruned as
> the code they describe is touched.

## Remembered settings — `player-prefs.js`

Volume, mute and playback speed are kept in `localStorage` (`wt-player-prefs`), per browser —
no server, no account. Storage is reached through `safeStorage()` only: in a sandboxed or
third-party iframe reading `window.localStorage` throws, and a player that cannot remember must
still play. Stored values are validated field by field on the way back in.

Restored in `usePlayerState` before the listeners go on. `muted` is restored only towards
silence, so a stored "unmuted" never overrides a stream the page started muted.

## The grace popup holds the film — `grace-hold.js`

The free grace window's popup (`#grace-cta`, docs/grace_token.md) stops the film when it comes up
and the answer resumes it -- only if the popup was what stopped it (owner, 2026-09-26). The hold
pauses a playing element. "Continue" and the close resume held playback; a film the viewer had
paused stays paused. Since movi-player owns the Play control, the page sees a Play only as the
element's `play` event, and any `play` while the popup holds -- the viewer's Play, a key, the
embed's `player_play` -- is the answer "continue" and plays (`via: play` in
`grace-soft-cta-click`). The trial link resumes nothing. Next, a teardown or the next file drop
the hold without resuming. While it holds playback the element carries `data-grace-cta-hold`, and
the resource page's transfer status reads the player as playing, not as a viewer's pause
(`lib/playerActivity.js`).

## Loading spinner — `stall-watch.js`

(Since 2026-09-26 the spinner is the buffering label, below; what shows and hides it is this.)
The spinner follows the **clock and the frames**, not the media events. After a seek past the
buffer `waiting` may never fire, and `seeked` / `canplay` fire when the first fragment is in —
well before the picture moves again (first seconds of a film, buffer shorter than the 10 s of a
double tap). `usePlayerState` samples the element every animation frame:

- **Into a stall:** not paused, not ended, no session seek in flight, `currentTime` unchanged for
  `STALL_MS` (300 ms).
- **Out of it:** evidence of playback, not a changed number — hls.js nudges `currentTime` forward
  while it sits in a buffer hole, and the first version took a nudge for "moving". With a frame
  counter (`getVideoPlaybackQuality`, only when `videoWidth > 0`): `RECOVER_FRAMES` (3) frames
  presented since the stall began — one is not enough, a finished seek paints its target frame
  and stands. Without one (audio, old browsers): the clock advancing on every sample for
  `RECOVER_MS` (250 ms).
- While the watchdog says "stalled", `canplay` / `playing` do not take the spinner down.

## Buffering label — `BufferingLabel.jsx`, `buffering-label.js`

Owner, 2026-09-26 (the canvas "Плеер: буферизация вместо спиннера", variant A). Where the spinner
was, and exactly when it was shown (a stall of the playing film, a start, a session seek, the
translation hold, the next file loading, a moved-to player before its first frame), the player
shows a compact pill: a small spinner and `player.buffering` ("Buffering"; 32 px, 36 px on touch,
40 px in fullscreen with a mouse). The plain pill takes no clicks: they go through to the picture,
as they went through the spinner. It is said by one live region of the player's
(`data-buffering-live`, `sr-only`, `role="status"`), there from the mount and empty while nothing
waits: "Buffering", or at the cap the lock's reason (`player.bufferingCap`). The pill's own
`role="status"`, mounted together with its words, was not read out by most screen readers, and the
lock, a button, said nothing (2026-10-03).

**At the plan's cap the pill is the lock**: a button "Buffering | [lock] 5 Mbps ›" with the viewer's
own cap (a paying viewer's own, "20 Mbps"), 44 px on touch. **Wherever the pill is shown** — the film
stalling, a seek (a session seek's new run or one inside the run), the next file starting, the hold
after a seek — every one of those waits comes through the limiter that holds the viewer (owner,
2026-09-26: "after a seek at the cap the pill says just Buffering, but the seek wait is limited by the
plan too"). It is plain inside the free grace window by the player's own movie time, while the grace
popup is up (or comes up this very render — a session seek past the window puts it up as the seek
starts, and the seek's wait is on screen behind it), and whenever the status has no word on the cap —
**unless the viewer has answered the grace popup** (below): from the answer on it is the lock at once,
until the status has a word of its own.

**Colours** (owner, 2026-09-26: on a white frame the lock's hover turned the pill into a light-pink
blob, the white "Buffering" gone — 1.02:1). The pill is dark in every state, `rgba(10,14,26,.9)`;
hovered or open only a white layer of 0.08 over the same base and a brighter edge, as the player's
other buttons. No pink fill anywhere on it: the pink is the cap's number, lock and chevron alone. On a
pure white frame the label reads 12.2:1 and the cap 6.1:1, hovered 9.6:1 and 4.8:1 —
`buffering-label.test.js` computes these from `player.css` and fails below 4.5:1. The keyboard's
ring is pink on a dark halo, seen on any frame.

**The card** is the transfer status's stream plan box — `.tx-pbox`, the status's own box (bolt, "the
video loads slower than it plays", the stream job's `data-status-stall-sub` line, the button
`offer.watchUncapped` and the trial note) with a close × — in a native `<dialog class="modal">`
opened with `showModal()`, like `#subtitles`, `#embed` and the grace popup. The top layer: centred on
the screen over DaisyUI's dimmed backdrop, never cut by the player's rounded `overflow:hidden` frame
(it was, inside the player — the owner's stage screenshot, 2026-09-26), and over a fullscreen player
(the top layer is above the fullscreen element). The dialog is the box's size container: side by side
from 670 px of screen, the text over a full-width 44 px button below that. **The card is the page's,
not the player's**: a Preact root of its own on `<body>` (`BufferingLabel.jsx` `openCapCard`), its
state the document's (open is "`.wt-cap-card-host` is on the page", a change is a `player_cap_card`
event on `window`). Rendered inside the player it went with it: moving to the next file destroys the
player it was opened from (`destroyPlayer({ keepStage: true })`) and a card opened while the next file
loaded — or at a stall near the end, before autoplay moved on under it — vanished with nobody closing
it (review, 2026-09-27). A top-layer dialog is on top wherever it sits in the document, over a
fullscreen stage as the grace popup is. The player on screen, whichever one it is by then, reads it
for its lock's open state (on its first frame and on subscribing). It stops its clicks,
double-clicks and keys from reaching the player (the shortcuts are on the document, where its keys
would bubble). Focus starts on the box itself (`tabindex="-1"`, no ring on a button the viewer did
not reach by keyboard); Tab reaches the × and the button. The backdrop's close is the mouse's way
out only (`tabindex="-1"`, `aria-hidden`): an invisible screen-sized button, it was a Tab stop read
out as "close" in English in every locale.

**Only the viewer closes it**: its ×, Esc (`cancel`), a click beside it (the backdrop form, as
`#subtitles` has), its button (the trial opens in a new tab), or the lock again where the card is
not modal. It stays open, saying what it said when opened, when the film plays again, the status
takes its word back (owner, 2026-09-26) or the next file's player replaces the one it was opened from
— the card is the label as it was, not the live one. Leaving the page (the view's `destroyPlayer()`,
without `keepStage`) takes it. A close the browser makes itself (Chrome's close watcher does not let
every Esc be cancelled) closes it for the player too. Without `showModal` (jsdom; a browser without
`<dialog>`) it is shown by DaisyUI's `.modal-open` class, and closes the same ways; in fullscreen it
leaves fullscreen first — a plain element on the page, outside the fullscreen stage, would be hidden
by it — as the grace popup's fallback does.

**Whether the viewer is held at the cap is the transfer status's word, not the player's.** The status
on the same page (`app/resource/status.js`) asks `lib/transferStatus.js` `playerLabel` on every draw:
the label stands whenever the view says the viewer is at the plan's cap (`view.plan`, the pink fact)
**and** the card's data came with it — the stream box (the server sends the variants only once the
cap has held for the box, `statusview.PlanBoxAfter`, and only with something faster on sale) and the
player's own link (`plan.player`, below). Not the player's own stall verdict: a seek's wait is not a
stall to `playerActivity`, and the lock was missing there. Taken back inside the free grace window
and while another offer is on screen or on its way (the grace popup: one offer at a time).
`present()`'s rules for the block's own box and line (the player's verdict, the file against the
cap, an answered grace popup) stay the block's. It publishes the label on `window` with an event
(`lib/playerLabel.js`: two bundles, two module copies — CLAUDE.md, shared JS state), only when it
changes, from the view the sticky bar keeps through a one-second gap in the data, and takes it back on
teardown. That view is the server's verdict (with the viewer at their last reading while the player
streams, `playing`), never the block's drawing of the page's phase (`forPhase`): a session seek's
POST pauses the element and reads as preparation there, and the resting view drawn for it has neither
the plan nor the cause — the lock went for the seek and the cause was lost (2026-10-02 to 10-03). No status on the page (an embed): no label, the plain pill always. A swarm or network stall
has no plan, and a cap before its box is due has no card: the plain pill. With the label goes **the
cause** the view names when a wait is *not* the cap's (`playerCause`, `''` for none): `swarm` (a few
seeders slower than the cap — statusview refuses to sell there, "selling one there quotes a wait it
cannot keep"), `noseed`, `missing` / `missing_idle` / `vault_missing` (pieces nobody connected has), `stalled` (a request of
the viewer's open 5 s without a byte) and `checking`. `stalled` is never the limiter: thp throttles
only the viewer's own responses (`External`, torrent-http-proxy `web.go`), and a bucket lets the cap
through; content-transcoder reads its source through thp's internal URL, uncapped, so a session
seek's playlist held while FFmpeg makes the first segment (`WaitForPlaylist`) is the transcoder's or
the swarm's wait — once the segment is there its bytes come at the cap, and the lock comes back. Not
a cause: the cap itself (box or not), the viewer's bytes flowing with no verdict on what binds them
(`active`, `cached_flow` — right after the grace answer, exactly the gap before thp's verdict), no
viewer on the chain, a gap. A null label alone cannot tell "no verdict yet" from "not the
cap"; the cause says the second out loud, for the answer's lock below.

The player adds only what it knows at this very moment (`capLock`, the label can be a second old):
by its own clock (a session seek's target included) the film is past its free grace window, and its
grace popup is not up or coming up.

**After the grace answer the player draws the lock itself** (owner, 2026-09-27: "after a seek the
20-minute popup appeared; after I answered that I want slow, a plain Buffering hung with no lock").
The viewer's answer — "continue at 5 Mbps", the close, or Play while the popup is up
(`data-grace-cta-answered` on the element) — says the rest of the film is at the cap, but the status's
word on it comes 8–15 s later (thp's verdict after ~3 events, the box 8 s after that), and the popup
kept the lock down while it was up. So once answered, **every wait the pill shows** (a stall, a seek,
the hold after one) is the lock at once, with the card the stream job rendered on the `<video>`
(`buffering-label.js` `answerLabel`, below). The status's label, the moment it is there, is the lock
instead (one source of truth).

**The answer covers only the gap before the status's word, never a word that says no** (review,
2026-09-27: a swarm-bound torrent kept the answer's lock and its trial pitch for the rest of the film,
while the block under the player blamed the swarm). Two words end it:

- the status names another cause for the wait (`playerCause` above: the swarm, no seeders, pieces
  nobody has, nothing flowing) — the plain pill, for as long as it names it; with the cause gone and
  no label yet (the bytes flow again, no verdict) the answer's lock stands in again;
- the status's own label has been up on this stretch of the film past the window (`statusSpoke`):
  from then on the lock is the status's alone, and its label taken back means the plain pill, not the
  answer's lock again. It takes it back only on a word: a stall or another cause at once, 10 s of the
  viewer under the cap (`statusview.PlanBoxHold`: the cap no longer binds), or no word at all any more
  (a gap in the data past 8 s, the status torn down). A seek back
  inside the window starts a new stretch: the status takes its label back there for the window, not
  for the cap, and past the window again the 8–15 s before its verdict are the answer's again.

Not covered: a swarm slower than the cap with more than three seeders (`fewSeeders`) — statusview
names no cause there (`active`: the viewer's bytes flow, not at the cap), so until the status's label
has come on that stretch the answer's lock stands for its waits. The status block says nothing there
either; a cause for it would be statusview's to add, not the player's to guess.

Plain all the same:

- back inside the grace window by movie time (a seek back before 20:00 plays at the grace rate again);
- the stream job says the file fits under the cap with room to spare (`data-status-fits-cap`) — a wait
  there is not the cap's as far as anyone knows before the server says so (the status's own word
  still makes it the lock);
- nothing faster on sale (no catalog, a promo plan no faster than the cap): the job renders no card;
- the next file loading (that wait is the next file's start, inside its own grace window), and the
  next file itself — a new element, without the answer — until the status says so;
- an embed: the job renders no card there (the lock has never been drawn in an embed; a new upsell on
  someone else's site is not this change's to add).

The status block's own rules for its box after the answer (`present()`: no box until the player's
first real stall) do not change — they are the block's, not the label's.

**The card for the answer** is rendered once into the page by the stream job, on the player's
`<video>` (`templates/views/action/stream_video.html`, `jobs/scripts/cap_card.go` `CapCard`, only
where there is a grace window, outside an embed, with a cap in the claims and something faster on
sale): `data-cap-card-rate` (the cap as the lock says it, `statusview.RateLabel` — the status's own
function), `-title` (`resource.status.streamStallTitle`), `-cta` (`offer.watchUncapped`), `-url` (the
promo plan through `trialURL … "player-label"`, else its checkout, else `/donate` — as the status's
`plan.player.url`), `-target`, `-note` (`offer.trialNote`, with a trial only), `-sub` (the cap alone,
`statusview.CapLine`) and the props `-auth`/`-tier`. Its line is the element's `data-status-stall-sub`
when the job knows what the file needs — the very line the status's label takes — and `-sub`
otherwise. Everything goes through `html/template`'s attribute escaping; the player takes the link
only as a path of ours or an https URL. `services/template/cap_card_render_test.go` renders it for
every language and offer state and compares it with the status's own stream box for the same viewer
(`statusview.Build`); it also writes the `__fixtures__/cap-card-video.html` the wiring tests run on.

**One source of truth.** The card has no copy of its own: its title, line, button label and note are
the stream box's, and its link is the server's `plan.player.url` — the stream box's destination
through the player's own `/trial` surface, `offer.FromPlayerLabel` (`/trial?from=player-label`; a
checkout or `/donate` link is the box's own), built in `services/statusview` next to the box's
`status-bar` link, so the two count apart in `webui_trial_shortlink_total{from}`. `plan.player.rate`
is the lock's number (`speed()`, a no-break space before the unit). Events: `player-label-lock-shown`
(the lock drawn, once per player and set of props), `donate-player-label-shown` (the card opened: on
screen because the viewer asked), `donate-player-label` (its button, Umami's own click), with the
status box's props, `location` `player` and `state` `stream_stall` (the stream box at a wait, whatever
the block shows at the time), and `source` — who raised the lock: `status` (the status's label) or
`grace-answer` (the stream job's card after the answer). One lock seen is one lock seen: the answer's
lock the status then takes over is counted once, with the source that raised it first (`lockKey`: the
props but for `source`); the card's opening and its button carry the source of the card opened.

## Media Session — `media-session.js`

Lock screen, notification shade, headset and media keys. Title from `data-resource-title`,
artwork from the element's `poster` (already blurred server-side where it must be). Two things
are ours to get right: **position state is reported in film time** (a transcoder run starts at
`seekOffset`; the element's own clock describes the run), and **seeks go through `handleSeek`**
(a session seek is a POST, not a `currentTime` write). `navigator.mediaSession` and
`MediaMetadata` are injected — testable, and a browser without them is a no-op.

## Usage events — `player-telemetry.js`

One Umami event per **decision**, not per press (`settled()` holds the last value until the
presses stop and is flushed on teardown):

| event | data | when |
|---|---|---|
| `player-speed` | `rate`, `source: menu\|key` | the rate actually changed |
| `player-tap-seek` | `dir: forward\|back`, `seconds` | a streak of double taps ended (900 ms) |
| `subtitle-delay` | `delay`, `source: dialog\|key` | the delay settled (2 s) |
| `player-media-session` | `action: play\|pause\|seek` | first use of each action per player |
| `player-label-lock-shown` | the status box's props, `location: player`, `source: status\|grace-answer` | the buffering label's lock drawn, once per player (see "Buffering label") |
| `donate-player-label-shown` | the same | the lock opened the plan card |
| `stream-start` | + `rate`, `subtitleDelay` | what the stream started with (remembered settings make no change event). The route/declaration fields of the HLS player are gone with it |
| `player-revived` | `path`, `route`, `audio`, `waited_s` (since the play request), `recoveries` | it played after a `player-dead`: the rule's error, count it against `player-dead`. `audio` on both is the start's audio class, as in `stream-start` (since 2026-09-30) |
| `player-recover-card-shown` | `reason: limit\|network\|media`, `status`, `cause` (404/403, for `limit`), `via` (for `limit`) | the card instead: the automatic restart spent within 5 min, another 4xx, or the backoff run out; sent when it shows (it waits while the film plays from its buffer) |
| `player-recover-card-click` | `reason`, `status` | its "Continue watching" |

Read them as shares of `stream-start` sessions; mobile share for `player-tap-seek`. The browser is the
Umami session's (`session.browser`, `session.os`: `edge-chromium`, `safari`, `ios`, `crios`, …); no
event carries its own. The queries for the audio are under "Watching the audio after the rollout".

## Next episode / next track — `next-item.js`, `next-item-go.js`

Design, numbers and the owner's decisions: `docs/superpowers/specs/2026-09-20-next-episode-design.md`.
86% of viewers who finish an episode open the next one by hand; the feature is about the minute that
transition costs.

**Server.** The stream job resolves what follows the file it renders (`jobs/scripts/next_item.go`
over the pure `services/next_item`): the next `(season, episode)` of the series among this torrent's
files — specials never bridge to regular seasons, a two-episode file is followed by what comes after
its last episode, an episode without a file is skipped — or, for audio, the next audio file of the
directory in natural order. A video that is not an episode has no next. It lands on the player
element as `data-next-item-id|path|kind|label`; **absent = the feature is off** for this stream
(a film, the last file, an embed, a failed lookup — and the kill switch). Bounded at 4 s.

**Track choices travel as intent** (`models.TrackCarry`, `handlers/action/carry.go`). Track ids mean
nothing in another file, so `readCarry()` reads the picker's current chips into `carry-*` form
fields — audio language + label; subtitles off, or language + origin — and the picker resolves them
against the new file's lists to an item id, which goes down the saved-choice path (the one place that
knows about locked items and the "None" switch). A carry outranks the file's own saved choice and
the ladder; same language from another origin beats the ladder; a carry the file cannot honour
changes nothing. It is part of the job cache key. The subtitle delay does not travel: it belongs to
one subtitle file. **Subtitles travel only when they are the viewer's choice** — `data-saved` on the
playing chip, rendered by the server for a saved choice and moved by `markTrack` on every persisted
pick. What the ladder or the audio-switch rule turned on is decided again by the next file's own
ladder (review 2026-09-28: carried, the ladder's English track beside a translation offer arrived as a
saved choice and beat the next episode's own track in the viewer's language). Audio still carries
what plays.

**Client.**
- Button right after Play (`NextIcon`: a triangle with a bar on its right), key `n` / `Shift+N`.
- `advancePlan()`: prewarm at 90% but never more than 5 min early (a prepared render and its
  transcoder session live ~10 min), only while playing in a visible tab; the "up next" card in the
  last 10 s (earlier when the credits are known, see below), video only, always a 10 → 0 countdown.
- `atEnd()`: `go` / `offer` (autoplay off) / `ask` ("still watching?" after 3 automatic moves with no
  pointer or key event — a sleeper must not warm up and transcode a season) / `stay` (cancelled).
- Autoplay is a remembered setting (`player-prefs` `autoplayNext`, default on) behind a switch
  (the design system's `toggle toggle-soft`, the same as the subtitles switch — a home-made solid-pink
  one was tried and looked like neither). It lives in the **"more" menu** (three dots, always the last control on the right; `SettingsControl.jsx`), with
  its name next to it and reachable at any time, for video and audio alike, and on the card as well.
  A bare switch in the audio bar was tried first: it said nothing about what it switched; then a
  gear, which read as bold beside the outline icons and promised more than a menu of one switch. The
  button is as narrow as its glyph (a square one left the dots stranded in empty space) and shows
  only where there is a next file. Its menu and the speed menu share `useAnchoredPopover`
  (top-layer popover placed from the button's rect, outside press / Escape / scroll close it) and
  the `.wt-player-menu` styles.
- **Music has no card at all** (owner): tracks follow one another like an album, or do not, by the
  switch. No "still listening?" either — `atEnd()` for `kind: track` is `go` or `stay`.
- The move itself (`createNextItemGo`): the next file's render is fetched off the page
  (`background-render.js`, with a silent Turnstile token for anonymous viewers), the old player is
  destroyed **keeping its stage**, the new one is mounted into the same stage, the address and title
  are updated, and the page around (`#content`: file card, list) is synced — at once when windowed,
  on leaving fullscreen otherwise. `syncPage` fetches the fresh `#content` aside, moves the live
  player into its new log container, then swaps. A carried AI translation is kicked with one HEAD
  to the track so its first lines are ready. What cannot be done quietly (an error card, a cap
  modal, a Turnstile checkbox) falls back to opening the next file the visible way.
- The request belongs to its player: unmount calls `dispose()`, aborting POST/SSE and discarding
  prepared results. A late token, progress callback or render cannot start a request, mount a
  player or navigate away. Before the swap, both the host and the captured stage must still be
  current. The swap itself disposes the old component; after it, the preserved stage identifies
  the transition allowed to finish saving choices and updating history. Losing the stage while
  `initPlayer` awaits translations also cancels initialization.
- A prepared render is valid for its **carry choices and confirmed preferred language**, as well
  as its eight-minute lifetime. `prepare()` reuses only a matching result/request and aborts a
  superseded request. Next checks again after waiting: a subtitle/audio choice made during a cold
  start gets a new render. An obsolete result cannot start its AI translation. This preserves the
  fast path when choices have not changed.
- A next file the viewer had already started asks "continue from … / start over" like any other
  file. The first version answered it silently (the settings-restart note, `markAutoResume`); that
  read as the saved position being ignored, and the question is the viewer's to answer.
  `resumeAt()` remains for the settings restart: a file ≥90% finished restarts from the top.
- While the next file loads (not prewarmed: a stream start like any other, up to a minute) the
  player says so: the buffering label, a spinner in the Next button, the card's kicker reads "Loading
  the next one…". Between the two players the stage keeps its height
  (`.wt-player-stage--switching`) — an empty block has none, and the page jumped.
- A player mounted by a move is **loading, not paused** (`awaitStart`): until its first frame it
  shows the buffering label, not the big Play button — both at once was two answers. Ends with `playing`,
  with the resume prompt (a question only the viewer can answer), or after 10 s (autoplay refused:
  Play is what they need). The empty stage shows the player's own spinner (`--empty`, the same SVG
  as `LoadingSpinner`), removed as soon as a player is in it.
- The file being left is **paused** the moment the move starts (button, key, card, the countdown): it
  used to play on under the spinner for as long as the next one took to start, and its saved position
  moved with it.
- **The wait is narrated.** `fetchStreamRender({ onProgress })` reports the job's log as it
  happens — the running step, and its status under it ("warming up torrent client, downloading
  10 MB — 37%") — and the card shows the latest line while the viewer waits. Kept from the silent
  prewarm too, so pressing Next midway shows where it is.
- **A cold start gets minutes** (`NEXT_RENDER_TIMEOUT_MS`, 10 min), not the 30 s a settings restart
  allows: with 30 s every slow start timed out into the visible fallback — a full page load — and
  the viewer waited half a minute and then watched the page restart. The job's own deadlines decide
  when a start has failed. The fallback remains for what cannot be quiet: an error card, a cap
  modal, a Turnstile checkbox.
- **One sync, the latest.** Several moves can happen in one fullscreen sitting; each used to leave
  its own "sync when fullscreen ends" behind, and on exit they raced — the slowest won, which could
  put episode 2's card and start form under episode 3's picture. `scheduleSync` keeps one pending
  URL and one listener, and a generation lets a newer `syncPage` overtake an older one in flight.
- **The live subtree is the whole action view** (`liveRoot`: the direct child of the log container),
  not `stage.parentNode`. The stream template wraps the video in a `<div class="relative">`, so the
  parent was an inner element and the view's marker (`data-async-view`, whose destroy handler is
  `destroyPlayer()`) sat on its ancestor: the sync told it that it was going, and the player that had
  just mounted was destroyed — "the new episode appears, then only the file list". For the same
  reason `mountOnStage` adopts the *content* of the render's wrapper rather than the wrapper, which
  nested one level deeper on every move.
- **`syncPage` runs the view lifecycle** around the live player (`destroyViews` / `activateViews`,
  split out of `lib/loadAsyncView.js` with a `skip` subtree): without it the new file list came back
  with its scripts never run (`resource/select.js`: no multi-select, no archive).
- **The carried choice is saved** once the new player is up (`persistDefaults` → the same PUT a chip
  click makes): a default is not a saved choice, and the next plain start of that file — a settings
  restart, a reload — would have asked the ladder again and could flip what the viewer carried over.
  Subtitle persistence requires `data-saved="true"`, including explicit Off and resolved carries.
  Automatically selected English and automatic None remain automatic, so another render still
  evaluates the ladder and its AI offer. Audio continues to persist what plays.
  Existing saved track IDs are preserved: an automatic default persisted by an older player
  cannot be distinguished from a genuine manual choice.
- A failed mount falls back to the visible way in: the old player is already gone by then, and a
  half-built page is the one outcome worse than a reload. A page without the start form or
  `#content` has no "next" at all (`canMoveOn`).
- **The prewarm does not ask whether the tab is visible**, and follows the element's `timeupdate`
  rather than `state.currentTime` (fed by `requestAnimationFrame`, which a background tab does not
  run at all). Music lives in a background tab: the first night in production 11 of 14 automatic
  moves between tracks came unprepared, ~8 s of silence between songs. And a film left to play out
  in a background tab moves on at `ended` all the same — refusing to prepare the next file does not
  save its start, it only moves it to the moment it hurts. The card stays on the state-driven
  effect: it is something to look at.
- **Music prewarms from the middle** (`PREWARM_AT_TRACK` 0.5): 10% of a three-minute song is
  eighteen seconds, less than a cold start, and the album would stutter between tracks.
- **The card is a top-layer popover** docked to the player's bottom-right corner, above the control
  bar (`useDockedPopover`): inside the frame (`overflow: hidden`) a phone-width player cut its top
  off. It is re-placed on scroll / resize / a change of its height, and re-shown on a fullscreen
  change (the top layer is ordered by arrival).
- Look: the player's own vocabulary (the glass buttons of the resume prompt), not the site's —
  a pink button here means a homepage CTA. `NextIcon` is Play's exact triangle plus a bar, in a
  30×24 box; the button is wider by what the bar adds, so the two triangles match.

**Credits from the container's chapters — `jobs/scripts/credits.go`.** The file's own answer, and the
first one asked: `content-prober` runs ffprobe with `-show_chapters` (since 2026-09-21), the stream
job reads the earliest chapter whose title names the closing ("End Credits", "Ending", "ED", "Outro",
«Титры», …) inside the 25 s … 10 min window before the end, and puts it on the player as
`data-credits-at`. Earliest, because "Ending" is followed by "Preview" and "End Credits" by
"Post-credits scene": the decision point is where the first begins, and there is a countdown and a
Cancel from there. Generic names ("Chapter 12") say where, not what, and match nothing. Known from
the first second, needs no subtitles (the subtitle guess found something in 11% of lookups the first
night: 78% of streams had no whole-file track). Probes cached before the change have no chapters;
they refresh within a week. RE2's `\b` is ASCII-only — the Cyrillic alternatives go without it.

**Credits from subtitle timings — `credits.js`** (when the chapters say nothing)**.** Dialogue ends, credits begin: the end of the last
cue + 3 s. It only moves things *earlier* — the card from "the last 10 s" to "when the talking
stops", and the prewarm a minute ahead of that (never earlier than a prepared render lives, 8 min).
With autoplay on, the card counts down `COUNTDOWN_S` (10 s) from the start of the credits and then
moves on, with Cancel on it (owner's decision, 2026-09-20; the first version only showed the card
early and moved on `ended`). `countdown()` runs in **film time** — a pause pauses it, a seek back
withdraws it, there is no timer to cancel. Without known credits the card comes up ten seconds
before the end, so the number is the same 10 → 0 (the first version came up 25 s out and printed
"next in 20 s"). The ten seconds start when the **card** does (`shownAt`), not at the credits: a viewer
who seeks into the credits arrived after "credits + 10 s" and was told "next in 0 s". The price of
a wrong guess is ten seconds to press Cancel; the guards below and the
discarding of late guesses (a post-credits scene) are what keep that rare. Timings do not depend on language, so any whole-file track
does: cues of an already loaded `<track>` first (authored times, `__absStart`), otherwise **one**
request for a whole-file chip's VTT (never a translation — that starts a paid job; never a muxed
track — hls.js feeds those segment by segment). Looked for once per file, past 60%. Guards:
≥20 cues; a trailing run of ≤2 cues / ≤15 s after ≥60 s of silence is a translator's signature and
is dropped (a real post-credits scene is more lines and survives, which lands the guess at the end
and discards it); a result later than 25 s before the end or earlier than 10 min before it is no
result. Event `next-item-credits {source: loaded|fetched|none, found, lead_s}`. Container chapters
("End Credits") would be more exact; `content-prober` does not ask ffprobe for them yet.

Between two players the new container takes the old picture's `aspect-ratio`
(`initPlayer({ aspectRatio })`): until `canplay` reports the real one it had a default height, and
the controls, pinned to its bottom, jumped inside the held stage.

**The stage.** Fullscreen is requested on `.wt-player-stage`, a wrapper that outlives the player it
holds (`initPlayer(target, { stage })`, `destroyPlayer({ keepStage })`): a fullscreen element stays
fullscreen while it stays in the document, whatever happens to its children. On the player's own
container, as before, every transition would have ended fullscreen — and a browser does not re-enter
it without a gesture. iOS native video fullscreen cannot be kept; accepted.

Events: `next-item-shown`, `next-item-prepared {ok}`, `next-item-go {how: auto|button|key|card,
prewarmed, fallback, fullscreen, wait_ms}`, `next-item-cancel`, `next-item-autoplay {on}`,
`next-item-still-watching`. `wait_ms` is the number the feature exists for; the baseline is ~58 s.

## "Watched" and the credits — `models.IsWatched`

One rule, two ways to satisfy it, whichever comes first: 90% of the duration (as always), or the start
of the credits when the player could tell where that is (`credits.js`; sent as `credits_at` with
every `PUT /watch/position`, `useWatchHistory`). An episode with ten minutes of credits ends, for the
viewer, at 80% — by the 90% rule alone it stayed unfinished forever: in Continue watching, in the
series' progress, and as the file offered for resume. It is the same moment the player offers the
next episode. `credits_at` is client input, so it is believed only inside the window credits can
occupy (25 s … 10 min before the end, the bounds `credits.js` uses); outside it the 90% rule stands
alone. The lookup therefore runs for signed-in viewers of any video, not only when there is a next
file. `watched` is still recomputed on every update (rewinding un-watches, as before).

## Saved position — `useWatchHistory`

A position under `MIN_SAVED_POSITION` (30 s) is neither saved nor offered: it is a viewer who looked
in and left, and "Continue from 0:12?" is a question about nothing. Exactly 0 still goes through —
that is "Start over" resetting a real position. A file the server calls `watched` (90%, or past the
credits — `models.IsWatched`) is not offered for resume either.

Direct seeks (including MP4 and nginx-vod without a transcoder session) update player state with
the clamped target immediately, even on pause: the animation-frame clock deliberately ignores
paused media. The viewer's seek also releases a restart's visual position hold; a resume seek
retains that hold through attachment by passing `{ restore: true }`.
