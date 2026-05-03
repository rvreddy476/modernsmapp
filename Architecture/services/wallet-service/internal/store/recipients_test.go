package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestBumpRecipient_InAtPostUser(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user := uuid.New()
	rid := uuid.New()
	label := "Aisha"
	if err := s.BumpRecipient(ctx, user, &rid, nil, &label); err != nil {
		t.Fatalf("bump: %v", err)
	}
	if err := s.BumpRecipient(ctx, user, &rid, nil, nil); err != nil {
		t.Fatalf("bump 2nd: %v", err)
	}
	rs, err := s.ListRecipients(ctx, user, 20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected 1 entry from 2 bumps; got %d", len(rs))
	}
	if rs[0].SendCount != 2 {
		t.Fatalf("expected send_count=2; got %d", rs[0].SendCount)
	}
}

func TestBumpRecipient_ExternalPhone(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user := uuid.New()
	phone := "9876543210"
	if err := s.BumpRecipient(ctx, user, nil, &phone, nil); err != nil {
		t.Fatalf("bump: %v", err)
	}
	rs, _ := s.ListRecipients(ctx, user, 20)
	if len(rs) != 1 {
		t.Fatalf("expected 1 row")
	}
	if rs[0].RecipientPhone == nil || *rs[0].RecipientPhone != phone {
		t.Fatalf("phone not stored")
	}
}

func TestBumpRecipient_RejectsEmpty(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	if err := s.BumpRecipient(context.Background(), uuid.New(), nil, nil, nil); err == nil {
		t.Fatalf("expected rejection when neither user nor phone given")
	}
}

func TestListRecipients_OrdersBySendCount(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user := uuid.New()
	a, b := uuid.New(), uuid.New()
	for i := 0; i < 3; i++ {
		if err := s.BumpRecipient(ctx, user, &a, nil, nil); err != nil {
			t.Fatalf("bump a %d: %v", i, err)
		}
	}
	if err := s.BumpRecipient(ctx, user, &b, nil, nil); err != nil {
		t.Fatalf("bump b: %v", err)
	}
	rs, _ := s.ListRecipients(ctx, user, 20)
	if len(rs) != 2 {
		t.Fatalf("expected 2 rows; got %d", len(rs))
	}
	if rs[0].RecipientUserID == nil || *rs[0].RecipientUserID != a {
		t.Fatalf("expected a first (highest count); got %v", rs[0].RecipientUserID)
	}
}
