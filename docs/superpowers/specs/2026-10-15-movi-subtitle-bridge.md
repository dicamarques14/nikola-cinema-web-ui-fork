# Subtitle and audio bridge for movi-player

Phase 4 of the movi-player migration (Phase 2: the server serves the raw
file; Phase 3: `Player.jsx` mounts `<movi-player>`). This spec is the design;
the port follows it task by task.

## Problem

Webtor's picker (`#subtitles` dialog, `track-dialog.js`, `track-picker.js`)
is the one place a viewer chooses subtitles and audio. Until now it drove two
renderers: hls.js for embedded tracks, `<track>` elements for side-loaded ones
(`subtitle-apply.js`). Both are gone. movi-player renders everything itself,
through an API that does not look like `<track>`:

| What | movi-player (0.4.1) |
| --- | --- |
| External track | `player.addSubtitleTrack({url, lang, label, format})`. `lang` is the **key**: an entry with an existing `lang` replaces it. Fetched on selection, not on add. |
| Select external | `await player.selectSubtitleLang(key \| null)`. Fetches, parses (VTT/SRT/TTML), renders. `null` = off. |
| Generated track | `player.appendSubtitleCues(key, label, cues, pending?)`. Cues `{start, end, text}`, any order, de-duplicated (same text within 0.1 s), sorted; the showing track redraws on the next tick. `removeSubtitleCues(key)` takes it out. |
| Embedded | `player.getSubtitleTracks()` / `player.selectSubtitleTrack(trackId \| null)`. `trackId` is the libav stream index (`Demuxer.ts:382`) = ffprobe's `index`. Text and image (PGS, DVB). |
| Audio | `player.getAudioTracks()` / `player.selectAudioTrack(trackId)`. Same id. |
| What is on | `player.getSubtitleLangs()` -> `{lang, label, active, pending}[]` (external and generated). |
| Delay | One global value: element `subtitleDelay` (seconds, VLC sign), event `subtitledelaychange`. Not persisted by movi-player. |
| `textTracks` | Mirrors with one synthetic cue. **Not usable** for reading cues or modes. |

`player` is `element.player`, `null` until the source is initialized. All
selection waits for `loadedmetadata` (embedded tracks are known only then).

## Decisions

1. **The picker stays the one source of truth.** movi-player's own captions and
   audio menus are switched off (`controlslist` += `nocc noaudio`); the
   "Subtitles & audio" control (`wt-tracks`, Player.jsx) moves from the menu to
   the bar, `before: 'settings'`, so it sits where CC sat. `nocc` only hides UI
   and keys (`MoviElement.ts:6974, 7031`); the API still renders.
2. **External key = chip id** (`data-id`: `os-…`, `us-…`, `tr-pt`, …), never a
   language. Webtor offers several tracks per language; keying by language
   would make them replace each other. movi-player's `persist` stays off for
   `subtitlelang`/`audiolang` (opt-in, `MoviElement.ts:39268`): webtor's PUT
   (`persistTrackChoice`) is the memory, movi-player must not restore its own.
3. **One external track registered at a time.** On selecting a side-loaded chip
   the bridge `addSubtitleTrack`s it (if not yet) and selects it. Nothing is
   fetched until chosen: same rate-limit reason `ensureTrackElement` documents.
4. **AI translations use the generated path.** The bridge fetches the growing
   VTT itself, parses it, and `appendSubtitleCues(id, label, cues, pending)`.
   No refetch-and-blank: the 15 s swap throttle (`TRACK_RELOAD_INTERVAL_MS`,
   `subtitle-track-reload.js:37`) existed only because a `<track>` src swap
   blanked the track. The poll's revision bump calls the bridge instead.
5. **Delay is per file, through movi-player's global delay.** Webtor's delay
   was already per file (`delayKey = resourceID:path`, `player-prefs.js`).
   Mount: `el.subtitleDelay = loadSubtitleDelay(delayKey)`. `subtitledelaychange`
   -> `saveSubtitleDelay`. The dialog row drives `el.subtitleDelay`. It now
   also applies to embedded tracks; the row's embedded-disable goes.
   `cue-offset.js` (VTTCue mutation, session offsets) is no longer used.
