// Package personverify is person-level identity verification (the
// "Verified" tick, docs/designs/short-posts-design.md section 3.4):
//
//	start     → a DigiLocker authorize URL with a single-use state and PKCE
//	callback  → the code is exchanged; the Aadhaar name and date of birth are
//	            read; the display name must match the name; the person must
//	            not already be verified on another account
//	selfie    → liveness and a face match against the DigiLocker photo; when
//	            every check passes the tick is set automatically (founder
//	            rule 19 Sep: no manual step on this path)
//
// One verified account per person: a keyed HMAC of the DigiLocker person key
// is bound to the account (profile.verified_persons). Changing the display
// name of a verified account clears the tick unless the new name still
// matches (see the profile service's UpdateProfile).
//
// Never logged: names, dates of birth, photos, codes, states, verifiers and
// hashes. Log lines carry the user id, the attempt id and a code.
package personverify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/atpost/identity-shared/digilocker"
	sharedEvents "github.com/atpost/identity-shared/events"
	"github.com/atpost/identity-shared/verification"
	"github.com/google/uuid"
)

// Timing and limits.
const (
	// AttemptTTL is how long a started attempt waits for its callback, and
	// how long a checked identity waits for its selfie.
	AttemptTTL = 15 * time.Minute
	// MaxAttemptsPerDay caps starts per user per rolling 24 hours.
	MaxAttemptsPerDay = 5
	// stateNamespace keys the state hash to this table.
	stateNamespace = "profile.person"
)

// Error is a refusal the HTTP layer answers with Status and Code.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code }

func refusal(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

// Refusal codes.
var (
	ErrDisabled = refusal(http.StatusServiceUnavailable, "PERSON_VERIFICATION_DISABLED", "Identity verification is not available yet")

	errProfileNotFound     = refusal(http.StatusNotFound, "PROFILE_NOT_FOUND", "Profile not found")
	errAlreadyVerified     = refusal(http.StatusConflict, "ALREADY_VERIFIED", "This account is already verified")
	errTooManyAttempts     = refusal(http.StatusTooManyRequests, "TOO_MANY_ATTEMPTS", "Too many verification attempts today; try again tomorrow")
	errInvalidState        = refusal(http.StatusBadRequest, "INVALID_STATE", "This verification link is invalid or has expired; start again")
	errProviderUnavailable = refusal(http.StatusBadGateway, "DIGILOCKER_UNAVAILABLE", "DigiLocker could not be reached; start again")
	errNoAadhaar           = refusal(http.StatusUnprocessableEntity, "NO_AADHAAR", "Your DigiLocker account has no Aadhaar linked")
	errNoReferencePhoto    = refusal(http.StatusUnprocessableEntity, "NO_REFERENCE_PHOTO", "DigiLocker returned no photo to compare your selfie with")
	errNameMismatch        = refusal(http.StatusUnprocessableEntity, "NAME_MISMATCH", "Your display name must match the name on your Aadhaar")
	errDuplicatePerson     = refusal(http.StatusConflict, "DUPLICATE_PERSON", "This person is already verified on another account")
	errIdentityMismatch    = refusal(http.StatusConflict, "IDENTITY_MISMATCH", "This account is already verified as a different person")
	errNoPendingAttempt    = refusal(http.StatusConflict, "NO_PENDING_ATTEMPT", "No verification is waiting for a selfie; start again")
	errFaceUnavailable     = refusal(http.StatusServiceUnavailable, "FACE_CHECK_UNAVAILABLE", "The selfie check is unavailable; try again shortly")
	errSelfieInvalid       = refusal(http.StatusUnprocessableEntity, "SELFIE_INVALID", "This selfie cannot be checked; record another")
	errLivenessFailed      = refusal(http.StatusUnprocessableEntity, "LIVENESS_FAILED", "The selfie did not pass the liveness check; start again")
	errFaceMismatch        = refusal(http.StatusUnprocessableEntity, "FACE_MISMATCH", "The selfie does not match your Aadhaar photo; start again")
)

// Store is the storage the service needs (*store.Store in production).
type Store interface {
	GetProfile(ctx context.Context, userID uuid.UUID) (*store.Profile, error)
	CountPersonAttemptsSince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error)
	CreatePersonAttempt(ctx context.Context, a store.PersonAttempt) error
	ConsumePersonAttemptState(ctx context.Context, userID uuid.UUID, stateHash string, now time.Time) (*store.PersonAttempt, error)
	MarkPersonIdentityChecked(ctx context.Context, id uuid.UUID, identityHash, nameHash string, photoSealed []byte, expiresAt time.Time) error
	RefusePersonAttempt(ctx context.Context, id uuid.UUID, code string, now time.Time) error
	PendingSelfieAttempt(ctx context.Context, userID uuid.UUID, now time.Time) (*store.PersonAttempt, error)
	LatestPersonAttempt(ctx context.Context, userID uuid.UUID) (*store.PersonAttempt, error)
	PersonBindingByIdentity(ctx context.Context, identityHash string) (*store.PersonBinding, error)
	PersonBindingByUser(ctx context.Context, userID uuid.UUID) (*store.PersonBinding, error)
	ApprovePerson(ctx context.Context, in store.ApprovePersonInput) (*store.Profile, error)
	SyncUserVerified(ctx context.Context, userID uuid.UUID, verified bool) (bool, error)
}

