package resource

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/webtor-io/web-ui/handlers/common"
	"github.com/webtor-io/web-ui/jobs/scripts"
	"github.com/webtor-io/web-ui/models"
	sv "github.com/webtor-io/web-ui/services/common"
	"github.com/webtor-io/web-ui/services/i18n"
	"github.com/webtor-io/web-ui/services/web"

	"github.com/gin-gonic/gin"
	"github.com/webtor-io/web-ui/services/api"
	"github.com/webtor-io/web-ui/services/job"
)

type PostArgs struct {
	File        []byte
	Query       string
	Instruction string
	HintVideoID string
	Claims      *api.Claims
	MagnetWait  time.Duration
	Debug       string
}

func (s *Handler) bindArgs(c *gin.Context) (*PostArgs, error) {
	file, _ := c.FormFile("resource")
	instruction, _ := c.GetPostForm("instruction")
	query, _ := c.GetPostForm("resource")
	if query == "" && strings.HasPrefix(c.Request.URL.Path, "/magnet") {
		query = strings.TrimPrefix(c.Request.URL.Path, "/") + c.Request.URL.RawQuery
	}
	if query != "" {
		if _, _, err := sv.ResolveQueryHash(query); err != nil {
			return &PostArgs{Query: query}, errors.Wrapf(err, "wrong resource provided query=%v", query)
		}
	}

	if file == nil && query == "" {
		return nil, errors.Errorf("no resource provided")
	}

	var fd []byte

	if file != nil {
		f, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer func(f multipart.File) {
			_ = f.Close()
		}(f)
		fd, err = io.ReadAll(f)
		if err != nil {
			return nil, err
		}
	}

	hintVideoID, _ := c.GetPostForm("hint_video_id")

	// "magnet-wait=long" is the dead-magnet card's retry: ten minutes instead
	// of one. Only that value means anything; the ordinary submit has none.
	var magnetWait time.Duration
	if v, _ := c.GetPostForm("magnet-wait"); v == "long" {
		magnetWait = scripts.MagnetWaitLong
	}
	// Dev-only failure playback; the query form works on the magnet GET route
	// too (/magnet:?xt=...&debug=magnet_dead).
	debug := ""
	if gin.Mode() != gin.ReleaseMode {
		if v, ok := c.GetPostForm("debug"); ok {
			debug = v
		} else {
			debug = c.Query("debug")
		}
	}

	return &PostArgs{
		File:        fd,
		Query:       query,
		Claims:      api.GetClaimsFromContext(c),
		Instruction: instruction,
		HintVideoID: hintVideoID,
		MagnetWait:  magnetWait,
		Debug:       debug,
	}, nil
}

// searchField marks a submit from the navbar's one search box (NH-3,
// partials/nav.html). Only that box routes titles: the box takes a title, a
// magnet, an infohash or a URL in one field, and a title there means "find
// this in Discover", not the "that is not a magnet" error the tool pages'
// magnet-only forms still answer free text with.
const searchField = "search"

// searchTitle reports the query a search-box submit should send to Discover
// search: free text (sv.ErrQueryFreeText) from the box, with no file and no
// tool instruction. Magnets, infohashes and URLs (a .torrent link, a web
// page) fall through to the resource flow and its existing messages.
func searchTitle(c *gin.Context) (string, bool) {
	if v, _ := c.GetPostForm(searchField); v == "" {
		return "", false
	}
	if inst, _ := c.GetPostForm("instruction"); inst != "" {
		return "", false
	}
	if f, _ := c.FormFile("resource"); f != nil {
		return "", false
	}
	q, _ := c.GetPostForm("resource")
	q = strings.TrimSpace(q)
	if q == "" {
		return "", false
	}
	if _, _, err := sv.ResolveQueryHash(q); !errors.Is(err, sv.ErrQueryFreeText) {
		return "", false
	}
	return q, true
}

type PostData struct {
	Job              *job.Job
	Args             *PostArgs
	Instruction      string
	Tool             *common.Tool
	ContinueWatching []*models.WatchHistory
}

func (s *Handler) post(c *gin.Context) {
	// Ensure RedirectWithError has a valid return URL (missing for magnet GET routes)
	if c.GetHeader("X-Return-Url") == "" {
		c.Request.Header.Set("X-Return-Url", "/")
	}

	if q, ok := searchTitle(c); ok {
		c.Redirect(http.StatusSeeOther, web.LangURL(i18n.GetLang(c), "/discover?search="+url.QueryEscape(q)))
		return
	}

	args, err := s.bindArgs(c)
	if err != nil {
		web.RedirectWithError(c, errors.Wrap(err, "wrong args provided"))
		return
	}

	loadJob, err := s.jobs.Load(web.NewContext(c), &scripts.LoadArgs{
		Query:       args.Query,
		File:        args.File,
		HintVideoID: args.HintVideoID,
		MagnetWait:  args.MagnetWait,
		Debug:       args.Debug,
	})
	if err != nil {
		web.RedirectWithError(c, errors.Wrap(err, "failed to load resource"))
		return
	}

	if !s.useDirectLinks {
		s.addResourceToSession(c, loadJob.ID)
	}

	if c.GetHeader("Accept") == "application/json" {
		c.JSON(http.StatusAccepted, gin.H{
			"job_log_url": web.LangURL(i18n.GetLang(c), fmt.Sprintf("/queue/%v/job/%v/log", loadJob.Queue, loadJob.ID)),
		})
		return
	}

	s.tb.Build("index").HTML(http.StatusAccepted, web.NewContext(c).WithData(newPostData(loadJob, args)))
}

// newPostData is the page a submit answers with: the view it was submitted
// from, now carrying the load job's log.
//
// A tool page's forms send the page's path in the hidden `instruction` field.
// Without the Tool the answer rendered the home page's H1 and <title> under
// the tool's URL (the POST answers 202, so the address bar does not move) —
// someone who came to convert a magnet saw the page change its mind. The
// Tool also puts the tool's slug into the log host (partials/load/progress),
// which is how it reaches the resource page after the redirect. The value is
// looked up in common.Tools, so a forged one is dropped, Instruction included:
// an unknown instruction used to render neither the home body nor a tool's.
func newPostData(j *job.Job, args *PostArgs) PostData {
	d := PostData{Job: j, Args: args}
	if t := common.ToolByURL(args.Instruction); t != nil {
		d.Tool = t
		d.Instruction = t.Url
	}
	return d
}
