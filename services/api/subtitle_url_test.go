package api

import (
	"strings"
	"testing"

	ra "github.com/webtor-io/rest-api/services"
)

func TestAttachExternalSubtitleConvertsTextFormats(t *testing.T) {
	ei := ra.ExportItem{URL: "https://edge.example.com/hash/file.mp4?token=abc"}
	for _, tc := range []struct {
		name        string
		u           string
		wantConvert bool
		wantName    string
	}{
		{name: "srt", u: "https://webtor.io/user-subtitle/file/deadbeef/subs.srt", wantConvert: true, wantName: "subs.vtt"},
		{name: "ass", u: "https://webtor.io/user-subtitle/file/deadbeef/subs.ass", wantConvert: true, wantName: "subs.vtt"},
		{name: "ssa", u: "https://webtor.io/user-subtitle/file/deadbeef/subs.ssa", wantConvert: true, wantName: "subs.vtt"},
		{name: "vtt passes through", u: "https://webtor.io/user-subtitle/file/deadbeef/subs.vtt", wantConvert: false},
		// ".srt" уже входит в имя ресурса, а не является расширением.
		{name: "no extension", u: "https://webtor.io/user-subtitle/file/deadbeef/subtitles", wantConvert: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := (&Api{}).AttachExternalSubtitle(ei, tc.u)
			gotConvert := strings.Contains(res, "~vtt/")
			if gotConvert != tc.wantConvert {
				t.Fatalf("~vtt conversion = %v, want %v; url: %s", gotConvert, tc.wantConvert, res)
			}
			if tc.wantConvert && !strings.HasSuffix(strings.Split(res, "?")[0], "/"+tc.wantName) {
				t.Errorf("converted url does not end with %q: %s", tc.wantName, res)
			}
			if !strings.Contains(res, "/ext/") {
				t.Errorf("url is not wrapped through /ext/: %s", res)
			}
			if !strings.HasSuffix(res, "?token=abc") {
				t.Errorf("export-item query lost: %s", res)
			}
		})
	}
}

func TestMakeSubtitleURLConvertsTextFormats(t *testing.T) {
	base := "https://edge.example.com/hash/file.mp4~vi/opensubtitles?token=abc"
	for _, tc := range []struct {
		format      string
		src         string
		wantConvert bool
	}{
		{format: "srt", src: "/one.srt", wantConvert: true},
		{format: "ass", src: "/two.ass", wantConvert: true},
		{format: "ssa", src: "/three.ssa", wantConvert: true},
		{format: "vtt", src: "/four.vtt", wantConvert: false},
	} {
		t.Run(tc.format, func(t *testing.T) {
			res := (&Api{}).makeSubtitleURL(base, ExtSubtitle{Src: tc.src, Format: tc.format})
			if got := strings.Contains(res, "~vtt/"); got != tc.wantConvert {
				t.Fatalf("~vtt conversion = %v, want %v; url: %s", got, tc.wantConvert, res)
			}
		})
	}
}

func TestAttachExternalSubtitleEncodesFilenameOnce(t *testing.T) {
	ei := ra.ExportItem{URL: "https://edge.example.com/hash/file.mp4?token=abc"}
	// PublicURL PathEscapes the name; AttachExternalFile must not escape the
	// already-escaped segment again (%20 → %2520), or torrent-http-proxy 404s.
	u := "https://webtor.io/user-subtitle/file/deadbeef/Universal%20Basic%20Guys.srt"
	res := (&Api{}).AttachExternalSubtitle(ei, u)
	if strings.Contains(res, "%2520") {
		t.Fatalf("filename was double-encoded: %s", res)
	}
	if !strings.Contains(res, "Universal%20Basic%20Guys.srt~vtt/Universal%20Basic%20Guys.vtt") {
		t.Fatalf("expected single-encoded .srt~vtt/.vtt suffix, got: %s", res)
	}
	if !strings.HasSuffix(res, "?token=abc") {
		t.Fatalf("export-item query lost: %s", res)
	}
}

func TestAttachExternalSubtitleKeepsExportPathPrefix(t *testing.T) {
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	ei := ra.ExportItem{URL: "https://webtor.example/torrent-http-proxy/" + hash + "/Dir/movie.mkv?token=abc&api-key=K"}
	u := "https://webtor.example/user-subtitle/file/deadbeef/Universal%20Basic%20Guys.srt"
	res := (&Api{}).AttachExternalSubtitle(ei, u)
	if !strings.Contains(res, "https://webtor.example/torrent-http-proxy/ext/") {
		t.Fatalf("expected /torrent-http-proxy/ext/ prefix, got: %s", res)
	}
	if strings.Contains(res, "%2520") {
		t.Fatalf("filename was double-encoded: %s", res)
	}
	if !strings.Contains(res, "Universal%20Basic%20Guys.srt~vtt/Universal%20Basic%20Guys.vtt") {
		t.Fatalf("expected single-encoded .srt~vtt/.vtt suffix, got: %s", res)
	}
	if !strings.Contains(res, "token=abc") || !strings.Contains(res, "api-key=K") {
		t.Fatalf("export-item query lost: %s", res)
	}
}

func TestAttachExternalSubtitleNoPrefixWhenInfohashIsFirst(t *testing.T) {
	const hash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	ei := ra.ExportItem{URL: "https://edge.example.com/" + hash + "/file.mp4?token=abc"}
	res := (&Api{}).AttachExternalSubtitle(ei, "https://webtor.io/user-subtitle/file/deadbeef/subs.srt")
	if strings.Contains(res, "/torrent-http-proxy/") {
		t.Fatalf("SaaS-shaped URL should not invent a prefix: %s", res)
	}
	want := "https://edge.example.com/ext/"
	if !strings.HasPrefix(strings.Split(res, "?")[0], "https://edge.example.com/ext/") && !strings.Contains(res, want) {
		t.Fatalf("expected host-root /ext/, got: %s", res)
	}
	if !strings.Contains(res, "https://edge.example.com/ext/") {
		t.Fatalf("expected https://edge.example.com/ext/, got: %s", res)
	}
}
