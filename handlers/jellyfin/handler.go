// Package jellyfin mounts the pages that play a Jellyfin library through the
// movi player: a searchable list, the player page, and the Range proxy.
package jellyfin

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	cs "github.com/webtor-io/common-services"

	"github.com/webtor-io/web-ui/models"
	"github.com/webtor-io/web-ui/services/auth"
	"github.com/webtor-io/web-ui/services/jellyfin"
	"github.com/webtor-io/web-ui/services/streamprefs"
	"github.com/webtor-io/web-ui/services/template"
	"github.com/webtor-io/web-ui/services/web"
)

const listLimit = 200

type Handler struct {
	svc   *jellyfin.Service
	tb    template.Builder[*web.Context]
	pg    *cs.PG
	addon *addonSubs
}

// RegisterHandler mounts /jellyfin for the administrator only. A nil svc (Jellyfin not
// configured) mounts nothing. pg keeps Continue watching (nil: no record);
// addonClient is the Stremio addon client the addon subtitles go through
// (nil: none).
func RegisterHandler(r *gin.Engine, tm *template.Manager[*web.Context], svc *jellyfin.Service, pg *cs.PG, prefs *streamprefs.Service, addonClient *http.Client) {
	if svc == nil {
		return
	}
	h := &Handler{svc: svc, tb: tm.MustRegisterViews("jellyfin/*").WithLayout("main"), pg: pg, addon: newAddonSubs(pg, prefs, addonClient)}
	g := r.Group("/jellyfin")
	g.Use(auth.HasAuth, adminOnly)
	g.GET("", h.index)
	g.GET("/show/:id", h.show)
	g.GET("/:id", h.play)
	g.GET("/:id/stream", h.stream)
	g.HEAD("/:id/stream", h.stream)
	g.GET("/:id/poster", h.poster)
	g.GET("/:id/subtitles/:file", h.subtitle)
	g.GET("/:id/addon-subtitles", h.addonList)
	g.GET("/:id/addon-subtitles/:file", h.addonFileGet)
}

type listData struct {
	Search string
	// Items are the search results; Shows and Movies the library otherwise.
	Items  []jellyfin.Item
	Shows  []jellyfin.Item
	Movies []jellyfin.Item
	Error  string
}

func (h *Handler) index(c *gin.Context) {
	search := strings.TrimSpace(c.Query("q"))
	d := &listData{Search: search}
	var err error
	if search != "" {
		d.Items, err = h.svc.Items(c.Request.Context(), search, listLimit)
	} else if d.Shows, err = h.svc.Shows(c.Request.Context()); err == nil {
		d.Movies, err = h.svc.Movies(c.Request.Context())
	}
	if err != nil {
		log.WithError(err).Warn("jellyfin: failed to list the library")
		d.Error = err.Error()
	}
	h.tb.Build("jellyfin/index").HTML(http.StatusOK, web.NewContext(c).WithData(d))
}

type showData struct {
	Show    *jellyfin.Item
	Seasons []jellyfin.Season
}

