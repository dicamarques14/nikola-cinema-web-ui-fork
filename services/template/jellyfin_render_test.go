package template_test

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/webtor-io/web-ui/services/jellyfin"
)

func renderJellyfin(t *testing.T, view string, data any) string {
	t.Helper()
	funcs := template.FuncMap{
		"t":     func(lang, key string, args ...interface{}) string { return key },
		"asset": func(p string) template.HTML { return "" },
	}
	tpl, err := template.New(view).Funcs(funcs).ParseFiles("../../templates/views/jellyfin/" + view)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "main", map[string]interface{}{"Data": data, "Lang": "en"}); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

const (
	jfShow = "ffffffffffffffffffffffffffffffff"
	jfEp   = "0123456789abcdef0123456789abcdef"
)

// The library page lists shows (to their season page) apart from movies.
func TestJellyfinIndexGroupsShowsAndMovies(t *testing.T) {
	data := struct {
		Search        string
		Items         []jellyfin.Item
		Shows, Movies []jellyfin.Item
		Error         string
	}{
		Shows:  []jellyfin.Item{{ID: jfShow, Name: "Seinfeld", Type: "Series", ImageID: jfShow}},
		Movies: []jellyfin.Item{{ID: jfEp, Name: "Heat", Type: "Movie"}},
	}
	out := renderJellyfin(t, "index.html", data)
	for _, want := range []string{"Shows", "Movies", `href="/jellyfin/show/` + jfShow + `"`, `href="/jellyfin/` + jfEp + `"`, `/jellyfin/` + jfShow + `/poster`} {
		if !strings.Contains(out, want) {
			t.Errorf("index lacks %q", want)
		}
	}
	if strings.Contains(out, "/jellyfin/"+jfEp+"/poster") {
		t.Error("an item without an image still asks for a poster")
	}
}

// The show page has one section per season, episodes linking to the player.
func TestJellyfinShowRendersSeasons(t *testing.T) {
	eps := []jellyfin.Item{
		{ID: jfEp, Name: "The Pony Remark", Type: "Episode", Season: 2, Episode: 2},
		{ID: jfEp, Name: "Pilot", Type: "Episode", Season: 1, Episode: 1},
	}
	data := struct {
		Show    *jellyfin.Item
		Seasons []jellyfin.Season
	}{&jellyfin.Item{ID: jfShow, Name: "Seinfeld", Type: "Series"}, jellyfin.GroupSeasons(eps)}
	out := renderJellyfin(t, "show.html", data)
	if n := strings.Count(out, `<details class="jellyfin-season`); n != 2 {
		t.Errorf("%d season sections, want 2", n)
	}
	if strings.Index(out, "Season 1") > strings.Index(out, "Season 2") {
		t.Error("seasons out of order")
	}
	if !strings.Contains(out, `href="/jellyfin/`+jfEp+`"`) {
		t.Error("episode does not link to the player")
	}
}

