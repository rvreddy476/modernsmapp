package processing

import (
	"strings"
	"testing"
)

// The storyboard builders are pure so the grid, the ffmpeg argv and the cue
// file can be pinned without ffmpeg.

func TestPlanStoryboardSkipsShortVideos(t *testing.T) {
	for _, d := range []float64{0, 12.5, 60} {
		if _, ok := PlanStoryboard(d); ok {
			t.Errorf("duration %v should get no storyboard", d)
		}
	}
	plan, ok := PlanStoryboard(60.001)
	if !ok {
		t.Fatal("a video just over a minute gets a storyboard")
	}
	if plan.Frames != 100 || plan.Columns != 10 || plan.Rows != 10 {
		t.Fatalf("grid = %d frames %dx%d, want 100 in 10x10", plan.Frames, plan.Columns, plan.Rows)
	}
	if plan.SheetWidth() != 1600 || plan.SheetHeight() != 900 {
		t.Fatalf("sheet = %dx%d, want 1600x900", plan.SheetWidth(), plan.SheetHeight())
	}
}

func TestPlanStoryboardIntervalIsDurationOverHundred(t *testing.T) {
	plan, _ := PlanStoryboard(1000)
	if plan.IntervalSeconds != 10 {
		t.Fatalf("interval = %v, want 10", plan.IntervalSeconds)
	}
	plan, _ = PlanStoryboard(90)
	if plan.IntervalSeconds != 0.9 {
		t.Fatalf("interval = %v, want 0.9", plan.IntervalSeconds)
	}
}

func TestStoryboardFFmpegArgs(t *testing.T) {
	plan, _ := PlanStoryboard(1000)
	got := strings.Join(StoryboardFFmpegArgs("/tmp/in", "/tmp/out.jpg", plan), " ")
	want := "-y -i /tmp/in -vf fps=0.1,scale=160:90,tile=10x10 -frames:v 1 -q:v 5 /tmp/out.jpg"
	if got != want {
		t.Fatalf("argv\n got %s\nwant %s", got, want)
	}
	// A non-round interval prints as a plain decimal, never an exponent.
	plan, _ = PlanStoryboard(3 * 3600)
	args := strings.Join(StoryboardFFmpegArgs("i", "o", plan), " ")
	if !strings.Contains(args, "fps=0.009259,") {
		t.Fatalf("three-hour rate not rendered as a decimal: %s", args)
	}
}

func TestStoryboardVTTCues(t *testing.T) {
	plan, _ := PlanStoryboard(1000)
	vtt := StoryboardVTT(plan, "storyboard.jpg")
	if !strings.HasPrefix(vtt, "WEBVTT\n") {
		t.Fatalf("not a WebVTT file: %q", vtt[:20])
	}
	cues := strings.Count(vtt, " --> ")
	if cues != 100 {
		t.Fatalf("cues = %d, want 100", cues)
	}
	for _, line := range []string{
		"00:00:00.000 --> 00:00:10.000\nstoryboard.jpg#xywh=0,0,160,90",
		"00:00:10.000 --> 00:00:20.000\nstoryboard.jpg#xywh=160,0,160,90",
		// frame 10 starts the second row
		"00:01:40.000 --> 00:01:50.000\nstoryboard.jpg#xywh=0,90,160,90",
		// frame 99 is the last tile; its cue ends at the duration
		"00:16:30.000 --> 00:16:40.000\nstoryboard.jpg#xywh=1440,810,160,90",
	} {
		if !strings.Contains(vtt, line) {
			t.Errorf("missing cue %q in\n%s", line, vtt)
		}
	}
}

func TestStoryboardVTTLastCueEndsAtDuration(t *testing.T) {
	// 61.5 s / 100 = 0.615 s per frame; rounding must not push the last
	// cue past the end of the video.
	plan, _ := PlanStoryboard(61.5)
	vtt := StoryboardVTT(plan, "storyboard.jpg")
	if !strings.Contains(vtt, " --> 00:01:01.500\n") {
		t.Fatalf("last cue does not end at 61.5 s:\n%s", vtt[len(vtt)-120:])
	}
}

func TestVTTTimestamp(t *testing.T) {
	cases := map[float64]string{0: "00:00:00.000", 1.5: "00:00:01.500", 3661.25: "01:01:01.250", 0.0004: "00:00:00.000"}
	for in, want := range cases {
		if got := vttTimestamp(in); got != want {
			t.Errorf("vttTimestamp(%v) = %s, want %s", in, got, want)
		}
	}
}
