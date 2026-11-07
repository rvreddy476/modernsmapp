// Email and phone verification: the verification error values, the capped
// OTP check shared by both channels, verification transactions and resend,
// and the one-time pending-to-active transition once a contact is verified.
// Pure move out of auth.go; behaviour is unchanged.

package service

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// --- Email/Phone Verification ---

var (
	// ErrEmailAlreadyVerified: there is nothing to send. The email relay
	// treats it as DONE, not as a failure to retry (M4, registration audit).
	ErrEmailAlreadyVerified = errors.New("email already verified")
	// ErrEmailUndeliverable: no send can ever succeed for this account (it
	// is gone, has no email, or the stored address is not an address). The
	// relay abandons the job instead of retrying it forever.
	ErrEmailUndeliverable = errors.New("verification email cannot be delivered")
	// ErrResetCodeInvalid is the single answer for every failed reset: wrong,
	// expired, spent or capped code, and an account that does not exist.
	ErrResetCodeInvalid = errors.New("invalid or expired code")
	// ErrVerifyCodeInvalid is the single answer for a failed email code.
	ErrVerifyCodeInvalid = errors.New("invalid or expired code")
)

// dummyCodeHash is compared against when there is no real code to compare,
// so a reset for an unknown account costs what one for a real account does.
// Same cost as store.SaveOTP (bcrypt.DefaultCost).
var dummyCodeHash = sync.OnceValue(func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("not-a-real-code"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return h
})

// RequestEmailVerification sends a verification OTP to the user's email.
func (s *Service) RequestEmailVerification(ctx context.Context, userID uuid.UUID) error {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return fmt.Errorf("%w: user not found", ErrEmailUndeliverable)
	}
	if user.Email == nil || *user.Email == "" {
		return fmt.Errorf("%w: no email on account", ErrEmailUndeliverable)
	}
	if user.EmailVerified {
		return ErrEmailAlreadyVerified
	}
	if _, err := NormalizeEmail(*user.Email); err != nil {
		return fmt.Errorf("%w: stored address is not valid", ErrEmailUndeliverable)
	}

	otp, err := s.generateOTP()
	if err != nil {
		return fmt.Errorf("failed to generate otp: %w", err)
	}

	if err := s.store.SaveOTP(ctx, *user.Email, otp, "email_verify", s.cfg.OTPExpiry); err != nil {
		return fmt.Errorf("save verification code: %w", err)
	}

	// LB-5: this SAVED the code and returned. It never sent anything, so no
	// verification email has ever been delivered by this service — and
	// registration created the account active anyway, which is why nobody
	// noticed. Now the account stays pending until this arrives, so a silent
	// failure here would lock the user out permanently. It is reported.
	if s.email == nil {
		return errors.New("email verification requires email delivery, which is not configured")
	}
	if err := s.email.Send(ctx, EmailMessage{
		To:      *user.Email,
		Subject: "Verify your Momentum email address",
		TextBody: fmt.Sprintf(
			"Your verification code is %s.\n\n"+
				"It expires in %d minutes. Enter it in the app to finish setting up "+
				"your account.\n\nIf you did not create a Momentum account, you can "+
				"ignore this email — the address will not be used.\n",
			otp, int(s.cfg.OTPExpiry.Minutes())),
	}); err != nil {
		return fmt.Errorf("send verification code: %w", err)
	}
	s.log.Info("verification code sent", "user_id", user.ID)
	return nil
}

