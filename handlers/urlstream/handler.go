// Package urlstream serves Stremio addon streams that are plain HTTP(S)
// files: the open endpoint Discover posts to, the player page, the Range
// proxy, the import status, and the /urlseed web seed torrent-web-seeder
// reads (docs/url_streams.md).
package urlstream

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	uuid "github.com/satori/go.uuid"
	log "github.com/sirupsen/logrus"
	cs "github.com/webtor-io/common-services"

	"github.com/webtor-io/web-ui/models"
	"github.com/webtor-io/web-ui/services/auth"
	"github.com/webtor-io/web-ui/services/i18n"
	"github.com/webtor-io/web-ui/services/template"
	"github.com/webtor-io/web-ui/services/urlsource"
	"github.com/webtor-io/web-ui/services/web"
)

type Handler struct {
	svc *urlsource.Service
	pg  *cs.PG
	tb  template.Builder[*web.Context]
}

// RegisterSeedHandler mounts /urlseed. It carries its own credential (an
// HMAC in the path) and is read by torrent-web-seeder, which has no session:
// mount it before the session, auth and only-authorized middleware.
func RegisterSeedHandler(r *gin.Engine, svc *urlsource.Service, pg *cs.PG) {
	if svc == nil {
		return
	}
	h := &Handler{svc: svc, pg: pg}
	r.GET("/urlseed/:id/:sig/*name", h.seed)
	r.HEAD("/urlseed/:id/:sig/*name", h.seed)
}

// RegisterHandler mounts the viewer's routes, all behind a login.
func RegisterHandler(r *gin.Engine, tm *template.Manager[*web.Context], svc *urlsource.Service, pg *cs.PG) {
	if svc == nil {
		return
	}
	h := &Handler{svc: svc, pg: pg, tb: tm.MustRegisterViews("urlstream/*").WithLayout("main")}
	g := r.Group("/url")
	g.Use(auth.HasAuth)
	g.POST("/open", h.open)
	g.GET("/:id", h.page)
	g.GET("/:id/stream", h.stream)
	g.HEAD("/:id/stream", h.stream)
	g.GET("/:id/status", h.status)
	g.POST("/:id/import", h.importNow)
}

type openBody struct {
	AddonID  string `json:"addon_id"`
	Type     string `json:"type"`
	ID       string `json:"id"`
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Title    string `json:"title"`
}

func (h *Handler) open(c *gin.Context) {
	var b openBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	addonID, err := uuid.FromString(b.AddonID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad addon id"})
		return
	}
	u := auth.GetUserFromContext(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	src, err := h.svc.Open(ctx, u.ID, &urlsource.OpenRequest{
		AddonID: addonID, Type: b.Type, ID: b.ID, URL: b.URL, Filename: b.Filename, Size: b.Size, Title: b.Title,
	})
	switch {
	case errors.Is(err, urlsource.ErrUnsupported):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "unsupported"})
		return
	case errors.Is(err, urlsource.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	case err != nil:
		log.WithError(err).Warn("urlstream: open failed")
		c.JSON(http.StatusBadGateway, gin.H{"error": "addon_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":       src.ID.String(),
		"play_url": i18n.LangPath(i18n.GetLang(c), "/url/"+src.ID.String()),
	})
}

// load is the viewer's own row named by :id, or an abort.
func (h *Handler) load(c *gin.Context) *models.UrlSource {
	id, err := uuid.FromString(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return nil
	}
	db := h.pg.Get()
	if db == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return nil
	}
	u := auth.GetUserFromContext(c)
	src, err := models.GetUserUrlSource(c.Request.Context(), db, u.ID, id)
	if err != nil {
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return nil
	}
	if src == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return nil
	}
	return src
}

type pageData struct {
	ID            string
	Title         string
	Filename      string
	StreamURL     string
	StatusURL     string
	ImportEnabled bool
	ImportURL     string
}

