package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// oauthLinkedSessionStore is the in-memory session store with one account
// linked to the provider, so a provider sign-in reaches session creation.
type oauthLinkedSessionStore struct {
	*adminSessionStore
	linked *store.User
}

func (s oauthLinkedSessionStore) GetUserByLoginProvider(context.Context, string, string) (*store.User, error) {
	return s.linked, nil
}

func oauthLinkedFixture(t *testing.T) (*Service, oauthLinkedSessionStore, *adminFixture) {
	t.Helper()
	f := newAdminFixture(t)
	u := f.addUser(t, false)
	st := oauthLinkedSessionStore{adminSessionStore: f.st, linked: u}
	return New(st, &fakeProducer{}, f.svc.cfg, f.svc.log, f.svc.rdb, nil), st, f
}

func onlySession(t *testing.T, st oauthLinkedSessionStore) *store.Session {
	t.Helper()
	if len(st.byID) != 1 {
		t.Fatalf("%d sessions, want 1", len(st.byID))
	}
	for _, s := range st.byID {
		return s
	}
	return nil
}

// Google sessions were opened with IP "" and user agent "": the refresh
// fingerprint had nothing to compare, and the empty IP overwrote last_ip.
func TestOAuthSessionRecordsTheRealClient(t *testing.T) {
	svc, st, f := oauthLinkedFixture(t)
	client := OAuthClient{IP: "203.0.113.7", UserAgent: "okhttp/4.12", DeviceID: "install-7", Platform: "android"}
	res, err := svc.loginOrRegisterOAuth(context.Background(), "google",
		&OAuthUserInfo{Email: *st.linked.Email, Sub: "s", EmailVerified: true}, client)
	if err != nil || res == nil || res.Auth == nil || res.Auth.Tokens.AccessToken == "" {
		t.Fatalf("provider sign-in = (%+v, %v), want a session", res, err)
	}
	sess := onlySession(t, st)
	if sess.IP != client.IP || sess.UserAgent != client.UserAgent || sess.DeviceID != client.DeviceID || sess.Platform != client.Platform {
		t.Fatalf("session recorded ip=%q ua=%q device=%q platform=%q, want the client's", sess.IP, sess.UserAgent, sess.DeviceID, sess.Platform)
	}
	if got, _ := f.mr.Get("last_ip:" + st.linked.ID.String()); got != client.IP {
		t.Fatalf("last_ip = %q, want %q", got, client.IP)
	}
}

// A client that sends no install id keeps the provider defaults.
func TestOAuthSessionWithoutDeviceIDKeepsDefaults(t *testing.T) {
	svc, st, _ := oauthLinkedFixture(t)
	if _, err := svc.loginOrRegisterOAuth(context.Background(), "google",
		&OAuthUserInfo{Email: *st.linked.Email, Sub: "s", EmailVerified: true},
		OAuthClient{IP: "203.0.113.7", UserAgent: "Mozilla/5.0"}); err != nil {
		t.Fatal(err)
	}
	if sess := onlySession(t, st); sess.DeviceID != oauthDeviceID || sess.Platform != "google" {
		t.Fatalf("device=%q platform=%q, want %q/google", sess.DeviceID, sess.Platform, oauthDeviceID)
	}
}

// The step-up envelope used to be dropped (nil, err), so the handler could
// only answer a generic failure. It now comes back with the sentinel.
func TestOAuthSignInReturnsTheStepUpEnvelope(t *testing.T) {
	svc, fstore, mr := newTestService(t, "enforce")
	email := "g@example.test"
	user := &store.User{ID: uuid.New(), Email: &email, EmailVerified: true, AccountStatus: store.AccountStatusActive}
	fstore.users = map[uuid.UUID]*store.User{user.ID: user}
	svc.store = &oauthExistingStore{fakeAnomalyStore: fstore, providerUser: user}
	mr.Set("last_ip:"+user.ID.String(), "203.0.113.10") // another /24

	res, err := svc.loginOrRegisterOAuth(context.Background(), "google",
		&OAuthUserInfo{Email: email, Sub: "s", EmailVerified: true},
		OAuthClient{IP: "198.51.100.99", UserAgent: "okhttp/4", DeviceID: "new-device-1"})
	if !errors.Is(err, ErrAnomalyStepUpRequired) {
		t.Fatalf("err = %v, want ErrAnomalyStepUpRequired", err)
	}
	if res == nil || res.Auth == nil || !res.Auth.RequiresStepUp || res.Auth.PendingToken == "" {
		t.Fatalf("result = %+v, want the step-up envelope", res)
	}
	if res.Auth.Tokens.AccessToken != "" || len(fstore.sessions) != 0 {
		t.Fatal("a gated provider sign-in minted a session")
	}
}

// An empty client IP is no signal: it must never erase the last-IP baseline
// the next login is graded against.
func TestEmptyIPNeverOverwritesLastIP(t *testing.T) {
	svc, fstore, mr := newTestService(t, "shadow")
	user := &store.User{ID: uuid.New()}
	fstore.users = map[uuid.UUID]*store.User{user.ID: user}
	mr.Set("last_ip:"+user.ID.String(), "203.0.113.10")

	if _, err := svc.createSessionForUser(context.Background(), user, []string{AMRFederated}, "", "google", "", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := mr.Get("last_ip:" + user.ID.String()); got != "203.0.113.10" {
		t.Fatalf("last_ip = %q after a sign-in with no IP, want it kept", got)
	}
	// No device id either, so the only signal an anomaly could come from
	// is the address: a sign-in with no IP must record nothing.
	if n := len(fstore.anomalies); n != 0 {
		t.Fatalf("a sign-in with no IP recorded %d anomalies (%+v), want none", n, fstore.anomalies)
	}
}
