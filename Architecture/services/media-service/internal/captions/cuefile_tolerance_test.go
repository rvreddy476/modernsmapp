package captions

import (
	"strings"
	"testing"
)

// REG-4: overlapping cues are valid WebVTT (two speakers, rolling captions,
// YouTube auto-caption exports) and many SRT tools emit small overlaps. They
// used to be refused with 400 CAPTION_OVERLAP.
func TestOverlappingCuesAreAccepted(t *testing.T) {
	for name, parse := range map[string]func() ([]Cue, error){
		"vtt": func() ([]Cue, error) {
			return ParseWebVTT("WEBVTT\n\n00:00:01.000 --> 00:00:04.000\nAlice\n\n00:00:03.500 --> 00:00:06.000\nBob\n")
		},
		"srt": func() ([]Cue, error) {
			return ParseSRT("1\n00:00:01,000 --> 00:00:04,000\nAlice\n\n2\n00:00:03,500 --> 00:00:06,000\nBob\n")
		},
		"same start": func() ([]Cue, error) {
			return ParseWebVTT("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\ntop\n\n00:00:01.000 --> 00:00:05.000\nbottom\n")
		},
	} {
		cues, err := parse()
		if err != nil {
			t.Fatalf("%s: overlapping cues refused: %v", name, err)
		}
		if len(cues) != 2 {
			t.Fatalf("%s: %d cues, want 2", name, len(cues))
		}
		// Each cue keeps its own end; the render is valid WebVTT that parses
		// back to the same cues.
		back, err := ParseWebVTT(RenderWebVTT(cues))
		if err != nil {
			t.Fatalf("%s: rendered file does not parse: %v", name, err)
		}
		for i := range cues {
			if back[i].StartMs != cues[i].StartMs || back[i].EndMs != cues[i].EndMs || back[i].Text != cues[i].Text {
				t.Fatalf("%s: cue %d round-tripped as %+v, want %+v", name, i, back[i], cues[i])
			}
		}
	}
	cues, _ := ParseWebVTT("WEBVTT\n\n00:00:01.000 --> 00:00:04.000\nAlice\n\n00:00:03.500 --> 00:00:06.000\nBob\n")
	if cues[0].EndMs != 4000 || cues[1].StartMs != 3500 {
		t.Fatalf("timings changed: %+v", cues)
	}
}

// REG-8's server half: the timestamp forms real files and the web's
// SRT-to-VTT conversion produce.
func TestTimestampFormsRealFilesUse(t *testing.T) {
	for name, src := range map[string]string{
		"single-digit hours, comma, sent as vtt": "WEBVTT\n\n1\n0:00:01,000 --> 0:00:02,000\nA\n",
		"comma in vtt":                           "WEBVTT\n\n00:00:01,000 --> 00:00:02.000\nA\n",
		"no hours":                               "WEBVTT\n\n00:01.000 --> 00:02.000\nA\n",
		"no hours, comma":                        "WEBVTT\n\n00:01,000 --> 00:02,000\nA\n",
		"single-digit hours":                     "WEBVTT\n\n0:00:01.000 --> 0:00:02.000\nA\n",
	} {
		cues, err := ParseWebVTT(src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(cues) != 1 || cues[0].StartMs != 1000 || cues[0].EndMs != 2000 {
			t.Fatalf("%s: %+v", name, cues)
		}
		// Served in the canonical form only.
		if out := RenderWebVTT(cues); strings.Contains(out, ",") || !strings.Contains(out, "00:00:01.000 --> 00:00:02.000") {
			t.Fatalf("%s: rendered %q", name, out)
		}
	}
	for name, src := range map[string]string{
		"single-digit hours in srt": "1\n0:00:01,000 --> 0:00:02,000\nA\n",
		"full stop in srt":          "1\n00:00:01.000 --> 00:00:02.000\nA\n",
	} {
		if cues, err := ParseSRT(src); err != nil || len(cues) != 1 || cues[0].StartMs != 1000 {
			t.Fatalf("%s: %+v %v", name, cues, err)
		}
	}
}
