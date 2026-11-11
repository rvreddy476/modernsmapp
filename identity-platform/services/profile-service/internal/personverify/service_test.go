package personverify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/service"
	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/atpost/identity-shared/crypto"
	"github.com/atpost/identity-shared/digilocker"
	sharedEvents "github.com/atpost/identity-shared/events"
	"github.com/atpost/identity-shared/verification"
	"github.com/google/uuid"
)

// ─── fakes ──────────────────────────────────────────────────────────────────

// fakeStore keeps attempts, bindings and profiles in memory and applies the
// same rules as the Postgres store (person_verification.go and the rename
// clause in profiles.go UpdateProfile).
type fakeStore struct {
	mu        sync.Mutex
	profiles  map[uuid.UUID]*store.Profile
	usrVerify map[uuid.UUID]bool
	attempts  map[uuid.UUID]*store.PersonAttempt
	stateUsed map[uuid.UUID]bool
	bindings  map[string]store.PersonBinding // by identity hash
}

func newFakeStore() *fakeStore {
	return &fakeStore{profiles: map[uuid.UUID]*store.Profile{}, usrVerify: map[uuid.UUID]bool{},
		attempts: map[uuid.UUID]*store.PersonAttempt{}, stateUsed: map[uuid.UUID]bool{}, bindings: map[string]store.PersonBinding{}}
}

func (f *fakeStore) addProfile(id uuid.UUID, displayName string) {
	f.profiles[id] = &store.Profile{UserID: id, DisplayName: displayName}
}

