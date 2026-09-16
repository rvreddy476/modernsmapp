package service

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/matcher"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Lane D6 unit tests: the automated moderation decision, the visibility
// table, and the deck card's photo URL. No database.

func cleanMediaStatus() *MediaPhotoStatus {
	return &MediaPhotoStatus{MediaID: uuid.New(), OwnerMatches: true, Kind: "image", Status: "ready",
		ModerationStatus: "passed", ContentType: "image/jpeg", ModerationScanned: true,
		ModerationScanner: "rekognition", ModerationLabels: []MediaPhotoLabel{}, Prepared: true}
}

func withLabels(labels ...MediaPhotoLabel) func(*MediaPhotoStatus) {
	return func(st *MediaPhotoStatus) { st.ModerationLabels = labels }
}

func TestClassifyPhoto(t *testing.T) {
	cfg := DefaultPhotoSafetyConfig()
	zero, one := 0, 1
	cases := []struct {
		name    string
		mutate  func(*MediaPhotoStatus)
		primary bool
		status  string
		reason  string
	}{
		{"clean primary", nil, true, store.PhotoStatusApproved, ""},
		{"explicit (v6 parent)", withLabels(MediaPhotoLabel{Name: "Nudity", Parent: "Explicit Nudity", Confidence: 97}), false, store.PhotoStatusRejected, PhotoReasonExplicit},
		{"explicit (v7 parent)", withLabels(MediaPhotoLabel{Name: "Exposed Male Genitalia", Parent: "Explicit", Confidence: 90}), true, store.PhotoStatusRejected, PhotoReasonExplicit},
		{"sexual activity", withLabels(MediaPhotoLabel{Name: "Sexual Activity", Confidence: 88}), false, store.PhotoStatusRejected, PhotoReasonExplicit},
		{"explicit below the bar is not explicit", withLabels(MediaPhotoLabel{Name: "Explicit Nudity", Confidence: 60}), false, store.PhotoStatusApproved, ""},
		{"explicit beats borderline", withLabels(MediaPhotoLabel{Name: "Swimwear or Underwear", Confidence: 95}, MediaPhotoLabel{Name: "Explicit Nudity", Confidence: 81}), false, store.PhotoStatusRejected, PhotoReasonExplicit},
		{"swimwear", withLabels(MediaPhotoLabel{Name: "Swimwear or Underwear", Confidence: 91}), false, store.PhotoStatusPendingReview, PhotoReasonBorderline},
		{"suggestive parent", withLabels(MediaPhotoLabel{Name: "Revealing Clothes", Parent: "Suggestive", Confidence: 85}), true, store.PhotoStatusPendingReview, PhotoReasonBorderline},
		{"violence", withLabels(MediaPhotoLabel{Name: "Violence", Confidence: 82}), false, store.PhotoStatusPendingReview, PhotoReasonBorderline},
		{"drugs", withLabels(MediaPhotoLabel{Name: "Drugs", Confidence: 90}), false, store.PhotoStatusPendingReview, PhotoReasonBorderline},
		{"hate symbols", withLabels(MediaPhotoLabel{Name: "Hate Symbols", Confidence: 99}), false, store.PhotoStatusPendingReview, PhotoReasonBorderline},
		{"borderline below the bar", withLabels(MediaPhotoLabel{Name: "Suggestive", Confidence: 79.9}), false, store.PhotoStatusApproved, ""},
		{"alcohol is not a review label", withLabels(MediaPhotoLabel{Name: "Alcohol", Confidence: 99}), false, store.PhotoStatusApproved, ""},
		{"never scanned", func(st *MediaPhotoStatus) { st.ModerationScanned = false }, false, store.PhotoStatusPendingReview, PhotoReasonNotScanned},
		{"primary with no face", func(st *MediaPhotoStatus) { st.FaceCount = &zero }, true, store.PhotoStatusPendingReview, PhotoReasonNoFace},
		{"non-primary with no face", func(st *MediaPhotoStatus) { st.FaceCount = &zero }, false, store.PhotoStatusApproved, ""},
		{"primary, face count unanswered", nil, true, store.PhotoStatusApproved, ""},
		{"primary with one face", func(st *MediaPhotoStatus) { st.FaceCount = &one }, true, store.PhotoStatusApproved, ""},
		{"media no longer passed", func(st *MediaPhotoStatus) { st.ModerationStatus = "rejected" }, true, store.PhotoStatusRejected, PhotoReasonMediaUnavailable},
		{"media not the owner's", func(st *MediaPhotoStatus) { st.OwnerMatches = false }, true, store.PhotoStatusRejected, PhotoReasonMediaUnavailable},
	}
	for _, tc := range cases {
		st := cleanMediaStatus()
		if tc.mutate != nil {
			tc.mutate(st)
		}
		d := ClassifyPhoto(cfg, st, tc.primary)
		if d.Status != tc.status || d.Reason != tc.reason || d.Source != store.PhotoSourceAuto {
			t.Errorf("%s: decision = %s/%s/%s, want %s/%s/auto", tc.name, d.Status, d.Reason, d.Source, tc.status, tc.reason)
		}
		if !json.Valid(d.Labels) {
			t.Errorf("%s: labels %q are not JSON", tc.name, d.Labels)
		}
	}
	if d := ClassifyPhoto(cfg, nil, true); d.Status != store.PhotoStatusRejected {
		t.Fatalf("nil status = %+v; want rejected", d)
	}
	// NO_FACE is config.
	noFaceOff := cfg
	noFaceOff.RequireFaceOnPrimary = false
	st := cleanMediaStatus()
	st.FaceCount = &zero
	if d := ClassifyPhoto(noFaceOff, st, true); d.Status != store.PhotoStatusApproved {
		t.Fatalf("require_face=false: %+v", d)
	}
}

