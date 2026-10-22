package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testID = "0123456789abcdef0123456789abcdef"

func newTestService(t *testing.T, h http.Handler, key string) *Service {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{base: u, key: key, api: srv.Client(), stream: srv.Client()}
}

func TestValidID(t *testing.T) {
	ok := []string{
		testID,
		"01234567-89ab-cdef-0123-456789abcdef",
		"ABCDEF0123456789ABCDEF0123456789",
	}
	for _, id := range ok {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
	}
	bad := []string{
		"",
		"../x",
		"../../etc/passwd",
		"abc/def",
		testID + "/x",
		"/" + testID[1:],
		testID[:31],
		testID + "0000000",
		strings.Repeat("g", 32),
		testID + "?x=1",
		testID + "\n",
	}
	for _, id := range bad {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

const itemsJSON = `{"Items":[
 {"Id":"` + testID + `","Name":"Pilot","Type":"Episode","SeriesName":"Show","ProductionYear":2010,
  "ParentIndexNumber":1,"IndexNumber":2,"RunTimeTicks":36000000000,"Container":"mkv",
  "MediaSources":[{"Container":"mp4","Size":12345,"MediaStreams":[
    {"Type":"Subtitle","Codec":"srt"},
    {"Type":"Video","Codec":"h264"},
    {"Type":"Audio","Codec":"aac"},
    {"Type":"Video","Codec":"hevc"},
    {"Type":"Audio","Codec":"ac3"}]}]},
 {"Id":"ffffffffffffffffffffffffffffffff","Name":"Movie","Type":"Movie","Container":"avi"}
]}`

func itemsHandler(seen *http.Request) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = *r
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(itemsJSON))
	})
}

func TestItemsParsesEpisodeTitle(t *testing.T) {
	s := newTestService(t, itemsHandler(nil), "k")
	items, err := s.Items(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	ep := items[0]
	if ep.Title() != "Show - Pilot" {
		t.Errorf("Title = %q, want %q", ep.Title(), "Show - Pilot")
	}
	if ep.Season != 1 || ep.Episode != 2 || ep.Year != 2010 {
		t.Errorf("season/episode/year = %d/%d/%d, want 1/2/2010", ep.Season, ep.Episode, ep.Year)
	}
}

func TestItemsTakesFirstCodecsFromMediaSource(t *testing.T) {
	s := newTestService(t, itemsHandler(nil), "k")
	items, err := s.Items(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	ep := items[0]
	if ep.VideoCodec != "h264" || ep.AudioCodec != "aac" {
		t.Errorf("codecs = %q/%q, want h264/aac", ep.VideoCodec, ep.AudioCodec)
	}
	if ep.Container != "mp4" || ep.Size != 12345 {
		t.Errorf("container/size = %q/%d, want mp4/12345 from media source", ep.Container, ep.Size)
	}
}

func TestItemsConvertsRuntimeTicksToDuration(t *testing.T) {
	s := newTestService(t, itemsHandler(nil), "k")
	items, err := s.Items(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Runtime != time.Hour {
		t.Errorf("Runtime = %v, want 1h", items[0].Runtime)
	}
}

func TestItemsMovieWithoutSourcesKeepsItemContainerAndPlainTitle(t *testing.T) {
	s := newTestService(t, itemsHandler(nil), "k")
	items, err := s.Items(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	m := items[1]
	if m.Title() != "Movie" || m.Container != "avi" || m.VideoCodec != "" {
		t.Errorf("movie = %+v", m)
	}
}

func TestItemsSendsTokenHeaderAndSearchQuery(t *testing.T) {
	var seen http.Request
	s := newTestService(t, itemsHandler(&seen), "secret")
	if _, err := s.Items(context.Background(), "pilot", 7); err != nil {
		t.Fatal(err)
	}
	if got := seen.Header.Get("X-Emby-Token"); got != "secret" {
		t.Errorf("X-Emby-Token = %q, want secret", got)
	}
	q := seen.URL.Query()
	if q.Get("SearchTerm") != "pilot" || q.Get("Limit") != "7" {
		t.Errorf("query = %v", q)
	}
}

func TestItemsErrorsWhenJellyfinAnswersNon200(t *testing.T) {
	s := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}), "bad")
	if _, err := s.Items(context.Background(), "", 10); err == nil {
		t.Fatal("want error on 401")
	}
}

func TestItemReturnsNilWhenUnknown(t *testing.T) {
	s := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Items":[]}`))
	}), "k")
	it, err := s.Item(context.Background(), testID)
	if err != nil || it != nil {
		t.Fatalf("Item = %v, %v; want nil, nil", it, err)
	}
}

