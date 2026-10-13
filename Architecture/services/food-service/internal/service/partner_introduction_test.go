package service

import (
	"context"
	"errors"
	"github.com/atpost/food-service/internal/riderkyc"
	"github.com/atpost/shared/ownedmedia"
	"github.com/google/uuid"
	"testing"
)

type introductionVerifier struct {
	imageCalls, videoCalls int
	err                    error
}

func (v *introductionVerifier) VerifyOwnedImage(context.Context, uuid.UUID, uuid.UUID) error {
	v.imageCalls++
	return v.err
}
func (v *introductionVerifier) VerifyOwnedIntroduction(context.Context, uuid.UUID, uuid.UUID) error {
	v.videoCalls++
	return v.err
}

type introductionStore struct {
	Store
	calls int
	last  map[string]any
}

func (s *introductionStore) AddRestaurantDocument(_ context.Context, _ uuid.UUID, _ uuid.UUID, in map[string]any) (map[string]any, error) {
	s.calls++
	s.last = in
	return map[string]any{"status": "PENDING"}, nil
}

func TestRestaurantSelfieAndIntroductionRequireOwnedPrivateMedia(t *testing.T) {
	for _, kind := range []string{"SELFIE", "INTRODUCTION_VIDEO"} {
		t.Run(kind, func(t *testing.T) {
			st := &introductionStore{}
			v := &introductionVerifier{err: ownedmedia.ErrInvalid}
			svc := New(st).WithDocumentMedia(v)
			owner, id := uuid.New(), uuid.New()
			in := map[string]any{"document_type": kind, "media_id": uuid.NewString(), "file_url": "https://untrusted.invalid/image", "document_number": "ignored"}
			if _, err := svc.AddRestaurantDocument(context.Background(), owner, id, in); err == nil || st.calls != 0 {
				t.Fatal("foreign media accepted")
			}
			v.err = nil
			if _, err := svc.AddRestaurantDocument(context.Background(), owner, id, in); err != nil || st.calls != 1 {
				t.Fatal("owned evidence not accepted", err)
			}
			if len(st.last) != 2 {
				t.Fatal("untrusted URL or number retained")
			}
			if kind == "SELFIE" && v.imageCalls != 2 || kind == "INTRODUCTION_VIDEO" && v.videoCalls != 2 {
				t.Fatal("wrong validator")
			}
			delete(in, "media_id")
			if _, err := svc.AddRestaurantDocument(context.Background(), owner, id, in); err == nil || st.calls != 1 {
				t.Fatal("URL-only evidence accepted")
			}
		})
	}
}

func TestDeliveryIntroductionUsesVideoValidation(t *testing.T) {
	st := &offlineDocStore{}
	v := &introductionVerifier{err: errors.New("invalid length")}
	svc := New(st).WithDocumentMedia(v)
	in := riderkyc.DocumentInput{DocumentType: "INTRODUCTION_VIDEO", MediaID: uuid.NewString()}
	if _, err := svc.AddDeliveryDocument(context.Background(), uuid.New(), in); err == nil || st.calls != 0 {
		t.Fatal("invalid introduction accepted")
	}
	v.err = nil
	if _, err := svc.AddDeliveryDocument(context.Background(), uuid.New(), in); err != nil || st.calls != 1 || v.videoCalls != 2 || v.imageCalls != 0 {
		t.Fatal("video validation not used", err)
	}
}