// The player page: the episode header, prev/next, and the subtitle picker
// with Jellyfin's text tracks as side-loaded chips the track bridge drives.
func TestJellyfinPlayRendersHeaderNavAndSubtitles(t *testing.T) {
	type chip struct {
		jellyfin.Subtitle
		ID, Src string
	}
	ep := &jellyfin.Item{ID: jfEp, Name: "The Pony Remark", Type: "Episode", Series: "Seinfeld", SeriesID: jfShow, Season: 2, Episode: 2, Overview: "Jerry and a pony.", ImageID: jfEp}
	prev := &jellyfin.Item{ID: jfShow, Name: "Pilot", Type: "Episode", Season: 2, Episode: 1}
	next := &jellyfin.Item{ID: "abcdefabcdefabcdefabcdefabcdefab", Name: "The Stake Out", Type: "Episode", Season: 2, Episode: 3}
	data := struct {
		Item           *jellyfin.Item
		Title          string
		StreamURL      string
		Prev, Next     *jellyfin.Item
		Subtitles      []chip
		Start          string
		ImageSubtitles int
		Audio          []audioChip
		StartAudio     string
	}{
		Item: ep, Title: "Seinfeld - The Pony Remark", StreamURL: "/jellyfin/" + jfEp + "/stream",
		Prev: prev, Next: next,
		Subtitles: []chip{
			{jellyfin.Subtitle{Index: 0, Label: "Portuguese", Language: "por", Text: true, External: true}, "jf-0", "/jellyfin/" + jfEp + "/subtitles/0.vtt"},
			{jellyfin.Subtitle{Index: 5, Label: "English", Language: "eng", Text: true, Default: true}, "jf-5", "/jellyfin/" + jfEp + "/subtitles/5.vtt"},
		},
		Start: "jf-5", ImageSubtitles: 2,
	}
	out := renderJellyfin(t, "play.html", data)
	for _, want := range []string{
		"Seinfeld", "S02E02", "The Pony Remark", "Jerry and a pony.",
		`href="/jellyfin/show/` + jfShow + `"`,
		`href="/jellyfin/` + jfShow + `"`, // prev
		`data-next-href="/jellyfin/abcdefabcdefabcdefabcdefabcdefab"`,
		`id="subtitles" data-persist="false" data-subtitles-off="false"`,
		`data-id="jf-5" data-provider="Jellyfin" data-src="/jellyfin/` + jfEp + `/subtitles/5.vtt"`,
		"2 image subtitle tracks",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("play page lacks %q", want)
		}
	}
	if strings.Contains(out, `id="audio-tracks"`) {
		t.Error("a single-track (here: no-track) file needs no audio picker")
	}
	if n := strings.Count(out, `data-default="true"`); n != 1 {
		t.Errorf("%d default subtitle chips, want exactly 1 (jf-5)", n)
	}
	if !strings.Contains(out, `data-label="English" data-srclang="eng" data-lang="eng"`) {
		t.Error("chip label/lang attributes missing")
	}

	// No start track: the hidden "none" carrier is the default, picker off.
	data.Start = ""
	out = renderJellyfin(t, "play.html", data)
	if !strings.Contains(out, `data-subtitles-off="true"`) || !strings.Contains(out, `data-lang="und" data-default="true"`) {
		t.Error("with no start track the picker must open off on the none carrier")
	}
}

type audioChip struct {
	jellyfin.Audio
	ID       string
	Playable bool
}

func TestJellyfinPlayRendersAudioPicker(t *testing.T) {
	ep := &jellyfin.Item{ID: jfEp, Name: "The Pony Remark", Type: "Episode"}
	data := struct {
		Item           *jellyfin.Item
		Title          string
		StreamURL      string
		Prev, Next     *jellyfin.Item
		Subtitles      []struct{}
		Start          string
		ImageSubtitles int
		Audio          []audioChip
		StartAudio     string
	}{
		Item: ep, Title: "x", StreamURL: "/jellyfin/" + jfEp + "/stream",
		Audio: []audioChip{
			{jellyfin.Audio{Index: 1, Stream: 1, Language: "eng", Label: "English · Dolby Digital+ · 5.1", Codec: "eac3"}, "jfa-1", true},
			{jellyfin.Audio{Index: 2, Stream: 2, Language: "por", Label: "Portuguese · WMA", Codec: "wmapro"}, "jfa-2", false},
		},
		StartAudio: "jfa-1",
	}
	out := renderJellyfin(t, "play.html", data)
	for _, want := range []string{
		`id="audio-tracks"`,
		`data-id="jfa-1" data-provider="MediaProbe" data-stream="1" data-srclang="eng" data-label="English · Dolby Digital&#43; · 5.1" data-codec="eac3"`,
		`class="audio track-chip min-h-12 px-4 track-chip-active"`,
		`data-unplayable="true"`,
		"wmapro · may not play",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("play page lacks %q", want)
		}
	}
	if n := strings.Count(out, `data-unplayable="true"`); n != 1 {
		t.Errorf("%d unplayable chips, want 1", n)
	}
}
