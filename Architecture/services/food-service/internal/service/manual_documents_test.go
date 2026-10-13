package service

import (
	"context"
	"errors"
	"github.com/atpost/food-service/internal/riderkyc"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/ownedmedia"
	"github.com/google/uuid"
	"testing"
)

type offlineDocStore struct {
	Store
	calls int
}

func (s *offlineDocStore) AddDeliveryPartnerDocument(_ context.Context, _ uuid.UUID, r postgres.DeliveryDocumentRecord) (*postgres.DeliveryDocument, error) {
	s.calls++
	return &postgres.DeliveryDocument{DocumentType: r.DocumentType, Status: r.Status}, nil
}
func TestOfflineDocumentOwnerCheckBeforeStore(t *testing.T) {
	owner, image := uuid.New(), uuid.New()
	st := &offlineDocStore{}
	svc := New(st)
	in := riderkyc.DocumentInput{DocumentType: "PASSPORT", MediaID: image.String()}
	if _, err := svc.AddDeliveryDocument(context.Background(), owner, in); !errors.Is(err, ownedmedia.ErrUnavailable) || st.calls != 0 {
		t.Fatal("unconfigured upload accepted")
	}
	svc.WithDocumentMedia(ownedmedia.Func(func(_ context.Context, o, m uuid.UUID) error {
		if o != owner || m != image {
			t.Fatal("wrong owner passed")
		}
		return ownedmedia.ErrInvalid
	}))
	if _, err := svc.AddDeliveryDocument(context.Background(), owner, in); err == nil || st.calls != 0 {
		t.Fatal("foreign upload reached store")
	}
	svc.WithDocumentMedia(ownedmedia.Func(func(context.Context, uuid.UUID, uuid.UUID) error { return nil }))
	doc, err := svc.AddDeliveryDocument(context.Background(), owner, in)
	if err != nil || st.calls != 1 || doc.Status != postgres.DocumentStatusPending {
		t.Fatal("owned upload must remain pending")
	}
}
