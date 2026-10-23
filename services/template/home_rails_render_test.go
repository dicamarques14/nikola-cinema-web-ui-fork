package template

import (
	"bytes"
	"html/template"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/webtor-io/web-ui/models"
)

type homeData struct {
	ContinueWatching []*models.WatchHistory
	Watchlist        []models.WatchlistItem
}

// postLikeData mirrors handlers/resource.PostData, which re-renders the
// index view after a failed paste and has no Watchlist field.
type postLikeData struct {
	ContinueWatching []*models.WatchHistory
}

func renderHomeRails(t *testing.T, data any) string {
	t.Helper()
	funcs := template.FuncMap{
		"t":        func(lang, key string) string { return key },
		"langPath": func(lang, p string) string { return p },
		"has": func(obj any, field string) bool {
			f := reflect.Indirect(reflect.ValueOf(obj)).FieldByName(field)
			return f.IsValid() && !f.IsNil()
		},
		"posterURL":      func(id string, w int, adult bool, ctx any) string { return "/poster/" + id },
		"showAdultBadge": func(adult bool, ctx any) bool { return adult },
		"timeAgoLang":    func(lang string, t time.Time) string { return "ago" },
		"asset":          func(string) template.HTML { return "" },
		"withContext": func(ctx, data any) any {
			return map[string]any{"Ctx": ctx, "Data": data}
		},
	}
	b, err := os.ReadFile("../../templates/partials/home_rails.html")
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := template.New("home_rails").Funcs(funcs).Parse(string(b))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	ctx := map[string]any{"Lang": "en"}
	if err := tpl.ExecuteTemplate(&buf, "home_rails", map[string]any{"Ctx": ctx, "Data": data}); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

// NH-2: empty rails are not rendered; with nothing to show, Home offers a
// way in instead; trending rails are always placed (JS fills or drops them).
func TestHomeRailsHideEmptyRails(t *testing.T) {
	for name, data := range map[string]any{"index": &homeData{}, "post": &postLikeData{}} {
		page := renderHomeRails(t, data)
		if strings.Contains(page, `id="home-continue-watching"`) || strings.Contains(page, `id="home-watchlist"`) {
			t.Errorf("%s: an empty rail rendered", name)
		}
		if !strings.Contains(page, `id="home-empty"`) {
			t.Errorf("%s: no empty state", name)
		}
		for _, typ := range []string{"movie", "series"} {
			if !strings.Contains(page, `data-home-trending="`+typ+`"`) {
				t.Errorf("%s: no %s trending rail", name, typ)
			}
		}
	}
}

func TestHomeRailsRenderBigPosters(t *testing.T) {
	year := int16(1999)
	page := renderHomeRails(t, &homeData{
		ContinueWatching: []*models.WatchHistory{{ResourceID: "abc", Path: "/m.mkv", Title: "The Matrix", Position: 30, Duration: 60}},
		Watchlist: []models.WatchlistItem{
			{VideoID: "tt0133093", Type: "movie", Title: "The Matrix", Year: &year, PosterURL: "https://img/p.jpg"},
			{VideoID: "tt0903747", Type: "series", Title: "Breaking Bad"},
		},
	})
	if strings.Contains(page, `id="home-empty"`) {
		t.Error("empty state shown next to content")
	}
	for _, want := range []string{
		`id="home-continue-watching"`,
		`href="/abc?file=%2fm.mkv#action=stream"`,
		`src="/poster/abc"`,
		`id="home-watchlist"`,
		`href="/discover?type=movie&id=tt0133093"`,
		`src="https://img/p.jpg"`,
		`href="/discover?type=series&id=tt0903747"`,
		`src="/lib/series/poster/tt0903747/500.jpg"`,
		`1999 · Movie`,
		`href="/discover?watchlist=1"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("rails lack %s", want)
		}
	}
	if n := strings.Count(page, `w-[220px] group`); n != 3 {
		t.Errorf("%d big poster cards, want 3", n)
	}
	if strings.Contains(page, `w-[140px]`) {
		t.Error("a small card slipped into the Home rails")
	}
}
