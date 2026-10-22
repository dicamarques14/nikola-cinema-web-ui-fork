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
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	uuid "github.com/satori/go.uuid"
	log "github.com/sirupsen/logrus"
	cs "github.com/webtor-io/common-services"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/language"

	"github.com/webtor-io/web-ui/models"
	"github.com/webtor-io/web-ui/services/auth"
	"github.com/webtor-io/web-ui/services/jellyfin"
	"github.com/webtor-io/web-ui/services/streamprefs"
	"github.com/webtor-io/web-ui/services/stremio"
)

// Subtitles from the viewer's Stremio subtitle addons (the ones in their
// addon list whose manifest offers "subtitles", e.g. OpenSubtitles v3), for
// a Jellyfin item, by its IMDb id (an episode: the series' plus
// ":season:episode"). Everything that names an addon stays here: the page
// gets a list of /jellyfin/:id/addon-subtitles/N chips, and N is resolved
// against the list this server fetched -- an addon's URL (which may carry
// its user's token) never reaches the browser or the log.

const (
	addonListTimeout = 8 * time.Second
	addonFileTimeout = 20 * time.Second
	addonCacheTTL    = 15 * time.Minute
	addonCacheMax    = 256
	addonFileMax     = 4 << 20
	addonPerLang     = 4
	addonMax         = 40
)

// addonSub is one subtitle an addon offered.
type addonSub struct {
	Lang  string // base language ("en"), "" when the addon's tag is unknown
	Tag   string // the addon's own tag ("eng", "pob")
	Addon string // the addon's name
	URL   string // never leaves the server
}

// addonChip is what the page gets for one addonSub.
type addonChip struct {
	ID      string `json:"id"`
	Src     string `json:"src"`
	Label   string `json:"label"`
	Lang    string `json:"lang"`
	Source  string `json:"source"`
	Variant string `json:"variant,omitempty"`
}

type addonEntry struct {
	at   time.Time
	subs []addonSub
}

type addonSubs struct {
	pg    *cs.PG
	prefs *streamprefs.Service
	list  *http.Client
	file  *http.Client

	mu    sync.Mutex
	cache map[string]addonEntry
}

// newAddonSubs reuses the Stremio client's transport, whose dialer refuses
// private addresses (services/egress): an addon is the user's input.
func newAddonSubs(pg *cs.PG, prefs *streamprefs.Service, base *http.Client) *addonSubs {
	if pg == nil || base == nil {
		return nil
	}
	tr := base.Transport
	return &addonSubs{
		pg: pg, prefs: prefs,
		list: &http.Client{Transport: tr, Timeout: addonListTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		// Subtitle files are often a redirect away (a CDN); every hop is
		// dialed through the same guard.
		file: &http.Client{Transport: tr, Timeout: addonFileTimeout, CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 3 || (r.URL.Scheme != "https" && r.URL.Scheme != "http") {
				return http.ErrUseLastResponse
			}
			return nil
		}},
		cache: map[string]addonEntry{},
	}
}

// addonContent is the Stremio type and id the addons are asked for: IMDb
// first; TMDb ("tmdb:N") only when there is no IMDb id, which few subtitle
// addons understand.
func addonContent(it *jellyfin.Item, series *jellyfin.Item) (typ, id string) {
	if it.Type == "Episode" {
		if series == nil || it.Season <= 0 || it.Episode <= 0 {
			return "", ""
		}
		base := series.Imdb
		if base == "" && series.Tmdb != "" {
			base = "tmdb:" + series.Tmdb
		}
		if base == "" {
			return "", ""
		}
		return "series", fmt.Sprintf("%s:%d:%d", base, it.Season, it.Episode)
	}
	if it.Imdb != "" {
		return "movie", it.Imdb
	}
	if it.Tmdb != "" {
		return "movie", "tmdb:" + it.Tmdb
	}
	return "", ""
}

func hasResource(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), want) {
			return true
		}
	}
	return false
}

// addonBase is an addon's base URL (its manifest URL without manifest.json).
func addonBase(u string) string {
	return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(u), "/manifest.json"), "/")
}

var addonTag = regexp.MustCompile(`^[A-Za-z]{2,3}([-_][A-Za-z0-9]{2,8})?$`)

// addonLang is the base language of an addon's tag ("eng" -> "en"); "pob"
// (OpenSubtitles' Brazilian Portuguese) is "pt" with variant "BR".
func addonLang(tag string) (lang, variant string) {
	t := strings.ToLower(strings.TrimSpace(tag))
	switch t {
	case "pob", "pt-br", "pt_br", "ptbr":
		return "pt", "BR"
	case "":
		return "", ""
	}
	if !addonTag.MatchString(t) {
		return "", ""
	}
	parsed, err := language.Parse(strings.ReplaceAll(t, "_", "-"))
	if err != nil {
		return "", ""
	}
	b, conf := parsed.Base()
	if conf == language.No {
		return "", ""
	}
	return b.String(), ""
}

