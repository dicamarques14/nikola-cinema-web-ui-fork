package resource

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func searchCtx(t *testing.T, form url.Values) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request = req
	return c
}

// NH-3: the navbar's one search box sends titles to Discover search and
// everything that names a torrent (magnet, infohash, .torrent URL) or a URL
// to the resource flow, unchanged.
func TestSearchBoxRoutesTitlesToDiscover(t *testing.T) {
	const hash = "08ada5a7a6183aae1e09d831df6748d566095a10"
	for _, tc := range []struct {
		name   string
		form   url.Values
		want   string
		search bool
	}{
		{"title", url.Values{"search": {"1"}, "resource": {"  The Matrix "}}, "The Matrix", true},
		{"title with symbols", url.Values{"search": {"1"}, "resource": {"Tom & Jerry"}}, "Tom & Jerry", true},
		{"magnet", url.Values{"search": {"1"}, "resource": {"magnet:?xt=urn:btih:" + hash}}, "", false},
		{"infohash", url.Values{"search": {"1"}, "resource": {hash}}, "", false},
		{"torrent url", url.Values{"search": {"1"}, "resource": {"https://example.com/a.torrent"}}, "", false},
		{"web page", url.Values{"search": {"1"}, "resource": {"https://example.com/watch"}}, "", false},
		{"empty", url.Values{"search": {"1"}, "resource": {"   "}}, "", false},
		// Not the search box: a magnet-only form keeps its free-text error.
		{"home/tool form", url.Values{"resource": {"The Matrix"}}, "", false},
		{"tool instruction", url.Values{"search": {"1"}, "resource": {"The Matrix"}, "instruction": {"magnet-to-torrent"}}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := searchTitle(searchCtx(t, tc.form))
			if ok != tc.search || got != tc.want {
				t.Errorf("searchTitle = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.search)
			}
		})
	}
}

// An uploaded .torrent from the search box's upload button is a resource,
// never a search.
func TestSearchBoxUploadIsNotASearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("search", "1")
	fw, _ := mw.CreateFormFile("resource", "a.torrent")
	_, _ = fw.Write([]byte("d4:infod4:name1:aee"))
	_ = mw.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", &body)
	c.Request.Header.Set("Content-Type", mw.FormDataContentType())
	if q, ok := searchTitle(c); ok {
		t.Errorf("an upload was routed to search as %q", q)
	}
}

// The redirect itself: 303 to Discover search with the title escaped, so the
// async form flow (lib/async.js follows it and pushes the final URL) lands
// on /discover?search=… with the query intact.
func TestSearchBoxRedirectsToDiscover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	r.POST("/", h.post)
	form := url.Values{"search": {"1"}, "resource": {"Tom & Jerry"}}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/discover?search=Tom+%26+Jerry" {
		t.Errorf("Location = %q", loc)
	}
}
