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
	// The victim's replay comes after the retry grace window.
	f.st.ageRotation(hashToken(stolen), RefreshReuseGrace+time.Second)
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
// This pins the conditional rotation itself, so the retry grace window (which
// deliberately honours a second presentation) is switched off;
// TestConcurrentRetriesLeaveOneLiveRefreshToken covers it switched on.
func TestConcurrentRefreshOnlyOneWins(t *testing.T) {
	for round := 0; round < 20; round++ {
		f, _, resp := newRefreshFixture(t)
		f.svc.refreshReuseGrace = 0
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

// A lost refresh response: the client never saw the rotated pair and retries
// with the token it has, inside the grace window. It is issued a fresh pair,
// the family is not revoked, nothing lands in the security inbox, and the
// successor it never received no longer refreshes.
func TestRefreshRetryInsideGraceRotatesAgain(t *testing.T) {
	f, _, resp := newRefreshFixture(t)
	ctx := context.Background()
	old := resp.Tokens.RefreshToken

	lost, err := f.svc.RefreshSession(ctx, old, "1.1.1.1", "okhttp/4")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	retried, err := f.svc.RefreshSession(ctx, old, "1.1.1.1", "okhttp/4")
	if err != nil {
		t.Fatalf("retry inside the grace window refused: %v", err)
	}
	if retried.SessionID != resp.SessionID || retried.Tokens.RefreshToken == lost.Tokens.RefreshToken {
		t.Fatal("retry did not rotate the same session to a new refresh token")
	}
	if sess, _ := f.st.GetSessionByID(ctx, resp.SessionID); sess == nil || sess.RevokedAt != nil {
		t.Fatal("a retry inside the grace window revoked the session")
	}
	if f.mr.Exists("sess_revoked:" + resp.SessionID.String()) {
		t.Fatal("a retry inside the grace window wrote the revocation cache")
	}
	for _, a := range f.st.anomalies {
		if a.AnomalyType == "session_revoked" {
			t.Fatalf("a retry inside the grace window was recorded as an anomaly: %+v", a.Metadata)
		}
	}
	if _, err := f.svc.RefreshSession(ctx, retried.Tokens.RefreshToken, "1.1.1.1", "okhttp/4"); err != nil {
		t.Fatalf("the retried pair does not refresh: %v", err)
	}
}

// The lost successor is cut off by the retry: presenting it afterwards is
// reuse, even inside the window, and burns the family.
func TestRefreshLostSuccessorIsNotHonouredAfterRetry(t *testing.T) {
	f, _, resp := newRefreshFixture(t)
	ctx := context.Background()
	old := resp.Tokens.RefreshToken
	lost, err := f.svc.RefreshSession(ctx, old, "1.1.1.1", "okhttp/4")
	if err != nil {
		t.Fatal(err)
	}
	retried, err := f.svc.RefreshSession(ctx, old, "1.1.1.1", "okhttp/4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RefreshSession(ctx, lost.Tokens.RefreshToken, "1.1.1.1", "okhttp/4"); err == nil {
		t.Fatal("the successor a retry replaced still refreshes")
	}
	if _, err := f.svc.RefreshSession(ctx, retried.Tokens.RefreshToken, "1.1.1.1", "okhttp/4"); err == nil {
		t.Fatal("presenting a cut-off successor must revoke the family")
	}
}

// Past the window a replay is reuse: refused, family revoked, recorded.
func TestRefreshReplayAfterGraceRevokesFamily(t *testing.T) {
	f, _, resp := newRefreshFixture(t)
	ctx := context.Background()
	old := resp.Tokens.RefreshToken
	next, err := f.svc.RefreshSession(ctx, old, "1.1.1.1", "okhttp/4")
	if err != nil {
		t.Fatal(err)
	}
	f.st.ageRotation(hashToken(old), RefreshReuseGrace+time.Second)
	if _, err := f.svc.RefreshSession(ctx, old, "1.1.1.1", "okhttp/4"); err == nil {
		t.Fatal("a replay after the grace window was honoured")
	}
	if _, err := f.svc.RefreshSession(ctx, next.Tokens.RefreshToken, "1.1.1.1", "okhttp/4"); err == nil {
		t.Fatal("a replay after the grace window must revoke the family")
	}
	if !refreshReuseRecorded(f) {
		t.Fatal("a replay after the grace window was not recorded")
	}
}

// Inside the window, but the successor has already been used to refresh:
// someone else holds the live chain, so the replay is real reuse.
func TestRefreshReplayAfterSuccessorUsedRevokesFamily(t *testing.T) {
	f, _, resp := newRefreshFixture(t)
	ctx := context.Background()
	old := resp.Tokens.RefreshToken
	next, err := f.svc.RefreshSession(ctx, old, "1.1.1.1", "okhttp/4")
	if err != nil {
		t.Fatal(err)
	}
	latest, err := f.svc.RefreshSession(ctx, next.Tokens.RefreshToken, "1.1.1.1", "okhttp/4")
	if err != nil {
		t.Fatalf("successor refresh: %v", err)
	}
	if _, err := f.svc.RefreshSession(ctx, old, "1.1.1.1", "okhttp/4"); err == nil {
		t.Fatal("a replay whose successor was already used was honoured")
	}
	if _, err := f.svc.RefreshSession(ctx, latest.Tokens.RefreshToken, "1.1.1.1", "okhttp/4"); err == nil {
		t.Fatal("a replay whose successor was used must revoke the family")
	}
	if !refreshReuseRecorded(f) {
		t.Fatal("reuse after the successor was used was not recorded")
	}
}

// A signed-out session is never resumed by a retry, however recent: the
// replay is refused and recorded as reuse.
func TestRefreshRetryOnSignedOutSessionIsReuse(t *testing.T) {
	f, _, resp := newRefreshFixture(t)
	ctx := context.Background()
	old := resp.Tokens.RefreshToken
	if _, err := f.svc.RefreshSession(ctx, old, "1.1.1.1", "okhttp/4"); err != nil {
		t.Fatal(err)
	}
	_ = f.st.RevokeSession(ctx, resp.SessionID)
	if _, err := f.svc.RefreshSession(ctx, old, "1.1.1.1", "okhttp/4"); err == nil {
		t.Fatal("a retry resumed a signed-out session")
	}
	if !refreshReuseRecorded(f) {
		t.Fatal("a replay against a signed-out session was not recorded as reuse")
	}
}

func refreshReuseRecorded(f *adminFixture) bool {
	for _, a := range f.st.anomalies {
		if a.AnomalyType == "session_revoked" && a.Metadata["reason"] == "refresh_token_reuse" {
			return true
		}
	}
	return false
}

// Many concurrent presentations of one token with the retry window on: more
// than one may be issued a pair, but the session is left with at most one
// refresh token that still works.
func TestConcurrentRetriesLeaveOneLiveRefreshToken(t *testing.T) {
	for round := 0; round < 20; round++ {
		f, _, resp := newRefreshFixture(t)
		ctx := context.Background()
		const n = 8
		var wg sync.WaitGroup
		var mu sync.Mutex
		var issued []*AuthResponse
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if out, err := f.svc.RefreshSession(ctx, resp.Tokens.RefreshToken, "1.1.1.1", "okhttp/4"); err == nil {
					mu.Lock()
					issued = append(issued, out)
					mu.Unlock()
				}
			}()
		}
		close(start)
		wg.Wait()
		if len(issued) == 0 {
			t.Fatalf("round %d: no concurrent refresh was issued a pair", round)
		}
		// Present the current token first: it must work, and it is the only one.
		sess, _ := f.st.GetSessionByID(ctx, resp.SessionID)
		live := 0
		for _, out := range issued {
			if out.SessionID != resp.SessionID {
				t.Fatalf("round %d: a retry forked a new session", round)
			}
			if sess.RevokedAt == nil && sess.RefreshToken == hashToken(out.Tokens.RefreshToken) {
				if _, err := f.svc.RefreshSession(ctx, out.Tokens.RefreshToken, "1.1.1.1", "okhttp/4"); err != nil {
					t.Fatalf("round %d: the session's current token does not refresh: %v", round, err)
				}
				live++
			}
		}
		if sess.RevokedAt == nil && live != 1 {
			t.Fatalf("round %d: %d issued refresh tokens are the live one, want 1", round, live)
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
