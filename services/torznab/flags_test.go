package torznab

import (
	"flag"
	"testing"

	"github.com/urfave/cli"
)

func TestIndexerLimitReached(t *testing.T) {
	cases := []struct {
		count, limit int
		paid, want   bool
	}{
		{0, 3, false, false},
		{2, 3, false, false},
		{3, 3, false, true},
		{7, 3, false, true},
		{7, 3, true, false},
		{50, 0, false, false}, // 0 = unlimited (self-hosted)
		{50, -1, false, false},
	}
	for _, tc := range cases {
		if got := IndexerLimitReached(tc.count, tc.limit, tc.paid); got != tc.want {
			t.Errorf("IndexerLimitReached(%d, %d, %v) = %v, want %v", tc.count, tc.limit, tc.paid, got, tc.want)
		}
	}
}

func TestMaxIndexersFlag(t *testing.T) {
	read := func(env string) int {
		t.Helper()
		if env != "" {
			t.Setenv("TORZNAB_MAX_INDEXERS", env)
		}
		app := cli.NewApp()
		set := flag.NewFlagSet("test", flag.ContinueOnError)
		for _, f := range RegisterFlags(nil) {
			f.Apply(set)
		}
		if err := set.Parse(nil); err != nil {
			t.Fatal(err)
		}
		return MaxIndexers(cli.NewContext(app, set, nil))
	}
	if got := read(""); got != DefaultMaxIndexers {
		t.Errorf("default: got %d, want %d", got, DefaultMaxIndexers)
	}
	if got := read("0"); got != 0 {
		t.Errorf("TORZNAB_MAX_INDEXERS=0: got %d, want 0 (unlimited)", got)
	}
	if got := read("10"); got != 10 {
		t.Errorf("TORZNAB_MAX_INDEXERS=10: got %d", got)
	}
	if got := read("-5"); got != 0 {
		t.Errorf("negative: got %d, want 0", got)
	}
}
