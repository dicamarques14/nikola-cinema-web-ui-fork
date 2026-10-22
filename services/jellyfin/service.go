// Package jellyfin lets the player play files from a Jellyfin server: the
// library is listed through its API and the original file is passed to the
// browser through a Range proxy that adds the API key, so the key never
// leaves this server.
package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
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
	// subs reads subtitle files: an embedded track is extracted from the
	// whole file on first read, which can outlast api's timeout.
	subs *http.Client
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
	s.subs = &http.Client{Timeout: 3 * time.Minute, CheckRedirect: redirect}
	// No overall timeout: a film streams for hours. A hung server is cut
	// off at the headers.
	s.stream = &http.Client{
		CheckRedirect: redirect,
		Transport:     &http.Transport{ResponseHeaderTimeout: 30 * time.Second, Proxy: http.ProxyFromEnvironment},
	}
	return s, nil
}

// Item is one library entry: a movie, an episode or a series.
type Item struct {
	ID         string
	Name       string
	Type       string
	Series     string
	SeriesID   string
	SeasonName string
	Year       int
	Season     int
	Episode    int
	Container  string
	VideoCodec string
	AudioCodec string
	Size       int64
	Runtime    time.Duration
	// Virtual is a "missing" entry Jellyfin knows from metadata only (an
	// episode not on disk): it has no file to stream and no image.
	Virtual bool
	// ImageID is the item whose Primary image stands for this one: its own,
	// else its series' (an episode without a still), else none.
	ImageID string
	// Overview is the synopsis, when the query asked for it.
	Overview string
	// MediaSourceID is the first version's id (the one Stream plays).
	MediaSourceID string
	// Subtitles are the first version's subtitle streams, in Jellyfin order.
	Subtitles []Subtitle
	// Audio are the first version's audio tracks, in file order.
	Audio []Audio
	// Imdb and Tmdb are the entry's own provider ids, when Jellyfin has them
	// (an episode's are its own, rarely set: see the series').
	Imdb string
	Tmdb string
}

// Code is "S01E02" for an episode, "" otherwise.
func (i Item) Code() string {
	if i.Type != "Episode" || i.Episode == 0 {
		return ""
	}
	return fmt.Sprintf("S%02dE%02d", i.Season, i.Episode)
}

// Title is the name with its series and numbering when it is an episode.
func (i Item) Title() string {
	if i.Type == "Episode" && i.Series != "" {
		return i.Series + " - " + i.Name
	}
	return i.Name
}

// IsSeries reports whether the entry is a show rather than something to play.
func (i Item) IsSeries() bool { return i.Type == "Series" }

// Href is the page of the entry: a show's season list, else the player.
func (i Item) Href() string {
	if i.IsSeries() {
		return "/jellyfin/show/" + i.ID
	}
	return "/jellyfin/" + i.ID
}

// Minutes is the runtime rounded to whole minutes.
func (i Item) Minutes() int { return int((i.Runtime + 30*time.Second) / time.Minute) }

type apiItem struct {
	ID                    string            `json:"Id"`
	Name                  string            `json:"Name"`
	Type                  string            `json:"Type"`
	SeriesName            string            `json:"SeriesName"`
	SeriesID              string            `json:"SeriesId"`
	SeasonName            string            `json:"SeasonName"`
	SeriesPrimaryImageTag string            `json:"SeriesPrimaryImageTag"`
	ImageTags             map[string]string `json:"ImageTags"`
	LocationType          string            `json:"LocationType"`
	ProductionYear        int               `json:"ProductionYear"`
	ParentIndexNumber     int               `json:"ParentIndexNumber"`
	IndexNumber           int               `json:"IndexNumber"`
	RunTimeTicks          int64             `json:"RunTimeTicks"`
	Container             string            `json:"Container"`
	Overview              string            `json:"Overview"`
	ProviderIds           map[string]string `json:"ProviderIds"`
	MediaSources          []struct {
		ID           string      `json:"Id"`
		Container    string      `json:"Container"`
		Size         int64       `json:"Size"`
		MediaStreams []apiStream `json:"MediaStreams"`
	} `json:"MediaSources"`
}

