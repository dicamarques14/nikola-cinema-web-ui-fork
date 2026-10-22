package models

import (
	"context"
	"sort"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/pkg/errors"
	uuid "github.com/satori/go.uuid"
)

// JellyfinResourceID is the resource_id the player sends to PUT/GET
// /watch/position for a Jellyfin item (path is the item id). Those positions
// go to jellyfin_watch, not watch_history (migration 76 says why).
const JellyfinResourceID = "jellyfin"

// JellyfinWatch is one Jellyfin item's playback state for a user, with what
// Continue watching needs to show it (recorded by the play page).
type JellyfinWatch struct {
	tableName struct{} `pg:"jellyfin_watch"`

	UserID     uuid.UUID `pg:"user_id,pk"`
	ItemID     string    `pg:"item_id,pk"`
	Title      string    `pg:"title,use_zero"`
	Series     string    `pg:"series,use_zero"`
	Code       string    `pg:"code,use_zero"`
	ImageID    string    `pg:"image_id,use_zero"`
	Position   float32   `pg:"position,use_zero"`
	Duration   float32   `pg:"duration,use_zero"`
	Watched    bool      `pg:"watched,use_zero"`
	NextItemID string    `pg:"next_item_id,use_zero"`
	NextTitle  string    `pg:"next_title,use_zero"`
	NextCode   string    `pg:"next_code,use_zero"`
	CreatedAt  time.Time `pg:"created_at"`
	UpdatedAt  time.Time `pg:"updated_at"`
}

// TouchJellyfinItem records what the play page knows about an item (title,
// series, poster, next episode) without touching its position. An empty
// NextItemID leaves the stored next episode alone.
func TouchJellyfinItem(ctx context.Context, db *pg.DB, w *JellyfinWatch) error {
	_, err := db.Model(w).
		Context(ctx).
		OnConflict("(user_id, item_id) DO UPDATE").
		Set("title = EXCLUDED.title").
		Set("series = EXCLUDED.series").
		Set("code = EXCLUDED.code").
		Set("image_id = EXCLUDED.image_id").
		// A visit that could not list the episodes keeps the known next one.
		Set("next_item_id = COALESCE(NULLIF(EXCLUDED.next_item_id, ''), jellyfin_watch.next_item_id)").
		Set("next_title = CASE WHEN EXCLUDED.next_item_id = '' THEN jellyfin_watch.next_title ELSE EXCLUDED.next_title END").
		Set("next_code = CASE WHEN EXCLUDED.next_item_id = '' THEN jellyfin_watch.next_code ELSE EXCLUDED.next_code END").
		Insert()
	return errors.Wrap(err, "failed to record jellyfin item")
}

// UpsertJellyfinPosition stores the player's position, with the same
// "watched" rule as torrents (IsWatched).
func UpsertJellyfinPosition(ctx context.Context, db *pg.DB, userID uuid.UUID, itemID string, position, duration, creditsAt float32) error {
	w := &JellyfinWatch{
		UserID: userID, ItemID: itemID,
		Position: position, Duration: duration,
		Watched: IsWatched(position, duration, creditsAt),
	}
	_, err := db.Model(w).
		Context(ctx).
		OnConflict("(user_id, item_id) DO UPDATE").
		Set("position = EXCLUDED.position").
		Set("duration = EXCLUDED.duration").
		Set("watched = EXCLUDED.watched").
		Insert()
	return errors.Wrap(err, "failed to upsert jellyfin position")
}

// GetJellyfinPosition is the stored position of one item, nil if none.
func GetJellyfinPosition(ctx context.Context, db *pg.DB, userID uuid.UUID, itemID string) (*JellyfinWatch, error) {
	w := &JellyfinWatch{}
	err := db.Model(w).Context(ctx).
		Where("user_id = ? AND item_id = ?", userID, itemID).
		Limit(1).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to get jellyfin position")
	}
	return w, nil
}

// GetJellyfinContinueWatching is the user's Continue watching entries from
// the Jellyfin library, most recent first.
func GetJellyfinContinueWatching(ctx context.Context, db *pg.DB, userID uuid.UUID, limit int) ([]*WatchHistory, error) {
	var rows []*JellyfinWatch
	err := db.Model(&rows).Context(ctx).
		Where("user_id = ?", userID).
		Order("updated_at DESC").
		Limit(200).
		Select()
	if err != nil {
		return nil, errors.Wrap(err, "failed to list jellyfin watch history")
	}
	return JellyfinContinueEntries(rows, limit), nil
}

// JellyfinContinueEntries picks the strip's entries from rows (most recent
// first): per show only its latest item, shown when half-watched; a finished
// episode with a known next one becomes that next episode (unless it was
// watched too); finished movies drop out. Rows never played (duration 0, the
// play page records an item on open) are skipped.
func JellyfinContinueEntries(rows []*JellyfinWatch, limit int) []*WatchHistory {
	byItem := map[string]*JellyfinWatch{}
	for _, r := range rows {
		byItem[r.ItemID] = r
	}
	seen := map[string]bool{}
	var out []*WatchHistory
	for _, r := range rows {
		if r.Duration <= 0 {
			continue
		}
		key := "i:" + r.ItemID
		if r.Series != "" {
			key = "s:" + r.Series
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if !r.Watched {
			out = append(out, jellyfinEntry(r.ItemID, r.Title, r.Code, r.Series, r.ImageID, r.Position, r.Duration, r.UpdatedAt))
		} else if r.NextItemID != "" {
			if n := byItem[r.NextItemID]; n != nil && n.Watched {
				continue
			}
			out = append(out, jellyfinEntry(r.NextItemID, r.NextTitle, r.NextCode, r.Series, r.ImageID, 0, 0, r.UpdatedAt))
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func jellyfinEntry(id, title, code, series, imageID string, pos, dur float32, at time.Time) *WatchHistory {
	name := title
	if series != "" {
		name = series
		if code != "" {
			name += " " + code
		}
		if title != "" {
			name += " — " + title
		}
	}
	w := &WatchHistory{
		ResourceID: JellyfinResourceID, Path: id,
		Position: pos, Duration: dur, UpdatedAt: at,
		Title: name, Href: "/jellyfin/" + id,
	}
	if imageID != "" {
		w.Poster = "/jellyfin/" + imageID + "/poster"
	}
	return w
}

// MergeContinueWatching puts extra entries among list by recency, keeping at
// most limit.
func MergeContinueWatching(list, extra []*WatchHistory, limit int) []*WatchHistory {
	if len(extra) == 0 {
		return list
	}
	out := append(append([]*WatchHistory{}, list...), extra...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
