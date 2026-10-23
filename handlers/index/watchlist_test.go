package index

import (
	"testing"

	"github.com/webtor-io/web-ui/models"
)

func TestMergeWatchlistNewestFirst(t *testing.T) {
	movies := []models.WatchlistItem{{VideoID: "m3", CreatedAt: 30}, {VideoID: "m1", CreatedAt: 10}}
	series := []models.WatchlistItem{{VideoID: "s4", CreatedAt: 40}, {VideoID: "s2", CreatedAt: 20}, {VideoID: "s0", CreatedAt: 0}}
	got := mergeWatchlist(movies, series)
	want := []string{"s4", "m3", "s2", "m1", "s0"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].VideoID != id {
			t.Errorf("[%d] = %s, want %s", i, got[i].VideoID, id)
		}
	}
	if len(mergeWatchlist(nil, nil)) != 0 {
		t.Error("two empty lists merged into something")
	}
}

func TestCapItems(t *testing.T) {
	items := make([]models.WatchlistItem, homeWatchlistLimit+5)
	if n := len(capItems(items, homeWatchlistLimit)); n != homeWatchlistLimit {
		t.Errorf("capped to %d, want %d", n, homeWatchlistLimit)
	}
	if n := len(capItems(items[:3], homeWatchlistLimit)); n != 3 {
		t.Errorf("short list changed length: %d", n)
	}
}
