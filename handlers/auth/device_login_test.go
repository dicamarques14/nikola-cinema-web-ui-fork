package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	uuid "github.com/satori/go.uuid"

	"github.com/webtor-io/web-ui/models"
	svcauth "github.com/webtor-io/web-ui/services/auth"
	"github.com/webtor-io/web-ui/services/devicelogin"
)

type deviceEnv struct {
	r *gin.Engine
	m *miniredis.Miniredis
	s *devicelogin.Store
}

// newDeviceRouter wires the device-login routes the way RegisterHandler does,
// minus templates and CSRF (covered by the session package). A request with
// X-Test-Admin carries a signed-in administrator, standing in for the auth
// middleware that runs before these routes in serve.go.
func newDeviceRouter(t *testing.T) *deviceEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	m := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	store := devicelogin.New(cl)

	r := gin.New()
	r.Use(sessions.Sessions("session", cookie.NewStore([]byte("test secret"))))
	r.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Admin") != "" {
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(),
				svcauth.UserContext{}, &models.User{UserID: uuid.NewV4(), Email: "admin@example.com"}))
		}
	})
	h := &Handler{devices: store, passwordFormActive: alwaysActive}
	r.GET("/login", h.login)
	h.registerDeviceLogin(r)
	r.GET("/test/admin", func(c *gin.Context) {
		if v, _ := sessions.Default(c).Get(svcauth.AdminSessionKey).(bool); v {
			c.String(http.StatusOK, "admin")
			return
		}
		c.String(http.StatusOK, "anonymous")
	})
	return &deviceEnv{r: r, m: m, s: store}
}

func (e *deviceEnv) do(method, path, cookie string, form url.Values, admin bool) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if admin {
		req.Header.Set("X-Test-Admin", "1")
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

// pendingToken returns the one device-login token in redis.
func (e *deviceEnv) pendingToken(t *testing.T) (string, *devicelogin.Request) {
	t.Helper()
	keys := e.m.Keys()
	var tok string
	for _, k := range keys {
		if strings.HasPrefix(k, "devlogin:t:") {
			if tok != "" {
				t.Fatalf("more than one device login request: %v", keys)
			}
			tok = strings.TrimPrefix(k, "devlogin:t:")
		}
	}
	if tok == "" {
		t.Fatalf("no device login request in redis: %v", keys)
	}
	req, err := e.s.Get(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	return tok, req
}

func TestDeviceLoginFullFlow(t *testing.T) {
	e := newDeviceRouter(t)

	// The car opens /login: a request is created and bound to its session.
	w := e.do(http.MethodGet, "/login?return-url=/library", "", nil, false)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /login: %d", w.Code)
	}
	car := lastSessionCookie(t, w)
	tok, req := e.pendingToken(t)

	// Reloading /login reuses the same request instead of minting another.
	e.do(http.MethodGet, "/login", car, nil, false)
	if tok2, _ := e.pendingToken(t); tok2 != tok {
		t.Fatal("a reload created a second request")
	}

	if body := e.do(http.MethodGet, "/login/device/status", car, nil, false).Body.String(); !strings.Contains(body, `"pending"`) {
		t.Fatalf("status before approval: %s", body)
	}

	// Claiming before approval goes back to the login page, signed out.
	w = e.do(http.MethodPost, "/login/device", car, url.Values{}, false)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
		t.Fatalf("early claim: %d %q", w.Code, w.Header().Get("Location"))
	}

	// The phone sees the code, then approves.
	w = e.do(http.MethodGet, "/login/approve?t="+url.QueryEscape(tok), "", nil, true)
	if w.Code != http.StatusOK {
		t.Fatalf("approve form: %d", w.Code)
	}
	w = e.do(http.MethodPost, "/login/approve", "", url.Values{"t": {tok}, "code": {req.FormattedCode()}}, true)
	if w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "done=1") {
		t.Fatalf("approve: %d %q", w.Code, w.Header().Get("Location"))
	}
	if body := e.do(http.MethodGet, "/login/device/status", car, nil, false).Body.String(); !strings.Contains(body, `"approved"`) {
		t.Fatalf("status after approval: %s", body)
	}

	// Another browser holding no token cannot ride on the approval.
	if e.do(http.MethodPost, "/login/device", "", url.Values{}, false).Header().Get("Location") != "/login" {
		t.Fatal("a browser without the token claimed the approval")
	}

	// The car claims and lands where it was headed, signed in.
	w = e.do(http.MethodPost, "/login/device", car, url.Values{}, false)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/library" {
		t.Fatalf("claim: %d %q", w.Code, w.Header().Get("Location"))
	}
	signedIn := lastSessionCookie(t, w)
	if body := e.do(http.MethodGet, "/test/admin", signedIn, nil, false).Body.String(); body != "admin" {
		t.Fatalf("after claim: %q", body)
	}

	// Single use: the request is gone.
	if _, err := e.s.Get(context.Background(), tok); err == nil {
		t.Fatal("request survived its claim")
	}
}

