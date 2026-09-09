package service

import (
	"testing"

	"github.com/atpost/media-service/internal/store/postgres"
)

// The `avatar` rendition alias exists because the image pipeline SKIPS a
// variant whose target is larger than the original
// (internal/processing/image.go), so which renditions an asset owns depends on
// the bytes the user uploaded. A profile payload that named a fixed variant
// would 404 for every avatar the pipeline skipped that rendition for — and the
// live database already holds an avatar with no renditions at all.
func TestAvatarRenditionFallsDownTheLadderToTheOriginal(t *testing.T) {
	v := func(name, key string) postgres.MediaVariant {
		return postgres.MediaVariant{Name: name, ObjectKey: key}
	}

	for name, tc := range map[string]struct {
		variants []postgres.MediaVariant
		want     string
	}{
		"prefers the smallest adequate rendition": {
			variants: []postgres.MediaVariant{
				v("medium_1080", "k/medium"), v("small_480", "k/small"), v("thumb_150", "k/thumb"),
			},
			want: "k/thumb",
		},
		"a small upload skipped thumb_150": {
			variants: []postgres.MediaVariant{v("small_480", "k/small"), v("medium_1080", "k/medium")},
			want:     "k/small",
		},
		"only the largest rendition survived": {
			variants: []postgres.MediaVariant{v("medium_1080", "k/medium")},
			want:     "k/medium",
		},
		// The case that makes a hard-coded variant name wrong: processing never
		// produced anything. The original always exists.
		"no renditions at all": {
			variants: nil,
			want:     "k/original",
		},
		// A rendition the ladder does not name must not be substituted blindly.
		"unrelated renditions are ignored": {
			variants: []postgres.MediaVariant{v("360p", "k/360p"), v("hls", "k/hls")},
			want:     "k/original",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := pickAvatarRendition(tc.variants, "k/original"); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
