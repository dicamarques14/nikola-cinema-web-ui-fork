// Package jellyfin lets the player play files from a Jellyfin server: the
// library is listed through its API and the original file is passed to the
// browser through a Range proxy that adds the API key, so the key never
// leaves this server.
package jellyfin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

const (
	URLFlag    = "jellyfin-url"
	APIKeyFlag = "jellyfin-api-key"
)

// RegisterFlags adds the Jellyfin settings.
func RegisterFlags(f []cli.Flag) []cli.Flag {
	return append(f,
		cli.StringFlag{
			Name:   URLFlag,
			Usage:  "base URL of a Jellyfin server whose library is played here (e.g. http://192.168.1.160:8096); empty disables it. Only the administrator can watch it",
			EnvVar: "JELLYFIN_URL",
		},
		cli.StringFlag{
			Name:   APIKeyFlag,
			Usage:  "Jellyfin API key (Dashboard -> API Keys)",
			EnvVar: "JELLYFIN_API_KEY",
		},
	)
}

// idRe is what a Jellyfin item id looks like: 32 hex digits, dashes allowed.
// Anything else never reaches the upstream path.
var idRe = regexp.MustCompile(`^[0-9a-fA-F-]{32,36}$`)

// ValidID reports whether id is safe to put in an upstream path.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Service is nil when Jellyfin is not configured.
type Service struct {
	base   *url.URL
	key    string
	api    *http.Client
	stream *http.Client
}

func New(c *cli.Context) (*Service, error) {
	raw := strings.TrimRight(strings.TrimSpace(c.String(URLFlag)), "/")
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.Errorf("bad %s", URLFlag)
	}
	if c.String(APIKeyFlag) == "" {
		return nil, errors.Errorf("%s is required with %s", APIKeyFlag, URLFlag)
	}
	s := &Service{base: u, key: c.String(APIKeyFlag)}
	// The key must never follow a redirect to another host (a .strm item
	// can send the stream anywhere): Go keeps custom headers across hosts.
	redirect := func(req *http.Request, via []*http.Request) error {
		if req.URL.Host != s.base.Host {
			return http.ErrUseLastResponse
		}
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	}
	s.api = &http.Client{Timeout: 20 * time.Second, CheckRedirect: redirect}
	// No overall timeout: a film streams for hours. A hung server is cut
	// off at the headers.
	s.stream = &http.Client{
		CheckRedirect: redirect,
		Transport:     &http.Transport{ResponseHeaderTimeout: 30 * time.Second, Proxy: http.ProxyFromEnvironment},
	}
	return s, nil
}

// Item is one playable library entry.
type Item struct {
	ID         string
	Name       string
	Type       string
	Series     string
	Year       int
	Season     int
	Episode    int
	Container  string
	VideoCodec string
	AudioCodec string
	Size       int64
	Runtime    time.Duration
}

// Title is the name with its series and numbering when it is an episode.
func (i Item) Title() string {
	if i.Type == "Episode" && i.Series != "" {
		return i.Series + " - " + i.Name
	}
	return i.Name
}

type apiItem struct {
	ID                string `json:"Id"`
	Name              string `json:"Name"`
	Type              string `json:"Type"`
	SeriesName        string `json:"SeriesName"`
	ProductionYear    int    `json:"ProductionYear"`
	ParentIndexNumber int    `json:"ParentIndexNumber"`
	IndexNumber       int    `json:"IndexNumber"`
	RunTimeTicks      int64  `json:"RunTimeTicks"`
	Container         string `json:"Container"`
	MediaSources      []struct {
		Container    string `json:"Container"`
		Size         int64  `json:"Size"`
		MediaStreams []struct {
			Type  string `json:"Type"`
			Codec string `json:"Codec"`
		} `json:"MediaStreams"`
	} `json:"MediaSources"`
}

func (a apiItem) item() Item {
	it := Item{
		ID: a.ID, Name: a.Name, Type: a.Type, Series: a.SeriesName, Year: a.ProductionYear,
		Season: a.ParentIndexNumber, Episode: a.IndexNumber, Container: a.Container,
		Runtime: time.Duration(a.RunTimeTicks) * 100, // ticks are 100 ns
	}
	if len(a.MediaSources) > 0 {
		ms := a.MediaSources[0]
		it.Size = ms.Size
		if ms.Container != "" {
			it.Container = ms.Container
		}
		for _, s := range ms.MediaStreams {
			switch {
			case s.Type == "Video" && it.VideoCodec == "":
				it.VideoCodec = s.Codec
			case s.Type == "Audio" && it.AudioCodec == "":
				it.AudioCodec = s.Codec
			}
		}
	}
	return it
}

