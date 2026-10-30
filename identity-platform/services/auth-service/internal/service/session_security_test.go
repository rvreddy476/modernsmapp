package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

func newRefreshFixture(t *testing.T) (*adminFixture, *store.User, *AuthResponse) {
	t.Helper()
	f := newAdminFixture(t)
	uid := uuid.New()
	f.st.users[uid] = &store.User{ID: uid, AccountStatus: store.AccountStatusActive}
	resp, err := f.svc.startSession(context.Background(), f.st.users[uid], "d", "android", "1.1.1.1", "okhttp/4", []string{AMRPassword})
	if err != nil {
		t.Fatal(err)
	}
	return f, f.st.users[uid], resp
}

// Was scratchpad probe TestProbeRefreshReuseNotDetected: the attacker
// refreshed first with a stolen token, the victim's replay of the old token
// was refused, and the attacker's rotated token kept refreshing for 30 days.
func TestRefreshReuseRevokesSessionFamily(t *testing.T) {
	f, _, resp := newRefreshFixture(t)
	ctx := context.Background()
	stolen := resp.Tokens.RefreshToken

	atk, err := f.svc.RefreshSession(ctx, stolen, "1.1.1.1", "okhttp/4")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if _, err := f.svc.RefreshSession(ctx, stolen, "1.1.1.1", "okhttp/4"); err == nil {
		t.Fatal("a rotated-away refresh token was accepted")
	}
	if _, err := f.svc.RefreshSession(ctx, atk.Tokens.RefreshToken, "1.1.1.1", "okhttp/4"); err == nil {
		t.Fatal("reuse of an old token must revoke the family; the rotated token still refreshes")
	}
	sess, _ := f.st.GetSessionByID(ctx, resp.SessionID)
	if sess == nil || sess.RevokedAt == nil {
		t.Fatal("session not revoked in the store after reuse")
	}
	if !f.mr.Exists("sess_revoked:" + resp.SessionID.String()) {
		t.Fatal("reuse did not write the access-token revocation cache")
	}
	found := false
	for _, a := range f.st.anomalies {
		if a.AnomalyType == "session_revoked" && a.Metadata["reason"] == "refresh_token_reuse" {
			found = true
		}
	}
	if !found {
		t.Fatal("reuse was not recorded in the security inbox")
	}
}

// An unknown token is refused without touching any session.
func TestRefreshUnknownTokenRevokesNothing(t *testing.T) {
	f, _, resp := newRefreshFixture(t)
	ctx := context.Background()
	if _, err := f.svc.RefreshSession(ctx, "never-issued", "1.1.1.1", "okhttp/4"); err == nil {
		t.Fatal("unknown token accepted")
	}
	if _, err := f.svc.RefreshSession(ctx, resp.Tokens.RefreshToken, "1.1.1.1", "okhttp/4"); err != nil {
		t.Fatalf("an unknown token must not burn a live session: %v", err)
	}
}

// Two concurrent refreshes with the same token: exactly one is issued a pair.
func TestConcurrentRefreshOnlyOneWins(t *testing.T) {
	for round := 0; round < 20; round++ {
		f, _, resp := newRefreshFixture(t)
		ctx := context.Background()
		const n = 8
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := f.svc.RefreshSession(ctx, resp.Tokens.RefreshToken, "1.1.1.1", "okhttp/4"); err == nil {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		close(start)
		wg.Wait()
		if wins > 1 {
			t.Fatalf("round %d: %d concurrent refreshes with one token all succeeded", round, wins)
		}
	}
}

// logoutRacingStore revokes the session between the refresh's read and its
// rotation, the window logout or logout-all can land in.
type logoutRacingStore struct{ *adminSessionStore }

func (r logoutRacingStore) RotateSessionWithFingerprint(ctx context.Context, id uuid.UUID, oldHash, newHash, ip string, exp time.Time, flagged bool) (bool, error) {
	_ = r.adminSessionStore.RevokeSession(ctx, id)
	return r.adminSessionStore.RotateSessionWithFingerprint(ctx, id, oldHash, newHash, ip, exp, flagged)
}

func TestRefreshRacingLogoutMintsNothing(t *testing.T) {
	f, _, resp := newRefreshFixture(t)
	svc := New(logoutRacingStore{f.st}, &fakeProducer{}, f.svc.cfg, f.svc.log, f.svc.rdb, nil)
	out, err := svc.RefreshSession(context.Background(), resp.Tokens.RefreshToken, "1.1.1.1", "okhttp/4")
	if err == nil || out != nil {
		t.Fatal("a refresh that lost the race with logout was issued tokens")
	}
}
