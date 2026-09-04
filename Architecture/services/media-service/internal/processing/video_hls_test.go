package processing

import "testing"

// A reel is capped at 720p on every path, not just the MP4 renditions: the
// HLS ladder used to encode a 1080p variant for a 2-minute phone clip after
// the MP4 pass had already skipped it, which doubled the wait on the phone.
func TestHLSLadderCapsReelsAt720p(t *testing.T) {
	got := hlsVariantsFor(HLSPlan{Reel: true, SourceHeight: 1080})
	if len(got) != 2 || got[0].Quality != "360p" || got[1].Quality != "720p" {
		t.Fatalf("reel ladder = %v, want 360p and 720p", qualities(got))
	}
}

func TestHLSLadderKeepsFullLadderForLongForm(t *testing.T) {
	got := hlsVariantsFor(HLSPlan{Reel: false, SourceHeight: 1080})
	if len(got) != 3 || got[2].Quality != "1080p" {
		t.Fatalf("long-form ladder = %v, want 360p, 720p and 1080p", qualities(got))
	}
}

// Upscaling a 720p source to a 1080p rung costs a full encode and gains no
// detail; the rung is dropped. A source below the lowest rung still gets it,
// so every video has at least one HLS variant to play.
func TestHLSLadderNeverExceedsTheSource(t *testing.T) {
	got := hlsVariantsFor(HLSPlan{Reel: false, SourceHeight: 720})
	if len(got) != 2 || got[1].Quality != "720p" {
		t.Fatalf("720p source ladder = %v, want 360p and 720p", qualities(got))
	}
	tiny := hlsVariantsFor(HLSPlan{Reel: true, SourceHeight: 240})
	if len(tiny) != 1 || tiny[0].Quality != "360p" {
		t.Fatalf("240p source ladder = %v, want the single lowest rung", qualities(tiny))
	}
}

// Unknown source height (0) must not drop everything.
func TestHLSLadderWithUnknownHeightUsesTheWholeLadder(t *testing.T) {
	got := hlsVariantsFor(HLSPlan{Reel: false})
	if len(got) != len(defaultHLSVariants) {
		t.Fatalf("unknown height ladder = %v, want all %d rungs", qualities(got), len(defaultHLSVariants))
	}
}

func TestReelsEncodeWithTheFasterPreset(t *testing.T) {
	if got := hlsPreset(HLSPlan{Reel: true}); got != "veryfast" {
		t.Fatalf("reel preset = %q, want veryfast", got)
	}
	if got := hlsPreset(HLSPlan{Reel: false}); got != "fast" {
		t.Fatalf("long-form preset = %q, want fast", got)
	}
}

func qualities(vs []HLSVariant) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Quality)
	}
	return out
}
