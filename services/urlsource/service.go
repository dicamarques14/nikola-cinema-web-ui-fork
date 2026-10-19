// Package urlsource plays and imports Stremio addon streams that are plain
// HTTP(S) files rather than torrents (docs/url_streams.md).
//
// Two stages. A click plays at once through /url/:id/stream, a Range
// pass-through proxy that sends the addon's proxyHeaders and never shows the
// browser the signed link. Meanwhile the file is downloaded to a staging
// directory, hashed into a private single-file torrent whose web seed is
// this server's /urlseed endpoint, and stored in the torrent store -- after
// that it is an ordinary resource and the whole torrent pipeline (library,
// Vault, subtitles, AI translation, resume) applies.
package urlsource

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/pkg/errors"
	uuid "github.com/satori/go.uuid"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	cs "github.com/webtor-io/common-services"

	"github.com/webtor-io/web-ui/models"
	"github.com/webtor-io/web-ui/services/common"
	"github.com/webtor-io/web-ui/services/egress"
	"github.com/webtor-io/web-ui/services/stremio"
)

// StoreFunc stores .torrent bytes in the torrent store and returns the
// resource id (the infohash) it was stored as.
type StoreFunc func(ctx context.Context, torrent []byte) (string, error)

type Service struct {
	pg          *cs.PG
	dir         string
	maxBytes    int64
	seedBase    string
	secret      []byte
	userAgent   string
	addonCl     *http.Client
	mediaCl     *http.Client
	store       StoreFunc
	autoImport  bool
	sem         chan struct{}
	mu          sync.Mutex
	running     map[uuid.UUID]*Progress
	touched     map[string]time.Time
	ctx         context.Context
	cancel      context.CancelFunc
	resolveLock sync.Map // url_source id -> *sync.Mutex
}

// New returns nil unless URL_STREAMS_ENABLED is set.
func New(c *cli.Context, pg *cs.PG, addonCl *http.Client) *Service {
	if !c.Bool(EnabledFlag) {
		return nil
	}
	conc := c.Int(ConcurrencyFlag)
	if conc < 1 {
		conc = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		pg:         pg,
		dir:        c.String(ImportDirFlag),
		maxBytes:   c.Int64(MaxBytesFlag),
		seedBase:   strings.TrimRight(c.String(SeedBaseURLFlag), "/"),
		autoImport: c.Bool(AutoImportFlag),
		secret:     []byte(c.String(common.SessionSecretFlag)),
		userAgent:  c.String(stremio.StremioUserAgentFlag),
		addonCl:    addonCl,
		mediaCl:    newMediaClient(c.Bool(stremio.StremioAllowPrivateNetworkFlag)),
		sem:        make(chan struct{}, conc),
		running:    map[uuid.UUID]*Progress{},
		touched:    map[string]time.Time{},
		ctx:        ctx,
		cancel:     cancel,
	}
	if db := pg.Get(); db != nil {
		if err := models.ResetStaleUrlSourceImports(ctx, db); err != nil {
			log.WithError(err).Warn("urlsource: failed to reset stale imports")
		}
	}
	return s
}

// newMediaClient fetches the origin. Every connection goes through the
// egress dialer, redirects included (a signed link usually redirects to a
// CDN); at most five, and only to http(s).
func newMediaClient(allowPrivate bool) *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{},
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   egress.DialControl(allowPrivate),
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
	}
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.Errorf("redirect to %s refused", req.URL.Scheme)
			}
			return nil
		},
	}
}

// SetStore enables the import (with URL_SEED_BASE_URL set).
func (s *Service) SetStore(f StoreFunc) {
	s.store = f
}

// ImportEnabled says whether files are turned into torrents, or only played
// through the proxy.
func (s *Service) ImportEnabled() bool {
	return s != nil && s.store != nil && s.seedBase != "" && s.dir != ""
}

func (s *Service) Close() {
	if s != nil {
		s.cancel()
	}
}

func (s *Service) db() (*pg.DB, error) {
	db := s.pg.Get()
	if db == nil {
		return nil, errors.New("no db")
	}
	return db, nil
}

// seedSig is the credential in a /urlseed URL: whoever has it can read the
// file, so it is an HMAC of the id under SESSION_SECRET and is never logged
// (helpers.RedactURL).
func (s *Service) seedSig(id uuid.UUID) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte("urlseed:" + id.String()))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// CheckSeedSig compares in constant time.
func (s *Service) CheckSeedSig(id uuid.UUID, sig string) bool {
	return hmac.Equal([]byte(s.seedSig(id)), []byte(sig))
}

// SeedURL is the web seed written into the torrent's url-list.
func (s *Service) SeedURL(id uuid.UUID) string {
	return s.seedBase + "/urlseed/" + id.String() + "/" + s.seedSig(id) + "/"
}

// hostOf is what the logs say about an origin link: its host only. The rest
// of a signed link is a credential.
func hostOf(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.LastIndex(raw, "@"); i >= 0 {
		raw = raw[i+1:]
	}
	return raw
}
