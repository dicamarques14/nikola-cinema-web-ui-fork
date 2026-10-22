package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// Missing ("virtual") episodes have no file: Jellyfin answers 400 to their
// stream and 404 to their image, so no listing may offer them.
func TestListingsExcludeVirtualItems(t *testing.T) {
	var seen []http.Request
	s := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, *r)
		_, _ = w.Write([]byte(`{"Items":[],"TotalRecordCount":0}`))
	}), "k")
	ctx := context.Background()
	if _, err := s.Items(ctx, "x", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Movies(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Episodes(ctx, testID); err != nil {
		t.Fatal(err)
	}
	for _, r := range seen {
		q := r.URL.Query()
		if q.Get("ExcludeLocationTypes") != "Virtual" || q.Get("IsMissing") != "false" {
			t.Errorf("%s: query %v keeps virtual items", r.URL.Path, q)
		}
	}
	if q := seen[2].URL.Query(); q.Get("ParentId") != testID || q.Get("IncludeItemTypes") != "Episode" {
		t.Errorf("episodes query = %v", q)
	}
}

func TestSearchIncludesShows(t *testing.T) {
	var seen http.Request
	s := newTestService(t, itemsHandler(&seen), "k")
	if _, err := s.Items(context.Background(), "x", 10); err != nil {
		t.Fatal(err)
	}
	if got := seen.URL.Query().Get("IncludeItemTypes"); got != "Series,Movie,Episode" {
		t.Errorf("IncludeItemTypes = %q", got)
	}
}

func TestVirtualFlagAndImageFallback(t *testing.T) {
	const series = "ffffffffffffffffffffffffffffffff"
	body := `{"Items":[
	 {"Id":"` + testID + `","Type":"Episode","LocationType":"Virtual","SeriesId":"` + series + `","SeriesPrimaryImageTag":"t"},
	 {"Id":"` + series + `","Type":"Series","ImageTags":{"Primary":"p"}},
	 {"Id":"` + testID + `","Type":"Movie"}]}`
	s := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}), "k")
	items, err := s.Items(context.Background(), "x", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !items[0].Virtual || items[0].ImageID != series {
		t.Errorf("episode = %+v, want virtual with the series image", items[0])
	}
	if items[1].ImageID != series || items[1].Href() != "/jellyfin/show/"+series {
		t.Errorf("series = %+v / %s", items[1], items[1].Href())
	}
	if items[2].ImageID != "" || items[2].Href() != "/jellyfin/"+testID {
		t.Errorf("movie = %+v / %s", items[2], items[2].Href())
	}
}

// Shows reads every page, so a library over one page is listed whole, and
// keeps only series with an episode on disk.
func TestShowsReadsAllPagesAndKeepsShowsWithEpisodes(t *testing.T) {
	const total = pageSize + 3
	calls := 0
	s := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Get("IncludeItemTypes") == "Episode" {
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": []apiItem{
				{ID: "e1", Type: "Episode", SeriesID: "0"}, {ID: "e2", Type: "Episode", SeriesID: strconv.Itoa(total - 1)},
			}, "TotalRecordCount": 2})
			return
		}
		start, _ := strconv.Atoi(q.Get("StartIndex"))
		limit, _ := strconv.Atoi(q.Get("Limit"))
		var items []apiItem
		for i := start; i < total && i < start+limit; i++ {
			items = append(items, apiItem{ID: strconv.Itoa(i), Type: "Series"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": items, "TotalRecordCount": total})
	}), "k")
	got, err := s.Shows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(got) != 2 || got[0].ID != "0" || got[1].ID != strconv.Itoa(total-1) {
		t.Errorf("got %+v in %d calls, want the 2 shows with episodes in 3 calls", got, calls)
	}
}

func TestGroupSeasons(t *testing.T) {
	eps := []Item{
		{Name: "s2e2", Season: 2, Episode: 2, SeasonName: "Season 2"},
		{Name: "sp1", Season: 0, Episode: 1},
		{Name: "s1e3", Season: 1, Episode: 3},
		{Name: "s2e1", Season: 2, Episode: 1, SeasonName: "Season 2"},
		{Name: "s1e1", Season: 1, Episode: 1},
	}
	got := GroupSeasons(eps)
	want := []struct {
		name string
		eps  []string
	}{{"Season 1", []string{"s1e1", "s1e3"}}, {"Season 2", []string{"s2e1", "s2e2"}}, {"Specials", []string{"sp1"}}}
	if len(got) != len(want) {
		t.Fatalf("got %d seasons, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name != w.name || len(got[i].Episodes) != len(w.eps) {
			t.Fatalf("season %d = %+v", i, got[i])
		}
		for j, n := range w.eps {
			if got[i].Episodes[j].Name != n {
				t.Errorf("season %s episode %d = %s, want %s", w.name, j, got[i].Episodes[j].Name, n)
			}
		}
	}
}