6. **Embedded chips are addressed by stream index.** The server adds
   `data-stream="<ffprobe index>"` to every MediaProbe chip (audio and
   subtitle). `data-mp-id`/`mp-N` ids stay as they are (saved choices and
   carry keep working); the bridge reads `data-stream` only.
7. **Bitmap subtitles become visible.** movi-player renders PGS and DVB.
   `embeddedSubtitleVisible` (`handlers/action/helper.go:450`) shows
   `hdmv_pgs_subtitle` and `dvb_subtitle` (badge as embedded). They are never
   a translation source (no Src), which `pickTranslationSource` already
   guarantees.
8. **Embedded tracks are no longer a translation source.** No transcoder
   playlist to read them from (Phase 2). The ladder already skips Src-less
   embedded tracks; nothing to do but update docs.

## The bridge: `assets/src/js/lib/player/movi-tracks.js` (new)

Pure module, no Preact. Takes the element, never a global.

```js
// createTrackBridge(el, { fetch = window.fetch } = {}) -> bridge
bridge.ready            // Promise: resolves at the element's loadedmetadata
bridge.showSubtitle(sel)   // sel: { id, provider, src, label, stream } | null (off)
bridge.showAudio(stream)   // ffprobe index
bridge.reloadTranslation(id, src)  // fetch src, parse, appendSubtitleCues; returns Promise<{cues, lastEnd}>
bridge.dropMissing(keepIds)        // removes registered external/generated tracks whose id is gone;
                                   // returns true if the one showing was dropped
bridge.activeId()       // the chip id on screen: external/generated key, 'mp-…' never
                        // (embedded answers by stream: activeStream()), '' for off
bridge.activeStream()   // embedded stream index on screen, or null
bridge.cuesOf(id)       // cues the bridge itself holds (generated tracks), [] otherwise
bridge.dispose()
```

Rules inside:

- Every operation awaits `ready`; the last one asked wins (a sequence number,
  like `persistTrackChoice`'s, so an earlier async select cannot land after a
  later one).
- `showSubtitle`: provider `MediaProbe` -> `selectSubtitleLang(null)` then
  `selectSubtitleTrack(Number(stream))`. `Translated` -> generated path
  (`reloadTranslation(id, firstTrackSrc(...))` then `selectSubtitleLang(id)`).
  Anything else -> `addSubtitleTrack({url: src, lang: id, label, format})`
  then `selectSubtitleLang(id)`. `null` or id `none` -> both off.
- `format`: `'vtt'` when the URL path contains `~vtt` or ends `.vtt`; `'srt'`
  for `.srt` without `~vtt`; else omitted. movi-player guesses SRT from any
  `.srt` in the URL (`MoviPlayer.ts`, `selectSubtitleLang`), which is wrong for
  thp's `file.srt~vtt`.
- VTT parsing for the generated path: a small `parseVttCues(text)` here
  (timestamps `hh:mm:ss.mmm` / `mm:ss.mmm`, multi-line text, cue settings
  ignored, `NOTE`/`STYLE` blocks skipped). `credits.js parseVttTimings` stays
  as it is.
- `selectSubtitleLang` returning `false` (fetch failed) is reported through an
  `onError(id)` callback the caller passes; the dialog uses it as
  `onTrackError` is used today.

## Changes by file

### Server (Go)

- `handlers/action/helper.go`: `ListItem` gains `Stream int` (ffprobe index;
  -1 when not MediaProbe). Set in `GetSubtitles` (embedded loop) and
  `GetAudioTracks`. `embeddedSubtitleVisible`: PGS and DVB visible (decision 7).
- `templates/views/action/stream_video.html:122, 255`: `data-stream="{{ .Stream }}"`
  on audio and subtitle chips where Provider is MediaProbe.
- Tests: `helper_test.go` (stream index on embedded chips; PGS visible),
  `services/template/stream_video_render_test.go` (attribute rendered).

### Client

- `movi-tracks.js` (new) + `movi-tracks.test.js` (fake element with
  `player` stub recording calls; key, format, sequencing, drop, off).
