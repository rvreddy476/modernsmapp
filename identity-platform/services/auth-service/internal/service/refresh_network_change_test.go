package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// A phone that moved from Wi-Fi to cellular, or from an IPv6 to an IPv4
// egress, presents the same client from a different network. That must
// rotate the pair, not burn the session — the dev tunnel signed the
// emulator out within minutes the day real client IPs started reaching
// this service.
func TestRefreshSession_NetworkChangeAloneDoesNotBurnTheSession(t *testing.T) {
	svc, fstore, _ := newTestService(t, "shadow")
	fstore.users = map[uuid.UUID]*store.User{}
	u := seedLifecycleUser(t, fstore, store.AccountStatusActive, nil)

	rs := &refreshStatusStore{fakeAnomalyStore: fstore, sess: &store.Session{
		ID: uuid.New(), UserID: u.ID,
		IP:        "2401:4900:88f7:1621::1",
		UserAgent: "okhttp/5.4.0",
		IsActive:  true, ExpiresAt: time.Now().Add(time.Hour),
	}}
	svc.store = rs

	_, err := svc.RefreshSession(context.Background(), "some-refresh-token", "122.177.240.54", "okhttp/5.4.0")
	if err != nil && strings.Contains(err.Error(), "refresh denied") {
		t.Fatalf("a network change alone was treated as theft: %v", err)
	}
	if rs.revoked {
		t.Fatal("the session was burned for a network change on the same client")
	}
}

// A different client family replaying the token is the theft shape the
// check exists for: denied, and the session revoked.
func TestRefreshSession_DifferentClientFamilyBurnsTheSession(t *testing.T) {
	svc, fstore, _ := newTestService(t, "shadow")
	fstore.users = map[uuid.UUID]*store.User{}
	u := seedLifecycleUser(t, fstore, store.AccountStatusActive, nil)

	rs := &refreshStatusStore{fakeAnomalyStore: fstore, sess: &store.Session{
		ID: uuid.New(), UserID: u.ID,
		IP:        "10.0.0.5",
		UserAgent: "okhttp/5.4.0",
		IsActive:  true, ExpiresAt: time.Now().Add(time.Hour),
	}}
	svc.store = rs

	_, err := svc.RefreshSession(context.Background(), "some-refresh-token", "10.0.0.5", "Mozilla/5.0 (Windows NT 10.0) Chrome/128")
	if err == nil || !strings.Contains(err.Error(), "refresh denied") {
		t.Fatalf("expected a fingerprint denial, got %v", err)
	}
	if !rs.revoked {
		t.Fatal("a replay from a different client family did not burn the session")
	}
}
