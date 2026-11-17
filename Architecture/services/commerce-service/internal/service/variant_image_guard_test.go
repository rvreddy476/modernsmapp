package service

// com-3: a seller-set variant image is vouched for to media-service by
// VisibleProductMediaIDs, so every write of one must be the actor's own,
// finished image. Before this, PATCH /variants/:id stored any id it was sent.
//
// The Service here has no store. The guards run before anything is read or
// written, so a refused call that reached the store would panic on the nil
// store: "nothing is written" is the test not panicking.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

type stubAsset struct {
	owner      uuid.UUID
	kind       string
	processing string
}

// variantMediaStub is media-service's GET /v1/media/:id over a fixed set of
// assets; anything else is 404. It counts the lookups.
func variantMediaStub(t *testing.T, assets map[uuid.UUID]stubAsset) (*media.Client, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		id, err := uuid.Parse(strings.TrimPrefix(r.URL.Path, "/v1/media/"))
		a, ok := assets[id]
		if err != nil || !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		processing := a.processing
		if processing == "" {
			processing = "ready"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"id": id, "uploader_id": a.owner, "file_type": a.kind,
			"processing_status": processing, "moderation_status": "passed",
		}})
	}))
	t.Cleanup(srv.Close)
	return media.New(srv.URL, "k"), &calls
}

type variantImageFixture struct {
	seller, victim                     uuid.UUID
	own, foreign, ownVideo, unfinished uuid.UUID
	svc                                *Service
	calls                              *int32
}

func newVariantImageFixture(t *testing.T) variantImageFixture {
	t.Helper()
	f := variantImageFixture{
		seller: uuid.New(), victim: uuid.New(),
		own: uuid.New(), foreign: uuid.New(), ownVideo: uuid.New(), unfinished: uuid.New(),
	}
	client, calls := variantMediaStub(t, map[uuid.UUID]stubAsset{
		f.own:        {owner: f.seller, kind: "image"},
		f.foreign:    {owner: f.victim, kind: "image"},
		f.ownVideo:   {owner: f.seller, kind: "video"},
		f.unfinished: {owner: f.seller, kind: "image", processing: "processing"},
	})
	f.svc = &Service{media: client}
	f.calls = calls
	return f
}

// noStoreTouched turns the nil-store panic into a named failure.
func noStoreTouched(t *testing.T, what string) {
	t.Helper()
	if r := recover(); r != nil {
		t.Fatalf("%s reached the store (panic: %v): the guard did not refuse before anything was read or written", what, r)
	}
}

func TestUpdateProductVariantRefusesAnotherUsersImageAndWritesNothing(t *testing.T) {
	f := newVariantImageFixture(t)
	defer noStoreTouched(t, "PATCH /variants/:id with another user's media")

	_, _, err := f.svc.UpdateProductVariant(context.Background(), f.seller, uuid.New(),
		map[string]any{"image_media_id": f.foreign.String()}, true)
	if !errors.Is(err, media.ErrNotYourMedia) {
		t.Fatalf("err = %v, want ErrNotYourMedia (403 NOT_YOUR_MEDIA)", err)
	}
	if strings.Contains(err.Error(), f.victim.String()) {
		t.Fatalf("the refusal names the real uploader, an ownership oracle: %v", err)
	}
}

func TestUpdateProductVariantRefusesAnUnparseableImageID(t *testing.T) {
	f := newVariantImageFixture(t)
	defer noStoreTouched(t, "PATCH /variants/:id with a non-UUID image")
	for _, v := range []any{"", "not-a-uuid", uuid.Nil.String(), 42.0, map[string]any{"id": f.own.String()}} {
		_, _, err := f.svc.UpdateProductVariant(context.Background(), f.seller, uuid.New(),
			map[string]any{"image_media_id": v}, true)
		if !errors.Is(err, media.ErrMediaNotFound) {
			t.Errorf("image_media_id=%#v: err = %v, want ErrMediaNotFound", v, err)
		}
	}
}

