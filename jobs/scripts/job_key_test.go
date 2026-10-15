package scripts

import (
	"testing"
	"time"

	"github.com/webtor-io/web-ui/models"
)

// Keys recorded at acf12dea for the fixtures in job_key_fixture_test.go,
// with the clock pinned to jobKeyAt. Removing the HEVC passthrough
// declaration (raw-file playback, movi-player) must not move them: a
// changed id splits the render cache and the dedup of a visitor's repeated
// starts for nothing.
const (
	goldenActionID = "a95e9c41040c89d71060c5bf48691c676bc86080"
	goldenEmbedKey = "7febc81e2317163fdc90871f25ab39db96056fb1"
)

func pinClock(t *testing.T) {
	t.Helper()
	actionClock = func() time.Time { return jobKeyAt }
	t.Cleanup(func() { actionClock = time.Now })
}

func actionID(vsud *models.VideoStreamUserData) string {
	_, id := Action(nil, nil, nil, nil, nil, nil, nil, nil, jobKeyContext(), "08ada5a7a6183aae1e09d831df6748d566095a10", "item-1", "stream-video", &models.StreamSettings{}, nil, vsud, WarmupSettings{}, GraceSettings{}, false, false, "", "", nil)
	return id
}

func embedKey(t *testing.T) string {
	t.Helper()
	_, hash, err := Embed(nil, nil, jobKeyContext(), nil, nil, nil, jobKeyEmbedSettings(), "", nil, WarmupSettings{})
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestActionIDIsUnchanged(t *testing.T) {
	pinClock(t)
	if got := actionID(jobKeyVSUD()); got != goldenActionID {
		t.Errorf("job id %s, want %s (acf12dea)", got, goldenActionID)
	}
}

func TestEmbedKeyIsUnchanged(t *testing.T) {
	pinClock(t)
	if got := embedKey(t); got != goldenEmbedKey {
		t.Errorf("embed key %s, want %s (acf12dea)", got, goldenEmbedKey)
	}
}
