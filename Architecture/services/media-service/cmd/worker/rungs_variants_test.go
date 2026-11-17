package main

import (
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/media-service/internal/store/postgres"
)

// Every rendition row handed to InsertVariants names its asset. The first
// end-to-end run of the video tier failed on exactly this: the rows came out
// of the rung markers with a zero media id, so the insert broke the foreign
// key on every attempt and no transcode ever finished.
func TestVariantsForCarryTheMediaID(t *testing.T) {
	id := uuid.New()
	res := &rungResult{variants: []postgres.MediaVariant{{Name: "360p"}, {Name: "thumb_150"}}}

	got := variantsFor(id, res)

	if len(got) != 2 {
		t.Fatalf("want 2 variants, got %d", len(got))
	}
	for _, v := range got {
		if v.MediaAssetID != id {
			t.Fatalf("variant %q has media id %s, want %s", v.Name, v.MediaAssetID, id)
		}
	}
	if res.variants[0].MediaAssetID != uuid.Nil {
		t.Fatalf("variantsFor must not change the result it reads")
	}
}
