package jellyfin

import (
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Subtitle is one subtitle stream of a file, external (a .srt beside it) or
// embedded. Only a text one can be served as WebVTT: Jellyfin cannot turn
// an image track (PGS, VobSub) into text.
type Subtitle struct {
	Index    int
	Language string
	Label    string
	Codec    string
	Default  bool
	Forced   bool
	External bool
	Text     bool
}

func (s apiStream) subtitle() Subtitle {
	return Subtitle{
		Index: s.Index, Language: s.Language, Codec: s.Codec,
		Default: s.IsDefault, Forced: s.IsForced, External: s.IsExternal,
		Text:  s.IsTextSubtitleStream,
		Label: subtitleLabel(s),
	}
}

// subtitleLabel is DisplayTitle without what the chip says on its own: the
// codec, "Default", "External" and the bare file counters Jellyfin puts in
// front of a second .srt of one language ("1 - Portuguese - SUBRIP").
func subtitleLabel(s apiStream) string {
	var parts []string
	for _, p := range strings.Split(s.DisplayTitle, " - ") {
		p = strings.TrimSpace(p)
		if p == "" || p == "Default" || p == "External" || strings.EqualFold(p, s.Codec) {
			continue
		}
		if _, err := strconv.Atoi(p); err == nil {
			continue
		}
		parts = append(parts, p)
	}
	l := strings.Join(parts, " · ")
	if l == "" {
		l = s.Language
	}
	if l == "" {
		l = "Subtitle " + strconv.Itoa(s.Index)
	}
	return l
}

// ID is the picker chip id of the track.
func (s Subtitle) ID() string { return "jf-" + strconv.Itoa(s.Index) }

// TextSubtitles are the tracks the player can show.
func (i Item) TextSubtitles() []Subtitle {
	var out []Subtitle
	for _, s := range i.Subtitles {
		if s.Text {
			out = append(out, s)
		}
	}
	return out
}

// ImageSubtitles counts the tracks that cannot be shown (PGS, VobSub).
func (i Item) ImageSubtitles() int {
	n := 0
	for _, s := range i.Subtitles {
		if !s.Text {
			n++
		}
	}
	return n
}

// Subtitle finds a text subtitle by stream index.
func (i Item) Subtitle(index int) (Subtitle, bool) {
	for _, s := range i.Subtitles {
		if s.Index == index && s.Text {
			return s, true
		}
	}
	return Subtitle{}, false
}

// StartSubtitle is the track to show when the page opens: the file's
// default, else a forced one (signs and foreign dialogue), else none.
func (i Item) StartSubtitle() (Subtitle, bool) {
	for _, s := range i.Subtitles {
		if s.Text && s.Default {
			return s, true
		}
	}
	for _, s := range i.Subtitles {
		if s.Text && s.Forced {
			return s, true
		}
	}
	return Subtitle{}, false
}

// maxSubtitleBytes cuts off a runaway body (the copy is streamed, not
// buffered).
const maxSubtitleBytes = 16 << 20

// Subtitle answers w with stream index of media source msID of item id as
// WebVTT. The caller has checked the ids and that the index is a text track.
func (s *Service) Subtitle(w http.ResponseWriter, r *http.Request, id, msID string, index int) {
	if !ValidID(id) || !ValidID(msID) || index < 0 {
		http.NotFound(w, r)
		return
	}
	path := "/Videos/" + id + "/" + msID + "/Subtitles/" + strconv.Itoa(index) + "/0/Stream.vtt"
	req, err := s.newRequest(r.Context(), http.MethodGet, path, url.Values{})
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cl := s.subs
	if cl == nil {
		cl = s.api
	}
	res, err := cl.Do(req)
	if err != nil {
		http.Error(w, "jellyfin unreachable", http.StatusBadGateway)
		return
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode == http.StatusNotFound:
		http.NotFound(w, r)
		return
	case res.StatusCode != http.StatusOK:
		http.Error(w, "jellyfin answered "+http.StatusText(res.StatusCode), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, io.LimitReader(res.Body, maxSubtitleBytes))
}

// Neighbours are the episodes before and after id in eps, in the order the
// show page lists them (GroupSeasons: specials last).
func Neighbours(eps []Item, id string) (prev, next *Item) {
	var flat []Item
	for _, s := range GroupSeasons(eps) {
		flat = append(flat, s.Episodes...)
	}
	for i := range flat {
		if flat[i].ID != id {
			continue
		}
		if i > 0 {
			p := flat[i-1]
			prev = &p
		}
		if i+1 < len(flat) {
			n := flat[i+1]
			next = &n
		}
		return prev, next
	}
	return nil, nil
}