// VerifyEmail checks the OTP and marks the user's email as verified.
// VerifyEmailWithTransaction is the public verification entry point.
//
// CLB-3: the account is named by the SERVER-ISSUED credential, never by the
// caller. A public route that accepted a user id would let anyone grind codes
// against any account they can name; this way the only accounts an attacker
// can attempt are the ones they were handed a credential for.
//
// Ordering matters and is deliberate:
//
//  1. look the credential up WITHOUT consuming it, so a mistyped code does not
//     burn the user's only way back in;
//  2. check the code, which is where the attempt counter and the OTP expiry
//     do their work;
//  3. consume the credential ATOMICALLY, so a replay of a correct submission
//     cannot re-enter the activation path;
//  4. activate, which is itself one-time.
//
// A wrong purpose, an expired credential, a forged one, or one belonging to
// another user all fail at step 1 with the same error — there is nothing for
// an attacker to learn from the difference.
func (s *Service) VerifyEmailWithTransaction(ctx context.Context, transaction, code string) error {
	userID, err := s.store.LookupVerificationTransaction(ctx, transaction, store.VerificationPurposeEmail)
	if err != nil {
		return err
	}

	if err := s.verifyEmailCode(ctx, userID, code); err != nil {
		return err
	}

	// Spend the credential. If a concurrent request already did, this one
	// matches no row and stops here rather than activating a second time.
	if _, err := s.store.ConsumeVerificationTransaction(ctx, transaction, store.VerificationPurposeEmail); err != nil {
		return err
	}

	return s.activateVerifiedAccount(ctx, userID)
}

// verifyEmailCode checks the emailed code and consumes it on success.
//
// H1 (registration audit): wrong guesses were counted and then ignored, so a
// squatter who registered someone else's address could grind the 6-digit code
// from rotating IPs, activate the account, and later be linked to the real
// owner's Google sign-in. Now, as VerifyOTP does:
//
//   - a per-ACCOUNT budget is charged first (emailVerifyPerHour/PerDay). It
//     is keyed on the user id, in its own fixed window, so neither a resend
//     (which stores a fresh code with attempts=0) nor a fresh verification
//     token from a pending login buys more guesses;
//   - a code that has reached OTP_MAX_ATTEMPTS is refused and deleted;
//   - a wrong guess is counted, and the code is deleted when the count reaches
//     the cap. A failure to count refuses, rather than allowing an uncounted
//     guess.
func (s *Service) verifyEmailCode(ctx context.Context, userID uuid.UUID, code string) error {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return errors.New("user not found")
	}
	if user.Email == nil {
		return errors.New("no email on account")
	}

	if err := s.allowAttempt(ctx, "email_verify_rl:hour:"+userID.String(),
		emailVerifyPerHour, emailVerifyHourWindow, "email_verify_hourly"); err != nil {
		return err
	}
	if err := s.allowAttempt(ctx, "email_verify_rl:day:"+userID.String(),
		emailVerifyPerDay, emailVerifyDayWindow, "email_verify_daily"); err != nil {
		return err
	}

	return s.checkCappedOTP(ctx, *user.Email, "email_verify", code)
}

// checkCappedOTP verifies code against the live (key, purpose) OTP under the
// OTP_MAX_ATTEMPTS cap, deleting the code on success and at the cap.
func (s *Service) checkCappedOTP(ctx context.Context, key, purpose, code string) error {
	otp, err := s.store.GetOTP(ctx, key, purpose)
	if err != nil {
		return err
	}
	if otp == nil {
		return ErrVerifyCodeInvalid
	}
	if otp.Attempts >= s.otpMaxAttempts() {
		_ = s.store.DeleteOTP(ctx, otp.ID)
		return ErrVerifyCodeInvalid
	}

	if err := bcrypt.CompareHashAndPassword([]byte(otp.Hash), []byte(code)); err != nil {
		attempts, incErr := s.store.IncrementOTPAttempts(ctx, otp.ID)
		if incErr != nil {
			return fmt.Errorf("count verification attempt: %w", incErr)
		}
		if attempts >= s.otpMaxAttempts() {
			_ = s.store.DeleteOTP(ctx, otp.ID)
		}
		return ErrVerifyCodeInvalid
	}

	_ = s.store.DeleteOTP(ctx, otp.ID)
	return nil
}

// defaultOTPMaxAttempts matches config's OTP_MAX_ATTEMPTS default.
const defaultOTPMaxAttempts = 5

// otpMaxAttempts is the per-code wrong-guess cap. An unset (zero) value
// means the default, never "refuse every code" and never "unlimited".
func (s *Service) otpMaxAttempts() int {
	if s.cfg == nil || s.cfg.OTPMaxAttempts <= 0 {
		return defaultOTPMaxAttempts
	}
	return s.cfg.OTPMaxAttempts
}

