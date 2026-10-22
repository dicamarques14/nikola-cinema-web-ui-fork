package jellyfin

import (
	"encoding/json"
	"testing"
)

func TestAudioTracksFromMediaStreams(t *testing.T) {
	var a apiItem
	if err := json.Unmarshal([]byte(`{"MediaSources":[{"Id":"abcdefabcdefabcdefabcdefabcdefab","MediaStreams":[
		{"Type":"Subtitle","Index":0,"IsExternal":true,"IsTextSubtitleStream":true,"Language":"por"},
		{"Type":"Video","Index":1,"Codec":"h264"},
		{"Type":"Audio","Index":2,"Codec":"eac3","Language":"eng","DisplayTitle":"English - Dolby Digital+ - 5.1 - Default","IsDefault":true},
		{"Type":"Audio","Index":3,"Codec":"wmapro","Language":"por","ChannelLayout":"stereo"}]}]}`), &a); err != nil {
		t.Fatal(err)
	}
	it := a.item()
	if len(it.Audio) != 2 || it.AudioCodec != "eac3" {
		t.Fatalf("audio = %+v, codec %q", it.Audio, it.AudioCodec)
	}
	if got := it.Audio[0]; got.Stream != 1 || got.Label != "English · Dolby Digital+ · 5.1" || !got.Playable() || got.ID() != "jfa-2" {
		t.Errorf("first track = %+v", got)
	}
	if got := it.Audio[1]; got.Stream != 2 || got.Label != "por · WMAPRO · stereo" || got.Playable() {
		t.Errorf("second track = %+v", got)
	}
	if s, ok := it.StartAudio(); !ok || s.Index != 2 {
		t.Errorf("start = %+v", s)
	}
	if _, ok := (Item{}).StartAudio(); ok {
		t.Error("no audio, no start track")
	}
}
