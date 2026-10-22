package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webtor-io/web-ui/services/jellyfin"
)

func TestAddonContent(t *testing.T) {
	ep := &jellyfin.Item{Type: "Episode", Season: 2, Episode: 5}
	if typ, id := addonContent(ep, &jellyfin.Item{Imdb: "tt0108778"}); typ != "series" || id != "tt0108778:2:5" {
		t.Errorf("episode = %s %s", typ, id)
	}
	if typ, id := addonContent(ep, &jellyfin.Item{Tmdb: "1668"}); typ != "series" || id != "tmdb:1668:2:5" {
		t.Errorf("episode tmdb = %s %s", typ, id)
	}
	if typ, _ := addonContent(ep, nil); typ != "" {
		t.Errorf("episode without series = %s", typ)
	}
	if typ, id := addonContent(&jellyfin.Item{Type: "Movie", Imdb: "tt1", Tmdb: "2"}, nil); typ != "movie" || id != "tt1" {
		t.Errorf("movie = %s %s", typ, id)
	}
	if typ, _ := addonContent(&jellyfin.Item{Type: "Movie"}, nil); typ != "" {
		t.Errorf("movie without ids = %s", typ)
	}
}

func TestAddonLang(t *testing.T) {
	for tag, want := range map[string]string{"eng": "en", "por": "pt", "pob": "pt", "spa": "es", "fr": "fr", "": "", "<x>": ""} {
		if got, _ := addonLang(tag); got != want {
			t.Errorf("addonLang(%q) = %q, want %q", tag, got, want)
		}
	}
	if _, v := addonLang("pob"); v != "BR" {
		t.Errorf("pob variant = %q", v)
	}
}

func TestRankAndChipsKeepURLsServerSide(t *testing.T) {
	var subs []addonSub
	for i := 0; i < 6; i++ {
		subs = append(subs, addonSub{Lang: "en", Tag: "eng", Addon: "A", URL: "https://secret.example/token/en"})
	}
	subs = append(subs, addonSub{Lang: "pt", Tag: "por", Addon: "A", URL: "https://secret.example/token/pt"},
		addonSub{Lang: "de", Tag: "ger", Addon: "A", URL: "https://secret.example/token/de"})
	r := rank(subs, "pt")
	if r[0].Lang != "pt" || r[1].Lang != "en" || r[len(r)-1].Lang != "de" {
		t.Errorf("order = %+v", r)
	}
	if len(r) != 1+addonPerLang+1 {
		t.Errorf("kept %d", len(r))
	}
	cs := chips("abc", r)
	if cs[0].Src != "/jellyfin/abc/addon-subtitles/0.srt" || cs[0].Label != "Portuguese" || cs[0].Source != "A" {
		t.Errorf("chip = %+v", cs[0])
	}
	if cs[2].Label != "English 2" {
		t.Errorf("numbering = %q", cs[2].Label)
	}
	for _, c := range cs {
		if strings.Contains(c.Src+c.Label+c.Source, "secret") {
			t.Fatalf("addon URL leaked: %+v", c)
		}
	}
}

func TestFetchOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tok/subtitles/series/tt1:1:2.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"subtitles":[{"id":"1","url":"https://cdn.example/a.srt","lang":"eng"},{"url":"javascript:alert(1)","lang":"eng"},{"url":"https://cdn.example/b.srt","lang":"pob"}]}`))
	}))
	defer srv.Close()
	a := newAddonSubs(nil, nil, srv.Client())
	if a != nil {
		t.Fatal("no pg must mean no addon subtitles")
	}
	a = &addonSubs{list: srv.Client(), cache: map[string]addonEntry{}}
	got, err := a.fetchOne(context.Background(), addonBase(srv.URL+"/tok/manifest.json"), "OS", "series", "tt1:1:2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Lang != "en" || got[1].Lang != "pt" || got[1].Addon != "OS" {
		t.Errorf("subs = %+v", got)
	}
	if _, err := a.fetchOne(context.Background(), srv.URL+"/other", "OS", "movie", "tt1"); err == nil || strings.Contains(err.Error(), srv.URL) {
		t.Errorf("error must not carry the URL: %v", err)
	}
}

func TestToUTF8(t *testing.T) {
	if got := string(toUTF8([]byte("\ufeffol\xe1"))); got != "olá" {
		t.Errorf("cp1252 = %q", got)
	}
	if got := string(toUTF8([]byte("olá"))); got != "olá" {
		t.Errorf("utf8 = %q", got)
	}
}
