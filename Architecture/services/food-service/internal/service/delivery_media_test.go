package service

import (
	"context"
	"errors"
	"github.com/atpost/shared/ownedmedia"
	"github.com/google/uuid"
	"testing"
)

type proofStore struct {
	Store
	calls int
}

func (s *proofStore) AttachProofURL(context.Context, uuid.UUID, uuid.UUID, string, string) error {
	s.calls++
	return nil
}
func TestDeliveryProofRequiresOwnedMedia(t *testing.T) {
	st := &proofStore{}
	svc := New(st)
	ctx := context.Background()
	user, order, media := uuid.New(), uuid.New(), uuid.New()
	if err := svc.AttachProofURL(ctx, user, order, "pickup", "https://untrusted.example/proof.jpg"); !errors.Is(err, ownedmedia.ErrInvalid) {
		t.Fatal(err)
	}
	if err := svc.AttachProofURL(ctx, user, order, "pickup", media.String()); !errors.Is(err, ownedmedia.ErrUnavailable) {
		t.Fatal(err)
	}
	svc.WithDocumentMedia(ownedmedia.Func(func(_ context.Context, u, m uuid.UUID) error {
		if u != user || m != media {
			t.Fatal("wrong ownership binding")
		}
		return ownedmedia.ErrInvalid
	}))
	if err := svc.AttachProofURL(ctx, user, order, "pickup", media.String()); !errors.Is(err, ownedmedia.ErrInvalid) || st.calls != 0 {
		t.Fatal("foreign media persisted", err)
	}
	svc.WithDocumentMedia(ownedmedia.Func(func(context.Context, uuid.UUID, uuid.UUID) error { return nil }))
	if err := svc.AttachProofURL(ctx, user, order, "pickup", media.String()); err != nil || st.calls != 1 {
		t.Fatal("valid proof", err)
	}
	if err := svc.AttachProofURL(ctx, user, order, "other", media.String()); err == nil || st.calls != 1 {
		t.Fatal("invalid phase")
	}
}
