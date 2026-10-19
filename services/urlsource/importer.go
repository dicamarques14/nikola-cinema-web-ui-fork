package urlsource

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	uuid "github.com/satori/go.uuid"
	log "github.com/sirupsen/logrus"

	"github.com/webtor-io/web-ui/models"
)

// Progress is a running import, for the status endpoint.
type Progress struct {
	phase atomic.Value // "queued" | "downloading" | "hashing" | "storing"
	done  atomic.Int64
	total atomic.Int64
}

func (p *Progress) Phase() string {
	v, _ := p.phase.Load().(string)
	return v
}
func (p *Progress) Done() int64  { return p.done.Load() }
func (p *Progress) Total() int64 { return p.total.Load() }

// Progress returns the running import of id, or nil.
func (s *Service) Progress(id uuid.UUID) *Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id]
}

func (s *Service) stagedPath(id uuid.UUID) string {
	return filepath.Join(s.dir, id.String())
}

// StagedFile is the complete downloaded copy of src, or "" when there is
// none (not imported yet, or evicted).
func (s *Service) StagedFile(src *models.UrlSource) string {
	if s.dir == "" {
		return ""
	}
	p := s.stagedPath(src.ID)
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() || (src.Size > 0 && fi.Size() != src.Size) {
		return ""
	}
	return p
}

// Touch marks a staged file as used (eviction is least recently used), at
// most once a minute per file.
func (s *Service) Touch(p string) {
	s.mu.Lock()
	last := s.touched[p]
	now := time.Now()
	if now.Sub(last) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.touched[p] = now
	s.mu.Unlock()
	_ = os.Chtimes(p, now, now)
}

// StartImport queues src's import unless it is done, running, or the import
// is off. Returns at once.
func (s *Service) StartImport(src *models.UrlSource) {
	if !s.ImportEnabled() || src.Status == models.UrlSourceImported {
		return
	}
	s.mu.Lock()
	if _, ok := s.running[src.ID]; ok {
		s.mu.Unlock()
		return
	}
	p := &Progress{}
	p.phase.Store("queued")
	p.total.Store(src.Size)
	s.running[src.ID] = p
	s.mu.Unlock()
	cp := *src
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.running, cp.ID)
			s.mu.Unlock()
		}()
		select {
		case s.sem <- struct{}{}:
		case <-s.ctx.Done():
			return
		}
		defer func() { <-s.sem }()
		l := log.WithField("url_source", cp.ID.String()).WithField("host", hostOf(cp.Url))
		if err := s.runImport(s.ctx, &cp, p); err != nil {
			l.WithError(err).Warn("urlsource: import failed")
			if db := s.pg.Get(); db != nil {
				_ = models.UpdateUrlSourceStatus(context.Background(), db, cp.ID, models.UrlSourceFailed, trimErr(err), "")
			}
			return
		}
		l.Info("urlsource: imported")
	}()
}

