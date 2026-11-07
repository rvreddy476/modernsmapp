package service

import (
	"testing"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

func TestAnAnonymousDonorIsNamedOnlyToThemselves(t *testing.T) {
	donor, other, owner := uuid.New(), uuid.New(), uuid.New()
	_ = owner // the fundraiser's owner is just another viewer here
	rows := []postgres.Donation{
		{ID: uuid.New(), DonorID: donor, AmountPaise: 500, IsAnonymous: true},
		{ID: uuid.New(), DonorID: other, AmountPaise: 700, IsAnonymous: false},
	}

	for name, viewer := range map[string]uuid.UUID{"signed out": uuid.Nil, "the owner": owner, "another donor": other} {
		got := redactDonations(rows, viewer)
		if len(got) != 2 {
			t.Fatalf("%s: %d rows, want 2", name, len(got))
		}
		if got[0].DonorID != nil {
			t.Errorf("%s sees the anonymous donor %v", name, *got[0].DonorID)
		}
		if got[0].AmountPaise != 500 || !got[0].IsAnonymous {
			t.Errorf("%s: the anonymous row must still show its amount: %+v", name, got[0])
		}
		if got[1].DonorID == nil || *got[1].DonorID != other {
			t.Errorf("%s: a named donor must be shown: %+v", name, got[1])
		}
	}

	self := redactDonations(rows, donor)
	if self[0].DonorID == nil || *self[0].DonorID != donor {
		t.Fatalf("the anonymous donor must see their own id: %+v", self[0])
	}
}

func TestAnAnonymousRowWithNoDonorIsNeverNamedToASignedOutViewer(t *testing.T) {
	got := redactDonations([]postgres.Donation{{ID: uuid.New(), DonorID: uuid.Nil, IsAnonymous: true}}, uuid.Nil)
	if got[0].DonorID != nil {
		t.Fatal("uuid.Nil viewer matched a uuid.Nil donor")
	}
}
