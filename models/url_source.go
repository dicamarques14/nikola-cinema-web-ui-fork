package models

import (
	"context"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/pkg/errors"
	uuid "github.com/satori/go.uuid"
)

// UrlSourceStatus is where a direct-URL stream is on its way to becoming a
// torrent (docs/url_streams.md).
type UrlSourceStatus string

const (
	// UrlSourceNew plays through the proxy; no import has run yet.
	UrlSourceNew UrlSourceStatus = "new"
	// UrlSourceImporting is being downloaded and hashed.
	UrlSourceImporting UrlSourceStatus = "importing"
	// UrlSourceImported has a torrent (InfoHash) in the torrent store.
	UrlSourceImported UrlSourceStatus = "imported"
	// UrlSourceFailed stopped; Error says why. Opening it again retries.
	UrlSourceFailed UrlSourceStatus = "failed"
)

// UrlSource is a file a Stremio addon serves over HTTP(S) rather than as a
// torrent. Url and ProxyHeaders are credentials (signed links, CDN cookies):
// they are read by the server only and must never be rendered or exported.
type UrlSource struct {
	tableName         struct{}          `pg:"url_source"`
	ID                uuid.UUID         `pg:"url_source_id,pk,type:uuid,default:uuid_generate_v4()"`
	UserID            uuid.UUID         `pg:"user_id,type:uuid"`
	StremioAddonUrlID *uuid.UUID        `pg:"stremio_addon_url_id,type:uuid"`
	ContentType       string            `pg:"content_type"`
	ContentID         string            `pg:"content_id"`
	Title             string            `pg:"title,use_zero"`
	Filename          string            `pg:"filename"`
	Size              int64             `pg:"size,use_zero"`
	Url               string            `pg:"url"`
	ProxyHeaders      map[string]string `pg:"proxy_headers,type:jsonb"`
	Status            UrlSourceStatus   `pg:"status"`
	Error             string            `pg:"error,use_zero"`
	InfoHash          *string           `pg:"info_hash"`
	StagingPath       string            `pg:"staging_path,use_zero"`
	CreatedAt         time.Time         `pg:"created_at,default:now()"`
	UpdatedAt         time.Time         `pg:"updated_at,default:now()"`
}

// CreateUrlSource inserts a new row; ID is filled in on return.
func CreateUrlSource(ctx context.Context, db *pg.DB, s *UrlSource) error {
	if s.Status == "" {
		s.Status = UrlSourceNew
	}
	_, err := db.Model(s).Context(ctx).Returning("*").Insert()
	return errors.Wrap(err, "failed to create url source")
}

// GetUrlSource returns the row by id, or nil.
func GetUrlSource(ctx context.Context, db *pg.DB, id uuid.UUID) (*UrlSource, error) {
	s := &UrlSource{}
	err := db.Model(s).Context(ctx).Where("url_source_id = ?", id).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to get url source")
	}
	return s, nil
}

// GetUserUrlSource returns the row by id only when it belongs to the user.
func GetUserUrlSource(ctx context.Context, db *pg.DB, userID, id uuid.UUID) (*UrlSource, error) {
	s := &UrlSource{}
	err := db.Model(s).Context(ctx).
		Where("url_source_id = ? AND user_id = ?", id, userID).
		Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to get url source")
	}
	return s, nil
}

// FindUserUrlSource is the user's earlier row for the same file of the same
// title (content id + filename + size), so opening it again reuses the
// import instead of starting another. Nil when there is none.
func FindUserUrlSource(ctx context.Context, db *pg.DB, userID uuid.UUID, contentID, filename string, size int64) (*UrlSource, error) {
	s := &UrlSource{}
	err := db.Model(s).Context(ctx).
		Where("user_id = ? AND content_id = ? AND filename = ? AND size = ?", userID, contentID, filename, size).
		Order("created_at DESC").
		Limit(1).
		Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to find url source")
	}
	return s, nil
}

// ListAllUserUrlSources returns every row of the user (data export).
func ListAllUserUrlSources(ctx context.Context, db *pg.DB, userID uuid.UUID) ([]UrlSource, error) {
	var out []UrlSource
	err := db.Model(&out).Context(ctx).
		Where("user_id = ?", userID).
		Order("created_at ASC").
		Select()
	if err != nil {
		return nil, errors.Wrap(err, "failed to list url sources")
	}
	return out, nil
}

// UpdateUrlSourceLink stores a re-resolved link and its headers.
func UpdateUrlSourceLink(ctx context.Context, db *pg.DB, id uuid.UUID, url string, headers map[string]string) error {
	_, err := db.Model(&UrlSource{}).Context(ctx).
		Set("url = ?", url).
		Set("proxy_headers = ?", headers).
		Where("url_source_id = ?", id).
		Update()
	return errors.Wrap(err, "failed to update url source link")
}

// UpdateUrlSourceStatus moves the row to status with an error text (may be
// empty) and, when non-empty, the staging path.
func UpdateUrlSourceStatus(ctx context.Context, db *pg.DB, id uuid.UUID, status UrlSourceStatus, errText, stagingPath string) error {
	q := db.Model(&UrlSource{}).Context(ctx).
		Set("status = ?", status).
		Set("error = ?", errText).
		Where("url_source_id = ?", id)
	if stagingPath != "" {
		q = q.Set("staging_path = ?", stagingPath)
	}
	_, err := q.Update()
	return errors.Wrap(err, "failed to update url source status")
}

// MarkUrlSourceImported records the torrent the import produced.
func MarkUrlSourceImported(ctx context.Context, db *pg.DB, id uuid.UUID, infoHash string) error {
	_, err := db.Model(&UrlSource{}).Context(ctx).
		Set("status = ?", UrlSourceImported).
		Set("error = ''").
		Set("info_hash = ?", infoHash).
		Where("url_source_id = ?", id).
		Update()
	return errors.Wrap(err, "failed to mark url source imported")
}

// ResetStaleUrlSourceImports turns rows left "importing" by a process that
// died into "failed", so opening them again restarts the import.
func ResetStaleUrlSourceImports(ctx context.Context, db *pg.DB) error {
	_, err := db.Model(&UrlSource{}).Context(ctx).
		Set("status = ?", UrlSourceFailed).
		Set("error = ?", "interrupted").
		Where("status = ?", UrlSourceImporting).
		Update()
	return errors.Wrap(err, "failed to reset stale url source imports")
}