// ResendVerificationWithTransaction re-sends the code for the account the
// credential names. It does NOT consume the credential: a user waiting on a
// slow mail provider may legitimately ask more than once, and burning their
// only credential on a resend would recreate the dead end this fixes.
func (s *Service) ResendVerificationWithTransaction(ctx context.Context, transaction string) error {
	userID, err := s.store.LookupVerificationTransaction(ctx, transaction, store.VerificationPurposeEmail)
	if err != nil {
		return err
	}

	// Per-ACCOUNT quota, checked after the token is exchanged for a user id.
	//
	// Without this the only cap on resend was the per-IP limiter, because
	// OTPRateLimit derives its strong caps from a `phone` field that this
	// route does not carry. That left two abuse paths open: flooding a
	// third party's inbox (burning this domain's sending reputation for every
	// user), and denying verification indefinitely — each resend replaces the
	// stored OTP, so an attacker resending faster than the victim reads mail
	// keeps the victim's code permanently stale.
	if err := s.checkResendQuota(ctx, userID); err != nil {
		return err
	}

	if err := s.RequestEmailVerification(ctx, userID); err != nil {
		return err
	}
	// M2 (registration audit): the code just sent is now the live one. A job
	// still queued from a failed first send must not make the relay send
	// another code that would replace it while the user is typing this one.
	if markErr := s.store.MarkUserEmailJobsSent(ctx, userID); markErr != nil {
		s.log.Warn("resend: verification sent but queued job not closed — relay may send another code",
			"event", "email_job_mark_failed", "user_id", userID, "err", markErr)
	}
	return nil
}

// VerifyEmail verifies by user id. CLB-3 moved the public entry point to
// VerifyEmailWithTransaction; this remains the internal form, callable only
// where the user id is already established by the server.
func (s *Service) VerifyEmail(ctx context.Context, userID uuid.UUID, code string) error {
	if err := s.verifyEmailCode(ctx, userID, code); err != nil {
		return err
	}
	return s.activateVerifiedAccount(ctx, userID)
}

// activateVerifiedAccount performs the one-time pending → active transition.
//
// LB-5: registration creates the account pending and issues no session, so
// this is the transition that makes it usable. ActivateVerifiedAccount only
// promotes a row that is still pending, which is what makes it one-time: a
// replay finds an active account and changes nothing.
func (s *Service) activateVerifiedAccount(ctx context.Context, userID uuid.UUID) error {
	if err := s.store.ActivateVerifiedAccount(ctx, userID); err != nil {
		if errors.Is(err, store.ErrAccountNotPending) {
			// Already active. The code was valid and is now consumed; report
			// success rather than an error the user cannot act on.
			return s.store.MarkEmailVerified(ctx, userID)
		}
		return fmt.Errorf("activate account: %w", err)
	}
	// Verified: nothing left to send. Close any queued job so the relay does
	// not mail a code for an account that no longer needs one (M2/M4).
	if err := s.store.MarkUserEmailJobsSent(ctx, userID); err != nil {
		s.log.Warn("activated but queued verification job not closed",
			"event", "email_job_mark_failed", "user_id", userID, "err", err)
	}
	return nil
}

// RequestPhoneVerification sends a verification OTP to the user's phone.
func (s *Service) RequestPhoneVerification(ctx context.Context, userID uuid.UUID) error {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return errors.New("user not found")
	}
	if user.Phone == "" {
		return errors.New("no phone on account")
	}
	if user.PhoneVerified {
		return errors.New("phone already verified")
	}

	otp, err := s.generateOTP()
	if err != nil {
		return fmt.Errorf("failed to generate otp: %w", err)
	}

	return s.store.SaveOTP(ctx, user.Phone, otp, "phone_verify", s.cfg.OTPExpiry)
}

// VerifyPhone checks the OTP and marks the user's phone as verified.
func (s *Service) VerifyPhone(ctx context.Context, userID uuid.UUID, code string) error {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return errors.New("user not found")
	}
	if user.Phone == "" {
		return errors.New("no phone on account")
	}

	// Same cap as email verification (H1, registration audit). The route is
	// retired, but the method must not be a capless verifier if re-enabled.
	if err := s.checkCappedOTP(ctx, user.Phone, "phone_verify", code); err != nil {
		return err
	}
	return s.store.MarkPhoneVerified(ctx, userID)
}
