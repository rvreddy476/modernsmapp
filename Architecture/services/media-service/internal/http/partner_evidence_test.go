package http

import (
	"context"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPartnerEvidencePrivateOwnershipAndLimits(t *testing.T) {
	f := newImageBytesFixture(t)
	owner, id := uuid.New(), f.video
	duration := 60000
	base := *f.records.assets[id]
	base.UploaderID = owner
	base.MimeType = "video/mp4"
	base.DurationMs = &duration
	base.FileSizeBytes = 16
	f.objects.objects[base.StorageKey] = []byte("private-recording")
	for _, tc := range []struct {
		name    string
		change  func(*postgres.MediaAsset)
		allowed bool
	}{
		{"valid", func(*postgres.MediaAsset) {}, true},
		{"foreign_owner", func(m *postgres.MediaAsset) { m.UploaderID = uuid.New() }, false},
		{"public", func(m *postgres.MediaAsset) { m.StorageKey = "public/movie" }, false},
		{"composer", func(m *postgres.MediaAsset) { m.UploadPurpose = "composer" }, false},
		{"deleted", func(m *postgres.MediaAsset) { m.ProcessingStatus = "deleted" }, false},
		{"moderated", func(m *postgres.MediaAsset) { m.ModerationStatus = "rejected" }, false},
		{"too_long", func(m *postgres.MediaAsset) { v := 60001; m.DurationMs = &v }, false},
		{"unknown_duration", func(m *postgres.MediaAsset) { m.DurationMs = nil }, false},
		{"too_large", func(m *postgres.MediaAsset) { m.FileSizeBytes = (64 << 20) + 1 }, false},
		{"wrong_mime", func(m *postgres.MediaAsset) { m.MimeType = "text/html" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			tc.change(&m)
			f.records.assets[id] = &m
			out, err := f.reader.PartnerEvidence(context.Background(), id, owner, "introduction_video")
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v err=%v", tc.allowed, err)
			}
			if tc.allowed && (out.ContentType != "video/mp4" || string(out.Bytes) != "private-recording") {
				t.Fatal("incorrect private bytes")
			}
		})
	}
	f.records.assets[id] = &base
	if _, err := f.reader.PartnerEvidence(context.Background(), id, owner, "selfie"); err == nil {
		t.Fatal("video accepted as selfie")
	}
}

func TestPartnerEvidenceRequiresSignedScopedAdminActor(t *testing.T) {
	signer, pub := mustSigner(t, "admin-service", "a1")
	for _, tc := range []struct {
		name, aud, scope, actor string
		want                    int
	}{
		{"valid_invalid_id", "media", PartnerEvidenceOperation, uuid.NewString(), 404},
		{"no_actor", "media", PartnerEvidenceOperation, "", 403},
		{"wrong_audience", "post", PartnerEvidenceOperation, uuid.NewString(), 403},
		{"wrong_scope", "media", "media:upload", uuid.NewString(), 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			h := New(nil)
			if err := h.RegisterPartnerEvidence(r, "a1", pub); err != nil {
				t.Fatal(err)
			}
			token, err := signer.Mint(tc.aud, "review", []string{tc.scope}, nil, time.Minute, servicetoken.WithActor(tc.actor))
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/v1/media/admin/evidence/invalid", nil)
			req.Header.Set("X-Service-Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d", w.Code, tc.want)
			}
		})
	}
	r := gin.New()
	h := New(nil)
	_ = h.RegisterPartnerEvidence(r, "a1", pub)
	req := httptest.NewRequest(http.MethodGet, "/v1/media/admin/evidence/invalid", nil)
	req.Header.Set("X-Internal-Service-Key", "legacy")
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("legacy headers admitted")
	}
}
