package service

import (
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/media-service/internal/store/postgres"
)

// The HEAD reports the row of the object the GET delivers. For the avatar
// alias the GET's choice is pickAvatarRendition; this pins that
// resolveServedObject lands on the same key for every shape of ladder.
func TestHeadAvatarResolvesTheKeyTheGetDelivers(t *testing.T) {
	id := uuid.New()
	size := int64(10)
	v := func(name string) postgres.MediaVariant {
		return postgres.MediaVariant{MediaAssetID: id, Name: name, Mime: "image/webp", SizeBytes: &size, ObjectKey: "user/u/" + name}
	}
	cases := map[string][]postgres.MediaVariant{
		"none":                 nil,
		"all":                  {v("medium_1080"), v("small_480"), v("thumb_150")},
		"no thumb":             {v("medium_1080"), v("small_480")},
		"medium only":          {v("medium_1080")},
		"unrelated renditions": {v("720p"), v("large_2048")},
	}
	for name, variants := range cases {
		media := &postgres.MediaAsset{ID: id, StorageKey: "user/u/original", MimeType: "image/png", FileSizeBytes: 99, Variants: variants}
		got, ok := resolveServedObject(media, AvatarVariant, true)
		want := pickAvatarRendition(variants, media.StorageKey)
		if !ok || got.key != want {
			t.Errorf("%s: the HEAD describes %q, the GET delivers %q", name, got.key, want)
		}
	}
}

func TestResolveServedObject(t *testing.T) {
	id := uuid.New()
	size := int64(4096)
	media := &postgres.MediaAsset{
		ID: id, StorageKey: "user/u/original", MimeType: "video/quicktime", FileSizeBytes: 8192,
		Variants: []postgres.MediaVariant{
			{Name: "720p", Mime: "video/mp4", SizeBytes: &size, ObjectKey: "user/u/720p.mp4"},
			{Name: "untyped", ObjectKey: "user/u/untyped"},
		},
	}
	cases := []struct {
		variant string
		ladder  bool
		ok      bool
		want    servedObject
	}{
		{"original", true, true, servedObject{"original", "user/u/original", "video/quicktime", 8192}},
		{"720p", true, true, servedObject{"720p", "user/u/720p.mp4", "video/mp4", 4096}},
		{"untyped", true, true, servedObject{"untyped", "user/u/untyped", "video/quicktime", -1}},
		{"hls", true, false, servedObject{}},
		{"", true, false, servedObject{}},
		{"1080p", true, false, servedObject{}},
		{AvatarVariant, true, true, servedObject{"original", "user/u/original", "video/quicktime", 8192}},
		{AvatarVariant, false, false, servedObject{}},
	}
	for _, c := range cases {
		got, ok := resolveServedObject(media, c.variant, c.ladder)
		if ok != c.ok || got != c.want {
			t.Errorf("%q (ladder=%v): got %+v, %v want %+v, %v", c.variant, c.ladder, got, ok, c.want, c.ok)
		}
	}
}
