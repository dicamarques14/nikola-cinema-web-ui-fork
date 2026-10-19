package auth

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"github.com/webtor-io/web-ui/services/auth"
	"github.com/webtor-io/web-ui/services/devicelogin"
	"github.com/webtor-io/web-ui/services/web"
)

// deviceTokenKey is where the waiting browser keeps its device-login token.
// The token never leaves that session except inside the QR link, and the
// claim reads it from here, so only the browser that showed the QR code can
// be signed in by approving it.
const deviceTokenKey = "device-login-token"

// DeviceLoginData drives the "sign in with your phone" panel on the password
// page.
type DeviceLoginData struct {
	QR         template.HTML
	Code       string
	ApproveURL string
	// ManualURL is the approve page without a token, for typing the code on
	// a device that cannot scan.
	ManualURL string
}

// ApproveData drives templates/views/auth/approve.html.
type ApproveData struct {
	Token     string
	Code      string
	UserAgent string
	Err       string
	Done      bool
}

type deviceStatusResponse struct {
	Status devicelogin.Status `json:"status"`
}

func (s *Handler) registerDeviceLogin(r *gin.Engine) {
	if s.devices == nil {
		return
	}
	r.GET("/login/device/status", s.deviceStatus)
	r.POST("/login/device", s.deviceClaim)
	// The approving device must already be the administrator.
	g := r.Group("/login/approve")
	g.Use(auth.HasAuth)
	g.GET("", s.approveForm)
	g.POST("", s.approve)
}

func (s *Handler) deviceActive(c *gin.Context) bool {
	return s.devices != nil && s.passwordFormActive != nil && s.passwordFormActive(c)
}

// deviceRequest returns this browser's live request, creating one
// (and remembering its token in the session) when there is none. Reusing it
// keeps a reload of /login from minting a new code every time.
func (s *Handler) deviceRequest(c *gin.Context) (*devicelogin.Request, error) {
	session := sessions.Default(c)
	if t, ok := session.Get(deviceTokenKey).(string); ok && t != "" {
		req, err := s.devices.Get(c.Request.Context(), t)
		// An approved one is kept too: a reload between the approval and the
		// claim must not throw the approval away.
		if err == nil {
			return req, nil
		}
		if err != nil && !errors.Is(err, devicelogin.ErrNotFound) {
			return nil, err
		}
	}
	req, err := s.devices.Create(c.Request.Context(), c.Request.UserAgent())
	if err != nil {
		return nil, err
	}
	session.Set(deviceTokenKey, req.Token)
	if err := session.Save(); err != nil {
		return nil, err
	}
	return req, nil
}

// deviceLoginData builds the QR panel. A failure only hides the panel: the
// password form next to it still works.
func (s *Handler) deviceLoginData(c *gin.Context) *DeviceLoginData {
	if !s.deviceActive(c) {
		return nil
	}
	req, err := s.deviceRequest(c)
	if err != nil {
		log.WithError(err).Warn("failed to start device login")
		return nil
	}
	base := baseURL(c)
	approve := base + "/login/approve?t=" + url.QueryEscape(req.Token)
	qr, err := devicelogin.QRSVG(approve)
	if err != nil {
		log.WithError(err).Warn("failed to render device login QR code")
		return nil
	}
	return &DeviceLoginData{
		QR:         template.HTML(qr), //nolint:gosec // generated SVG, no user input
		Code:       req.FormattedCode(),
		ApproveURL: approve,
		ManualURL:  base + "/login/approve",
	}
}

// baseURL is the origin the browser used to reach us, so the QR code points
// the phone at the same address the car is using. Behind a TLS-terminating
// proxy the scheme comes from X-Forwarded-Proto; without one, release mode
// assumes https, since its session cookie is Secure and cannot work over
// plain http anyway.
func baseURL(c *gin.Context) string {
	scheme := "https"
	if p := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0]); p == "http" || p == "https" {
		scheme = p
	} else if c.Request.TLS == nil && gin.IsDebugging() {
		scheme = "http"
	}
	return scheme + "://" + c.Request.Host
}

