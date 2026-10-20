package service

import (
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
)

// A saved draft publishes FROM the draft row, so what the upload studio put
// on it must reach the post (migration 068).
func TestApplyDraftScreenFieldsCarriesLabelAndPlace(t *testing.T) {
	name, place := "Charminar", "ChIJm5MaboqXyzsR-xPxquRpWss"
	input := &CreatePostInput{}
	applyDraftScreenFields(input, &postgres.ReelDraft{AIGenerated: true, LocationName: &name, LocationPlaceID: &place})
	if !input.AIGenerated || input.LocationName == nil || *input.LocationName != name || input.LocationPlaceID == nil || *input.LocationPlaceID != place {
		t.Fatalf("got ai=%v name=%v place=%v", input.AIGenerated, input.LocationName, input.LocationPlaceID)
	}
}

// A place id without a name is never published on its own.
func TestApplyDraftScreenFieldsNeedsAName(t *testing.T) {
	blank, place := "  ", "p1"
	input := &CreatePostInput{}
	applyDraftScreenFields(input, &postgres.ReelDraft{LocationName: &blank, LocationPlaceID: &place})
	if input.LocationName != nil || input.LocationPlaceID != nil || input.AIGenerated {
		t.Fatalf("got name=%v place=%v ai=%v", input.LocationName, input.LocationPlaceID, input.AIGenerated)
	}
}