// fetch asks every subtitle addon of the user at once; a slow or broken
// addon only loses its own results.
func (a *addonSubs) fetch(ctx context.Context, userID uuid.UUID, typ, id string) []addonSub {
	db := a.pg.Get()
	if db == nil {
		return nil
	}
	addons, err := models.GetUserStremioAddonUrls(ctx, db, userID)
	if err != nil {
		log.WithError(err).Warn("jellyfin: failed to list the subtitle addons")
		return nil
	}
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		got = map[int][]addonSub{}
	)
	for i, ad := range addons {
		if !hasResource(ad.ManifestResources, "subtitles") {
			continue
		}
		name := "Addon"
		if ad.Name != nil && strings.TrimSpace(*ad.Name) != "" {
			name = strings.TrimSpace(*ad.Name)
		}
		wg.Add(1)
		go func(i int, base, name string) {
			defer wg.Done()
			subs, err := a.fetchOne(ctx, base, name, typ, id)
			if err != nil {
				// The addon's name only: its URL may carry a token.
				log.WithError(sanitizeErr(err)).WithField("addon", name).Warn("jellyfin: subtitle addon failed")
				return
			}
			mu.Lock()
			got[i] = subs
			mu.Unlock()
		}(i, addonBase(ad.Url), name)
	}
	wg.Wait()
	var out []addonSub
	for i := range addons {
		out = append(out, got[i]...)
	}
	return out
}

// sanitizeErr drops the URL from a client error (url.Error prints it).
func sanitizeErr(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return fmt.Errorf("%s: %v", ue.Op, ue.Err)
	}
	return err
}

