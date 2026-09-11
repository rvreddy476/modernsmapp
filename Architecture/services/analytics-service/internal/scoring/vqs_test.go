package scoring

import (
	"testing"

	"github.com/atpost/shared/postclassify"
)

// The long-form threshold is 10 s and the short-form one 3 s. The
// function used to compare the raw literal "long_video", so a legacy
// "video" row — which IS long-form — was scored against the reel bar:
// five seconds of a ten-minute video earned the full base score.
func TestVQSTreatsVideoAsLongForm(t *testing.T) {
	const durationMS, watchedMS = 600_000, 5_000
	canonical := ComputeVQS(postclassify.LongVideo, durationMS, watchedMS, 1, nil, 1)
	legacy := ComputeVQS("video", durationMS, watchedMS, 1, nil, 1)
	if legacy != canonical {
		t.Fatalf("ComputeVQS(\"video\") = %v, ComputeVQS(\"long_video\") = %v; a legacy video must be scored as long-form", legacy, canonical)
	}
	short := ComputeVQS(postclassify.Flick, durationMS, watchedMS, 1, nil, 1)
	if short <= canonical {
		t.Fatalf("5 s of a flick (%v) must outscore 5 s of a long_video (%v): the thresholds differ", short, canonical)
	}
	// And the legacy short-form synonyms still get the short-form bar.
	if reel := ComputeVQS("reel", durationMS, watchedMS, 1, nil, 1); reel != short {
		t.Fatalf("ComputeVQS(\"reel\") = %v, want the flick score %v", reel, short)
	}
}
