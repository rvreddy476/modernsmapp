// Account creation: password policy (validatePassword and its errors),
// password and consent-carrying registration, and handle assignment inside
// the registration transaction. Pure move out of auth.go; behaviour is
// unchanged.

package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/handle"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
	ErrPasswordTooWeak  = errors.New("password must contain at least one uppercase letter, one digit, and one special character")
)

var (
	hasUppercase = regexp.MustCompile(`[A-Z]`)
	hasDigit     = regexp.MustCompile(`[0-9]`)
	hasSpecial   = regexp.MustCompile(`[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]`)
)

func validatePassword(pw string) error {
	if len(pw) < 8 {
		return ErrPasswordTooShort
	}
	if !hasUppercase.MatchString(pw) || !hasDigit.MatchString(pw) || !hasSpecial.MatchString(pw) {
		return ErrPasswordTooWeak
	}
	return nil
}

// RegisterWithPassword delegates to the consent-aware launch registration
// path below; the old optional 13+ helper has been removed.
func (s *Service) RegisterWithPassword(ctx context.Context, phone, email, password, firstName, lastName, dob, gender string) (*AuthResponse, error) {
	return s.RegisterWithConsent(ctx, phone, email, password, firstName, lastName, dob, gender,
		RegistrationConsent{})
}