func (h *Handler) page(c *gin.Context) {
	src := h.load(c)
	if src == nil {
		return
	}
	title := src.Title
	if title == "" {
		title = src.Filename
	}
	h.tb.Build("urlstream/play").HTML(http.StatusOK, web.NewContext(c).WithData(&pageData{
		ID:            src.ID.String(),
		Title:         title,
		Filename:      src.Filename,
		StreamURL:     "/url/" + src.ID.String() + "/stream",
		StatusURL:     "/url/" + src.ID.String() + "/status",
		ImportEnabled: h.svc.ImportEnabled(),
		ImportURL:     "/url/" + src.ID.String() + "/import",
	}))
}

// stream is the player's source: the staged copy once there is one, the
// origin through the proxy until then.
func (h *Handler) stream(c *gin.Context) {
	src := h.load(c)
	if src == nil {
		return
	}
	if p := h.svc.StagedFile(src); p != "" {
		serveFile(c, h.svc, p, src.Filename)
		return
	}
	h.svc.Proxy(c.Writer, c.Request, src)
}

func serveFile(c *gin.Context, svc *urlsource.Service, p, name string) {
	f, err := os.Open(p)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	svc.Touch(p)
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Type", "application/octet-stream")
	http.ServeContent(c.Writer, c.Request, name, fi.ModTime(), f)
}

type statusView struct {
	Status        string `json:"status"`
	Phase         string `json:"phase,omitempty"`
	Done          int64  `json:"done"`
	Total         int64  `json:"total"`
	Error         string `json:"error,omitempty"`
	ImportEnabled bool   `json:"import_enabled"`
	InfoHash      string `json:"info_hash,omitempty"`
	ResourceURL   string `json:"resource_url,omitempty"`
	Path          string `json:"path,omitempty"`
}

// importNow is the player page's Import button: it starts the import of the
// viewer's source (a no-op when one runs or is done) and answers the status.
// POST, so the session's CSRF check applies.
func (h *Handler) importNow(c *gin.Context) {
	src := h.load(c)
	if src == nil {
		return
	}
	if !h.svc.ImportEnabled() {
		c.JSON(http.StatusConflict, gin.H{"error": "import_disabled"})
		return
	}
	h.svc.StartImport(src)
	c.JSON(http.StatusOK, h.statusOf(c, src))
}

func (h *Handler) status(c *gin.Context) {
	src := h.load(c)
	if src == nil {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, h.statusOf(c, src))
}

func (h *Handler) statusOf(c *gin.Context, src *models.UrlSource) statusView {
	v := statusView{Status: string(src.Status), Total: src.Size, ImportEnabled: h.svc.ImportEnabled()}
	if p := h.svc.Progress(src.ID); p != nil {
		v.Status = string(models.UrlSourceImporting)
		v.Phase, v.Done, v.Total = p.Phase(), p.Done(), p.Total()
	} else if src.Status == models.UrlSourceImporting {
		// Not running here: a process that died mid-import.
		v.Status = string(models.UrlSourceFailed)
	}
	if v.Status == string(models.UrlSourceFailed) {
		v.Error = src.Error
	}
	if src.Status == models.UrlSourceImported && src.InfoHash != nil {
		v.InfoHash = *src.InfoHash
		v.ResourceURL = i18n.LangPath(i18n.GetLang(c), "/"+*src.InfoHash) + "?file-idx=0#action=stream"
		v.Path = "/" + src.Filename
	}
	return v
}

// seed is the torrent's web seed. Whoever holds the URL may read the file;
// the HMAC in it is the whole check.
func (h *Handler) seed(c *gin.Context) {
	id, err := uuid.FromString(c.Param("id"))
	if err != nil || !h.svc.CheckSeedSig(id, c.Param("sig")) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	db := h.pg.Get()
	if db == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	src, err := models.GetUrlSource(c.Request.Context(), db, id)
	if err != nil {
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	if src == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if p := h.svc.StagedFile(src); p != "" {
		serveFile(c, h.svc, p, src.Filename)
		return
	}
	// Evicted: from the origin, renewing the link when it has expired. A
	// different copy than the one hashed fails its piece check in the
	// seeder; nothing wrong reaches a viewer.
	h.svc.Proxy(c.Writer, c.Request, src)
}
