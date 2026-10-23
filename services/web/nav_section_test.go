package web

import "testing"

// NH-1: the navbar marks the destination the page belongs to.
func TestNavSection(t *testing.T) {
	for _, tc := range []struct{ lang, path, want string }{
		{"en", "/", "home"},
		{"en", "/discover", "discover"},
		{"en", "/lib/", "library"},
		{"en", "/lib/movies", "library"},
		{"en", "/library", ""},
		{"en", "/jellyfin", "jellyfin"},
		{"en", "/vault", "vault"},
		{"en", "/notifications", "notifications"},
		{"en", "/profile", "profile"},
		{"en", "/08ada5a7a6183aae1e09d831df6748d566095a10", ""},
		{"en", "/magnet-to-torrent", ""},
		{"ru", "/ru/", "home"},
		{"ru", "/ru/discover", "discover"},
		{"", "/lib/", "library"},
	} {
		if got := (&Context{Lang: tc.lang, Path: tc.path}).NavSection(); got != tc.want {
			t.Errorf("NavSection(%q, %q) = %q, want %q", tc.lang, tc.path, got, tc.want)
		}
	}
}