// Publisher emits identity.verification.changed (*events.Producer).
type Publisher interface {
	PublishVerificationChanged(ctx context.Context, payload sharedEvents.IdentityVerificationChangedPayload) error
}

// Service runs person verification.
type Service struct {
	cfg        Config
	store      Store
	pub        Publisher
	invalidate func(ctx context.Context, p *store.Profile)
	now        func() time.Time
	log        *slog.Logger
}

// New builds the service. pub and invalidate may be nil.
func New(cfg Config, st Store, pub Publisher, invalidate func(context.Context, *store.Profile), logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{cfg: cfg, store: st, pub: pub, invalidate: invalidate, now: time.Now, log: logger}
}

// WithClock replaces the clock. For tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Enabled reports whether the routes serve.
func (s *Service) Enabled() bool { return s != nil && s.cfg.Enabled }

// StartResult is the start route's answer.
type StartResult struct {
	AttemptID    uuid.UUID `json:"attempt_id"`
	AuthorizeURL string    `json:"authorize_url"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// AttemptStatus is the newest attempt, as the owner sees it.
type AttemptStatus struct {
	ID          uuid.UUID `json:"id"`
	Status      string    `json:"status"`
	RefusalCode string    `json:"refusal_code,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// Status is the owner's verification state.
type Status struct {
	IsVerified        bool           `json:"is_verified"`
	VerificationLevel string         `json:"verification_level"`
	BadgeFlags        int            `json:"badge_flags"`
	Attempt           *AttemptStatus `json:"attempt"`
	// Next is the step the client shows: "digilocker", "selfie" or "" (done).
	Next string `json:"next"`
}

// Start opens an attempt.
func (s *Service) Start(ctx context.Context, userID uuid.UUID) (*StartResult, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	p, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errProfileNotFound
	}
	if p.IsVerified && p.VerificationLevel == verification.LevelPerson {
		return nil, errAlreadyVerified
	}
	now := s.now().UTC()
	n, err := s.store.CountPersonAttemptsSince(ctx, userID, now.Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	if n >= MaxAttemptsPerDay {
		return nil, errTooManyAttempts
	}
	state, err := digilocker.NewState()
	if err != nil {
		return nil, err
	}
	verifier, err := digilocker.NewCodeVerifier()
	if err != nil {
		return nil, err
	}
	sealed, err := s.cfg.Seal.Seal([]byte(verifier))
	if err != nil {
		return nil, err
	}
	authURL, err := s.authorizeURL(p.DisplayName, state, digilocker.CodeChallengeS256(verifier))
	if err != nil {
		return nil, err
	}
	a := store.PersonAttempt{
		ID: uuid.New(), UserID: userID, StateHash: digilocker.HashState(stateNamespace, state),
		CodeVerifierSealed: sealed, ExpiresAt: now.Add(AttemptTTL), CreatedAt: now,
	}
	if err := s.store.CreatePersonAttempt(ctx, a); err != nil {
		return nil, err
	}
	s.log.Info("person verification started", "user_id", userID, "attempt_id", a.ID)
	return &StartResult{AttemptID: a.ID, AuthorizeURL: authURL, ExpiresAt: a.ExpiresAt}, nil
}

// authorizeURL is DigiLocker's authorize URL. In mock mode (development
// only) it is the redirect URI carrying a mock code whose identity has the
// profile's display name, as if DigiLocker had already returned.
func (s *Service) authorizeURL(displayName, state, challenge string) (string, error) {
	dl := s.cfg.DigiLocker
	if dl.Mock() {
		base := dl.RedirectURI
		if base == "" {
			base = "http://localhost/person-verification/return"
		}
		u, err := url.Parse(base)
		if err != nil {
			return "", err
		}
		q := u.Query()
		q.Set("code", digilocker.MockCodeForName(displayName))
		q.Set("state", state)
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	return digilocker.AuthorizeURL(dl.AuthorizeURL, dl.ClientID, dl.RedirectURI, state, challenge)
}

// Callback finishes the DigiLocker leg. The state must be one this user
// started, unused and unexpired; it is consumed before anything else, so a
// replay finds nothing whatever the outcome.
func (s *Service) Callback(ctx context.Context, userID uuid.UUID, state, code string) (*Status, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	if !digilocker.ValidState(state) || code == "" || len(code) > 512 {
		return nil, errInvalidState
	}
	now := s.now().UTC()
	a, err := s.store.ConsumePersonAttemptState(ctx, userID, digilocker.HashState(stateNamespace, state), now)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, errInvalidState
	}
	verifier, err := s.cfg.Seal.Open(a.CodeVerifierSealed)
	if err != nil {
		return nil, s.refuse(ctx, a.ID, userID, errInvalidState)
	}
	session, err := s.cfg.DigiLocker.Client.ExchangeCode(ctx, code, string(verifier))
	if err != nil {
		return nil, s.refuse(ctx, a.ID, userID, errProviderUnavailable)
	}
	id, err := s.cfg.DigiLocker.Client.Identity(ctx, session)
	switch {
	case errors.Is(err, digilocker.ErrNoAadhaar):
		return nil, s.refuse(ctx, a.ID, userID, errNoAadhaar)
	case err != nil:
		return nil, s.refuse(ctx, a.ID, userID, errProviderUnavailable)
	}
	key, err := id.PersonKey()
	if err != nil {
		return nil, s.refuse(ctx, a.ID, userID, errNoAadhaar)
	}
	if len(id.Photo) == 0 {
		return nil, s.refuse(ctx, a.ID, userID, errNoReferencePhoto)
	}

	p, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, s.refuse(ctx, a.ID, userID, errProfileNotFound)
	}
	if !digilocker.NamesMatch(p.DisplayName, id.Name) {
		return nil, s.refuse(ctx, a.ID, userID, errNameMismatch)
	}
	identityHash := s.mac("person-identity.v1", key)
	if b, err := s.store.PersonBindingByIdentity(ctx, identityHash); err != nil {
		return nil, err
	} else if b != nil && b.UserID != userID {
		return nil, s.refuse(ctx, a.ID, userID, errDuplicatePerson)
	}
	if b, err := s.store.PersonBindingByUser(ctx, userID); err != nil {
		return nil, err
	} else if b != nil && !hmac.Equal([]byte(b.IdentityHash), []byte(identityHash)) {
		return nil, s.refuse(ctx, a.ID, userID, errIdentityMismatch)
	}

	photo, err := s.cfg.Seal.Seal(id.Photo)
	if err != nil {
		return nil, err
	}
	expires := now.Add(AttemptTTL)
	if err := s.store.MarkPersonIdentityChecked(ctx, a.ID, identityHash, s.NameHash(id.Name), photo, expires); err != nil {
		if errors.Is(err, store.ErrAttemptGone) {
			return nil, errInvalidState
		}
		return nil, err
	}
	s.log.Info("person verification identity checked", "user_id", userID, "attempt_id", a.ID)
	return &Status{
		IsVerified: p.IsVerified, VerificationLevel: p.VerificationLevel, BadgeFlags: p.BadgeFlags,
		Attempt: &AttemptStatus{ID: a.ID, Status: store.PersonAttemptIdentityChecked, ExpiresAt: expires},
		Next:    "selfie",
	}, nil
}

