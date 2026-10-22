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
