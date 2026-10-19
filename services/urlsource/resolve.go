package urlsource

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/pkg/errors"
	uuid "github.com/satori/go.uuid"

	"github.com/webtor-io/web-ui/models"
	"github.com/webtor-io/web-ui/services/stremio"
)

// ErrNotFound is a stream the addon no longer returns (or never did).
var ErrNotFound = errors.New("stream not found at the addon")

// ErrUnsupported is a stream this phase cannot play: adaptive (HLS/DASH)
// or not http(s).
var ErrUnsupported = errors.New("stream type not supported")

var idRe = regexp.MustCompile(`^[A-Za-z0-9:._%-]{1,200}$`)
var typeRe = regexp.MustCompile(`^[a-z]{1,20}$`)

// OpenRequest is what the Discover row sends. URL is only used to find the
// same stream in the server's own fetch from the addon -- it is never
// fetched as given.
type OpenRequest struct {
	AddonID  uuid.UUID
	Type     string
	ID       string
	URL      string
	Filename string
	Size     int64
	Title    string
}

// IsAdaptive says whether a link is an HLS or DASH manifest.
func IsAdaptive(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	return strings.HasSuffix(p, ".m3u8") || strings.HasSuffix(p, ".mpd")
}

// IsDirectURLStream is a stream this package handles: an http(s) link, no
// infohash, not adaptive.
func IsDirectURLStream(s *stremio.StreamItem) bool {
	if s.InfoHash != "" || s.Url == "" {
		return false
	}
	u, err := url.Parse(s.Url)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	return !IsAdaptive(s.Url)
}

func streamFilename(s *stremio.StreamItem) string {
	if s.BehaviorHints != nil && s.BehaviorHints.Filename != "" {
		return SanitizeFilename(s.BehaviorHints.Filename)
	}
	if u, err := url.Parse(s.Url); err == nil {
		if b := path.Base(u.Path); b != "" && b != "/" && b != "." {
			return SanitizeFilename(b)
		}
	}
	return "video"
}

func streamSize(s *stremio.StreamItem) int64 {
	if s.BehaviorHints != nil {
		return s.BehaviorHints.VideoSize
	}
	return 0
}

func streamHeaders(s *stremio.StreamItem) map[string]string {
	if s.BehaviorHints != nil && s.BehaviorHints.ProxyHeaders != nil {
		return CleanProxyHeaders(s.BehaviorHints.ProxyHeaders.Request)
	}
	return nil
}

// SanitizeFilename makes a name fit for a torrent and a file system: no
// path separators or control characters, at most 200 bytes.
func SanitizeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.Trim(name, "."))
	if len(name) > 200 {
		ext := path.Ext(name)
		if len(ext) > 10 {
			ext = ""
		}
		name = strings.ToValidUTF8(name[:200-len(ext)], "") + ext
	}
	if name == "" {
		return "video"
	}
	return name
}

// MatchStream finds the stream the viewer picked among the addon's answer:
// the same link first, then the same file name and size, then (size
// unknown) the same name alone.
func MatchStream(streams []stremio.StreamItem, wantURL, filename string, size int64) *stremio.StreamItem {
	var cands []*stremio.StreamItem
	for i := range streams {
		if IsDirectURLStream(&streams[i]) {
			cands = append(cands, &streams[i])
		}
	}
	if wantURL != "" {
		for _, s := range cands {
			if s.Url == wantURL {
				return s
			}
		}
	}
	if filename == "" {
		return nil
	}
	for _, s := range cands {
		if streamFilename(s) == filename && size > 0 && streamSize(s) == size {
			return s
		}
	}
	if size <= 0 {
		for _, s := range cands {
			if streamFilename(s) == filename {
				return s
			}
		}
	}
	return nil
}

func (s *Service) fetchAddonStreams(ctx context.Context, addonURL, typ, id string) ([]stremio.StreamItem, error) {
	base := strings.TrimSuffix(addonURL, "/manifest.json")
	u := base + "/stream/" + typ + "/" + url.PathEscape(id) + ".json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build addon request")
	}
	req.Header.Set("Accept", "application/json")
	if s.userAgent != "" {
		req.Header.Set("User-Agent", s.userAgent)
	}
	res, err := s.addonCl.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "addon %s unreachable", hostOf(base))
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, errors.Errorf("addon %s answered %d", hostOf(base), res.StatusCode)
	}
	var sr stremio.StreamsResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&sr); err != nil {
		return nil, errors.Wrap(err, "failed to decode addon streams")
	}
	return sr.Streams, nil
}