// SubmitSelfie runs liveness and the face match and, when both pass,
// approves: the tick is set with no human step.
func (s *Service) SubmitSelfie(ctx context.Context, userID, selfieMediaID uuid.UUID) (*Status, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	if selfieMediaID == uuid.Nil {
		return nil, errSelfieInvalid
	}
	now := s.now().UTC()
	a, err := s.store.PendingSelfieAttempt(ctx, userID, now)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, errNoPendingAttempt
	}
	photo, err := s.cfg.Seal.Open(a.ReferencePhotoSealed)
	if err != nil || len(photo) == 0 {
		return nil, s.refuse(ctx, a.ID, userID, errNoReferencePhoto)
	}
	res, err := s.cfg.Face.VerifySelfie(ctx, SelfieCheck{UserID: userID, SelfieMediaID: selfieMediaID, ReferencePhoto: photo})
	switch {
	case errors.Is(err, ErrSelfieInvalid):
		return nil, errSelfieInvalid
	case err != nil:
		// Unavailable, or anything that is not an answer: never a pass,
		// never a refusal. The attempt keeps waiting until it expires.
		s.log.Warn("person verification face check unavailable", "user_id", userID, "attempt_id", a.ID)
		return nil, errFaceUnavailable
	case res == nil || res.Similarity < 0 || res.Similarity > 100:
		return nil, errFaceUnavailable
	case !res.Live:
		return nil, s.refuse(ctx, a.ID, userID, errLivenessFailed)
	case res.Similarity < s.cfg.MinSimilarity:
		return nil, s.refuse(ctx, a.ID, userID, errFaceMismatch)
	}

	p, err := s.store.ApprovePerson(ctx, store.ApprovePersonInput{
		AttemptID: a.ID, UserID: userID, IdentityHash: a.IdentityHash, NameHash: a.NameHash,
		HashName: s.NameHash, Similarity: res.Similarity,
	})
	switch {
	case errors.Is(err, store.ErrNameChanged):
		return nil, s.refuse(ctx, a.ID, userID, errNameMismatch)
	case errors.Is(err, store.ErrDuplicatePerson):
		return nil, s.refuse(ctx, a.ID, userID, errDuplicatePerson)
	case errors.Is(err, store.ErrIdentityMismatch):
		return nil, s.refuse(ctx, a.ID, userID, errIdentityMismatch)
	case errors.Is(err, store.ErrAttemptGone):
		return nil, errNoPendingAttempt
	case err != nil:
		return nil, err
	}
	s.log.Info("person verification approved", "user_id", userID, "attempt_id", a.ID)
	s.changed(ctx, p, verification.ReasonPersonVerified)
	return &Status{
		IsVerified: p.IsVerified, VerificationLevel: p.VerificationLevel, BadgeFlags: p.BadgeFlags,
		Attempt: &AttemptStatus{ID: a.ID, Status: store.PersonAttemptApproved, ExpiresAt: a.ExpiresAt},
	}, nil
}

