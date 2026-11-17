//go:build integration

package http

// com-3, over the real routes and a live database: PATCH /variants/:id used
// to store any image_media_id it was sent, and VisibleProductMediaIDs then
// told media-service the seller (and, once the listing is live, everyone)
// may see it. Another user's private photo, read back through commerce.
//
//	COMMERCE_TEST_DSN=postgres://.../commerce_it_test go test -tags=integration ./internal/http/... -run VariantImage -v
//
// CI only, on commerce_it_test. Fixtures: seedSellerSurface and mediaEngine
// (seller_surface_integration_test.go).

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

func variantImageRow(t *testing.T, variantID uuid.UUID) (image *uuid.UUID, approval string) {
	t.Helper()
	if err := edgePool.QueryRow(context.Background(), `
		SELECT pv.image_media_id, p.approval_status
		  FROM product_variants pv JOIN products p ON p.id = pv.product_id
		 WHERE pv.id = $1`, variantID).Scan(&image, &approval); err != nil {
		t.Fatalf("read variant: %v", err)
	}
	return image, approval
}

func TestVariantImage_AnotherUsersMediaIsRefusedAndNothingIsWritten(t *testing.T) {
	s := seedSellerSurface(t, 5)
	victimUpload := uuid.New()
	r := mediaEngine(t, mediaStub(t, victimUpload, s.otherUserID, "image"))

	// `revalidate: true` so the approved listing's review gate is not what
	// refuses it: the refusal must be the media check's.
	w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+s.variantID.String(), s.sellerUserID,
		map[string]any{"image_media_id": victimUpload, "revalidate": true})
	if w.Code != http.StatusForbidden || !bytes.Contains(w.Body.Bytes(), []byte("NOT_YOUR_MEDIA")) {
		t.Fatalf("PATCH with another user's media = %d, want 403 NOT_YOUR_MEDIA\n%s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte(s.otherUserID.String())) {
		t.Fatalf("the refusal named the real uploader, an ownership oracle\n%s", w.Body.String())
	}

	image, approval := variantImageRow(t, s.variantID)
	if image != nil {
		t.Fatalf("variant image_media_id = %s after a refused PATCH, want NULL", image)
	}
	if approval != "approved" {
		t.Fatalf("listing approval_status = %s after a refused PATCH, want approved (nothing written)", approval)
	}

	// And commerce does not vouch for it, to the seller or to anyone.
	store := postgres.New(edgePool)
	for _, viewer := range []uuid.UUID{s.sellerUserID, uuid.Nil} {
		set, err := store.VisibleProductMediaIDs(context.Background(), viewer, []uuid.UUID{victimUpload})
		if err != nil {
			t.Fatal(err)
		}
		if set[victimUpload] {
			t.Fatalf("VisibleProductMediaIDs vouches for another user's media to viewer %s", viewer)
		}
	}
}

func TestVariantImage_TheSellersOwnImageIsStored(t *testing.T) {
	s := seedSellerSurface(t, 5)
	ownUpload := uuid.New()
	r := mediaEngine(t, mediaStub(t, ownUpload, s.sellerUserID, "image"))

	w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+s.variantID.String(), s.sellerUserID,
		map[string]any{"image_media_id": ownUpload, "revalidate": true})
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH with the seller's own image = %d, want 200\n%s", w.Code, w.Body.String())
	}
	image, _ := variantImageRow(t, s.variantID)
	if image == nil || *image != ownUpload {
		t.Fatalf("variant image_media_id = %v, want %s", image, ownUpload)
	}
	set, err := postgres.New(edgePool).VisibleProductMediaIDs(context.Background(), s.sellerUserID, []uuid.UUID{ownUpload})
	if err != nil {
		t.Fatal(err)
	}
	if !set[ownUpload] {
		t.Fatal("the seller cannot see their own variant image through commerce")
	}

	// Clearing the image needs no media check and is always allowed.
	w = call(t, r, http.MethodPatch, "/v1/commerce/variants/"+s.variantID.String(), s.sellerUserID,
		map[string]any{"image_media_id": nil, "revalidate": true})
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH clearing the image = %d, want 200\n%s", w.Code, w.Body.String())
	}
	if image, _ := variantImageRow(t, s.variantID); image != nil {
		t.Fatalf("variant image_media_id = %s after clearing, want NULL", image)
	}
}