func trimErr(err error) string {
	s := err.Error()
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func (s *Service) runImport(ctx context.Context, src *models.UrlSource, p *Progress) error {
	db, err := s.db()
	if err != nil {
		return err
	}
	if src.Size > s.maxBytes {
		return errors.Errorf("file is larger than URL_IMPORT_MAX_BYTES (%d)", s.maxBytes)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return errors.Wrap(err, "failed to create the staging directory")
	}
	final := s.stagedPath(src.ID)
	if err := models.UpdateUrlSourceStatus(ctx, db, src.ID, models.UrlSourceImporting, "", final); err != nil {
		return err
	}
	if s.StagedFile(src) == "" {
		if err := s.evict(src.ID, src.Size); err != nil {
			return err
		}
		p.phase.Store("downloading")
		size, err := s.download(ctx, src, final, p)
		if err != nil {
			return err
		}
		if src.Size == 0 {
			src.Size = size
		}
	}
	f, err := os.Open(final)
	if err != nil {
		return errors.Wrap(err, "failed to open the staged file")
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	p.phase.Store("hashing")
	p.done.Store(0)
	p.total.Store(fi.Size())
	torrent, hash, err := BuildTorrent(f, src.Filename, fi.Size(), s.SeedURL(src.ID), func(n int64) { p.done.Store(n) })
	if err != nil {
		return err
	}
	p.phase.Store("storing")
	id, err := s.store(ctx, torrent)
	if err != nil {
		return errors.Wrap(err, "failed to store the torrent")
	}
	if id != "" && !strings.EqualFold(id, hash) {
		log.WithField("stored", id).WithField("built", hash).Warn("urlsource: torrent store answered another infohash")
		hash = strings.ToLower(id)
	}
	return models.MarkUrlSourceImported(ctx, db, src.ID, hash)
}

// download fetches the whole file into final (via final+".part"), resuming
// with a Range after a dropped connection. Returns the size.
func (s *Service) download(ctx context.Context, src *models.UrlSource, final string, p *Progress) (int64, error) {
	part := final + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, errors.Wrap(err, "failed to create the staging file")
	}
	defer func() { _ = f.Close() }()
	total := src.Size
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt*attempt) * time.Second):
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		off, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			return 0, err
		}
		if total > 0 && off >= total {
			break
		}
		h := http.Header{}
		if off > 0 {
			h.Set("Range", "bytes="+strconv.FormatInt(off, 10)+"-")
		}
		res, err := s.Fetch(ctx, src, http.MethodGet, h)
		if err != nil {
			lastErr = err
			continue
		}
		switch {
		case res.StatusCode == http.StatusOK:
			// A full answer: start over (the origin ignored the Range).
			if off > 0 {
				if err := f.Truncate(0); err != nil {
					_ = res.Body.Close()
					return 0, err
				}
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					_ = res.Body.Close()
					return 0, err
				}
				off = 0
			}
			if res.ContentLength > 0 {
				total = res.ContentLength
			}
		case res.StatusCode == http.StatusPartialContent:
			if t := totalFromContentRange(res.Header.Get("Content-Range")); t > 0 {
				total = t
			}
		default:
			_ = res.Body.Close()
			lastErr = errors.Errorf("origin answered %d", res.StatusCode)
			if res.StatusCode >= 400 && res.StatusCode < 500 && res.StatusCode != http.StatusTooManyRequests {
				return 0, lastErr
			}
			continue
		}
		if total > s.maxBytes {
			_ = res.Body.Close()
			return 0, errors.Errorf("file is larger than URL_IMPORT_MAX_BYTES (%d)", s.maxBytes)
		}
		p.total.Store(total)
		p.done.Store(off)
		_, err = io.Copy(f, &countingReader{r: res.Body, n: off, progress: func(n int64) { p.done.Store(n) }})
		_ = res.Body.Close()
		if err != nil {
			lastErr = errors.Wrap(err, "download interrupted")
			continue
		}
		cur, _ := f.Seek(0, io.SeekEnd)
		if total <= 0 || cur >= total {
			total = cur
			lastErr = nil
			break
		}
		lastErr = errors.New("download ended early")
	}
	if lastErr != nil {
		return 0, lastErr
	}
	if err := f.Sync(); err != nil {
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(part, final); err != nil {
		return 0, errors.Wrap(err, "failed to finish the staging file")
	}
	return total, nil
}

func totalFromContentRange(v string) int64 {
	i := strings.LastIndex(v, "/")
	if i < 0 {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v[i+1:]), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// evict makes room for need bytes under the staging cap, oldest-used files
// first. Partial downloads of running imports are kept.
func (s *Service) evict(self uuid.UUID, need int64) error {
	if need > s.maxBytes {
		return errors.Errorf("file is larger than URL_IMPORT_MAX_BYTES (%d)", s.maxBytes)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return errors.Wrap(err, "failed to read the staging directory")
	}
	type item struct {
		path  string
		size  int64
		mtime time.Time
	}
	var items []item
	var total int64
	s.mu.Lock()
	busy := map[string]bool{}
	for id := range s.running {
		busy[id.String()] = true
		busy[id.String()+".part"] = true
	}
	s.mu.Unlock()
	busy[self.String()] = true
	busy[self.String()+".part"] = true
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		total += fi.Size()
		if busy[e.Name()] {
			continue
		}
		items = append(items, item{filepath.Join(s.dir, e.Name()), fi.Size(), fi.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mtime.Before(items[j].mtime) })
	for _, it := range items {
		if total+need <= s.maxBytes {
			break
		}
		if err := os.Remove(it.path); err == nil {
			total -= it.size
			log.WithField("file", filepath.Base(it.path)).Info("urlsource: evicted staged file")
		}
	}
	if total+need > s.maxBytes {
		return errors.New("no room in the staging directory (imports in progress fill it)")
	}
	return nil
}