// Status is the owner's verification state and newest attempt.
func (s *Service) Status(ctx context.Context, userID uuid.UUID) (*Status, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	p, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errProfileNotFound
	}
	out := &Status{IsVerified: p.IsVerified, VerificationLevel: p.VerificationLevel, BadgeFlags: p.BadgeFlags}
	a, err := s.store.LatestPersonAttempt(ctx, userID)
	if err != nil {
		return nil, err
	}
	if a != nil {
		out.Attempt = &AttemptStatus{ID: a.ID, Status: a.Status, RefusalCode: a.RefusalCode, ExpiresAt: a.ExpiresAt}
	}
	personVerified := p.IsVerified && p.VerificationLevel == verification.LevelPerson
	switch {
	case personVerified:
		out.Next = ""
	case a != nil && a.Status == store.PersonAttemptIdentityChecked && a.ExpiresAt.After(s.now()):
		out.Next = "selfie"
	default:
		out.Next = "digilocker"
	}
	return out, nil
}

// NameHash is the keyed hash of a normalised name ("" without a key or for
// a name that normalises to nothing, which matches no stored hash).
func (s *Service) NameHash(name string) string {
	n := digilocker.NormaliseName(name)
	if n == "" || len(s.cfg.HashKey) == 0 {
		return ""
	}
	return s.mac("person-name.v1", n)
}