func (s *Service) newRequest(ctx context.Context, method, path string, q url.Values) (*http.Request, error) {
	u := *s.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if s.key != "" {
		req.Header.Set("X-Emby-Token", s.key)
	}
	return req, nil
}

// Items lists movies and episodes, filtered by a search term when given.
func (s *Service) Items(ctx context.Context, search string, limit int) ([]Item, error) {
	q := url.Values{}
	q.Set("Recursive", "true")
	q.Set("IncludeItemTypes", "Movie,Episode")
	q.Set("Fields", "MediaSources,ProductionYear")
	q.Set("SortBy", "SortName")
	q.Set("Limit", strconv.Itoa(limit))
	if search != "" {
		q.Set("SearchTerm", search)
	}
	var out struct {
		Items []apiItem `json:"Items"`
	}
	if err := s.getJSON(ctx, "/Items", q, &out); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(out.Items))
	for _, a := range out.Items {
		items = append(items, a.item())
	}
	return items, nil
}

// Item is one entry by id; nil when Jellyfin does not know it.
func (s *Service) Item(ctx context.Context, id string) (*Item, error) {
	q := url.Values{}
	q.Set("Ids", id)
	q.Set("Fields", "MediaSources,ProductionYear")
	var out struct {
		Items []apiItem `json:"Items"`
	}
	if err := s.getJSON(ctx, "/Items", q, &out); err != nil {
		return nil, err
	}
	if len(out.Items) == 0 {
		return nil, nil
	}
	it := out.Items[0].item()
	return &it, nil
}

func (s *Service) getJSON(ctx context.Context, path string, q url.Values, v any) error {
	req, err := s.newRequest(ctx, http.MethodGet, path, q)
	if err != nil {
		return err
	}
	res, err := s.api.Do(req)
	if err != nil {
		return errors.New("jellyfin unreachable")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return errors.Errorf("jellyfin answered %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(v)
}

// responseHeaders are what a byte-range player needs from the origin.
var responseHeaders = []string{
	"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges",
	"Last-Modified", "ETag",
}

// requestHeaders are what the player may send on to the origin.
var requestHeaders = []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"}

// Stream answers w with the original file of item id, Range passed through.
func (s *Service) Stream(w http.ResponseWriter, r *http.Request, id string) {
	q := url.Values{}
	q.Set("static", "true")
	s.pass(w, r, "/Videos/"+id+"/stream", q, true)
}

// Poster answers w with the primary image of item id.
func (s *Service) Poster(w http.ResponseWriter, r *http.Request, id string) {
	q := url.Values{}
	q.Set("maxWidth", "320")
	s.pass(w, r, "/Items/"+id+"/Images/Primary", q, false)
}

func (s *Service) pass(w http.ResponseWriter, r *http.Request, path string, q url.Values, media bool) {
	method := http.MethodGet
	if r.Method == http.MethodHead {
		method = http.MethodHead
	}
	req, err := s.newRequest(r.Context(), method, path, q)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	for _, k := range requestHeaders {
		if v := r.Header.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	// Byte ranges must be the origin's bytes, not a compressed body.
	req.Header.Set("Accept-Encoding", "identity")
	cl := s.api
	if media {
		cl = s.stream
	}
	res, err := cl.Do(req)
	if err != nil {
		http.Error(w, "jellyfin unreachable", http.StatusBadGateway)
		return
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusOK, http.StatusPartialContent, http.StatusNotModified, http.StatusRequestedRangeNotSatisfiable:
	case http.StatusNotFound:
		http.NotFound(w, r)
		return
	default:
		http.Error(w, "jellyfin answered "+http.StatusText(res.StatusCode), http.StatusBadGateway)
		return
	}
	for _, k := range responseHeaders {
		if v := res.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	if media {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(res.StatusCode)
	if method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, res.Body)
}
