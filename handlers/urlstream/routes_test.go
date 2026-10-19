package urlstream

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// The /url and /urlseed routes share the root with the resource page's
// /:resource_id routes (handlers/resource). gin panics on a conflict at
// registration, so this registers the same shapes in serve.go's order and
// checks each path reaches its own handler.
func TestRoutesCoexistWithResourcePage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	name := func(n string) gin.HandlerFunc { return func(c *gin.Context) { c.String(200, n) } }
	r.GET("/urlseed/:id/:sig/*name", name("seed"))
	r.GET("/share", name("share"))
	r.GET("/:resource_id/status", name("resource-status"))
	r.GET("/:resource_id", name("resource"))
	g := r.Group("/url")
	g.POST("/open", name("open"))
	g.GET("/:id", name("page"))
	g.GET("/:id/stream", name("stream"))
	g.GET("/:id/status", name("status"))
	g.POST("/:id/import", name("import"))
	for path, want := range map[string]string{
		"/urlseed/a/b/Film%20(2024).mkv": "seed",
		"/url/x":                         "page",
		"/url/x/stream":                  "stream",
		"/url/x/status":                  "status",
		"/0123abcd":                      "resource",
		"/0123abcd/status":               "resource-status",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Body.String() != want {
			t.Errorf("%s reached %q (%d), want %q", path, w.Body.String(), w.Code, want)
		}
	}
	for path, want := range map[string]string{"/url/open": "open", "/url/x/import": "import"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Body.String() != want {
			t.Errorf("POST %s reached %q (%d), want %q", path, w.Body.String(), w.Code, want)
		}
	}
}