func TestVariantImagePatchRule(t *testing.T) {
	f := newVariantImageFixture(t)
	ctx := context.Background()
	cases := []struct {
		name    string
		updates map[string]any
		want    error
	}{
		{"own finished image", map[string]any{"image_media_id": f.own.String()}, nil},
		{"own image, padded", map[string]any{"image_media_id": "  " + f.own.String() + " "}, nil},
		{"another user's image", map[string]any{"image_media_id": f.foreign.String()}, media.ErrNotYourMedia},
		{"own video", map[string]any{"image_media_id": f.ownVideo.String()}, media.ErrMediaWrongKind},
		{"own unfinished upload", map[string]any{"image_media_id": f.unfinished.String()}, media.ErrMediaNotReady},
		{"unknown id", map[string]any{"image_media_id": uuid.NewString()}, media.ErrMediaNotFound},
	}
	for _, tc := range cases {
		err := f.svc.verifyVariantImagePatch(ctx, f.seller, tc.updates)
		if tc.want == nil && err != nil {
			t.Errorf("%s: refused: %v", tc.name, err)
		}
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestVariantImagePatchWithoutAnImageAsksNobody(t *testing.T) {
	f := newVariantImageFixture(t)
	ctx := context.Background()
	for _, u := range []map[string]any{
		{"selling_price_minor": 12900},
		{"image_media_id": nil}, // clearing the image
	} {
		if err := f.svc.verifyVariantImagePatch(ctx, f.seller, u); err != nil {
			t.Errorf("%v: %v", u, err)
		}
	}
	if n := atomic.LoadInt32(f.calls); n != 0 {
		t.Fatalf("media-service asked %d time(s) for a patch with no image", n)
	}
}

func TestAddProductVariantRefusesAnotherUsersImageAndWritesNothing(t *testing.T) {
	f := newVariantImageFixture(t)
	defer noStoreTouched(t, "variant create with another user's media")

	v := &postgres.ProductVariant{SKU: "SKU-1", ImageMediaID: &f.foreign}
	_, err := f.svc.AddProductVariant(context.Background(), f.seller, uuid.New(), v, 1)
	if !errors.Is(err, media.ErrNotYourMedia) {
		t.Fatalf("err = %v, want ErrNotYourMedia", err)
	}

	v = &postgres.ProductVariant{SKU: "SKU-2", ImageMediaID: &f.ownVideo}
	if _, err := f.svc.AddProductVariant(context.Background(), f.seller, uuid.New(), v, 1); !errors.Is(err, media.ErrMediaWrongKind) {
		t.Fatalf("a video as a variant image: err = %v, want ErrMediaWrongKind", err)
	}
}

// The create guard lets the seller's own image through: it reaches the store,
// which in this test is nil, so the call panics past the guard.
func TestAddProductVariantAcceptsTheSellersOwnImage(t *testing.T) {
	f := newVariantImageFixture(t)
	reached := func() (out bool) {
		defer func() { out = recover() != nil }()
		v := &postgres.ProductVariant{SKU: "SKU-3", ImageMediaID: &f.own}
		_, _ = f.svc.AddProductVariant(context.Background(), f.seller, uuid.New(), v, 1)
		return false
	}()
	if !reached {
		t.Fatal("the seller's own finished image was refused before the store")
	}
}

func TestAttributeMediaIDsListsEveryMediaAnswer(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	text := "cotton"
	sets := []postgres.AttributeValueSet{
		{Values: []postgres.ProductAttributeValue{{ValueMediaID: &a}, {ValueText: &text}}},
		{Values: []postgres.ProductAttributeValue{{ValueMediaID: &b}}},
		{},
	}
	got := attributeMediaIDs(sets)
	if len(got) != 2 || *got[0] != a || *got[1] != b {
		t.Fatalf("attributeMediaIDs = %v, want [%s %s]", got, a, b)
	}

	// And the rule applied to them is the ownership rule.
	f := newVariantImageFixture(t)
	foreign := []postgres.AttributeValueSet{{Values: []postgres.ProductAttributeValue{{ValueMediaID: &f.own}, {ValueMediaID: &f.foreign}}}}
	if err := f.svc.verifyMedia(context.Background(), f.seller, media.KindAny, attributeMediaIDs(foreign)...); !errors.Is(err, media.ErrNotYourMedia) {
		t.Fatalf("a media attribute naming another user's upload: err = %v, want ErrNotYourMedia", err)
	}
}
