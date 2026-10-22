package jellyfin

import (
	"strconv"
	"strings"
)

// Audio is one audio track of a file. The player (movi-player) demuxes the
// original file and decodes every track itself, so a switch is client-side:
// Stream is the track's index in the file, which is what the player selects
// by.
type Audio struct {
	// Index is Jellyfin's index. It is not the file's: Jellyfin numbers the
	// external subtitle files first and shifts the embedded streams after
	// them (see fileStreams).
	Index    int
	Stream   int
	Language string
	Label    string
	Codec    string
	Channels int
	Default  bool
}

func (s apiStream) audio() Audio {
	return Audio{
		Index: s.Index, Stream: s.Index, Language: s.Language, Codec: s.Codec,
		Channels: s.Channels, Default: s.IsDefault, Label: audioLabel(s),
	}
}

// fileStreams turns Jellyfin's indexes of the audio tracks into the file's,
// taking off the external streams Jellyfin counted in front of them.
func fileStreams(audio []Audio, streams []apiStream) []Audio {
	ext := 0
	for _, s := range streams {
		if s.IsExternal {
			ext++
		}
	}
	for i := range audio {
		if audio[i].Stream -= ext; audio[i].Stream < 0 {
			audio[i].Stream = audio[i].Index
		}
	}
	return audio
}

// audioLabel is DisplayTitle ("English - Dolby Digital+ - 5.1 - Default")
// without the flags the chip shows on its own, falling back to language,
// codec and channels.
func audioLabel(s apiStream) string {
	var parts []string
	for _, p := range strings.Split(s.DisplayTitle, " - ") {
		p = strings.TrimSpace(p)
		if p == "" || p == "Default" || p == "External" || p == "Forced" {
			continue
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		for _, p := range []string{s.Language, strings.ToUpper(s.Codec), s.ChannelLayout} {
			if p != "" {
				parts = append(parts, p)
			}
		}
	}
	if len(parts) == 0 {
		return "Track " + strconv.Itoa(s.Index)
	}
	return strings.Join(parts, " · ")
}

// playableAudio are the codecs movi-player's WASM decoder is known to
// handle (it decodes all audio in software, downmixed to stereo). Anything
// else is offered with a warning rather than left to fail in silence.
var playableAudio = map[string]bool{
	"aac": true, "mp3": true, "mp2": true, "ac3": true, "eac3": true,
	"dts": true, "dca": true, "truehd": true, "mlp": true, "flac": true,
	"opus": true, "vorbis": true, "alac": true,
}

// Playable reports whether the browser player is expected to decode it.
func (a Audio) Playable() bool {
	c := strings.ToLower(a.Codec)
	return playableAudio[c] || strings.HasPrefix(c, "pcm_")
}

// ID is the picker chip id of the track.
func (a Audio) ID() string { return "jfa-" + strconv.Itoa(a.Index) }

// StartAudio is the track the player opens on: Jellyfin's default, else the
// first. ok is false for a file with no audio.
func (i Item) StartAudio() (Audio, bool) {
	for _, a := range i.Audio {
		if a.Default {
			return a, true
		}
	}
	if len(i.Audio) > 0 {
		return i.Audio[0], true
	}
	return Audio{}, false
}
