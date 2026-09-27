package dubbing

import (
	"strings"
	"testing"
)

func TestAssemblyFilterPlacesEachSegmentAtItsStart(t *testing.T) {
	segs := []Segment{
		{StartMs: 0, EndMs: 2000, AudioMs: 1800, Path: "a.mp3"},
		{StartMs: 5000, EndMs: 7000, AudioMs: 1500, Path: "b.mp3"},
	}
	filter, label, err := AssemblyFilter(segs, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if label != "[out]" {
		t.Errorf("out label: got %q", label)
	}
	for _, want := range []string{
		"[0:a]aformat=sample_rates=44100:channel_layouts=stereo,adelay=delays=0:all=1[s0]",
		"[1:a]aformat=sample_rates=44100:channel_layouts=stereo,adelay=delays=5000:all=1[s1]",
		"[s0][s1]amix=inputs=2:normalize=0:dropout_transition=0,apad=whole_dur=10000ms,atrim=end=10.000[out]",
	} {
		if !strings.Contains(filter, want) {
			t.Errorf("filter missing %q:\n%s", want, filter)
		}
	}
	if strings.Contains(filter, "atempo") {
		t.Errorf("clips that fit their slot must not be stretched: %s", filter)
	}
}

// A clip longer than its slot is sped up; the slot runs to the next
// segment's start, so silence between utterances is room the dub may use.
func TestAssemblyFilterStretchesOnlyWhenTheClipOverruns(t *testing.T) {
	segs := []Segment{
		// slot 0–4000 (next starts at 4000), clip 6000 → factor 1.5
		{StartMs: 0, EndMs: 2000, AudioMs: 6000, Path: "a.mp3"},
		// slot 4000–10000 (video end), clip 3000 → fits
		{StartMs: 4000, EndMs: 5000, AudioMs: 3000, Path: "b.mp3"},
	}
	filter, _, err := AssemblyFilter(segs, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filter, "[0:a]aformat=sample_rates=44100:channel_layouts=stereo,atempo=1.5000,adelay=delays=0:all=1[s0]") {
		t.Errorf("first clip should be stretched 1.5x:\n%s", filter)
	}
	if strings.Contains(filter, "[1:a]aformat=sample_rates=44100:channel_layouts=stereo,atempo") {
		t.Errorf("second clip fits and must not be stretched:\n%s", filter)
	}
}

// atempo takes 0.5–2.0 per instance: larger factors are chained, and the
// chain is capped so speech stays intelligible.
func TestTempoChainStaysWithinFFmpegBounds(t *testing.T) {
	cases := []struct {
		factor float64
		want   []string
	}{
		{1.0, nil},
		{1.5, []string{"atempo=1.5000"}},
		{2.0, []string{"atempo=2.0000"}},
		{3.0, []string{"atempo=2.0", "atempo=1.5000"}},
		{4.0, []string{"atempo=2.0", "atempo=2.0000"}},
		{9.0, []string{"atempo=2.0", "atempo=2.0000"}}, // capped at 4x
	}
	for _, c := range cases {
		got := tempoChain(c.factor)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("factor %.1f: got %v want %v", c.factor, got, c.want)
		}
	}
}

func TestAssemblyFilterSingleSegmentSkipsAmix(t *testing.T) {
	filter, _, err := AssemblyFilter([]Segment{{StartMs: 1000, EndMs: 3000, AudioMs: 1000, Path: "a.mp3"}}, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(filter, "amix") {
		t.Errorf("one clip needs no mixer: %s", filter)
	}
	if !strings.HasSuffix(filter, "[s0]apad=whole_dur=4000ms,atrim=end=4.000[out]") {
		t.Errorf("single clip must still be padded and trimmed: %s", filter)
	}
}

func TestAssemblyFilterRejectsNothingToAssemble(t *testing.T) {
	if _, _, err := AssemblyFilter(nil, 1000); err == nil {
		t.Error("no segments must be an error, not silence")
	}
	if _, _, err := AssemblyFilter([]Segment{{Path: "a"}}, 0); err == nil {
		t.Error("zero duration must be an error")
	}
}

func TestAssembleArgsOneInputPerSegmentAndHardDuration(t *testing.T) {
	args, err := AssembleArgs([]Segment{
		{StartMs: 0, EndMs: 1000, AudioMs: 900, Path: "a.mp3"},
		{StartMs: 1000, EndMs: 2000, AudioMs: 900, Path: "b.mp3"},
	}, 2500, "out.m4a")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-i a.mp3 -i b.mp3", "-map [out]", "-t 2.500", "-c:a aac", "-f mp4 out.m4a"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q: %s", want, joined)
		}
	}
}
