package models

import (
	"context"
	"testing"
	"time"
)

func TestJellyfinContinueEntries(t *testing.T) {
	now := time.Now()
	at := func(m int) time.Time { return now.Add(-time.Duration(m) * time.Minute) }
	rows := []*JellyfinWatch{ // most recent first, like the query
		{ItemID: "e2", Title: "Pilot", Series: "Show", Code: "S01E02", ImageID: "s", Position: 1500, Duration: 1500, Watched: true, NextItemID: "e3", NextTitle: "Third", NextCode: "S01E03", UpdatedAt: at(1)},
		{ItemID: "e1", Series: "Show", Code: "S01E01", Position: 300, Duration: 1500, UpdatedAt: at(2)},
		{ItemID: "m1", Title: "Film", ImageID: "m1", Position: 600, Duration: 6000, UpdatedAt: at(3)},
		{ItemID: "m2", Title: "Done", Position: 5900, Duration: 6000, Watched: true, UpdatedAt: at(4)},
		{ItemID: "opened", Title: "Never played", UpdatedAt: at(5)},
		{ItemID: "x2", Series: "Other", Code: "S01E01", Position: 1400, Duration: 1500, Watched: true, NextItemID: "x3", UpdatedAt: at(6)},
		{ItemID: "x3", Series: "Other", Position: 1450, Duration: 1500, Watched: true, UpdatedAt: at(7)},
	}
	got := JellyfinContinueEntries(rows, 10)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if got[0].Path != "e3" || got[0].Title != "Show S01E03 — Third" || got[0].Href != "/jellyfin/e3" || got[0].Poster != "/jellyfin/s/poster" || got[0].Progress() != 0 {
		t.Errorf("next episode entry = %+v", got[0])
	}
	if got[1].Path != "m1" || got[1].Title != "Film" || got[1].Progress() != 10 || got[1].ResourceID != JellyfinResourceID {
		t.Errorf("movie entry = %+v", got[1])
	}
	if l := JellyfinContinueEntries(rows, 1); len(l) != 1 {
		t.Errorf("limit: %d", len(l))
	}
}

func TestMergeContinueWatching(t *testing.T) {
	now := time.Now()
	a := []*WatchHistory{{Path: "a", UpdatedAt: now.Add(-time.Hour)}, {Path: "b", UpdatedAt: now.Add(-3 * time.Hour)}}
	j := []*WatchHistory{{Path: "j", UpdatedAt: now.Add(-2 * time.Hour)}}
	got := MergeContinueWatching(a, j, 2)
	if len(got) != 2 || got[0].Path != "a" || got[1].Path != "j" {
		t.Errorf("merge = %v %v", got[0].Path, got[1].Path)
	}
}

// Migration 76 and the queries, against a real Postgres (skipped without
// docker).
func TestJellyfinWatchStore(t *testing.T) {
	db := startTestPostgres(t)
	ctx := context.Background()
	uid := createTestUser(t, db, "jf@example.com")
	if err := TouchJellyfinItem(ctx, db, &JellyfinWatch{UserID: uid, ItemID: "e1", Title: "Pilot", Series: "Show", Code: "S01E01", NextItemID: "e2", NextTitle: "Two", NextCode: "S01E02"}); err != nil {
		t.Fatal(err)
	}
	if w, err := GetJellyfinPosition(ctx, db, uid, "e1"); err != nil || w == nil || w.Duration != 0 {
		t.Fatalf("after touch: %+v %v", w, err)
	}
	if err := UpsertJellyfinPosition(ctx, db, uid, "e1", 600, 1500, 0); err != nil {
		t.Fatal(err)
	}
	// A later visit must not reset the position.
	if err := TouchJellyfinItem(ctx, db, &JellyfinWatch{UserID: uid, ItemID: "e1", Title: "Pilot!", Series: "Show", Code: "S01E01"}); err != nil {
		t.Fatal(err)
	}
	w, err := GetJellyfinPosition(ctx, db, uid, "e1")
	if err != nil || w == nil || w.Position != 600 || w.Title != "Pilot!" || w.Watched {
		t.Fatalf("position: %+v %v", w, err)
	}
	list, err := GetJellyfinContinueWatching(ctx, db, uid, 10)
	if err != nil || len(list) != 1 || list[0].Path != "e1" {
		t.Fatalf("in progress: %+v %v", list, err)
	}
	if err := UpsertJellyfinPosition(ctx, db, uid, "e1", 1450, 1500, 0); err != nil {
		t.Fatal(err)
	}
	list, err = GetJellyfinContinueWatching(ctx, db, uid, 10)
	if err != nil || len(list) != 1 || list[0].Path != "e2" || list[0].Title != "Show S01E02 — Two" {
		t.Fatalf("next episode: %+v %v", list, err)
	}
	if w, _ := GetJellyfinPosition(ctx, db, uid, "nope"); w != nil {
		t.Fatalf("missing item: %+v", w)
	}
}