// deviceStatus is what the waiting browser polls.
func (s *Handler) deviceStatus(c *gin.Context) {
	if !s.deviceActive(c) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "no-store")
	t, _ := sessions.Default(c).Get(deviceTokenKey).(string)
	if t == "" {
		c.JSON(http.StatusOK, deviceStatusResponse{Status: devicelogin.StatusExpired})
		return
	}
	st, err := s.devices.Status(c.Request.Context(), t)
	if err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	c.JSON(http.StatusOK, deviceStatusResponse{Status: st})
}

// deviceClaim signs the waiting browser in once its request is approved.
func (s *Handler) deviceClaim(c *gin.Context) {
	if !s.deviceActive(c) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	t, _ := sessions.Default(c).Get(deviceTokenKey).(string)
	if err := s.devices.Claim(c.Request.Context(), t); err != nil {
		if errors.Is(err, devicelogin.ErrNotApproved) || errors.Is(err, devicelogin.ErrNotFound) {
			// Not approved yet, or expired: back to the login page, which
			// shows the same code or a fresh one.
			c.Redirect(http.StatusFound, "/login")
			return
		}
		log.WithError(err).Error("failed to claim device login")
		s.renderPassword(c, http.StatusServiceUnavailable, PasswordLoginData{Err: "auth.password.sessionFailed"})
		return
	}
	returnURL, err := s.signIn(c)
	if err != nil {
		s.renderPassword(c, http.StatusInternalServerError, PasswordLoginData{Err: "auth.password.sessionFailed"})
		return
	}
	c.Redirect(http.StatusFound, returnURL)
}

func (s *Handler) renderApprove(c *gin.Context, code int, data ApproveData) {
	if s.tb == nil {
		c.Status(code)
		return
	}
	s.tb.Build("auth/approve").HTML(code, web.NewContext(c).WithData(data))
}

// approveForm shows the approving device what it is about to sign in: the
// code (to compare with the waiting screen) and that browser's user agent.
// Without a token it asks for the code instead.
func (s *Handler) approveForm(c *gin.Context) {
	if !s.deviceActive(c) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if c.Query("done") != "" {
		s.renderApprove(c, http.StatusOK, ApproveData{Done: true})
		return
	}
	ctx := c.Request.Context()
	t := c.Query("t")
	if t == "" && c.Query("code") != "" {
		var err error
		t, err = s.devices.TokenByCode(ctx, c.Query("code"))
		if err != nil {
			s.renderApprove(c, http.StatusNotFound, ApproveData{Err: "auth.device.notFound"})
			return
		}
	}
	if t == "" {
		s.renderApprove(c, http.StatusOK, ApproveData{})
		return
	}
	req, err := s.devices.Get(ctx, t)
	if err != nil {
		s.renderApprove(c, http.StatusNotFound, ApproveData{Err: "auth.device.notFound"})
		return
	}
	s.renderApprove(c, http.StatusOK, ApproveData{Token: t, Code: req.FormattedCode(), UserAgent: req.UserAgent})
}

func (s *Handler) approve(c *gin.Context) {
	if !s.deviceActive(c) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	err := s.devices.Approve(c.Request.Context(), c.PostForm("t"), c.PostForm("code"))
	switch {
	case err == nil:
		c.Redirect(http.StatusFound, "/login/approve?done=1")
	case errors.Is(err, devicelogin.ErrNotFound), errors.Is(err, devicelogin.ErrCodeMismatch):
		s.renderApprove(c, http.StatusNotFound, ApproveData{Err: "auth.device.notFound"})
	default:
		log.WithError(err).Error("failed to approve device login")
		s.renderApprove(c, http.StatusServiceUnavailable, ApproveData{Err: "auth.device.failed"})
	}
}
