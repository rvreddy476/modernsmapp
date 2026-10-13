package partnerwork

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInternalCoordinatorRejectsMissingKeyBeforeDatabase(t *testing.T) {
	for _, key := range []string{"", "different"} {
		r := httptest.NewRequest(http.MethodPost, "/internal/partner-work", strings.NewReader(`{"action":"read","user_id":"`+uuid.NewString()+`"}`))
		r.Header.Set("X-Internal-Key", key)
		w := httptest.NewRecorder()
		InternalHandler(nil, "secret-test-key").ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", w.Code)
		}
	}
}

func TestRemoteCoordinatorUsesOriginalExpiryAndRetainsUnknownOutcome(t *testing.T) {
	db := testDB(t)
	srv := httptest.NewServer(InternalHandler(db, "secret-test-key"))
	defer srv.Close()
	c := NewClient(srv.URL, "secret-test-key")
	ctx := context.Background()
	u, j := uuid.New(), uuid.New()
	cat := CommerceDelivery
	s, err := c.Read(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.SetDuty(ctx, u, &cat, s.Session.Revision); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	a, err := c.Offer(ctx, u, cat, j, start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Offer(ctx, u, cat, j, start, start.Add(time.Hour))
	if err != nil || !a.OfferExpiresAt.Equal(b.OfferExpiresAt) {
		t.Fatalf("renewed deadline %+v %+v %v", a, b, err)
	}
	if err = c.Decide(ctx, u, cat, a.ID, "accept"); err != nil {
		t.Fatal(err)
	}
	other := FoodDelivery
	s, err = c.Read(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.SetDuty(ctx, u, &other, s.Session.Revision); !errors.Is(err, ErrBusy) {
		t.Fatalf("abandoned remote accepted delivery: %v", err)
	}
	if err = c.Decide(ctx, u, cat, a.ID, "release"); err != nil {
		t.Fatal(err)
	}
	if err = c.Decide(ctx, u, cat, a.ID, "release"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteCoordinatorFailsClosedWhenUnconfigured(t *testing.T) {
	_, err := NewClient("", "").Read(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("unconfigured coordinator accepted request")
	}
}