// RegisterWithConsent is the registration entry point.
//
// SR-6: the age gate was 13 AND was skipped entirely when no date of birth was
// supplied — and `dob` was an optional request field, so any client could
// bypass it by omission. There was no consent capture at all. Both are now
// mandatory and enforced here, at the service layer, so a second handler
// cannot reintroduce the bypass.
func (s *Service) RegisterWithConsent(ctx context.Context, phone, email, password, firstName, lastName, dob, gender string, consent RegistrationConsent) (*AuthResponse, error) {
	// M1 (registration audit): one normal form for the email, so case and
	// whitespace variants are one account, and junk like "foo" is refused
	// here instead of becoming a pending account whose email job can never
	// be delivered.
	normEmail, err := NormalizeEmail(email)
	if err != nil {
		return nil, err
	}
	email = normEmail

	// H2 (registration audit): the phone is format-checked and then DROPPED.
	//
	// It used to be stored unvalidated and unverified in the UNIQUE phone
	// column, and login/reset tried that column first, so registering
	// phone="victim@gmail.com" hijacked the victim's email sign-in and
	// recovery, and registering a real number squatted it for good.
	// Registration verifies the email only; nothing here can prove the caller
	// owns the number, so it must not claim it. Dropping it (rather than
	// parking it in a pending column) is the safer of the two options: no
	// second unverified identity exists anywhere, and no client sends a phone
	// on this route today (Android's RegisterRequestDto has none). A number
	// is attached later through a flow that verifies it. A malformed value is
	// still refused so a client learns its input is wrong.
	if strings.TrimSpace(phone) != "" {
		if _, err := NormalizePhoneE164(phone); err != nil {
			return nil, err
		}
	}
	phone = ""

	if err := validatePassword(password); err != nil {
		return nil, err
	}
	if err := CheckRegistrationEligibility(dob, consent, time.Now()); err != nil {
		return nil, err
	}

	// Audit A9: bcrypt cost env-tunable via BCRYPT_COST.
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cfg.BcryptCost)
	if err != nil {
		return nil, err
	}

	// Transactional: create auth user + usr record + profile + outbox event
	tx, err := s.store.DB().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	user, err := s.store.CreateUserWithPasswordTx(ctx, tx, phone, email, string(hash))
	if err != nil {
		return nil, err
	}

	if err := s.store.CreateUserRecordTx(ctx, tx, user.ID); err != nil {
		return nil, fmt.Errorf("failed to create user record: %w", err)
	}

	displayName := firstName + " " + lastName
	if strings.TrimSpace(displayName) == "" {
		displayName = "User " + user.ID.String()[:8]
	}
	if err := s.store.CreateProfileTx(ctx, tx, user.ID, displayName, firstName, lastName, dob, gender); err != nil {
		return nil, fmt.Errorf("failed to create profile: %w", err)
	}

	username, err := s.assignHandleTx(ctx, tx, user.ID, email, firstName, lastName)
	if err != nil {
		return nil, fmt.Errorf("failed to assign username: %w", err)
	}

	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}
	outboxPayload := events.UserRegisteredPayload{
		UserID:    user.ID.String(),
		Username:  username,
		Phone:     phone,
		Email:     emailPtr,
		FirstName: firstName,
		LastName:  lastName,
		DOB:       dob,
		Gender:    gender,
		CreatedAt: time.Now(),
	}
	if err := s.store.InsertOutboxEventTx(ctx, tx, events.UserRegistered, user.ID.String(), outboxPayload); err != nil {
		return nil, fmt.Errorf("failed to insert outbox event: %w", err)
	}

	// LB-5: the account is created PENDING, and the versioned consent is
	// recorded, both inside this transaction.
	//
	// Previously the account was created ACTIVE and this function returned
	// access and refresh tokens immediately, with no verification challenge
	// sent — so someone else's email address became a working account and the
	// real owner was never contacted. The accepted terms version was checked
	// in memory and discarded, leaving no record of what anyone agreed to.
	if err := s.store.SetAccountPendingTx(ctx, tx, user.ID); err != nil {
		return nil, fmt.Errorf("failed to mark account pending: %w", err)
	}
	var declaredDOB *time.Time
	if parsed, perr := ParseDOB(dob, time.Now()); perr == nil {
		declaredDOB = &parsed
	}
	if err := s.store.RecordConsentTx(ctx, tx, user.ID, store.RegistrationConsent{
		TermsVersion:    consent.Version,
		AcceptedTerms:   consent.Accepted,
		AcceptedPrivacy: consent.Accepted,
		DeclaredDOB:     declaredDOB,
	}); err != nil {
		return nil, fmt.Errorf("failed to record consent: %w", err)
	}

	// Enqueue the verification email INSIDE the transaction.
	//
	// This is the durability half of the fix: the account row and the
	// obligation to contact its owner commit together. Either both exist or
	// neither does, so there is no window in which an account is created that
	// nobody will ever be told about.
	if err := s.store.EnqueueEmailJobTx(ctx, tx, user.ID, store.EmailJobPurposeVerify); err != nil {
		return nil, fmt.Errorf("failed to enqueue verification email: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Fast path: try to send immediately so the common case is instant.
	//
	// A failure here is NOT fatal any more. It used to return an error, which
	// meant a mail-provider blip produced a committed account whose owner saw
	// "registration failed" — with nothing that would ever retry. The job is
	// already durably queued, so the relay owns delivery from here and the
	// registration is reported honestly as the success it is.
	if err := s.RequestEmailVerification(ctx, user.ID); err != nil {
		s.log.Warn("registration: immediate verification send failed — relay will retry",
			"event", "email_immediate_send_failed",
			"user_id", user.ID, "err", err)
	} else if err := s.store.MarkUserEmailJobsSent(ctx, user.ID); err != nil {
		// Worst case the relay sends a second code. A duplicate email is a
		// far smaller failure than a silently undelivered one.
		s.log.Warn("verification sent but job not marked — relay may resend",
			"event", "email_job_mark_failed", "user_id", user.ID, "err", err)
	}

	// CLB-3: issue the verification transaction.
	//
	// The code goes to the mailbox; this goes to the client. Neither alone
	// activates anything — the client must present both, and the credential
	// names which account without the client being able to choose it. Before
	// this, the code had nowhere to be submitted: verify and resend sat behind
	// the auth middleware and derived the user from X-User-Id, which only a
	// verified session produces.
	vt, err := s.store.CreateVerificationTransaction(ctx, user.ID,
		store.VerificationPurposeEmail, store.VerificationTransactionTTL)
	if err != nil {
		s.log.Error("registration: verification transaction not issued", "err", err, "user_id", user.ID)
		return nil, fmt.Errorf("account created but verification could not be started: %w", err)
	}

	// No tokens. The caller must verify first — that is the whole point of a
	// pending account. Returning a session here would mean an unverified
	// address is a usable identity.
	return &AuthResponse{
		User:                  user,
		RequiresVerification:  true,
		VerificationToken:     vt.Token,
		VerificationExpiresAt: &vt.ExpiresAt,
	}, nil
}

// assignHandleTx gives a brand-new account its public handle, inside the
// transaction that is creating it.
//
// Every account-creation path calls this — email registration, phone
// registration and OAuth sign-up — because a NULL username is what left
// accounts with no /u/<handle> address at all. The handle is derived from
// the email address the way LinkedIn and YouTube do it; a phone-only signup
// has no email and no name, so it lands on the "member" base and gets its
// uniqueness from the suffix.
//
// Uniqueness is settled by the partial unique index on
// profile.profiles(username), not by a check-then-write — see
// store.AssignUsernameTx.
func (s *Service) assignHandleTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, email, firstName, lastName string) (string, error) {
	base := handle.Base(email, firstName, lastName)
	candidates := append(handle.Candidates(base), handle.Fallback(), handle.Fallback())
	return s.store.AssignUsernameTx(ctx, tx, userID, candidates)
}