func (a *addonSubs) fetchOne(ctx context.Context, base, name, typ, id string) ([]addonSub, error) {
	u := fmt.Sprintf("%s/subtitles/%s/%s.json", base, typ, url.PathEscape(id))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("bad addon url")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.list.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var body struct {
		Subtitles []struct {
			URL  string `json:"url"`
			Lang string `json:"lang"`
		} `json:"subtitles"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("bad response")
	}
	var out []addonSub
	for _, s := range body.Subtitles {
		p, err := url.Parse(s.URL)
		if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" {
			continue
		}
		lang, _ := addonLang(s.Lang)
		out = append(out, addonSub{Lang: lang, Tag: s.Lang, Addon: name, URL: s.URL})
	}
	return out, nil
}

// rank orders subs for the picker -- the viewer's language first, then
// English, then the rest by name -- keeping a few per language.
func rank(subs []addonSub, preferred string) []addonSub {
	pri := func(l string) int {
		switch {
		case l != "" && l == preferred:
			return 0
		case l == "en":
			return 1
		case l == "":
			return 3
		}
		return 2
	}
	name := func(l string) string {
		if d := stremio.NewLangDisplay(l); d.Name != "" {
			return d.Name
		}
		return l
	}
	out := append([]addonSub(nil), subs...)
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := pri(out[i].Lang), pri(out[j].Lang)
		if pi != pj {
			return pi < pj
		}
		return name(out[i].Lang) < name(out[j].Lang)
	})
	per := map[string]int{}
	var kept []addonSub
	for _, s := range out {
		k := s.Lang + "|" + strings.ToLower(s.Tag)
		if per[k] >= addonPerLang {
			continue
		}
		per[k]++
		kept = append(kept, s)
		if len(kept) >= addonMax {
			break
		}
	}
	return kept
}

// chips are what the page gets: index-addressed, labelled by language and
// addon, numbered within a language.
func chips(itemID string, subs []addonSub) []addonChip {
	n := map[string]int{}
	out := make([]addonChip, 0, len(subs))
	for i, s := range subs {
		lang, variant := addonLang(s.Tag)
		label := strings.ToUpper(s.Tag)
		if d := stremio.NewLangDisplay(lang); d.Name != "" {
			label = d.Name
		}
		if variant != "" {
			label += " (" + variant + ")"
		}
		k := s.Lang + "|" + variant
		n[k]++
		if n[k] > 1 {
			label += " " + strconv.Itoa(n[k])
		}
		if label == "" {
			label = "Unknown"
		}
		if lang == "" {
			lang = "und"
		}
		out = append(out, addonChip{
			ID:      "addon-" + strconv.Itoa(i),
			Src:     "/jellyfin/" + itemID + "/addon-subtitles/" + strconv.Itoa(i) + ".srt",
			Label:   label,
			Lang:    lang,
			Source:  s.Addon,
			Variant: variant,
		})
	}
	return out
}

func cacheKey(userID uuid.UUID, itemID string) string { return userID.String() + "|" + itemID }

func (a *addonSubs) cached(key string) ([]addonSub, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.cache[key]
	if !ok || time.Since(e.at) > addonCacheTTL {
		return nil, false
	}
	return e.subs, true
}

func (a *addonSubs) store(key string, subs []addonSub) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.cache) >= addonCacheMax {
		for k, e := range a.cache {
			if time.Since(e.at) > addonCacheTTL || len(a.cache) >= addonCacheMax {
				delete(a.cache, k)
			}
		}
	}
	a.cache[key] = addonEntry{at: time.Now(), subs: subs}
}

// resolve is the ranked list for an item, from the cache or the addons.
func (h *Handler) resolveAddonSubs(c *gin.Context, user *auth.User, id string) ([]addonSub, bool) {
	key := cacheKey(user.ID, id)
	if subs, ok := h.addon.cached(key); ok {
		return subs, true
	}
	ctx := c.Request.Context()
	it, err := h.svc.Item(ctx, id)
	if err != nil || it == nil {
		return nil, false
	}
	var series *jellyfin.Item
	if it.Type == "Episode" && jellyfin.ValidID(it.SeriesID) {
		if series, err = h.svc.Item(ctx, it.SeriesID); err != nil {
			return nil, false
		}
	}
	typ, cid := addonContent(it, series)
	var subs []addonSub
	if typ != "" {
		subs = rank(h.addon.fetch(ctx, user.ID, typ, cid), h.preferredLang(c, user))
	}
	h.addon.store(key, subs)
	return subs, true
}

func (h *Handler) preferredLang(c *gin.Context, user *auth.User) string {
	picked, _ := sessions.Default(c).Get(models.PreferredLangSessionKey).(string)
	tags, _, err := language.ParseAcceptLanguage(c.GetHeader("Accept-Language"))
	if err != nil {
		tags = nil
	}
	return h.addon.prefs.PreferredContentLang(c.Request.Context(), user, picked, tags, "")
}

// addonList answers the item's addon subtitles as chips (JSON); an empty
// list when the user has no subtitle addon or the item no IMDb id.
func (h *Handler) addonList(c *gin.Context) {
	id := c.Param("id")
	user := auth.GetUserFromContext(c)
	if !jellyfin.ValidID(id) || h.addon == nil || !user.HasAuth() {
		c.JSON(http.StatusOK, []addonChip{})
		return
	}
	subs, ok := h.resolveAddonSubs(c, user, id)
	if !ok {
		c.JSON(http.StatusOK, []addonChip{})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, chips(id, subs))
}

var addonFile = regexp.MustCompile(`^([0-9]{1,3})\.srt$`)

// addonFileGet answers one of the item's addon subtitles, as the addon
// served it (SRT or WebVTT, which the player's side-load parses alike), in
// UTF-8.
func (h *Handler) addonFileGet(c *gin.Context) {
	id := c.Param("id")
	m := addonFile.FindStringSubmatch(c.Param("file"))
	user := auth.GetUserFromContext(c)
	if !jellyfin.ValidID(id) || m == nil || h.addon == nil || !user.HasAuth() {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	n, _ := strconv.Atoi(m[1])
	subs, ok := h.resolveAddonSubs(c, user, id)
	if !ok || n >= len(subs) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	s := subs[n]
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, s.URL, nil)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	resp, err := h.addon.file.Do(req)
	if err != nil {
		log.WithError(sanitizeErr(err)).WithField("addon", s.Addon).Warn("jellyfin: failed to fetch an addon subtitle")
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		log.WithField("addon", s.Addon).WithField("status", resp.StatusCode).Warn("jellyfin: addon subtitle answered an error")
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, addonFileMax+1))
	if err != nil || len(body) > addonFileMax {
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	c.Header("Cache-Control", "private, max-age=3600")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", toUTF8(body))
}

// toUTF8: most addon files are UTF-8 already (OpenSubtitles converts on
// its "subencoding-stremio-utf8" URLs); anything else is taken for
// Windows-1252, the usual encoding of the rest.
func toUTF8(b []byte) []byte {
	b = []byte(strings.TrimPrefix(string(b), "\ufeff"))
	if utf8.Valid(b) {
		return b
	}
	if out, err := charmap.Windows1252.NewDecoder().Bytes(b); err == nil {
		return out
	}
	return b
}
