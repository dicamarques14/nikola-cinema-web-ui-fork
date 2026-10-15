package embed

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/pkg/errors"
	"github.com/webtor-io/web-ui/models"
	"github.com/webtor-io/web-ui/services/api"
	"github.com/webtor-io/web-ui/services/claims"
	"github.com/webtor-io/web-ui/services/web"

	"github.com/gin-gonic/gin"
	"github.com/webtor-io/web-ui/services/embed"
	"github.com/webtor-io/web-ui/services/job"
)

type PostArgs struct {
	ID            string
	EmbedSettings *models.EmbedSettings
	// Purge: start the job again rather than replay the cached one. The
	// player's own restart after its transcoder session is gone sends it
	// (lib/player/stream-restart.js): the embed's render is cached for the
	// hour, and a replay hands back the dead session.
	Purge bool
}

type PostData struct {
	ID             string
	EmbedSettings  *models.EmbedSettings
	DomainSettings *embed.DomainSettingsData
	Job            *job.Job
}

func (s *Handler) bindPostArgs(c *gin.Context) (*PostArgs, error) {
	rawSettings := c.PostForm("settings")
	var settings models.EmbedSettings
	err := json.Unmarshal([]byte(rawSettings), &settings)
	if err != nil {
		return nil, err
	}
	id := c.Query("id")

	return &PostArgs{
		ID:            id,
		EmbedSettings: &settings,
		Purge:         c.PostForm("purge") == "true",
	}, nil

}

func (s *Handler) post(c *gin.Context) {
	// A placeholder rendered before embeds were switched off can still post.
	if !s.enabled {
		s.off(c)
		return
	}
	tpl := s.tb.Build("embed/post")
	pd := PostData{}
	args, err := s.bindPostArgs(c)
	if err != nil {
		tpl.HTML(http.StatusBadRequest, web.NewContext(c).WithData(pd).WithErr(err))
		return
	}
	pd.ID = args.ID
	u, err := url.Parse(args.EmbedSettings.Referer)
	if err != nil {
		tpl.HTML(http.StatusBadRequest, web.NewContext(c).WithData(pd).WithErr(err))
		return
	}
	domain := u.Hostname()
	dsd, err := s.ds.Get(domain)
	if err != nil {
		tpl.HTML(http.StatusBadRequest, web.NewContext(c).WithData(pd).WithErr(err))
		return
	}
	pd.EmbedSettings = args.EmbedSettings
	pd.DomainSettings = dsd
	uClaims := claims.GetFromContext(c)
	uSessionID := api.GenerateSessionID(c)
	if dsd.Claims != nil {
		uClaims = dsd.Claims
		uSessionID = dsd.SessionID
	}
	c, err = s.api.SetClaims(c, domain, uClaims, uSessionID)
	if err != nil {
		_ = c.AbortWithError(http.StatusInternalServerError, errors.Wrap(err, "failed to set embed claims"))
		return
	}
	embedJob, err := s.jobs.Embed(web.NewContext(c), s.cl, args.EmbedSettings, dsd, args.Purge)
	if err != nil {
		tpl.HTML(http.StatusBadRequest, web.NewContext(c).WithData(pd).WithErr(err))
		return
	}
	pd.Job = embedJob
	tpl.HTML(http.StatusAccepted, web.NewContext(c).WithData(pd))
}
