package service

// KYC media kinds (31 Oct 2026): a new KYC upload must be media-service's
// private `document` kind; an id already on the seller's KYC rows may still
// be the legacy image it was uploaded as. And the reviewer's read picks the
// media-service route by the asset's kind.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

func kindSet(kinds []media.Kind) map[media.Kind]bool {
	out := map[media.Kind]bool{}
	for _, k := range kinds {
		out[k] = true
	}
	return out
}

// Strict (COMMERCE_KYC_REQUIRE_DOCUMENT_KIND=true): a new upload must be a
// document.
func TestStrictANewKYCUploadMustBeADocument(t *testing.T) {
	onFile := map[uuid.UUID]bool{uuid.New(): true}
	kinds := kindSet(kycMediaKinds(uuid.New(), onFile, true))
	if len(kinds) != 1 || !kinds[media.KindDocument] {
		t.Fatalf("strict: a new KYC upload may be %v, want only the document kind", kinds)
	}
}

// Strict: an id already on file may stay the image it was uploaded as.
func TestStrictAKYCUploadAlreadyOnFileMayStayAnImage(t *testing.T) {
	legacy := uuid.New()
	kinds := kindSet(kycMediaKinds(legacy, map[uuid.UUID]bool{legacy: true}, true))
	if len(kinds) != 2 || !kinds[media.KindDocument] || !kinds[media.KindImage] {
		t.Fatalf("strict: an on-file KYC upload may be %v, want document or image", kinds)
	}
}

// Default (switch off): today's Android app uploads KYC as images, so a new
// upload may be a document or an image.
func TestDefaultANewKYCUploadMayBeADocumentOrAnImage(t *testing.T) {
	kinds := kindSet(kycMediaKinds(uuid.New(), map[uuid.UUID]bool{}, false))
	if len(kinds) != 2 || !kinds[media.KindDocument] || !kinds[media.KindImage] {
		t.Fatalf("default: a new KYC upload may be %v, want document or image", kinds)
	}
}

// Neither setting ever accepts any kind (a video is always refused).
func TestNoSettingAcceptsAnyKind(t *testing.T) {
	id := uuid.New()
	for _, strict := range []bool{false, true} {
		for _, onFile := range []map[uuid.UUID]bool{{}, {id: true}} {
			kinds := kindSet(kycMediaKinds(id, onFile, strict))
			if kinds[media.KindAny] || kinds[media.KindVideo] || len(kinds) == 0 {
				t.Fatalf("strict=%v onFile=%v: kinds %v admit a video", strict, onFile[id], kinds)
			}
		}
	}
}

// The switch is off unless set.
func TestTheSwitchDefaultsOff(t *testing.T) {
	if New(nil, nil, "").kycRequireDocumentKind {
		t.Fatal("a fresh service requires the document kind; the current Android app would be refused")
	}
	if !New(nil, nil, "").WithKYCRequireDocumentKind(true).kycRequireDocumentKind {
		t.Fatal("WithKYCRequireDocumentKind(true) did not take")
	}
}

func TestTheReviewerReadIsPickedByKind(t *testing.T) {
	if src, ok := kycSourceFor("document"); !ok || src != kycFromDocumentBytes {
		t.Fatal("a document is not read through document-bytes")
	}
	if src, ok := kycSourceFor("image"); !ok || src != kycFromImageBytes {
		t.Fatal("a legacy image is not read through image-bytes")
	}
	for _, other := range []string{"video", "audio", "", "Document "} {
		if _, ok := kycSourceFor(other); ok {
			t.Errorf("file_type %q is opened as a KYC document", other)
		}
	}
}

func TestOnlyTheExactValueTrueRequiresTheDocumentKind(t *testing.T) {
	if !KYCRequireDocumentKind("true") {
		t.Fatal(`"true" did not turn the strict rule on`)
	}
	for _, v := range []string{"", "false", "0", "1", "yes", "TRUE", " true", "on"} {
		if KYCRequireDocumentKind(v) {
			t.Errorf("COMMERCE_KYC_REQUIRE_DOCUMENT_KIND=%q turned the strict rule on", v)
		}
	}
}

func TestTheSwitchCanBeTurnedBackOff(t *testing.T) {
	if New(nil, nil, "").WithKYCRequireDocumentKind(true).WithKYCRequireDocumentKind(false).kycRequireDocumentKind {
		t.Fatal("WithKYCRequireDocumentKind(false) left the strict rule on")
	}
}

// kycStub is media-service's record route: every id is a ready, passed asset
// of the kind named for it, uploaded by owner.
func kycStub(t *testing.T, owner uuid.UUID, kinds map[uuid.UUID]string) *media.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(strings.TrimPrefix(r.URL.Path, "/v1/media/"))
		kind, ok := kinds[id]
		if err != nil || !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"id": id, "uploader_id": owner, "file_type": kind,
			"processing_status": "ready", "moderation_status": "passed",
		}})
	}))
	t.Cleanup(srv.Close)
	return media.New(srv.URL, "k")
}

// The switch end to end through verifyKYCMedia, with the on-file lookup faked.
func TestVerifyKYCMediaFollowsTheSwitch(t *testing.T) {
	ctx := context.Background()
	owner, seller := uuid.New(), uuid.New()
	newImage, legacyImage, doc, video := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	client := kycStub(t, owner, map[uuid.UUID]string{newImage: "image", legacyImage: "image", doc: "document", video: "video"})
	lookups := 0
	onFile := func(context.Context, uuid.UUID) (map[uuid.UUID]bool, error) {
		lookups++
		return map[uuid.UUID]bool{legacyImage: true}, nil
	}
	docs := func(ids ...uuid.UUID) []postgres.SellerDocument {
		out := make([]postgres.SellerDocument, len(ids))
		for i, id := range ids {
			out[i] = postgres.SellerDocument{DocumentType: "pan_card", MediaID: id}
		}
		return out
	}

	lenient := &Service{media: client, kycOnFile: onFile}
	if err := lenient.verifyKYCMedia(ctx, owner, seller, docs(newImage, doc)); err != nil {
		t.Fatalf("default: a new image or document was refused: %v", err)
	}
	if err := lenient.verifyKYCMedia(ctx, owner, seller, docs(video)); !errors.Is(err, media.ErrMediaWrongKind) {
		t.Fatalf("default: a video: %v, want ErrMediaWrongKind", err)
	}
	if lookups != 0 {
		t.Fatalf("default: read the on-file ids %d time(s); it needs none", lookups)
	}

	strict := (&Service{media: client, kycOnFile: onFile}).WithKYCRequireDocumentKind(true)
	if err := strict.verifyKYCMedia(ctx, owner, seller, docs(newImage)); !errors.Is(err, media.ErrMediaWrongKind) {
		t.Fatalf("strict: a new image: %v, want ErrMediaWrongKind", err)
	}
	if err := strict.verifyKYCMedia(ctx, owner, seller, docs(legacyImage, doc)); err != nil {
		t.Fatalf("strict: an on-file image with a new document was refused: %v", err)
	}
	if err := strict.verifyKYCMedia(ctx, owner, seller, docs(video)); !errors.Is(err, media.ErrMediaWrongKind) {
		t.Fatalf("strict: a video: %v, want ErrMediaWrongKind", err)
	}
	if lookups == 0 {
		t.Fatal("strict: never asked which ids are on file")
	}
}
