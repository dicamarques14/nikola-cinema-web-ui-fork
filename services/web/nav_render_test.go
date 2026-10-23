package web

import (
	"bytes"
	"html/template"
	"os"
	"regexp"
	"strings"
	"testing"

	uuid "github.com/satori/go.uuid"
	"github.com/webtor-io/web-ui/services/auth"
)

// renderNav executes the whole partials/nav.html against a real *Context
// (so NavSection and Onboarding resolve as in production) with the helper
// funcs stubbed.
func renderNav(t *testing.T, ctx *Context) string {
	t.Helper()
	b, err := os.ReadFile("../../templates/partials/nav.html")
	if err != nil {
		t.Fatal(err)
	}
	funcs := template.FuncMap{
		"t":           func(lang, key string) string { return key },
		"tp":          func(lang, key string, args ...any) string { return key },
		"langPath":    func(lang, p string) string { return p },
		"tierName":    func(any) string { return "free" },
		"hasAuth":     func(u *auth.User) bool { return u.HasAuth() },
		"profileName": func(*auth.User) string { return "me" },
		"asset":       func(string) template.HTML { return "" },
	}
	tpl, err := template.New("nav").Funcs(funcs).Parse(string(b))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "nav", ctx); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func signedIn() *auth.User {
	return &auth.User{ID: uuid.NewV4(), Email: "me@example.com"}
}

var currentRe = regexp.MustCompile(`<a [^>]*aria-current="page"[^>]*>`)

// NH-1: one centered row of destinations, and the page's own is marked.
func TestNavMarksTheCurrentDestination(t *testing.T) {
	for path, want := range map[string]string{
		"/":          `href="/"`,
		"/discover":  `href="/discover"`,
		"/lib/":      `href="/lib/"`,
		"/vault":     `href="/vault"`,
		"/profile":   `href="/profile"`,
		"/jellyfin":  `href="/jellyfin"`,
		"/some-hash": "",
	} {
		page := renderNav(t, &Context{Lang: "en", Path: path, User: signedIn(), Vault: true, Jellyfin: true})
		got := currentRe.FindAllString(page, -1)
		if want == "" {
			if len(got) != 0 {
				t.Errorf("%s: nothing should be current, got %v", path, got)
			}
			continue
		}
		if len(got) != 1 || !strings.Contains(got[0], want) {
			t.Errorf("%s: current = %v, want one link with %s", path, got, want)
		}
	}
}

func TestNavCenterHoldsTheMainDestinations(t *testing.T) {
	page := renderNav(t, &Context{Lang: "en", Path: "/", User: signedIn(), Jellyfin: true})
	i := strings.Index(page, `class="navbar-center`)
	j := strings.Index(page, `class="navbar-end`)
	if i < 0 || j < i {
		t.Fatal("no centered destinations ahead of the right-hand cluster")
	}
	center := page[i:j]
	for _, href := range []string{`href="/"`, `href="/discover"`, `href="/discover?watchlist=1"`, `href="/lib/"`, `href="/jellyfin"`} {
		if !strings.Contains(center, href) {
			t.Errorf("center lacks %s", href)
		}
	}
	if strings.Contains(center, `href="/vault"`) {
		t.Error("Vault belongs on the right, not in the centered row")
	}
	// Every tile is a 56px target.
	if n, m := strings.Count(center, "<a "), strings.Count(center, "h-14 min-h-14"); n != m {
		t.Errorf("%d of %d centered links are 56px tall", m, n)
	}

	noJF := renderNav(t, &Context{Lang: "en", Path: "/", User: signedIn()})
	if strings.Contains(noJF, `href="/jellyfin"`) {
		t.Error("Jellyfin shows without the integration enabled")
	}
	anon := renderNav(t, &Context{Lang: "en", Path: "/", User: &auth.User{}})
	if strings.Contains(anon, `class="navbar-center`) {
		t.Error("anonymous visitors get the signed-in destinations")
	}
}

// NH-3: the one search box posts to the resource flow flagged as a search,
// is a 56px / 18px search field, and the .torrent upload lives next to it.
func TestNavSearchBox(t *testing.T) {
	page := renderNav(t, &Context{Lang: "en", Path: "/discover", User: signedIn()})
	i := strings.Index(page, `data-nav-search-form`)
	if i < 0 {
		t.Fatal("no nav search form")
	}
	start := strings.LastIndex(page[:i], "<form ")
	end := strings.Index(page[i:], "</form>")
	form := page[start : i+end]
	for _, want := range []string{
		`action="/"`, `method="post"`, `role="search"`, `data-async-target="main"`,
		`<input type="hidden" name="search" value="1" />`,
		`type="search" name="resource" enterkeyhint="search"`,
		`h-14`, `text-lg`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("search form lacks %s", want)
		}
	}
	if strings.Contains(form, `name="instruction"`) {
		t.Error("the search box must not carry a tool instruction")
	}
	for _, want := range []string{`class="nav-dropzone`, `id="nav-dropzone-input" class="dropzone-input hidden" name="resource" type="file"`, `for="nav-dropzone-input"`} {
		if !strings.Contains(page, want) {
			t.Errorf("upload lacks %s", want)
		}
	}
}