func (s *Service) mac(label, value string) string {
	m := hmac.New(sha256.New, s.cfg.HashKey)
	m.Write([]byte(label))
	m.Write([]byte{0})
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))
}

// refuse ends the attempt with the refusal's code and returns the refusal.
// A failure to record it is logged; the user still gets the refusal.
func (s *Service) refuse(ctx context.Context, attemptID, userID uuid.UUID, e *Error) error {
	if err := s.store.RefusePersonAttempt(ctx, attemptID, e.Code, s.now().UTC()); err != nil {
		s.log.Error("person verification: recording refusal failed", "user_id", userID, "attempt_id", attemptID, "code", e.Code, "err", err)
	}
	s.log.Info("person verification refused", "user_id", userID, "attempt_id", attemptID, "code", e.Code)
	return e
}

// RenameKeepsTick reports whether newDisplayName still matches the
// DigiLocker name the account was verified with. False on any doubt (no
// key, no binding, a read error), which clears the tick.
func (s *Service) RenameKeepsTick(ctx context.Context, userID uuid.UUID, newDisplayName string) bool {
	if s == nil || len(s.cfg.HashKey) == 0 {
		return false
	}
	b, err := s.store.PersonBindingByUser(ctx, userID)
	if err != nil || b == nil {
		return false
	}
	h := s.NameHash(newDisplayName)
	return h != "" && hmac.Equal([]byte(h), []byte(b.NameHash))
}

// AfterRename runs after a display-name write that was allowed to clear the
// tick. It mirrors is_verified onto usr.users and emits the event when the
// tick went (it was person-verified before, or usr.users still said so).
func (s *Service) AfterRename(ctx context.Context, userID uuid.UUID, p *store.Profile, wasPersonVerified bool) {
	if s == nil || p == nil || p.VerificationLevel == verification.LevelPerson {
		return
	}
	changed, err := s.store.SyncUserVerified(ctx, userID, p.IsVerified)
	if err != nil {
		s.log.Error("person verification: clearing usr.users.is_verified failed", "user_id", userID, "err", err)
	}
	if !changed && !wasPersonVerified {
		return
	}
	s.log.Info("person verification cleared by display name change", "user_id", userID)
	s.changed(ctx, p, verification.ReasonDisplayNameChanged)
}

func (s *Service) changed(ctx context.Context, p *store.Profile, reason string) {
	if s.invalidate != nil {
		s.invalidate(ctx, p)
	}
	if s.pub == nil {
		return
	}
	err := s.pub.PublishVerificationChanged(ctx, sharedEvents.IdentityVerificationChangedPayload{
		UserID: p.UserID.String(), IsVerified: p.IsVerified, VerificationLevel: p.VerificationLevel,
		BadgeFlags: p.BadgeFlags, Reason: reason, ChangedAt: s.now().UTC(),
	})
	if err != nil {
		s.log.Warn("person verification: publishing identity.verification.changed failed", "user_id", p.UserID, "reason", reason, "err", err)
	}
}
