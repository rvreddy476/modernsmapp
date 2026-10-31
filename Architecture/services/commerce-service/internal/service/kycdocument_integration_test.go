//go:build integration

package service

// KYC step 5 against a real database and a stand-in media-service record
// route (31 Oct 2026). Which media ids may stay images is decided by the
// seller's own seller_documents rows, so only the database proves it:
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/service/ -run KYCDocumentIT -v

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// kycMediaStub answers GET /v1/media/:id as a ready, passed asset of the
// kind named for that id, uploaded by owner.
type kycMediaStub struct {
	mu    sync.Mutex
	owner uuid.UUID
	kinds map[uuid.UUID]string
}

func (s *kycMediaStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[1] != "media" {
		http.NotFound(w, r)
		return
	}
	id, err := uuid.Parse(parts[2])
	s.mu.Lock()
	kind, ok := s.kinds[id]
	s.mu.Unlock()
	if err != nil || !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
		"id": id, "uploader_id": s.owner, "file_type": kind,
		"processing_status": "ready", "moderation_status": "passed",
	}})
}

func TestKYCDocumentITStrictNewUploadsMustBeDocumentsAndOnFileImagesStay(t *testing.T) {
	ctx := context.Background()
	sellerID := seedSeller(t, "kycdoc")
	var userID uuid.UUID
	if err := svcTestPool.QueryRow(ctx, `SELECT user_id FROM sellers WHERE id = $1`, sellerID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	legacyImage, newImage, newDoc, newBack, video := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	stub := &kycMediaStub{owner: userID, kinds: map[uuid.UUID]string{
		legacyImage: "image", newImage: "image", newDoc: "document", newBack: "document", video: "video",
	}}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)
	// The strict setting: COMMERCE_KYC_REQUIRE_DOCUMENT_KIND=true.
	svc := (&Service{store: postgres.New(svcTestPool), media: media.New(srv.URL, "k")}).WithKYCRequireDocumentKind(true)

	// A seller who finished KYC before documents existed: their PAN is on
	// file as an image upload.
	if _, err := svcTestPool.Exec(ctx, `
		INSERT INTO seller_documents (id, seller_id, document_type, media_id, verification_status, uploaded_at)
		VALUES ($1, $2, 'pan_card', $3, 'pending', NOW())`, uuid.New(), sellerID, legacyImage); err != nil {
		t.Fatal(err)
	}
	mediaOf := func(docType string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := svcTestPool.QueryRow(ctx,
			`SELECT media_id FROM seller_documents WHERE seller_id = $1 AND document_type = $2`, sellerID, docType).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	// 1. Resubmitting the step with the PAN image already on file still works.
	if err := svc.SaveDocuments(ctx, userID, []postgres.SellerDocument{
		{DocumentType: "pan_card", MediaID: legacyImage},
	}); err != nil {
		t.Fatalf("resubmitting an on-file image was refused: %v", err)
	}

	// 2. A NEW image upload is refused, and nothing is written.
	err := svc.SaveDocuments(ctx, userID, []postgres.SellerDocument{
		{DocumentType: "pan_card", MediaID: legacyImage},
		{DocumentType: "cancelled_cheque", MediaID: newImage},
	})
	if !errors.Is(err, media.ErrMediaWrongKind) {
		t.Fatalf("a new KYC image upload: %v, want ErrMediaWrongKind", err)
	}
	var cheques int
	if err := svcTestPool.QueryRow(ctx,
		`SELECT count(*) FROM seller_documents WHERE seller_id = $1 AND document_type = 'cancelled_cheque'`, sellerID).Scan(&cheques); err != nil {
		t.Fatal(err)
	}
	if cheques != 0 {
		t.Fatal("a refused KYC set was partly written")
	}

	// 3. A video is never accepted, front or back.
	if err := svc.SaveDocuments(ctx, userID, []postgres.SellerDocument{
		{DocumentType: "passport", MediaID: newDoc, BackMediaID: &video},
	}); !errors.Is(err, media.ErrMediaWrongKind) {
		t.Fatalf("a video back: %v, want ErrMediaWrongKind", err)
	}

	// 4. New documents are accepted, front and back.
	if err := svc.SaveDocuments(ctx, userID, []postgres.SellerDocument{
		{DocumentType: "pan_card", MediaID: newDoc},
		{DocumentType: "passport", MediaID: newImage, BackMediaID: &newBack},
	}); !errors.Is(err, media.ErrMediaWrongKind) {
		t.Fatalf("a new image front with a document back: %v, want ErrMediaWrongKind", err)
	}
	if err := svc.SaveDocuments(ctx, userID, []postgres.SellerDocument{
		{DocumentType: "pan_card", MediaID: newDoc},
		{DocumentType: "cancelled_cheque", MediaID: newBack},
	}); err != nil {
		t.Fatalf("new private documents were refused: %v", err)
	}
	if got := mediaOf("pan_card"); got != newDoc {
		t.Fatalf("pan_card media = %s, want the new document", got)
	}

	// 5. Once replaced, the legacy image is no longer on file: it cannot come
	// back as a "resubmission".
	if err := svc.SaveDocuments(ctx, userID, []postgres.SellerDocument{
		{DocumentType: "pan_card", MediaID: legacyImage},
	}); !errors.Is(err, media.ErrMediaWrongKind) {
		t.Fatalf("a replaced legacy image came back: %v, want ErrMediaWrongKind", err)
	}
}

// The default setting (switch off): today's Android app uploads KYC as
// images, and a new image is accepted beside a document. A video never is.
func TestKYCDocumentITDefaultAcceptsANewImage(t *testing.T) {
	ctx := context.Background()
	sellerID := seedSeller(t, "kycdef")
	var userID uuid.UUID
	if err := svcTestPool.QueryRow(ctx, `SELECT user_id FROM sellers WHERE id = $1`, sellerID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	newImage, newDoc, video := uuid.New(), uuid.New(), uuid.New()
	stub := &kycMediaStub{owner: userID, kinds: map[uuid.UUID]string{newImage: "image", newDoc: "document", video: "video"}}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)
	svc := &Service{store: postgres.New(svcTestPool), media: media.New(srv.URL, "k")}

	if err := svc.SaveDocuments(ctx, userID, []postgres.SellerDocument{
		{DocumentType: "pan_card", MediaID: newImage},
		{DocumentType: "cancelled_cheque", MediaID: newDoc},
	}); err != nil {
		t.Fatalf("default setting refused a new image or document: %v", err)
	}
	if err := svc.SaveDocuments(ctx, userID, []postgres.SellerDocument{
		{DocumentType: "gst_certificate", MediaID: video},
	}); !errors.Is(err, media.ErrMediaWrongKind) {
		t.Fatalf("default setting, a video: %v, want ErrMediaWrongKind", err)
	}
}
