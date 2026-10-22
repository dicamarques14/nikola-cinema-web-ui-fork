package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubtitleLabelDropsCodecFlagsAndCounters(t *testing.T) {
	cases := []struct {
		in   apiStream
		want string
	}{
		{apiStream{DisplayTitle: "0 - Portuguese - SUBRIP - External", Codec: "subrip"}, "Portuguese"},
		{apiStream{DisplayTitle: "English (forced) - Default - SUBRIP", Codec: "subrip"}, "English (forced)"},
		{apiStream{DisplayTitle: "Signs - English - Default - MOV_TEXT", Codec: "mov_text"}, "Signs · English"},
		{apiStream{Language: "fre", Codec: "subrip"}, "fre"},
		{apiStream{Index: 4}, "Subtitle 4"},
	}
	for _, c := range cases {
		if got := subtitleLabel(c.in); got != c.want {
			t.Errorf("subtitleLabel(%q) = %q, want %q", c.in.DisplayTitle, got, c.want)
		}
	}
}

const subsJSON = `{"Items":[{"Id":"` + testID + `","Name":"Ep","Type":"Episode","Overview":"Plot.",
  "MediaSources":[{"Id":"` + testID + `","Container":"mkv","MediaStreams":[
    {"Type":"Video","Codec":"h264","Index":2},
    {"Type":"Subtitle","Codec":"subrip","Index":0,"Language":"por","DisplayTitle":"Portuguese - SUBRIP - External","IsExternal":true,"IsTextSubtitleStream":true},
    {"Type":"Subtitle","Codec":"PGSSUB","Index":4,"Language":"eng","DisplayTitle":"English - PGSSUB"},
    {"Type":"Subtitle","Codec":"subrip","Index":5,"Language":"eng","DisplayTitle":"English (forced) - SUBRIP","IsForced":true,"IsTextSubtitleStream":true}]}]}]}`

func TestItemReadsSubtitlesOverviewAndSource(t *testing.T) {
	var seen http.Request
	s := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = *r
		_, _ = w.Write([]byte(subsJSON))
	}), "k")
	it, err := s.Item(context.Background(), testID)
	if err != nil || it == nil {
		t.Fatalf("Item = %v, %v", it, err)
	}
	if !strings.Contains(seen.URL.Query().Get("Fields"), "Overview") {
		t.Errorf("Fields = %q, want Overview", seen.URL.Query().Get("Fields"))
	}
	if it.Overview != "Plot." || it.MediaSourceID != testID {
		t.Errorf("overview/source = %q/%q", it.Overview, it.MediaSourceID)
	}
	if n := len(it.TextSubtitles()); n != 2 {
		t.Errorf("text subtitles = %d, want 2", n)
	}
	if it.ImageSubtitles() != 1 {
		t.Errorf("image subtitles = %d, want 1", it.ImageSubtitles())
	}
	if _, ok := it.Subtitle(4); ok {
		t.Error("an image track must not be servable")
	}
	if st, ok := it.StartSubtitle(); !ok || st.Index != 5 {
		t.Errorf("StartSubtitle = %v, %v; want the forced track 5", st, ok)
	}
}

func TestStartSubtitlePrefersDefaultAndElseNone(t *testing.T) {
	it := Item{Subtitles: []Subtitle{{Index: 1, Text: true, Forced: true}, {Index: 2, Text: true, Default: true}}}
	if st, _ := it.StartSubtitle(); st.Index != 2 {
		t.Errorf("StartSubtitle = %d, want the default 2", st.Index)
	}
	it = Item{Subtitles: []Subtitle{{Index: 1, Text: true}, {Index: 2, Default: true}}}
	if _, ok := it.StartSubtitle(); ok {
		t.Error("no default/forced text track: start with none")
	}
}

func TestSubtitleProxiesVTTWithToken(t *testing.T) {
	var seen http.Request
	s := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = *r
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("WEBVTT\n\n00:00.000 --> 00:01.000\nhi\n"))
	}), "secret")
	rec := httptest.NewRecorder()
	s.Subtitle(rec, httptest.NewRequest("GET", "/x", nil), testID, testID, 5)
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	if want := "/Videos/" + testID + "/" + testID + "/Subtitles/5/0/Stream.vtt"; seen.URL.Path != want {
		t.Errorf("path = %q, want %q", seen.URL.Path, want)
	}
	if seen.Header.Get("X-Emby-Token") != "secret" {
		t.Error("token header not sent")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/vtt") {
		t.Errorf("Content-Type = %q, want text/vtt", ct)
	}
	if !strings.HasPrefix(rec.Body.String(), "WEBVTT") {
		t.Errorf("body = %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String()+strings.Join(rec.Header().Values("Location"), ""), "secret") {
		t.Error("key leaked")
	}
}

func TestSubtitleMapsErrorsAndRejectsBadInput(t *testing.T) {
	code := http.StatusNotFound
	s := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}), "k")
	run := func(id string, idx int) int {
		rec := httptest.NewRecorder()
		s.Subtitle(rec, httptest.NewRequest("GET", "/x", nil), id, testID, idx)
		return rec.Code
	}
	if c := run(testID, 1); c != 404 {
		t.Errorf("404 upstream -> %d, want 404", c)
	}
	code = 500
	if c := run(testID, 1); c != 502 {
		t.Errorf("500 upstream -> %d, want 502", c)
	}
	if c := run("../etc", 1); c == 200 {
		t.Error("bad id accepted")
	}
	if c := run(testID, -1); c == 200 {
		t.Error("negative index accepted")
	}
}

func TestNeighboursWalksSeasonsInOrder(t *testing.T) {
	eps := []Item{
		{ID: "b", Season: 1, Episode: 2},
		{ID: "c", Season: 2, Episode: 1},
		{ID: "a", Season: 1, Episode: 1},
	}
	prev, next := Neighbours(eps, "b")
	if prev == nil || prev.ID != "a" || next == nil || next.ID != "c" {
		t.Errorf("Neighbours(b) = %v, %v", prev, next)
	}
	prev, next = Neighbours(eps, "a")
	if prev != nil || next == nil || next.ID != "b" {
		t.Errorf("Neighbours(a) = %v, %v", prev, next)
	}
	if p, n := Neighbours(eps, "zz"); p != nil || n != nil {
		t.Error("unknown id must have no neighbours")
	}
}
