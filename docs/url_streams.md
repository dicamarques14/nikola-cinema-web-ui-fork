# Direct-URL addon streams

Some Stremio addons answer `/stream` with plain HTTP(S) files: `url`, no
`infoHash`, often `behaviorHints.notWebReady` and
`behaviorHints.proxyHeaders.request` (Referer, User-Agent, CDN cookies), links
that are signed and expire (hours to days). Webtor plays these through a proxy,
and imports them into a torrent when the viewer asks for it.
Phase 1 covers single files (mp4/mkv); HLS/DASH manifests (`.m3u8`/`.mpd`)
stay greyed out with "Not supported yet".

Off unless `URL_STREAMS_ENABLED` (self-hosted turns it on).

## Stage 1: play through the proxy

- Discover (`lib/discover/stream.js` `streamKind`) calls a stream `url` when it
  has an http(s) link, no infohash (in the field or the link) and is not a
  manifest. `externalUrl` is a web page, never a file, and is ignored.
  `client.js` tags each stream with the addon's row id (`addonId`) and the
  request's `streamType`/`streamId`; Cinemeta and other addons without a row
  are not playable this way.
- `StreamRow` makes the row clickable ("Direct link" chip) when
  `window._urlStreams` is set; the click posts to `POST /url/open`
  (`DiscoverApp.openUrlStream`).
- `POST /url/open` (`handlers/urlstream`, login required) never fetches the
  link the browser sent. It loads the user's addon row, asks the addon for the
  same `/stream/<type>/<id>.json` itself and finds the stream there
  (`urlsource.MatchStream`: same link, else same filename + `videoSize`). The
  signed link and its headers are stored in `url_source` and never rendered,
  exported or logged (logs carry the host only). The answer is `/url/<id>`.
- `GET /url/:id` is a reduced player page (`templates/views/urlstream/play.html`,
  `app/urlstream.js`): movi-player with no track picker (movi's own CC/audio
  menus stand in), no share, no embed, no Vault, no torrent status.
- `GET|HEAD /url/:id/stream` (owner only) is a Range pass-through: the staged
  file once there is one, the origin until then.

### The origin request (`services/urlsource`)

- Every connection goes through the egress dialer (`services/egress`), redirects
  included (≤ 5, http/https only); private addresses are refused unless
  `STREMIO_ADDON_ALLOW_PRIVATE_NETWORK`.
- Sent: the addon's `proxyHeaders.request` minus hop-by-hop/framing headers,
  `Host`, `Range`, `Proxy-*` and anything with CR/LF (`CleanProxyHeaders`), then
  only `Range`, `If-Range`, `Accept` from the incoming request, and
  `Accept-Encoding: identity`. The viewer's cookies and address stay here.
- Returned: only `Content-Type/Length/Range`, `Accept-Ranges`, `Last-Modified`,
  `ETag`. No `Set-Cookie`, no `Location`.
- A 403/410 is an expired link: the addon is asked again for the same
  content id and the stream with the same filename + size is stored
  (`Service.Resolve`, one attempt per source at a time), then the request is
  repeated once.

## Stage 2: import into a torrent (on request)

- Opening a stream starts nothing: `POST /url/open` only records the source
  for the proxy. The player page shows **Import to Webtor** while the source is
  `new` or `failed`; the button posts to `POST /url/:id/import` (login,
  owner only, the session's CSRF check; 409 when the import is off), which
  calls `Service.StartImport` and answers the status. `URL_IMPORT_AUTO=true`
  restores the old behaviour (Open starts it).
- The import runs in-process (at most `URL_IMPORT_CONCURRENCY` at once). It is
  not a `jobs/` job: those are tied to a page's job log, while an import
  outlives the page and is watched through `GET /url/:id/status`. A process
  that dies mid-import leaves its row "importing"; startup turns such rows into
  "failed", and the button is offered again (resuming the `.part` file with a
  Range when the origin allows).
- Download to `URL_IMPORT_DIR/<id>.part`, rename to `<id>`. The directory is
  capped by `URL_IMPORT_MAX_BYTES`; before a download, least recently used
  files (mtime, touched on reads) are evicted, partial files of running imports
  kept.
- `BuildTorrent` hashes it into a single-file torrent: `private=1` (BEP 27, no
  DHT/PEX announce), piece length from anacrolix `ChoosePieceLength`, and one
  web seed (BEP 19 `url-list`) =
  `URL_SEED_BASE_URL/urlseed/<id>/<hmac>/`. The torrent is stored with
  `StoreResource` exactly like an uploaded .torrent, and `url_source.info_hash`
  is set.
- `GET|HEAD /urlseed/:id/:sig/*name` is read by torrent-web-seeder. It has no
  session, so it is mounted before the session/auth/only-authorized layers
  (`serve.go`) and carries its own credential: `sig` is
  HMAC-SHA256(`SESSION_SECRET`, "urlseed:"+id), 32 hex, compared in constant
  time; `helpers.RedactURL` removes it from the access log. It serves the
  staged file, or — evicted — the origin through the same proxy (renewing the
  link). A different copy than the one hashed fails the seeder's piece check;
  Vault is the way to keep a file for good.
- Self-hosted: `URL_SEED_BASE_URL=http://127.0.0.1:8097` (web-ui's own port,
  not nginx). Empty disables stage 2 and the page stays proxy-only.

## Switch-over

`app/urlstream.js` asks `/url/:id/status` on load: `new`/`failed` shows the
Import button (and stops polling), `importing` polls every 3 s ("Importing into
Webtor… n%", "Preparing the full player… n%"). When an import this page saw run
reaches `imported`, it offers the full player with a 5 s countdown ("Stay here"
cancels the countdown, the button stays). A source already imported before the
page opened gets an "Open imported version" link instead, no countdown.
Before leaving, a position ≥ 30 s is written for the new resource with
`PUT /watch/position` (`resource_id` = infohash, `path` = "/" + filename, the
single file's path), so the resource page offers "Continue from …". The URL page
keeps its own position in `localStorage` (`wt-url-pos:<id>`) meanwhile.
The resource URL is `/<infohash>?file-idx=0#action=stream`.

## Table `url_source`

Migration 75. user, addon row (`ON DELETE SET NULL`: the link can no longer be
renewed), `content_type`/`content_id`, `title`, `filename`, `size`, `url`,
`proxy_headers` (jsonb), `status` (new/importing/imported/failed), `error`,
`info_hash`, `staging_path`. Exported in `/profile/export` as `url_sources`
without `url`/`proxy_headers` (docs/data_export.md). Reopening the same file of
the same title (content id + filename + size) reuses the row.

## Gaps

- HLS/DASH: would need an ffmpeg `-c copy` remux before stage 2.
- While an import runs, the file is fetched twice (player + import).
- The torrent's web seed depends on the staged file or the addon; with both
  gone (and no Vault) the resource cannot be read.
- Multi-pod deployments: the import and its progress are per process.