- `track-dialog.js`:
  - `activateSubtitle`: replace `ensureTrackElement` + `applySubtitleSelection`
    with `bridgeFor(container).showSubtitle(selectionFrom(target))`.
    `selectionFrom` adds `src` (`firstTrackSrc` for Translated), `label`,
    `stream` to `selectionFor`'s fields.
  - audio click and `swapSubtitlesDialog` audio: `showAudio(data-stream)`
    instead of `window.hlsPlayer.audioTrack`.
  - `syncUploadMarks`: "what is playing" from `bridge.activeId()`, not
    `textTracks`.
  - async delete path and `swapSubtitlesDialog`: `bridge.dropMissing(chipIDs)`
    instead of `dropDeletedTracks(video, …)`.
  - `bridgeFor(container)`: the bridge of the player in that container,
    registered by `Player.jsx` (module-level `Map` is wrong here -- see
    web-ui CLAUDE.md on duplicated modules; hang it on the element:
    `el.__wtTracks`).
  - Selectors `video.player, audio.player` -> `movi-player.player`.
- `Player.jsx`:
  - create the bridge after `upgradeToMovi`, store on the element, dispose in
    `destroyPlayer`.
  - `upgradeToMovi` drops `<track>` children (the bridge owns tracks; movi
    would register them keyed by srclang).
  - after `bridge.ready`: activate the dialog's `.subtitle[data-default="true"]`
    with `persist: false` and the default audio chip (what hls.js's
    `initDefaultTracks` did).
  - `controlsListFor`: add `nocc noaudio` when the picker exists; `wt-tracks`
    goes on the bar.
  - delay: mount from `loadSubtitleDelay`, save on `subtitledelaychange`, the
    dialog row (`#subtitle-delay`, paint + delegated click) drives
    `el.subtitleDelay`. Port `paintSubDelay` from the pre-Phase-3 Player.jsx
    (`git show d554f4a0:assets/src/js/lib/player/Player.jsx`, lines 1471-1518)
    without the embedded disable.
  - bring back `useSubtitleTranslation` and the offer / catch-up pills
    (same file, lines 262-277, 661-732, 1990-2055), with `seekOffset` 0.
- `hooks/useSubtitleTranslation.js`:
  - `reloadSubtitleTrack(video, id, src, …)` -> `bridge.reloadTranslation(id, src)`.
  - "where the cues in the track end" (`:515-527`, `el.track.cues`) ->
    `bridge.cuesOf(id)` last end.
  - drop `applyCueOffset` (`:540`), the hls reassert (`:545-547`),
    `seekOffsetRef` arithmetic (always 0).
  - the swap throttle can drop to the poll interval; keep the constant but
    stop using it for the generated path.
- `credits.js cuesOfLoadedTracks`: take a cue list (`bridge.cuesOf(id)` of the
  showing track) instead of reading `<track>` elements. `Player.jsx` passes it.
- Not touched: `track-picker.js`, `subtitle-rules.js`, `subtitle-offer.js`,
  `subtitle-progress.js`, `subtitle-catchup.js`, `subtitle-telemetry.js`,
  `preferred-lang.js` -- pure DOM/logic.
- Dead after this phase (delete in Phase 5): `subtitle-apply.js` (all of it --
  the bridge replaces both halves), `subtitle-track-reload.js` reload half
  (`dropDeletedTracks` too, replaced), `cue-offset.js`.

## Port order (Sonnet)

1. Server: `Stream` + `data-stream`, PGS/DVB visible; Go tests. (`go test ./handlers/action/ ./services/template/`)
2. `movi-tracks.js` + its tests, standalone.
3. `track-dialog.js` onto the bridge.
4. `Player.jsx`: bridge lifecycle, default activation, `controlslist`, bar control, delay row.
5. `useSubtitleTranslation.js` onto the bridge; bring the pills back in `Player.jsx`.
6. `credits.js` cue source.
7. `npm test` on the touched files; webpack dev build.

Each step builds on the one before and leaves the page working.

## Verification (manual, needs a running instance)

- Embedded text track and a PGS track select and render; "Off" clears both.
- An OpenSubtitles track loads only when picked (one request in the network tab).
- Two English tracks in a row: the second replaces the first, both selectable.
- Upload a `.srt`; it is selected at once; delete it while showing -> Off.
- AI translation: cues appear progressively without blanking.
- Audio switch on a dual-audio MKV.
- Delay row and the saved delay survive a reload of the same file.
- The movi-player CC/audio buttons are gone; "Subtitles & audio" is on the bar.