func (f *fakeStore) GetProfile(_ context.Context, id uuid.UUID) (*store.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.profiles[id]
	if !ok {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}

func (f *fakeStore) GetRegistrationDOB(context.Context, uuid.UUID) (*time.Time, error) {
	return nil, nil
}

func (f *fakeStore) UpdateProfile(_ context.Context, id uuid.UUID, p store.UpdateProfileParams) (*store.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.profiles[id]
	if !ok {
		return nil, nil
	}
	if p.DisplayName != nil {
		if p.ClearPersonVerificationOnRename && *p.DisplayName != cur.DisplayName && cur.VerificationLevel == verification.LevelPerson {
			cur.IsVerified = false
			cur.VerificationLevel = ""
			cur.BadgeFlags &^= verification.BadgePersonVerified
		}
		cur.DisplayName = *p.DisplayName
	}
	if p.Bio != nil {
		cur.Bio = *p.Bio
	}
	cp := *cur
	return &cp, nil
}

func (f *fakeStore) CountPersonAttemptsSince(_ context.Context, id uuid.UUID, since time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, a := range f.attempts {
		if a.UserID == id && !a.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) CreatePersonAttempt(_ context.Context, a store.PersonAttempt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a.Status = store.PersonAttemptStarted
	f.attempts[a.ID] = &a
	return nil
}

func (f *fakeStore) ConsumePersonAttemptState(_ context.Context, id uuid.UUID, stateHash string, now time.Time) (*store.PersonAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.attempts {
		if a.StateHash == stateHash && a.UserID == id && a.Status == store.PersonAttemptStarted && !f.stateUsed[a.ID] && a.ExpiresAt.After(now) {
			f.stateUsed[a.ID] = true
			cp := *a
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) MarkPersonIdentityChecked(_ context.Context, id uuid.UUID, ih, nh string, photo []byte, exp time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.attempts[id]
	if a == nil || a.Status != store.PersonAttemptStarted {
		return store.ErrAttemptGone
	}
	a.Status, a.IdentityHash, a.NameHash, a.ReferencePhotoSealed, a.ExpiresAt, a.CodeVerifierSealed = store.PersonAttemptIdentityChecked, ih, nh, photo, exp, nil
	return nil
}

func (f *fakeStore) RefusePersonAttempt(_ context.Context, id uuid.UUID, code string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.attempts[id]; a != nil && (a.Status == store.PersonAttemptStarted || a.Status == store.PersonAttemptIdentityChecked) {
		a.Status, a.RefusalCode, a.ReferencePhotoSealed, a.CodeVerifierSealed = store.PersonAttemptRefused, code, nil, nil
	}
	return nil
}

func (f *fakeStore) PendingSelfieAttempt(_ context.Context, id uuid.UUID, now time.Time) (*store.PersonAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var best *store.PersonAttempt
	for _, a := range f.attempts {
		if a.UserID == id && a.Status == store.PersonAttemptIdentityChecked && a.ExpiresAt.After(now) && (best == nil || a.CreatedAt.After(best.CreatedAt)) {
			best = a
		}
	}
	if best == nil {
		return nil, nil
	}
	cp := *best
	return &cp, nil
}

func (f *fakeStore) LatestPersonAttempt(_ context.Context, id uuid.UUID) (*store.PersonAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var best *store.PersonAttempt
	for _, a := range f.attempts {
		if a.UserID == id && (best == nil || !a.CreatedAt.Before(best.CreatedAt)) {
			best = a
		}
	}
	if best == nil {
		return nil, nil
	}
	cp := *best
	return &cp, nil
}

func (f *fakeStore) PersonBindingByIdentity(_ context.Context, ih string) (*store.PersonBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.bindings[ih]; ok {
		return &b, nil
	}
	return nil, nil
}

func (f *fakeStore) PersonBindingByUser(_ context.Context, id uuid.UUID) (*store.PersonBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.bindings {
		if b.UserID == id {
			cp := b
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) ApprovePerson(_ context.Context, in store.ApprovePersonInput) (*store.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.profiles[in.UserID]
	if p == nil {
		return nil, store.ErrAttemptGone
	}
	if in.HashName == nil || in.HashName(p.DisplayName) != in.NameHash {
		return nil, store.ErrNameChanged
	}
	if b, ok := f.bindings[in.IdentityHash]; ok && b.UserID != in.UserID {
		return nil, store.ErrDuplicatePerson
	}
	for ih, b := range f.bindings {
		if b.UserID == in.UserID && ih != in.IdentityHash {
			return nil, store.ErrIdentityMismatch
		}
	}
	a := f.attempts[in.AttemptID]
	if a == nil || a.UserID != in.UserID || a.Status != store.PersonAttemptIdentityChecked {
		return nil, store.ErrAttemptGone
	}
	f.bindings[in.IdentityHash] = store.PersonBinding{IdentityHash: in.IdentityHash, UserID: in.UserID, NameHash: in.NameHash}
	a.Status, a.ReferencePhotoSealed = store.PersonAttemptApproved, nil
	p.IsVerified, p.VerificationLevel = true, verification.LevelPerson
	p.BadgeFlags |= verification.BadgePersonVerified
	f.usrVerify[in.UserID] = true
	cp := *p
	return &cp, nil
}

func (f *fakeStore) SyncUserVerified(_ context.Context, id uuid.UUID, v bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.usrVerify[id] == v {
		return false, nil
	}
	f.usrVerify[id] = v
	return true, nil
}

// fakeDigiLocker returns a fixed identity and records the verifier sent.
type fakeDigiLocker struct {
	identity     digilocker.Identity
	exchangeErr  error
	identityErr  error
	lastVerifier string
	lastCode     string
}

func (d *fakeDigiLocker) ExchangeCode(_ context.Context, code, verifier string) (*digilocker.Session, error) {
	d.lastCode, d.lastVerifier = code, verifier
	if d.exchangeErr != nil {
		return nil, d.exchangeErr
	}
	return &digilocker.Session{}, nil
}

func (d *fakeDigiLocker) Identity(context.Context, *digilocker.Session) (*digilocker.Identity, error) {
	if d.identityErr != nil {
		return nil, d.identityErr
	}
	id := d.identity
	return &id, nil
}

type fakePublisher struct {
	events []sharedEvents.IdentityVerificationChangedPayload
}

func (p *fakePublisher) PublishVerificationChanged(_ context.Context, e sharedEvents.IdentityVerificationChangedPayload) error {
	p.events = append(p.events, e)
	return nil
}

// ─── fixture ────────────────────────────────────────────────────────────────

const (
	testHashKey = "1111111111111111111111111111111111111111111111111111111111111111"
	testSealKey = "2222222222222222222222222222222222222222222222222222222222222222"
)

var (
	userA = uuid.MustParse("6a3e0000-0000-4000-8000-00000000000a")
	userB = uuid.MustParse("6a3e0000-0000-4000-8000-00000000000b")
	t0    = time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
)

func ashaIdentity() digilocker.Identity {
	return digilocker.Identity{Name: "Asha Rao", DOB: time.Date(1992, 6, 5, 0, 0, 0, 0, time.UTC),
		Photo: []byte("aadhaar-photo"), AadhaarLast4: "1234", AccountID: "dl-1"}
}

type fixture struct {
	svc   *Service
	st    *fakeStore
	dl    *fakeDigiLocker
	face  *MockFaceVerifier
	pub   *fakePublisher
	clock *time.Time
	logs  *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	box, err := crypto.NewSecretBox(testSealKey)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := parseKey(testHashKey)
	dl := &fakeDigiLocker{identity: ashaIdentity()}
	face := &MockFaceVerifier{Result: SelfieResult{Live: true, Similarity: 97, Provider: "test"}}
	cfg := Config{
		Enabled: true, HashKey: key, Seal: box, Face: face, MinSimilarity: DefaultMinSimilarity,
		DigiLocker: digilocker.Settings{Mode: digilocker.ModeHTTP, Client: dl,
			AuthorizeURL: digilocker.DefaultAuthorizeURL, ClientID: "client-1", RedirectURI: "https://app.example/verify/return"},
	}
	st := newFakeStore()
	st.addProfile(userA, "Asha Rao")
	st.addProfile(userB, "Asha Rao")
	pub := &fakePublisher{}
	clock := t0
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	svc := New(cfg, st, pub, nil, logger).WithClock(func() time.Time { return clock })
	return &fixture{svc: svc, st: st, dl: dl, face: face, pub: pub, clock: &clock, logs: &logs}
}

// start returns the state carried by the authorize URL.
func (f *fixture) start(t *testing.T, user uuid.UUID) (string, *StartResult) {
	t.Helper()
	res, err := f.svc.Start(context.Background(), user)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	u, err := url.Parse(res.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("state"), res
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

// verify runs start → callback → selfie for user.
func (f *fixture) verify(t *testing.T, user uuid.UUID) (*Status, error) {
	t.Helper()
	state, _ := f.start(t, user)
	if _, err := f.svc.Callback(context.Background(), user, state, "code-1"); err != nil {
		return nil, err
	}
	return f.svc.SubmitSelfie(context.Background(), user, uuid.New())
}

// ─── tests ──────────────────────────────────────────────────────────────────

func TestDisabled_EveryStepAnswersDisabled(t *testing.T) {
	f := newFixture(t)
	f.svc.cfg.Enabled = false
	ctx := context.Background()
	_, err := f.svc.Start(ctx, userA)
	wantCode(t, err, "PERSON_VERIFICATION_DISABLED")
	_, err = f.svc.Callback(ctx, userA, strings.Repeat("a", 43), "c")
	wantCode(t, err, "PERSON_VERIFICATION_DISABLED")
	_, err = f.svc.SubmitSelfie(ctx, userA, uuid.New())
	wantCode(t, err, "PERSON_VERIFICATION_DISABLED")
	_, err = f.svc.Status(ctx, userA)
	wantCode(t, err, "PERSON_VERIFICATION_DISABLED")
	if ErrDisabled.Status != 503 {
		t.Fatalf("disabled status = %d, want 503", ErrDisabled.Status)
	}
	if len(f.st.attempts) != 0 {
		t.Fatal("a disabled service must not record attempts")
	}
}

func TestStart_AuthorizeURLCarriesStateAndS256ChallengeOfTheStoredVerifier(t *testing.T) {
	f := newFixture(t)
	state, res := f.start(t, userA)
	u, _ := url.Parse(res.AuthorizeURL)
	q := u.Query()
	if !digilocker.ValidState(state) || q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "client-1" ||
		q.Get("redirect_uri") != "https://app.example/verify/return" || q.Get("response_type") != "code" {
		t.Fatalf("authorize URL params wrong: %v", q)
	}
	a := f.st.attempts[res.AttemptID]
	if a == nil || a.StateHash != digilocker.HashState(stateNamespace, state) || a.StateHash == state {
		t.Fatal("only the state hash may be stored")
	}
	verifier, err := f.svc.cfg.Seal.Open(a.CodeVerifierSealed)
	if err != nil || bytes.Contains(a.CodeVerifierSealed, verifier) {
		t.Fatal("the verifier must be stored sealed")
	}
	if digilocker.CodeChallengeS256(string(verifier)) != q.Get("code_challenge") {
		t.Fatal("code_challenge is not S256 of the stored verifier")
	}
	// The callback sends exactly that verifier to DigiLocker.
	if _, err := f.svc.Callback(context.Background(), userA, state, "code-1"); err != nil {
		t.Fatal(err)
	}
	if f.dl.lastVerifier != string(verifier) || f.dl.lastCode != "code-1" {
		t.Fatal("the exchange did not carry the stored code_verifier")
	}
}

func TestStart_RefusesVerifiedAccountsAndTooManyAttempts(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < MaxAttemptsPerDay; i++ {
		f.start(t, userB)
	}
	_, err := f.svc.Start(context.Background(), userB)
	wantCode(t, err, "TOO_MANY_ATTEMPTS")
	if _, err := f.verify(t, userA); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Start(context.Background(), userA)
	wantCode(t, err, "ALREADY_VERIFIED")
}

func TestCallback_ReplayedForeignExpiredOrMalformedStateIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	state, _ := f.start(t, userA)

	// Another user cannot use A's state.
	_, err := f.svc.Callback(ctx, userB, state, "code-1")
	wantCode(t, err, "INVALID_STATE")
	// Malformed.
	_, err = f.svc.Callback(ctx, userA, "not-a-state", "code-1")
	wantCode(t, err, "INVALID_STATE")
	_, err = f.svc.Callback(ctx, userA, state, "")
	wantCode(t, err, "INVALID_STATE")
	// The owner's first use works, a replay does not.
	if _, err := f.svc.Callback(ctx, userA, state, "code-1"); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Callback(ctx, userA, state, "code-1")
	wantCode(t, err, "INVALID_STATE")

	// Expired.
	state2, _ := f.start(t, userB)
	*f.clock = f.clock.Add(AttemptTTL + time.Second)
	_, err = f.svc.Callback(ctx, userB, state2, "code-1")
	wantCode(t, err, "INVALID_STATE")
}

func TestCallback_ProviderFailuresRefuseTheAttempt(t *testing.T) {
	f := newFixture(t)
	f.dl.exchangeErr = digilocker.ErrProvider
	state, res := f.start(t, userA)
	_, err := f.svc.Callback(context.Background(), userA, state, "code-1")
	wantCode(t, err, "DIGILOCKER_UNAVAILABLE")
	if f.st.attempts[res.AttemptID].Status != store.PersonAttemptRefused {
		t.Fatal("attempt must be refused")
	}
	f.dl.exchangeErr, f.dl.identityErr = nil, digilocker.ErrNoAadhaar
	state, _ = f.start(t, userA)
	_, err = f.svc.Callback(context.Background(), userA, state, "code-1")
	wantCode(t, err, "NO_AADHAAR")
	f.dl.identityErr = nil
	f.dl.identity.Photo = nil
	state, _ = f.start(t, userA)
	_, err = f.svc.Callback(context.Background(), userA, state, "code-1")
	wantCode(t, err, "NO_REFERENCE_PHOTO")
}

func TestCallback_NameMismatchIsRefused(t *testing.T) {
	f := newFixture(t)
	f.st.profiles[userA].DisplayName = "Asha R"
	state, res := f.start(t, userA)
	_, err := f.svc.Callback(context.Background(), userA, state, "code-1")
	wantCode(t, err, "NAME_MISMATCH")
	a := f.st.attempts[res.AttemptID]
	if a.Status != store.PersonAttemptRefused || a.RefusalCode != "NAME_MISMATCH" || a.ReferencePhotoSealed != nil {
		t.Fatalf("attempt = %v", a)
	}
	// Normalised: case, punctuation and order do not matter.
	f.st.profiles[userA].DisplayName = "RAO, asha"
	state, _ = f.start(t, userA)
	if _, err := f.svc.Callback(context.Background(), userA, state, "code-1"); err != nil {
		t.Fatalf("normalised name must match: %v", err)
	}
}

func TestDuplicatePersonIsRefusedOnASecondAccount(t *testing.T) {
	f := newFixture(t)
	if _, err := f.verify(t, userA); err != nil {
		t.Fatal(err)
	}
	// Same Aadhaar person on account B: refused at the callback.
	state, _ := f.start(t, userB)
	_, err := f.svc.Callback(context.Background(), userB, state, "code-1")
	wantCode(t, err, "DUPLICATE_PERSON")
	if f.st.profiles[userB].IsVerified {
		t.Fatal("B must not be verified")
	}
}

func TestDuplicatePersonRacingToApprovalIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// Both accounts pass the callback before either approves.
	sa, _ := f.start(t, userA)
	sb, _ := f.start(t, userB)
	if _, err := f.svc.Callback(ctx, userA, sa, "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Callback(ctx, userB, sb, "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SubmitSelfie(ctx, userA, uuid.New()); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.SubmitSelfie(ctx, userB, uuid.New())
	wantCode(t, err, "DUPLICATE_PERSON")
}

func TestAccountBoundToAnotherPersonIsRefused(t *testing.T) {
	f := newFixture(t)
	if _, err := f.verify(t, userA); err != nil {
		t.Fatal(err)
	}
	// Tick cleared by a rename, then re-verification with someone else's DigiLocker.
	p := f.st.profiles[userA]
	p.IsVerified, p.VerificationLevel, p.BadgeFlags = false, "", 0
	f.dl.identity.AadhaarLast4 = "9999"
	state, _ := f.start(t, userA)
	_, err := f.svc.Callback(context.Background(), userA, state, "c")
	wantCode(t, err, "IDENTITY_MISMATCH")
}

func TestSelfie_FaceMismatchLivenessAndUnavailable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	state, res := f.start(t, userA)
	if _, err := f.svc.Callback(ctx, userA, state, "c"); err != nil {
		t.Fatal(err)
	}
	// Unavailable: no verdict, the attempt keeps waiting.
	f.face.Err = ErrFaceUnavailable
	_, err := f.svc.SubmitSelfie(ctx, userA, uuid.New())
	wantCode(t, err, "FACE_CHECK_UNAVAILABLE")
	if f.st.attempts[res.AttemptID].Status != store.PersonAttemptIdentityChecked {
		t.Fatal("an unavailable provider must leave the attempt pending")
	}
	// Below the threshold: refused, photo wiped, nothing verified.
	f.face.Err = nil
	f.face.Result = SelfieResult{Live: true, Similarity: DefaultMinSimilarity - 0.5}
	_, err = f.svc.SubmitSelfie(ctx, userA, uuid.New())
	wantCode(t, err, "FACE_MISMATCH")
	a := f.st.attempts[res.AttemptID]
	if a.Status != store.PersonAttemptRefused || a.ReferencePhotoSealed != nil || f.st.profiles[userA].IsVerified {
		t.Fatal("a face mismatch must refuse, wipe the photo and verify nothing")
	}
	_, err = f.svc.SubmitSelfie(ctx, userA, uuid.New())
	wantCode(t, err, "NO_PENDING_ATTEMPT")

	// Not live.
	state, _ = f.start(t, userA)
	if _, err := f.svc.Callback(ctx, userA, state, "c"); err != nil {
		t.Fatal(err)
	}
	f.face.Result = SelfieResult{Live: false, Similarity: 99}
	_, err = f.svc.SubmitSelfie(ctx, userA, uuid.New())
	wantCode(t, err, "LIVENESS_FAILED")
	if f.st.profiles[userA].IsVerified {
		t.Fatal("a failed liveness check must verify nothing")
	}
}

func TestSelfie_NameChangedSinceCallbackIsRefused(t *testing.T) {
	f := newFixture(t)
	state, _ := f.start(t, userA)
	if _, err := f.svc.Callback(context.Background(), userA, state, "c"); err != nil {
		t.Fatal(err)
	}
	f.st.profiles[userA].DisplayName = "Someone Else"
	_, err := f.svc.SubmitSelfie(context.Background(), userA, uuid.New())
	wantCode(t, err, "NAME_MISMATCH")
}

func TestSuccess_SetsColumnsBindsPersonAndEmitsEvent(t *testing.T) {
	f := newFixture(t)
	st, err := f.verify(t, userA)
	if err != nil {
		t.Fatal(err)
	}
	p := f.st.profiles[userA]
	if !st.IsVerified || !p.IsVerified || p.VerificationLevel != "person" || p.BadgeFlags&verification.BadgePersonVerified == 0 || !f.st.usrVerify[userA] {
		t.Fatalf("columns not set: %+v", p)
	}
	b, _ := f.st.PersonBindingByUser(context.Background(), userA)
	if b == nil || len(b.IdentityHash) != 64 {
		t.Fatal("person not bound")
	}
	key, _ := ashaIdentity().PersonKey()
	if strings.Contains(b.IdentityHash, "1234") || b.IdentityHash == key {
		t.Fatal("the binding must hold a keyed hash, not the key")
	}
	if len(f.pub.events) != 1 {
		t.Fatalf("events = %d, want 1", len(f.pub.events))
	}
	e := f.pub.events[0]
	if e.UserID != userA.String() || !e.IsVerified || e.VerificationLevel != "person" || e.Reason != verification.ReasonPersonVerified || e.BadgeFlags&verification.BadgePersonVerified == 0 {
		t.Fatalf("event = %+v", e)
	}
	for _, a := range f.st.attempts {
		if a.ReferencePhotoSealed != nil {
			t.Fatal("the photo must be wiped after approval")
		}
	}
	// Nothing personal in the logs.
	for _, secret := range []string{"Asha", "Rao", b.IdentityHash, b.NameHash, "aadhaar-photo", "code-1"} {
		if strings.Contains(f.logs.String(), secret) {
			t.Fatalf("logs carry %q", secret)
		}
	}
	s, err := f.svc.Status(context.Background(), userA)
	if err != nil || !s.IsVerified || s.Next != "" || s.Attempt == nil || s.Attempt.Status != store.PersonAttemptApproved {
		t.Fatalf("status = %+v, %v", s, err)
	}
}

func TestNameChange_ClearsTheTickUnlessTheNameStillMatches(t *testing.T) {
	f := newFixture(t)
	if _, err := f.verify(t, userA); err != nil {
		t.Fatal(err)
	}
	f.pub.events = nil
	profiles := service.New(nil, nil, nil, nil, nil).
		WithProfileWriteStore(f.st, func() time.Time { return t0 }).
		WithPersonVerification(f.svc)
	ctx := context.Background()

	// Same name, different case and order: the tick stays.
	keep := "RAO Asha"
	p, err := profiles.UpdateProfile(ctx, userA, store.UpdateProfileParams{DisplayName: &keep})
	if err != nil || !p.IsVerified || p.VerificationLevel != "person" || len(f.pub.events) != 0 {
		t.Fatalf("a matching rename must keep the tick: %+v %v", p, err)
	}
	// A bio edit does not touch it.
	bio := "hello"
	if p, _ = profiles.UpdateProfile(ctx, userA, store.UpdateProfileParams{Bio: &bio}); !p.IsVerified {
		t.Fatal("a bio edit cleared the tick")
	}
	// A different name clears it everywhere and says so.
	other := "Asha The Great"
	p, err = profiles.UpdateProfile(ctx, userA, store.UpdateProfileParams{DisplayName: &other})
	if err != nil {
		t.Fatal(err)
	}
	if p.IsVerified || p.VerificationLevel != "" || p.BadgeFlags&verification.BadgePersonVerified != 0 || f.st.usrVerify[userA] {
		t.Fatalf("rename did not clear the tick: %+v", p)
	}
	if len(f.pub.events) != 1 || f.pub.events[0].IsVerified || f.pub.events[0].Reason != verification.ReasonDisplayNameChanged {
		t.Fatalf("events = %+v", f.pub.events)
	}
	// The person stays bound to this account; re-verifying under the
	// original name works again.
	orig := "Asha Rao"
	if _, err := profiles.UpdateProfile(ctx, userA, store.UpdateProfileParams{DisplayName: &orig}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.verify(t, userA); err != nil {
		t.Fatalf("re-verification: %v", err)
	}
}

func TestNameChange_WithoutPersonVerificationWiredStillClears(t *testing.T) {
	f := newFixture(t)
	if _, err := f.verify(t, userA); err != nil {
		t.Fatal(err)
	}
	profiles := service.New(nil, nil, nil, nil, nil).WithProfileWriteStore(f.st, nil)
	same := "Asha Rao Two"
	p, err := profiles.UpdateProfile(context.Background(), userA, store.UpdateProfileParams{DisplayName: &same})
	if err != nil || p.IsVerified {
		t.Fatalf("an unwired rename must clear the tick: %+v %v", p, err)
	}
}

func TestRenameKeepsTick_FalseWithoutKeyOrBinding(t *testing.T) {
	f := newFixture(t)
	if f.svc.RenameKeepsTick(context.Background(), userA, "Asha Rao") {
		t.Fatal("no binding must not keep a tick")
	}
	if _, err := f.verify(t, userA); err != nil {
		t.Fatal(err)
	}
	if !f.svc.RenameKeepsTick(context.Background(), userA, "asha rao") {
		t.Fatal("matching name must keep the tick")
	}
	f.svc.cfg.HashKey = nil
	if f.svc.RenameKeepsTick(context.Background(), userA, "Asha Rao") {
		t.Fatal("without a key nothing can be checked, so the tick goes")
	}
}

func TestNameHash_IsKeyed(t *testing.T) {
	f := newFixture(t)
	h1 := f.svc.NameHash("Asha Rao")
	f.svc.cfg.HashKey = bytes.Repeat([]byte{3}, 32)
	if hmac.Equal([]byte(h1), []byte(f.svc.NameHash("Asha Rao"))) || f.svc.NameHash("") != "" {
		t.Fatal("name hash must depend on the key and be empty for an empty name")
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }

	cfg, err := ConfigFromEnv(get)
	if err != nil || cfg.Enabled || !cfg.Production {
		t.Fatalf("default = %+v, %v; want disabled and production when no environment is set", cfg, err)
	}
	env["APP_ENV"] = "dev"
	env[EnvEnabled] = "true"
	env[EnvSealKey], env[EnvFaceMode] = testSealKey, "mock"
	if _, err := ConfigFromEnv(get); err == nil {
		t.Fatal("enabled without the hash key must fail")
	}
	env[EnvHashKey], env[EnvSealKey] = testHashKey, ""
	if _, err := ConfigFromEnv(get); err == nil {
		t.Fatal("enabled without the seal key must fail")
	}
	env[EnvSealKey], env[EnvFaceMode] = testSealKey, ""
	if _, err := ConfigFromEnv(get); err == nil {
		t.Fatal("enabled without a face provider must fail")
	}
	env[EnvFaceMode] = "mock"
	cfg, err = ConfigFromEnv(get)
	if err != nil || !cfg.Enabled || !cfg.DigiLocker.Mock() || cfg.Face == nil {
		t.Fatalf("dev mock config = %v", err)
	}
	env["APP_ENV"] = "production"
	if _, err := ConfigFromEnv(get); err == nil {
		t.Fatal("mock providers must be refused in production")
	}
	env[EnvHashKey] = "short"
	env[EnvEnabled] = "false"
	if _, err := ConfigFromEnv(get); err == nil || strings.Contains(err.Error(), "short") {
		t.Fatalf("a bad key must fail without echoing it: %v", err)
	}
	env[EnvHashKey] = ""
	env[EnvEnabled] = "maybe"
	if _, err := ConfigFromEnv(get); err == nil {
		t.Fatal("a non-boolean switch must fail")
	}
}

func TestMockAuthorizeURL_ReturnsToTheRedirectWithAMockCode(t *testing.T) {
	f := newFixture(t)
	f.svc.cfg.DigiLocker = digilocker.Settings{Mode: digilocker.ModeMock, Client: digilocker.NewMockClient(), RedirectURI: "http://localhost:3000/verify"}
	state, res := f.start(t, userA)
	u, _ := url.Parse(res.AuthorizeURL)
	if u.Host != "localhost:3000" || u.Query().Get("code") == "" || state == "" {
		t.Fatalf("mock authorize URL = %s", res.AuthorizeURL)
	}
	// The real mock client verifies end to end with the profile's name.
	if _, err := f.svc.Callback(context.Background(), userA, state, u.Query().Get("code")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SubmitSelfie(context.Background(), userA, uuid.New()); err != nil {
		t.Fatal(err)
	}
}

func TestFaceFromEnv(t *testing.T) {
	env := map[string]string{EnvFaceMode: "mock"}
	get := func(k string) string { return env[k] }
	if _, _, err := FaceFromEnv(get, true); err == nil {
		t.Fatal("the mock face provider must be refused in production")
	}
	if f, min, err := FaceFromEnv(get, false); err != nil || f == nil || min != DefaultMinSimilarity {
		t.Fatalf("dev mock = %v %v %v", f, min, err)
	}
	env[EnvFaceMode] = ""
	if f, _, err := FaceFromEnv(get, true); err != nil || f != nil {
		t.Fatalf("default must be disabled: %v %v", f, err)
	}
	env[EnvFaceMode] = "http"
	if _, _, err := FaceFromEnv(get, false); err == nil {
		t.Fatal("an unbuilt provider must be refused")
	}
	env[EnvFaceMode], env[EnvMinSimilarity] = "mock", "0"
	if _, _, err := FaceFromEnv(get, false); err == nil {
		t.Fatal("a threshold outside 1-100 must be refused")
	}
}