// fileServer serves 10 bytes with real Range support and records the request.
func fileServer(seen *http.Request) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = *r
		}
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, "f.mp4", time.Time{}, strings.NewReader("0123456789"))
	})
}

func proxy(s *Service, method, rangeHdr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/jellyfin/stream/"+testID, nil)
	if rangeHdr != "" {
		req.Header.Set("Range", rangeHdr)
	}
	rec := httptest.NewRecorder()
	s.Stream(rec, req, testID)
	return rec
}

func TestStreamForwardsRangeAndReturns206(t *testing.T) {
	var seen http.Request
	s := newTestService(t, fileServer(&seen), "secret")
	rec := proxy(s, http.MethodGet, "bytes=2-5")
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if got := seen.Header.Get("Range"); got != "bytes=2-5" {
		t.Errorf("upstream Range = %q, want bytes=2-5", got)
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Errorf("Content-Range = %q, want bytes 2-5/10", got)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q, want bytes", got)
	}
	if rec.Body.String() != "2345" {
		t.Errorf("body = %q, want 2345", rec.Body.String())
	}
}

func TestStreamSendsTokenAndIdentityEncodingUpstream(t *testing.T) {
	var seen http.Request
	s := newTestService(t, fileServer(&seen), "secret")
	proxy(s, http.MethodGet, "")
	if got := seen.Header.Get("X-Emby-Token"); got != "secret" {
		t.Errorf("X-Emby-Token = %q, want secret", got)
	}
	if got := seen.Header.Get("Accept-Encoding"); got != "identity" {
		t.Errorf("Accept-Encoding = %q, want identity", got)
	}
	if want := "/Videos/" + testID + "/stream"; seen.URL.Path != want {
		t.Errorf("upstream path = %q, want %q", seen.URL.Path, want)
	}
}

func TestStreamDoesNotLeakKeyToBrowser(t *testing.T) {
	s := newTestService(t, fileServer(nil), "secret")
	rec := proxy(s, http.MethodGet, "")
	for k, v := range rec.Header() {
		if strings.Contains(strings.Join(v, ","), "secret") {
			t.Errorf("header %s leaks key", k)
		}
	}
}

func TestStreamMaps404To404(t *testing.T) {
	s := newTestService(t, http.NotFoundHandler(), "k")
	if rec := proxy(s, http.MethodGet, ""); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestStreamMaps500To502(t *testing.T) {
	s := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}), "k")
	if rec := proxy(s, http.MethodGet, ""); rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestStreamMaps401To502(t *testing.T) {
	s := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}), "k")
	if rec := proxy(s, http.MethodGet, ""); rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (upstream auth failure must not reach browser as 401)", rec.Code)
	}
}

func TestStreamReturns502WhenUpstreamUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u, _ := url.Parse(srv.URL)
	srv.Close()
	s := &Service{base: u, key: "k", api: &http.Client{}, stream: &http.Client{}}
	if rec := proxy(s, http.MethodGet, ""); rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestStreamHeadReturnsNoBody(t *testing.T) {
	var seen http.Request
	s := newTestService(t, fileServer(&seen), "k")
	rec := proxy(s, http.MethodHead, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if seen.Method != http.MethodHead {
		t.Errorf("upstream method = %s, want HEAD", seen.Method)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Length"); got != "10" {
		t.Errorf("Content-Length = %q, want 10", got)
	}
}

func TestStreamPassesThrough416(t *testing.T) {
	s := newTestService(t, fileServer(nil), "k")
	rec := proxy(s, http.MethodGet, "bytes=50-60")
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("status = %d, want 416", rec.Code)
	}
}

func TestPosterUsesPrimaryImagePathAndCachesPrivately(t *testing.T) {
	var seen http.Request
	s := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = *r
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("img"))
	}), "k")
	rec := httptest.NewRecorder()
	s.Poster(rec, httptest.NewRequest(http.MethodGet, "/p", nil), testID)
	if want := "/Items/" + testID + "/Images/Primary"; seen.URL.Path != want {
		t.Errorf("path = %q, want %q", seen.URL.Path, want)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.HasPrefix(got, "private") {
		t.Errorf("Cache-Control = %q, want private", got)
	}
}

func TestBaseURLPathPrefixIsKept(t *testing.T) {
	var seen http.Request
	srv := httptest.NewServer(itemsHandler(&seen))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL + "/jf/")
	s := &Service{base: u, api: srv.Client(), stream: srv.Client()}
	if _, err := s.Items(context.Background(), "", 1); err != nil {
		t.Fatal(err)
	}
	if seen.URL.Path != "/jf/Items" {
		t.Errorf("path = %q, want /jf/Items", seen.URL.Path)
	}
	if seen.Header.Get("X-Emby-Token") != "" {
		t.Error("token header sent although key is empty")
	}
}