type apiStream struct {
	Type                 string `json:"Type"`
	Codec                string `json:"Codec"`
	Index                int    `json:"Index"`
	Language             string `json:"Language"`
	Title                string `json:"Title"`
	DisplayTitle         string `json:"DisplayTitle"`
	IsDefault            bool   `json:"IsDefault"`
	IsForced             bool   `json:"IsForced"`
	IsExternal           bool   `json:"IsExternal"`
	IsTextSubtitleStream bool   `json:"IsTextSubtitleStream"`
	Channels             int    `json:"Channels"`
	ChannelLayout        string `json:"ChannelLayout"`
}

func (a apiItem) item() Item {
	it := Item{
		ID: a.ID, Name: a.Name, Type: a.Type, Series: a.SeriesName, SeriesID: a.SeriesID,
		SeasonName: a.SeasonName, Year: a.ProductionYear,
		Season: a.ParentIndexNumber, Episode: a.IndexNumber, Container: a.Container,
		Runtime:  time.Duration(a.RunTimeTicks) * 100, // ticks are 100 ns
		Virtual:  a.LocationType == "Virtual",
		Overview: a.Overview,
		Imdb:     providerID(a.ProviderIds, "Imdb"),
		Tmdb:     providerID(a.ProviderIds, "Tmdb"),
	}
	switch {
	case a.ImageTags["Primary"] != "":
		it.ImageID = a.ID
	case a.SeriesPrimaryImageTag != "" && ValidID(a.SeriesID):
		it.ImageID = a.SeriesID
	}
	if len(a.MediaSources) > 0 {
		ms := a.MediaSources[0]
		it.Size = ms.Size
		if ValidID(ms.ID) {
			it.MediaSourceID = ms.ID
		}
		if ms.Container != "" {
			it.Container = ms.Container
		}
		for _, s := range ms.MediaStreams {
			switch {
			case s.Type == "Video" && it.VideoCodec == "":
				it.VideoCodec = s.Codec
			case s.Type == "Audio":
				if it.AudioCodec == "" {
					it.AudioCodec = s.Codec
				}
				it.Audio = append(it.Audio, s.audio())
			case s.Type == "Subtitle":
				it.Subtitles = append(it.Subtitles, s.subtitle())
			}
		}
		it.Audio = fileStreams(it.Audio, ms.MediaStreams)
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

// onDisk keeps a listing to what has a file: Jellyfin also lists "missing"
// episodes (LocationType Virtual, known from metadata only), which have no
// stream (Jellyfin answers 400) and no image (404).
func onDisk(q url.Values) {
	q.Set("ExcludeLocationTypes", "Virtual")
	q.Set("IsMissing", "false")
}

// Items searches movies, shows and episodes. An empty search lists movies and
// episodes, as before.
func (s *Service) Items(ctx context.Context, search string, limit int) ([]Item, error) {
	q := url.Values{}
	q.Set("Recursive", "true")
	q.Set("IncludeItemTypes", "Movie,Episode")
	q.Set("Fields", "MediaSources,ProductionYear")
	q.Set("SortBy", "SortName")
	q.Set("Limit", strconv.Itoa(limit))
	onDisk(q)
	if search != "" {
		q.Set("IncludeItemTypes", "Series,Movie,Episode")
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

// pageSize and maxItems bound the paged listings below: whole libraries are
// read a page at a time, up to a cap that keeps a page render bounded.
const (
	pageSize = 500
	maxItems = 10000
)

// all reads every page of an /Items query.
func (s *Service) all(ctx context.Context, q url.Values) ([]Item, error) {
	var items []Item
	for start := 0; start < maxItems; start += pageSize {
		q.Set("StartIndex", strconv.Itoa(start))
		q.Set("Limit", strconv.Itoa(pageSize))
		var out struct {
			Items            []apiItem `json:"Items"`
			TotalRecordCount int       `json:"TotalRecordCount"`
		}
		if err := s.getJSON(ctx, "/Items", q, &out); err != nil {
			return nil, err
		}
		for _, a := range out.Items {
			items = append(items, a.item())
		}
		if len(out.Items) < pageSize || start+len(out.Items) >= out.TotalRecordCount {
			break
		}
	}
	return items, nil
}

// Shows lists, by name, every series with at least one episode on disk (a
// series Jellyfin only knows from metadata has nothing to play).
func (s *Service) Shows(ctx context.Context) ([]Item, error) {
	q := url.Values{}
	q.Set("Recursive", "true")
	q.Set("IncludeItemTypes", "Series")
	q.Set("Fields", "ProductionYear")
	q.Set("SortBy", "SortName")
	shows, err := s.all(ctx, q)
	if err != nil {
		return nil, err
	}
	q = url.Values{}
	q.Set("Recursive", "true")
	q.Set("IncludeItemTypes", "Episode")
	q.Set("EnableImages", "false")
	onDisk(q)
	eps, err := s.all(ctx, q)
	if err != nil {
		return nil, err
	}
	has := map[string]bool{}
	for _, e := range eps {
		has[e.SeriesID] = true
	}
	out := shows[:0]
	for _, sh := range shows {
		if has[sh.ID] {
			out = append(out, sh)
		}
	}
	return out, nil
}

// Movies lists every movie on disk, by name.
func (s *Service) Movies(ctx context.Context) ([]Item, error) {
	q := url.Values{}
	q.Set("Recursive", "true")
	q.Set("IncludeItemTypes", "Movie")
	q.Set("Fields", "ProductionYear")
	q.Set("SortBy", "SortName")
	onDisk(q)
	return s.all(ctx, q)
}

// Episodes lists the episodes of a series that are on disk.
func (s *Service) Episodes(ctx context.Context, seriesID string) ([]Item, error) {
	q := url.Values{}
	q.Set("Recursive", "true")
	q.Set("ParentId", seriesID)
	q.Set("IncludeItemTypes", "Episode")
	q.Set("SortBy", "ParentIndexNumber,IndexNumber,SortName")
	onDisk(q)
	return s.all(ctx, q)
}

// Season is one season of a show with its episodes in order.
type Season struct {
	Number   int
	Name     string
	Episodes []Item
}

// GroupSeasons groups episodes by season number, seasons ascending with the
// specials (season 0) last, episodes by number.
func GroupSeasons(eps []Item) []Season {
	idx := map[int]int{}
	var out []Season
	for _, e := range eps {
		i, ok := idx[e.Season]
		if !ok {
			name := e.SeasonName
			if name == "" {
				if e.Season == 0 {
					name = "Specials"
				} else {
					name = "Season " + strconv.Itoa(e.Season)
				}
			}
			idx[e.Season] = len(out)
			i = len(out)
			out = append(out, Season{Number: e.Season, Name: name})
		}
		out[i].Episodes = append(out[i].Episodes, e)
	}
	key := func(n int) int {
		if n <= 0 {
			return int(^uint(0) >> 1)
		}
		return n
	}
	sort.SliceStable(out, func(a, b int) bool { return key(out[a].Number) < key(out[b].Number) })
	for _, se := range out {
		sort.SliceStable(se.Episodes, func(a, b int) bool { return se.Episodes[a].Episode < se.Episodes[b].Episode })
	}
	return out
}

// Item is one entry by id; nil when Jellyfin does not know it.
func (s *Service) Item(ctx context.Context, id string) (*Item, error) {
	q := url.Values{}
	q.Set("Ids", id)
	q.Set("Fields", "MediaSources,ProductionYear,Overview,ProviderIds")
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

// providerID is ids[key] when it is a plain id: it is later put into an addon
// URL, so nothing but letters, digits and "-" passes.
func providerID(ids map[string]string, key string) string {
	v := strings.TrimSpace(ids[key])
	if v == "" || len(v) > 32 {
		return ""
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return ""
		}
	}
	return v
}