// show lists a series' episodes on disk, season by season.
func (h *Handler) show(c *gin.Context) {
	id := c.Param("id")
	if !jellyfin.ValidID(id) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	it, err := h.svc.Item(c.Request.Context(), id)
	if err != nil {
		log.WithError(err).Warn("jellyfin: failed to read a show")
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	if it == nil || !it.IsSeries() {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	eps, err := h.svc.Episodes(c.Request.Context(), id)
	if err != nil {
		log.WithError(err).Warn("jellyfin: failed to list episodes")
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	h.tb.Build("jellyfin/show").HTML(http.StatusOK, web.NewContext(c).WithData(&showData{
		Show:    it,
		Seasons: jellyfin.GroupSeasons(eps),
	}))
}

type playData struct {
	Item      *jellyfin.Item
	Title     string
	StreamURL string
	// Prev and Next are the neighbouring episodes on disk, nil at either end
	// and for a movie.
	Prev *jellyfin.Item
	Next *jellyfin.Item
	// Subtitles are the text tracks the picker offers, Start the one shown
	// when the page opens (nil: none), ImageSubtitles how many could not be.
	Subtitles      []subtitleChip
	Start          string
	ImageSubtitles int
	// Audio are the file's audio tracks as picker chips, StartAudio the chip
	// id of the one playing when the page opens.
	Audio      []audioChip
	StartAudio string
}

// audioChip is an audio track as the picker renders it.
type audioChip struct {
	jellyfin.Audio
	ID       string
	Playable bool
}

// subtitleChip is a text subtitle as the picker renders it.
type subtitleChip struct {
	jellyfin.Subtitle
	ID  string
	Src string
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
	if it != nil && it.IsSeries() {
		c.Redirect(http.StatusFound, it.Href())
		return
	}
	// A missing (virtual) episode has no file: Jellyfin answers 400 to its
	// stream, so there is nothing to play.
	if it == nil || it.Virtual {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	d := &playData{
		Item:           it,
		Title:          it.Title(),
		StreamURL:      "/jellyfin/" + id + "/stream",
		ImageSubtitles: it.ImageSubtitles(),
	}
	for _, s := range it.TextSubtitles() {
		d.Subtitles = append(d.Subtitles, subtitleChip{
			Subtitle: s,
			ID:       s.ID(),
			Src:      "/jellyfin/" + id + "/subtitles/" + strconv.Itoa(s.Index) + ".vtt",
		})
	}
	if s, ok := it.StartSubtitle(); ok {
		d.Start = s.ID()
	}
	for _, a := range it.Audio {
		d.Audio = append(d.Audio, audioChip{Audio: a, ID: a.ID(), Playable: a.Playable()})
	}
	if a, ok := it.StartAudio(); ok {
		d.StartAudio = a.ID()
	}
	if it.Type == "Episode" && jellyfin.ValidID(it.SeriesID) {
		// The way on is a nicety: a failed listing leaves the buttons out.
		if eps, err := h.svc.Episodes(c.Request.Context(), it.SeriesID); err != nil {
			log.WithError(err).Warn("jellyfin: failed to list the neighbouring episodes")
		} else {
			d.Prev, d.Next = jellyfin.Neighbours(eps, it.ID)
		}
	}
	h.record(c, it, d.Next)
	h.tb.Build("jellyfin/play").HTML(http.StatusOK, web.NewContext(c).WithData(d))
}

// record notes what Continue watching shows for this item (the player
// reports the position itself, PUT /watch/position). Best effort.
func (h *Handler) record(c *gin.Context, it *jellyfin.Item, next *jellyfin.Item) {
	user := auth.GetUserFromContext(c)
	if h.pg == nil || !user.HasAuth() {
		return
	}
	db := h.pg.Get()
	if db == nil {
		return
	}
	w := &models.JellyfinWatch{
		UserID: user.ID, ItemID: it.ID, Title: it.Name, Series: it.Series, ImageID: it.ImageID,
	}
	if it.Type == "Episode" {
		w.Code = it.Code()
	}
	if next != nil {
		w.NextItemID, w.NextTitle, w.NextCode = next.ID, next.Name, next.Code()
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if err := models.TouchJellyfinItem(ctx, db, w); err != nil {
		log.WithError(err).Warn("jellyfin: failed to record the item for Continue watching")
	}
}

// subtitleFile is the route's last segment: a stream index and ".vtt".
var subtitleFile = regexp.MustCompile(`^([0-9]{1,4})\.vtt$`)

// subtitle answers one text subtitle track of an item as WebVTT. The index
// must be a text track of the item's first version (what the player plays),
// so nothing but a known stream reaches Jellyfin.
func (h *Handler) subtitle(c *gin.Context) {
	id := c.Param("id")
	m := subtitleFile.FindStringSubmatch(c.Param("file"))
	if !jellyfin.ValidID(id) || m == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	index, _ := strconv.Atoi(m[1])
	it, err := h.svc.Item(c.Request.Context(), id)
	if err != nil {
		log.WithError(err).Warn("jellyfin: failed to read an item")
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	if it == nil || it.MediaSourceID == "" {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if _, ok := it.Subtitle(index); !ok {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	h.svc.Subtitle(c.Writer, c.Request, id, it.MediaSourceID, index)
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