// Open is a click on a direct-URL row: the server asks the addon itself
// (the browser's copy of the link is only a key to find it), records the
// stream, and starts its import.
func (s *Service) Open(ctx context.Context, userID uuid.UUID, r *OpenRequest) (*models.UrlSource, error) {
	if !typeRe.MatchString(r.Type) || !idRe.MatchString(r.ID) {
		return nil, errors.New("bad stream id")
	}
	if r.URL != "" && IsAdaptive(r.URL) {
		return nil, ErrUnsupported
	}
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	addon, err := models.GetStremioAddonUrlByID(ctx, db, r.AddonID)
	if err != nil {
		return nil, err
	}
	if addon == nil || addon.UserID != userID {
		return nil, ErrNotFound
	}
	streams, err := s.fetchAddonStreams(ctx, addon.Url, r.Type, r.ID)
	if err != nil {
		return nil, err
	}
	st := MatchStream(streams, r.URL, SanitizeFilename(r.Filename), r.Size)
	if st == nil {
		return nil, ErrNotFound
	}
	name := streamFilename(st)
	size := streamSize(st)
	headers := streamHeaders(st)

	existing, err := models.FindUserUrlSource(ctx, db, userID, r.ID, name, size)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.Url != st.Url {
			if err := models.UpdateUrlSourceLink(ctx, db, existing.ID, st.Url, headers); err != nil {
				return nil, err
			}
			existing.Url, existing.ProxyHeaders = st.Url, headers
		}
		s.StartImport(existing)
		return existing, nil
	}
	title := strings.TrimSpace(r.Title)
	if len(title) > 300 {
		title = strings.ToValidUTF8(title[:300], "")
	}
	src := &models.UrlSource{
		UserID:            userID,
		StremioAddonUrlID: &addon.ID,
		ContentType:       r.Type,
		ContentID:         r.ID,
		Title:             title,
		Filename:          name,
		Size:              size,
		Url:               st.Url,
		ProxyHeaders:      headers,
		Status:            models.UrlSourceNew,
	}
	if err := models.CreateUrlSource(ctx, db, src); err != nil {
		return nil, err
	}
	s.StartImport(src)
	return src, nil
}

// Resolve asks the addon again for an expired link of src and stores the
// new one. Concurrent callers for the same source share one attempt.
func (s *Service) Resolve(ctx context.Context, src *models.UrlSource, staleURL string) error {
	mu, _ := s.resolveLock.LoadOrStore(src.ID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	db, err := s.db()
	if err != nil {
		return err
	}
	cur, err := models.GetUrlSource(ctx, db, src.ID)
	if err != nil {
		return err
	}
	if cur == nil {
		return ErrNotFound
	}
	if cur.Url != staleURL {
		// Someone resolved it meanwhile.
		src.Url, src.ProxyHeaders = cur.Url, cur.ProxyHeaders
		return nil
	}
	if cur.StremioAddonUrlID == nil {
		return errors.New("addon removed; the link cannot be renewed")
	}
	addon, err := models.GetStremioAddonUrlByID(ctx, db, *cur.StremioAddonUrlID)
	if err != nil {
		return err
	}
	if addon == nil {
		return errors.New("addon removed; the link cannot be renewed")
	}
	streams, err := s.fetchAddonStreams(ctx, addon.Url, cur.ContentType, cur.ContentID)
	if err != nil {
		return err
	}
	st := MatchStream(streams, "", cur.Filename, cur.Size)
	if st == nil {
		return ErrNotFound
	}
	headers := streamHeaders(st)
	if err := models.UpdateUrlSourceLink(ctx, db, cur.ID, st.Url, headers); err != nil {
		return err
	}
	src.Url, src.ProxyHeaders = st.Url, headers
	return nil
}