func TestDeviceApproveRequiresSignedInAdmin(t *testing.T) {
	e := newDeviceRouter(t)
	e.do(http.MethodGet, "/login", "", nil, false)
	tok, req := e.pendingToken(t)
	w := e.do(http.MethodPost, "/login/approve", "", url.Values{"t": {tok}, "code": {req.Code}}, false)
	if w.Code == http.StatusFound && strings.Contains(w.Header().Get("Location"), "done=1") {
		t.Fatal("an anonymous visitor approved a device login")
	}
	if st, _ := e.s.Status(context.Background(), tok); st != devicelogin.StatusPending {
		t.Fatalf("status: %v, want pending", st)
	}
}

func TestDeviceApproveRejectsWrongCode(t *testing.T) {
	e := newDeviceRouter(t)
	e.do(http.MethodGet, "/login", "", nil, false)
	tok, _ := e.pendingToken(t)
	w := e.do(http.MethodPost, "/login/approve", "", url.Values{"t": {tok}, "code": {"ZZZZZZ"}}, true)
	if w.Code != http.StatusNotFound {
		t.Fatalf("wrong code: %d, want 404", w.Code)
	}
}

func TestDeviceApproveByTypedCode(t *testing.T) {
	e := newDeviceRouter(t)
	e.do(http.MethodGet, "/login", "", nil, false)
	_, req := e.pendingToken(t)
	w := e.do(http.MethodGet, "/login/approve?code="+strings.ToLower(req.FormattedCode()), "", nil, true)
	if w.Code != http.StatusOK {
		t.Fatalf("lookup by typed code: %d", w.Code)
	}
	if w := e.do(http.MethodGet, "/login/approve?code=XXXXXX", "", nil, true); w.Code != http.StatusNotFound {
		t.Fatalf("unknown code: %d", w.Code)
	}
}

func TestDeviceStatusWithoutRequestIsExpired(t *testing.T) {
	e := newDeviceRouter(t)
	if body := e.do(http.MethodGet, "/login/device/status", "", nil, false).Body.String(); !strings.Contains(body, `"expired"`) {
		t.Fatalf("status: %s", body)
	}
}

func TestDeviceRoutesInertWithoutPasswordMode(t *testing.T) {
	e := newDeviceRouter(t)
	// Rebuild with the gate closed (SuperTokens configured / no password).
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(sessions.Sessions("session", cookie.NewStore([]byte("test secret"))))
	h := &Handler{devices: e.s, passwordFormActive: func(*gin.Context) bool { return false }}
	h.registerDeviceLogin(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login/device/status", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status route answered %d without password mode", w.Code)
	}
}

func TestBaseURL(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	defer gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ proto, want string }{
		{"", "https://webtor.lan"},
		{"http", "http://webtor.lan"},
		{"https, http", "https://webtor.lan"},
		{"gopher", "https://webtor.lan"},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "http://webtor.lan/login", nil)
		if tc.proto != "" {
			c.Request.Header.Set("X-Forwarded-Proto", tc.proto)
		}
		if got := baseURL(c); got != tc.want {
			t.Errorf("proto %q: got %q, want %q", tc.proto, got, tc.want)
		}
	}
}

// lastSessionCookie is the session cookie a browser keeps: a request that
// saves the session twice (return-url, then the device token) sends two
// Set-Cookie headers, and the last one wins.
func lastSessionCookie(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	v := ""
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" {
			v = c.Name + "=" + c.Value
		}
	}
	if v == "" {
		t.Fatalf("no session cookie in response (Set-Cookie: %v)", w.Header().Values("Set-Cookie"))
	}
	return v
}
