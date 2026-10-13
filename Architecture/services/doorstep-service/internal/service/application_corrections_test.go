package service

import (
	"context"
	"testing"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/shared/ownedmedia"
	"github.com/google/uuid"
)

type correctionTestStore struct {
	ProStore
	pro   model.Professional
	saved bool
}

func (s *correctionTestStore) ProfessionalByUser(context.Context, uuid.UUID) (*model.Professional, error) {
	return &s.pro, nil
}
func (s *correctionTestStore) ReplaceRejectedApplicationIdentity(context.Context, uuid.UUID, string, uuid.UUID) (*model.ProDocument, error) {
	s.saved = true
	return &model.ProDocument{Status: "pending"}, nil
}

func TestIdentityCorrectionRequiresOwnedMediaAndPendingApplication(t *testing.T) {
	for _, tc := range []struct {
		name, status, kind, media string
		owned                     bool
		want                      bool
	}{
		{"valid", "pending_verification", "passport", uuid.NewString(), true, true},
		{"foreign image", "pending_verification", "passport", uuid.NewString(), false, false},
		{"invalid media", "pending_verification", "passport", "not-an-id", true, false},
		{"unsupported proof", "pending_verification", "aadhaar", uuid.NewString(), true, false},
		{"approved account", "approved", "passport", uuid.NewString(), true, false},
		{"blocked account", "blocked", "passport", uuid.NewString(), true, false},
		{"rejected account", "rejected", "passport", uuid.NewString(), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uid := uuid.New()
			st := &correctionTestStore{pro: model.Professional{ID: uuid.New(), UserID: uid, Status: tc.status, Application: &model.PartnerApplication{}}}
			s := &Service{pro: ProDeps{Store: st, Documents: ownedmedia.Func(func(_ context.Context, owner, id uuid.UUID) error {
				if owner != uid || id.String() != tc.media {
					t.Fatal("incorrect media owner")
				}
				if !tc.owned {
					return ownedmedia.ErrInvalid
				}
				return nil
			})}}
			_, err := s.ProCorrectIdentity(context.Background(), uid, IdentityCorrectionInput{Kind: tc.kind, MediaID: tc.media})
			if (err == nil) != tc.want || st.saved != tc.want {
				t.Fatalf("saved=%v err=%v", st.saved, err)
			}
		})
	}
}
