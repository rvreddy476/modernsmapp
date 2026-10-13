package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

type contactProStore struct {
	ProStore
	pro model.Professional
}

func (s *contactProStore) ProfessionalByUser(context.Context, uuid.UUID) (*model.Professional, error) {
	return &s.pro, nil
}

type contactDispatchStore struct {
	DispatchStore
	customer uuid.UUID
	checks   int
	denyAt   int
}

func (s *contactDispatchStore) ProContactCustomer(context.Context, uuid.UUID, uuid.UUID) (uuid.UUID, error) {
	s.checks++
	if s.denyAt > 0 && s.checks >= s.denyAt {
		return uuid.Nil, store.ErrNotFound
	}
	return s.customer, nil
}

func TestCustomerContactChecksAccessBeforeAndAfterIdentityLookup(t *testing.T) {
	for _, tc := range []struct {
		name      string
		denyAt    int
		phone     string
		lookupErr error
		want      bool
		lookups   int
	}{
		{"accepted", 0, "+919876543210", nil, true, 1},
		{"not assigned", 1, "+919876543210", nil, false, 0},
		{"cancelled during lookup", 2, "+919876543210", nil, false, 1},
		{"identity unavailable", 0, "", errors.New("offline"), false, 1},
		{"invalid phone", 0, "not-a-number", nil, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := &contactDispatchStore{customer: uuid.New(), denyAt: tc.denyAt}
			lookups := 0
			s := &Service{ds: DispatchDeps{Store: ds}, pro: ProDeps{Store: &contactProStore{pro: model.Professional{ID: uuid.New()}}, VerifiedPhone: func(_ context.Context, id uuid.UUID) (string, error) {
				lookups++
				if id != ds.customer {
					t.Fatal("lookup was not scoped to booking customer")
				}
				return tc.phone, tc.lookupErr
			}}}
			contact, err := s.ProCustomerContact(context.Background(), uuid.New(), uuid.New())
			if (err == nil) != tc.want || lookups != tc.lookups {
				t.Fatalf("got contact=%v err=%v lookups=%d", contact, err, lookups)
			}
			if !tc.want && contact != nil {
				t.Fatal("contact leaked")
			}
			if tc.want && contact.Phone != tc.phone {
				t.Fatal("wrong number")
			}
		})
	}
}