func TestPhotoVariantFor(t *testing.T) {
	stranger := PhotoViewer{}
	cases := []struct {
		visibility string
		viewer     PhotoViewer
		want       string
	}{
		{"public", stranger, PhotoVariantFull},
		{"public", PhotoViewer{OwnerBlursUntilMatch: true}, PhotoVariantBlurred},
		{"public", PhotoViewer{OwnerBlursUntilMatch: true, Matched: true}, PhotoVariantFull},
		{"match_only", stranger, PhotoVariantBlurred},
		{"match_only", PhotoViewer{OwnerSparkedViewer: true}, PhotoVariantBlurred},
		{"match_only", PhotoViewer{Matched: true}, PhotoVariantFull},
		{"match_only", PhotoViewer{IsOwner: true, OwnerBlursUntilMatch: true}, PhotoVariantFull},
		{"sparked_only", stranger, PhotoVariantBlurred},
		{"sparked_only", PhotoViewer{OwnerSparkedViewer: true}, PhotoVariantFull},
		{"sparked_only", PhotoViewer{OwnerSparkedViewer: true, OwnerBlursUntilMatch: true}, PhotoVariantBlurred},
		{"", stranger, PhotoVariantBlurred},
		{"surprise", stranger, PhotoVariantBlurred},
	}
	for _, tc := range cases {
		if got := PhotoVariantFor(tc.visibility, tc.viewer); got != tc.want {
			t.Errorf("%q %+v = %s, want %s", tc.visibility, tc.viewer, got, tc.want)
		}
	}
}

func deckCandidate(id, photoID uuid.UUID, visibility string, blur bool) matcher.ScoredCandidate {
	return matcher.ScoredCandidate{Candidate: &store.CandidateProfile{
		UserID: id, PrimaryPhotoID: &photoID, PrimaryPhotoVisibility: visibility, BlurPhotosUntilMatch: blur,
	}}
}

// The deck names the photo image route for the variant the viewer may have,
// and nothing that identifies the media.
func TestBuildCard_PhotoVisibilityInTheDeck(t *testing.T) {
	svc := &Service{}
	owner, photoID := uuid.New(), uuid.New()
	matched := map[uuid.UUID]struct{}{owner: {}}

	check := func(name string, card PulseCard, wantVariant string) {
		t.Helper()
		want := "/v1/dating/photos/" + photoID.String() + "/" + wantVariant
		if card.Profile.PrimaryPhotoURL != want || card.Profile.PrimaryPhotoBlurred != (wantVariant == PhotoVariantBlurred) {
			t.Fatalf("%s: url=%q blurred=%v; want %q", name, card.Profile.PrimaryPhotoURL, card.Profile.PrimaryPhotoBlurred, want)
		}
		raw, _ := json.Marshal(card)
		for _, leak := range []string{"/media/", "media_id", "?blurred=1"} {
			if strings.Contains(string(raw), leak) {
				t.Fatalf("%s: card JSON contains %q: %s", name, leak, raw)
			}
		}
		if wantVariant == PhotoVariantBlurred && strings.Contains(string(raw), "/full") {
			t.Fatalf("%s: a blurred card also names the full image: %s", name, raw)
		}
	}
	check("match_only, stranger", svc.buildCard(deckCandidate(owner, photoID, "match_only", false), nil, nil, nil, nil), PhotoVariantBlurred)
	check("match_only, matched", svc.buildCard(deckCandidate(owner, photoID, "match_only", false), nil, matched, nil, nil), PhotoVariantFull)
	check("public, stranger", svc.buildCard(deckCandidate(owner, photoID, "public", false), nil, nil, nil, nil), PhotoVariantFull)
	check("public + blur until match, stranger", svc.buildCard(deckCandidate(owner, photoID, "public", true), nil, nil, nil, nil), PhotoVariantBlurred)
	check("public + blur until match, matched", svc.buildCard(deckCandidate(owner, photoID, "public", true), nil, matched, nil, nil), PhotoVariantFull)
	sparked := deckCandidate(owner, photoID, "sparked_only", false)
	check("sparked_only, not sparked", svc.buildCard(sparked, nil, nil, nil, nil), PhotoVariantBlurred)
	sparked.Candidate.SparkedViewer = true
	check("sparked_only, owner sparked the viewer", svc.buildCard(sparked, nil, nil, nil, nil), PhotoVariantFull)

	none := svc.buildCard(matcher.ScoredCandidate{Candidate: &store.CandidateProfile{UserID: owner}}, nil, nil, nil, nil)
	if none.Profile.PrimaryPhotoURL != "" {
		t.Fatalf("no primary photo: url=%q", none.Profile.PrimaryPhotoURL)
	}
}

// The other dating reads (match detail, incoming sparks, explain, and the
// internal profile preview's {user_id, first_name}) carry no photo URL or
// media id, so the deck is the only read that returns photos today. This
// pins that: a field added to one of them must go through PhotoVariantFor.
func TestNonDeckReadsCarryNoPhotos(t *testing.T) {
	for _, v := range []any{store.Match{}, store.Spark{}, CandidateExplanation{}, ExplainReason{}} {
		typ := reflect.TypeOf(v)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			tag := strings.ToLower(f.Tag.Get("json") + f.Name)
			if strings.Contains(tag, "media") || strings.Contains(tag, "photo") || strings.Contains(tag, "url") {
				t.Errorf("%s.%s (%q) looks like a photo field; enforce visibility through PhotoVariantFor", typ.Name(), f.Name, f.Tag.Get("json"))
			}
		}
	}
}
