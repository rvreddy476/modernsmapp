package processing

import "testing"

func TestThumbnailTimestampStaysInsideShortVideo(t *testing.T) {
	for _, duration := range []float64{0.08, 0.5, 1, 2.999, 181} {
		got := thumbnailTimestamp(duration)
		if got <= 0 || got >= duration {
			t.Fatalf("duration %.3f produced out-of-range timestamp %.3f", duration, got)
		}
	}
}

func TestThumbnailTimestampUsesPreciseDuration(t *testing.T) {
	if got := thumbnailTimestamp(1); got != 0.25 {
		t.Fatalf("one-second clip timestamp = %v, want 0.25", got)
	}
	if got := thumbnailTimestamp(0); got != 0 {
		t.Fatalf("zero duration timestamp = %v, want 0", got)
	}
}
