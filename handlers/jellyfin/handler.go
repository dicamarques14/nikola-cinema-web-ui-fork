// Package jellyfin mounts the pages that play a Jellyfin library through the
// movi player: a searchable list, the player page, and the Range proxy.
package jellyfin

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/webtor-io/web-ui/services/auth"
	"github.com/webtor-io/web-ui/services/jellyfin"
	"github.com/webtor-io/web-ui/services/template"
	"github.com/webtor-io/web-ui/services/web"
)

const listLimit = 200

type Handler struct {
	svc *jellyfin.Service
	tb  template.Builder[*web.Context]
}

// RegisterHandler mounts /jellyfin for the administrator only. A nil svc (Jellyfin not
// configured) mounts nothing.
func RegisterHandler(r *gin.Engine, tm *template.Manager[*web.Context], svc *jellyfin.Service) {
	if svc == nil {
		return
	}
	h := &Handler{svc: svc, tb: tm.MustRegisterViews("jellyfin/*").WithLayout("main")}
	g := r.Group("/jellyfin")
	g.Use(auth.HasAuth, adminOnly)
	g.GET("", h.index)
	g.GET("/:id", h.play)
	g.GET("/:id/stream", h.stream)
	g.HEAD("/:id/stream", h.stream)
	g.GET("/:id/poster", h.poster)
}

type listData struct {
	Search string
	Items  []jellyfin.Item
	Error  string
}

func (h *Handler) index(c *gin.Context) {
	search := strings.TrimSpace(c.Query("q"))
	d := &listData{Search: search}
	items, err := h.svc.Items(c.Request.Context(), search, listLimit)
	if err != nil {
		log.WithError(err).Warn("jellyfin: failed to list the library")
		d.Error = err.Error()
	}
	d.Items = items
	h.tb.Build("jellyfin/index").HTML(http.StatusOK, web.NewContext(c).WithData(d))
}

type playData struct {
	Item      *jellyfin.Item
	Title     string
	StreamURL string
}

func (h *Handler) play(c *gin.Context) {
	id := c.Param("id")
	if !jellyfin.ValidID(id) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	it, err := h.svc.Item(c.Request.Context(), id)
	if err != nil {
		log.WithError(err).Warn("jellyfin: failed to read an item")
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	if it == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	h.tb.Build("jellyfin/play").HTML(http.StatusOK, web.NewContext(c).WithData(&playData{
		Item:      it,
		Title:     it.Title(),
		StreamURL: "/jellyfin/" + id + "/stream",
	}))
}

func (h *Handler) stream(c *gin.Context) {
	id := c.Param("id")
	if !jellyfin.ValidID(id) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	h.svc.Stream(c.Writer, c.Request, id)
}

func (h *Handler) poster(c *gin.Context) {
	id := c.Param("id")
	if !jellyfin.ValidID(id) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	h.svc.Poster(c.Writer, c.Request, id)
}

// adminOnly answers 404 to anyone but the administrator: the library is the
// operator's own, not something every account of the instance may stream.
func adminOnly(c *gin.Context) {
	if !auth.IsAdmin(c) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Next()
}
