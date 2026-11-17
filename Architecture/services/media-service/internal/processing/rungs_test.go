package processing

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestFFmpegThreadsFollowTheContainerLimit(t *testing.T) {
	none := func(string) string { return "" }
	if got := ffmpegThreadsFrom(none, 3); got != 3 {
		t.Fatalf("threads = %d, want GOMAXPROCS 3", got)
	}
	if got := ffmpegThreadsFrom(none, 0); got != 1 {
		t.Fatalf("threads = %d, want at least 1", got)
	}
	set := func(v string) func(string) string {
		return func(k string) string {
			if k == ffmpegThreadsEnv {
				return v
			}
			return ""
		}
	}
	if got := ffmpegThreadsFrom(set("2"), 8); got != 2 {
		t.Fatalf("override = %d, want 2", got)
	}
	if got := ffmpegThreadsFrom(set("-1"), 4); got != 4 {
		t.Fatalf("bad override = %d, want GOMAXPROCS", got)
	}
}

func names(rs []Rung) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Name)
	}
	return out
}

func TestRungPlanMatchesTheLadder(t *testing.T) {
	if got := names(RungPlan(&VideoMeta{Height: 2160}, false)); !slices.Equal(got, []string{"thumb_150", "360p", "480p", "720p", "1080p", "4k"}) {
		t.Fatalf("4K long plan = %v", got)
	}
	if got := names(RungPlan(&VideoMeta{Height: 1920}, true)); !slices.Equal(got, []string{"thumb_150", "360p", "480p", "720p"}) {
		t.Fatalf("reel plan = %v", got)
	}
	if got := names(RungPlan(&VideoMeta{Height: 240}, false)); !slices.Equal(got, []string{"thumb_150"}) {
		t.Fatalf("240p plan = %v", got)
	}
}

func TestRungRecipeNamesTheProfile(t *testing.T) {
	r := Rung{"720p", 720, RungMP4}
	if RungRecipe(r, true) == RungRecipe(r, false) {
		t.Fatal("reel and long-form recipes must differ: a reused rendition would have the wrong profile")
	}
	if !strings.Contains(RungRecipe(r, false), longFormMP4Profile.Preset) {
		t.Fatalf("recipe %q does not name the preset", RungRecipe(r, false))
	}
}

// lv-15: a rung that fails is logged and left out, never fatal.
func TestTranscodeLadderLeavesOutAFailedRung(t *testing.T) {
	run := func(_ context.Context, args []string) ([]byte, error) {
		if slices.Contains(args, "scale=-2:480") {
			return []byte("boom"), errors.New("exit 1")
		}
		return nil, nil
	}
	outs := transcodeLadder(context.Background(), "/in", t.TempDir(), &VideoMeta{Height: 720, DurationFloat: 10}, false, run)
	got := []string{}
	for _, o := range outs {
		got = append(got, o.Name)
	}
	if !slices.Equal(got, []string{"thumb_150", "360p", "720p"}) {
		t.Fatalf("outputs = %v", got)
	}
}
