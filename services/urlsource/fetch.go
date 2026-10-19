package urlsource

import (
	"context"
	"io"
	"net/http"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"github.com/webtor-io/web-ui/models"
)

// Fetch requests src from its origin with the stored proxy headers and the
// allow-listed headers of incoming. A 403 or 410 is an expired link: the
// addon is asked for a fresh one and the request made once more. The caller
// closes the body.
func (s *Service) Fetch(ctx context.Context, src *models.UrlSource, method string, incoming http.Header) (*http.Response, error) {
	if method != http.MethodHead {
		method = http.MethodGet
	}
	res, err := s.fetchOnce(ctx, src, method, incoming)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusGone {
		return res, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	_ = res.Body.Close()
	stale := src.Url
	if err := s.Resolve(ctx, src, stale); err != nil {
		log.WithError(err).WithField("host", hostOf(stale)).Warn("urlsource: failed to renew an expired link")
		return nil, errors.Wrap(err, "link expired")
	}
	return s.fetchOnce(ctx, src, method, incoming)
}

func (s *Service) fetchOnce(ctx context.Context, src *models.UrlSource, method string, incoming http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, src.Url, nil)
	if err != nil {
		return nil, errors.Wrap(err, "bad origin link")
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, ErrUnsupported
	}
	req.Header = buildUpstreamHeaders(src.ProxyHeaders, incoming, s.userAgent)
	res, err := s.mediaCl.Do(req)
	if err != nil {
		// The error text carries the whole link; say the host only.
		return nil, errors.Errorf("origin %s unreachable", hostOf(src.Url))
	}
	return res, nil
}

// Proxy answers w with src's bytes from the origin, Range passed through.
func (s *Service) Proxy(w http.ResponseWriter, r *http.Request, src *models.UrlSource) {
	res, err := s.Fetch(r.Context(), src, r.Method, r.Header)
	if err != nil {
		http.Error(w, "origin unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode == http.StatusOK, res.StatusCode == http.StatusPartialContent,
		res.StatusCode == http.StatusRequestedRangeNotSatisfiable, res.StatusCode == http.StatusNotModified:
	default:
		http.Error(w, "origin answered "+http.StatusText(res.StatusCode), http.StatusBadGateway)
		return
	}
	CopyResponseHeaders(w.Header(), res.Header)
	if w.Header().Get("Accept-Ranges") == "" && res.StatusCode == http.StatusPartialContent {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(res.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, res.Body)
}
