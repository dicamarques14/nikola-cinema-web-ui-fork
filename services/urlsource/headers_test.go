package urlsource

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/webtor-io/web-ui/services/stremio"
)

func TestUpstreamHeaders(t *testing.T) {
	incoming := http.Header{}
	incoming.Set("Range", "bytes=10-")
	incoming.Set("Cookie", "session=viewer")
	incoming.Set("Authorization", "Bearer viewer")
	incoming.Set("X-Forwarded-For", "1.2.3.4")
	h := buildUpstreamHeaders(map[string]string{
		"referer":        "https://addon.example/",
		"Cookie":         "CloudFront-Policy=abc",
		"Host":           "evil.internal",
		"Range":          "bytes=0-",
		"Proxy-Thing":    "x",
		"Bad Name":       "x",
		"X-Split":        "a\r\nInjected: 1",
		"Content-Length": "0",
	}, incoming, "UA/1")
	want := map[string]string{
		"Referer": "https://addon.example/", "Cookie": "CloudFront-Policy=abc",
		"Range": "bytes=10-", "User-Agent": "UA/1", "Accept-Encoding": "identity",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	for _, k := range []string{"Host", "Authorization", "X-Forwarded-For", "Proxy-Thing", "X-Split", "Content-Length"} {
		if h.Get(k) != "" {
			t.Errorf("%s must not be sent, got %q", k, h.Get(k))
		}
	}
	if len(h) != len(want) {
		t.Errorf("unexpected headers: %v", h)
	}
}

func TestResponseHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Range", "bytes 0-1/2")
	src.Set("Set-Cookie", "origin=1")
	src.Set("Location", "https://cdn.example/x")
	dst := http.Header{}
	CopyResponseHeaders(dst, src)
	if dst.Get("Content-Range") == "" || dst.Get("Set-Cookie") != "" || dst.Get("Location") != "" {
		t.Fatalf("copied %v", dst)
	}
}

// The origin client must refuse loopback (and so every private address):
// an addon's link pointing inward is SSRF.
func TestMediaClientRefusesPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	if res, err := newMediaClient(false).Get(srv.URL); err == nil {
		_ = res.Body.Close()
		t.Fatal("loopback origin was reached")
	}
	res, err := newMediaClient(true).Get(srv.URL)
	if err != nil {
		t.Fatalf("allowed private network still refused: %v", err)
	}
	_ = res.Body.Close()
}

func TestMatchStream(t *testing.T) {
	streams := []stremio.StreamItem{
		{Url: "https://a.example/hls/master.m3u8"},
		{InfoHash: "0123456789abcdef0123456789abcdef01234567"},
		{Url: "https://pengu.example/r/abc?sig=1", BehaviorHints: &stremio.StreamBehaviorHints{Filename: "Film.mkv", VideoSize: 42}},
		{Url: "https://pengu.example/r/def?sig=1", BehaviorHints: &stremio.StreamBehaviorHints{Filename: "Film.mp4", VideoSize: 7}},
	}
	if s := MatchStream(streams, "https://pengu.example/r/def?sig=1", "", 0); s == nil || s.BehaviorHints.Filename != "Film.mp4" {
		t.Fatalf("by link: %+v", s)
	}
	// A renewed link: same file, new signature.
	if s := MatchStream(streams, "https://pengu.example/r/abc?sig=OLD", "Film.mkv", 42); s == nil || s.Url != "https://pengu.example/r/abc?sig=1" {
		t.Fatalf("by name and size: %+v", s)
	}
	if s := MatchStream(streams, "", "Film.mkv", 41); s != nil {
		t.Fatalf("a different size matched: %+v", s)
	}
	if s := MatchStream(streams, "https://a.example/hls/master.m3u8", "", 0); s != nil {
		t.Fatal("an HLS stream matched")
	}
}
