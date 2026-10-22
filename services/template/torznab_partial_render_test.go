package template

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"testing"
	"time"

	uuid "github.com/satori/go.uuid"
	"github.com/webtor-io/web-ui/models"
)

// TestTorznabIndexersPartialRenders parses and executes the profile Torznab
// section standalone. The template manager parses every partial at startup,
// so a broken one takes the process down rather than failing a single page —
// and a nil-pointer method call inside a range only shows up at execute time,
// which no build or vet step reaches.
func TestTorznabIndexersPartialRenders(t *testing.T) {
	funcs := template.FuncMap{
		"t":        func(lang, key string, args ...interface{}) string { return key },
		"tp":       func(lang, key string, args ...interface{}) string { return key },
		"langPath": func(lang, p string) string { return p },
		"asset":    func(p string) template.HTML { return template.HTML("<script src=\"" + p + "\"></script>") },
		"deref": func(s *string) string {
			if s == nil {
				return ""
			}
			return *s
		},
		"isPaid":      func(_ interface{}) bool { return false },
		"withContext": func(ctx, data interface{}) interface{} { return data },
	}
	tpl, err := template.New("torznab_indexers.html").Funcs(funcs).
		ParseFiles("../../templates/partials/profile/torznab_indexers.html")
	if err != nil {
		t.Fatalf("failed to parse partial: %v", err)
	}

	name := "My Jackett"
	key := "secret"
	now := time.Now()

	for _, tt := range []struct {
		name string
		data []models.TorznabIndexer
	}{
		{name: "empty list"},
		{
			// The row a fully probed indexer renders.
			name: "probed indexer",
			data: []models.TorznabIndexer{{
				ID:            uuid.NewV4(),
				Url:           "https://jackett.example.com/torznab",
				Name:          &name,
				ApiKey:        &key,
				Enabled:       true,
				Caps:          &models.TorznabCaps{MovieParams: []string{"q", "imdbid"}, TVParams: []string{"q"}},
				CapsFetchedAt: &now,
			}},
		},
		{
			// A row from before a successful probe: no name, no caps, no
			// key. Every one of those is a nil pointer the template has to
			// survive.
			name: "bare indexer",
			data: []models.TorznabIndexer{{
				ID:  uuid.NewV4(),
				Url: "https://feed.example.com/torznab",
			}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := map[string]interface{}{
				"Lang":   "en",
				"Claims": nil,
				"Data": map[string]interface{}{
					"TorznabIndexers": tt.data,
					"ErrKey":          "",
					"ErrNum":          0,
					// 0 is unlimited: the self-hosted default.
					"TorznabIndexerLimit": 0,
				},
			}
			var buf bytes.Buffer
			if err := tpl.ExecuteTemplate(&buf, "profile/torznab_indexers", ctx); err != nil {
				t.Fatalf("failed to render partial: %v", err)
			}
			if !strings.Contains(buf.String(), "profile.indexers.title") {
				t.Error("rendered output is missing the section heading")
			}
		})
	}
}

// TestTorznabIndexersPartialCapAndError pins the add form against the
// configured cap (TORZNAB_MAX_INDEXERS, 0 = unlimited) and the error line
// quoting its number.
func TestTorznabIndexersPartialCapAndError(t *testing.T) {
	funcs := template.FuncMap{
		"t": func(lang, key string, args ...interface{}) string { return key },
		"tp": func(lang, key string, args ...interface{}) string {
			return key + "#" + fmt.Sprint(args...)
		},
		"langPath":    func(lang, p string) string { return p },
		"asset":       func(p string) template.HTML { return "" },
		"deref":       func(s *string) string { return "" },
		"isPaid":      func(_ interface{}) bool { return false },
		"withContext": func(ctx, data interface{}) interface{} { return data },
	}
	tpl, err := template.New("torznab_indexers.html").Funcs(funcs).
		ParseFiles("../../templates/partials/profile/torznab_indexers.html")
	if err != nil {
		t.Fatalf("failed to parse partial: %v", err)
	}
	three := make([]models.TorznabIndexer, 3)
	for i := range three {
		three[i] = models.TorznabIndexer{ID: uuid.NewV4(), Url: "https://feed.example.com/torznab"}
	}
	render := func(limit int, errKey string, errNum int) string {
		t.Helper()
		var buf bytes.Buffer
		err := tpl.ExecuteTemplate(&buf, "profile/torznab_indexers", map[string]interface{}{
			"Lang": "en", "Claims": nil,
			"Data": map[string]interface{}{
				"TorznabIndexers": three, "ErrKey": errKey, "ErrNum": errNum,
				"TorznabIndexerLimit": limit,
			},
		})
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		return buf.String()
	}
	if out := render(3, "", 0); !strings.Contains(out, "profile.indexers.maxReached") {
		t.Error("limit 3 with 3 indexers: max-reached notice missing")
	}
	if out := render(0, "", 0); strings.Contains(out, "profile.indexers.maxReached") {
		t.Error("limit 0 (unlimited): form must stay enabled")
	}
	if out := render(0, "error.indexerHttpStatus", 404); !strings.Contains(out, "error.indexerHttpStatus#Num404") {
		t.Errorf("error with number not rendered through tp:\n%s", out)
	}
	if out := render(0, "error.indexerInvalidApiKey", 0); !strings.Contains(out, "error.indexerInvalidApiKey") {
		t.Error("plain error key not rendered")
	}
}
