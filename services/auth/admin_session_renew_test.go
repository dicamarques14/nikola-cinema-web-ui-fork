package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

func newRenewRouter(now *time.Time) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test secret"))
	store.Options(sessions.Options{Path: "/", MaxAge: 3600})
	r.Use(sessions.Sessions("session", store))
	r.Use(renewAdminSession(func() time.Time { return *now }))
	r.GET("/signin", func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set(AdminSessionKey, true)
		s.Set(AdminRenewedAtKey, now.Unix())
		_ = s.Save()
		c.Status(http.StatusOK)
	})
	r.GET("/page", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func get(r *gin.Engine, path, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func cookieOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatal("no session cookie")
	return ""
}

func TestRenewAdminSessionOncePerInterval(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := newRenewRouter(&now)
	ck := cookieOf(t, get(r, "/signin", ""))

	// Within the interval: no write, so no Set-Cookie.
	now = now.Add(time.Hour)
	if w := get(r, "/page", ck); len(w.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("renewed too early: %v", w.Header().Values("Set-Cookie"))
	}

	// After the interval: the session is re-saved with a full Max-Age.
	now = now.Add(AdminRenewInterval)
	w := get(r, "/page", ck)
	sc := strings.Join(w.Header().Values("Set-Cookie"), " ")
	if !strings.Contains(sc, "session=") || !strings.Contains(sc, "Max-Age=3600") {
		t.Fatalf("expected a renewed session cookie with Max-Age, got %q", sc)
	}
}

func TestRenewAdminSessionIgnoresAnonymous(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := newRenewRouter(&now)
	if w := get(r, "/page", ""); len(w.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("anonymous request must not write a session: %v", w.Header().Values("Set-Cookie"))
	}
}
