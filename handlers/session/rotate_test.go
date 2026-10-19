package session

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-contrib/sessions/redis"
	"github.com/gin-gonic/gin"
)

func rotateRouter(t *testing.T, store sessions.Store) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store.Options(sessions.Options{Path: "/", MaxAge: 3600})
	r := gin.New()
	r.Use(sessions.Sessions("session", store))
	r.GET("/set", func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("kept", "yes")
		_ = s.Save()
	})
	r.GET("/signin", func(c *gin.Context) {
		if err := Rotate(c); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		s := sessions.Default(c)
		s.Set("admin", true)
		_ = s.Save()
	})
	r.GET("/read", func(c *gin.Context) {
		s := sessions.Default(c)
		kept, _ := s.Get("kept").(string)
		admin, _ := s.Get("admin").(bool)
		if admin {
			c.String(http.StatusOK, kept+",admin")
			return
		}
		c.String(http.StatusOK, kept)
	})
	return r
}

func req(r *gin.Engine, path, ck string) *httptest.ResponseRecorder {
	q := httptest.NewRequest(http.MethodGet, path, nil)
	if ck != "" {
		q.Header.Set("Cookie", ck)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, q)
	return w
}

func lastCookie(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	v := ""
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" {
			v = c.Name + "=" + c.Value
		}
	}
	if v == "" {
		t.Fatalf("no session cookie (Set-Cookie: %v)", w.Header().Values("Set-Cookie"))
	}
	return v
}

func TestRotateRedisGivesNewIDAndDropsOld(t *testing.T) {
	m := miniredis.RunT(t)
	store, err := redis.NewStore(2, "tcp", m.Addr(), "", []byte("test secret"))
	if err != nil {
		t.Fatal(err)
	}
	r := rotateRouter(t, store)

	before := lastCookie(t, req(r, "/set", ""))
	w := req(r, "/signin", before)
	if w.Code != http.StatusOK {
		t.Fatalf("signin: %d %s", w.Code, w.Body.String())
	}
	if n := len(w.Result().Cookies()); n != 1 {
		t.Errorf("signin sent %d session cookies, want exactly 1", n)
	}
	after := lastCookie(t, w)
	if after == before {
		t.Fatal("session ID was not rotated")
	}
	if got := req(r, "/read", after).Body.String(); got != "yes,admin" {
		t.Fatalf("rotated session: got %q, want values kept plus admin", got)
	}
	// The pre-sign-in ID is dead: it neither carries the privilege nor the
	// old values.
	if got := req(r, "/read", before).Body.String(); strings.Contains(got, "admin") || got != "" {
		t.Fatalf("old session ID still resolves: %q", got)
	}
	if keys := m.Keys(); len(keys) != 1 {
		t.Fatalf("redis keys after rotation: %v, want only the new session", keys)
	}
}

func TestRotateCookieStoreIsNoop(t *testing.T) {
	r := rotateRouter(t, cookie.NewStore([]byte("test secret")))
	before := lastCookie(t, req(r, "/set", ""))
	after := lastCookie(t, req(r, "/signin", before))
	if got := req(r, "/read", after).Body.String(); got != "yes,admin" {
		t.Fatalf("got %q", got)
	}
}

func TestRotateWithoutSessionIsNoop(t *testing.T) {
	m := miniredis.RunT(t)
	store, err := redis.NewStore(2, "tcp", m.Addr(), "", []byte("test secret"))
	if err != nil {
		t.Fatal(err)
	}
	r := rotateRouter(t, store)
	w := req(r, "/signin", "")
	if got := req(r, "/read", lastCookie(t, w)).Body.String(); got != ",admin" {
		t.Fatalf("got %q", got)
	}
}
