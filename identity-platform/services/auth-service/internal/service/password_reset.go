// Password reset: ForgotPassword sends a reset code to the account's phone
// or email without revealing whether the account exists, and ResetPassword
// checks the code and sets the new password. Pure move out of auth.go;
// behaviour is unchanged.

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/identity-auth-service/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// --- Password Reset ---

// ForgotPassword sends a password reset OTP to the user's phone or email.
func (s *Service) ForgotPassword(ctx context.Context, identifier string) error {
	// Shape-based lookup, never phone-first (H2, registration audit).
	user, err := s.lookupUserByIdentifier(ctx, identifier)
	if err != nil {
		return err
	}
	if user == nil {
		// Don't reveal whether the user exists
		return nil
	}

	otp, err := s.generateOTP()
	if err != nil {
		return fmt.Errorf("failed to generate otp: %w", err)
	}

	// SR-6: recovery is EMAIL ONLY at launch.
	//
	// This used to prefer the phone as the OTP key and then send nothing at
	// all — there was no delivery mechanism in the service. The result was a
	// "Forgot password" that returned 200 while no code ever arrived: account
	// recovery did not exist. SMS is not wired either, so a phone-keyed code
	// would still be undeliverable.
	if user.Email == nil || strings.TrimSpace(*user.Email) == "" {
		// Same silent return as the unknown-user case: revealing that an
		// account exists but has no email is still an account oracle.
		s.log.Warn("password reset requested for an account with no email address",
			"user_id", user.ID)
		return nil
	}
	otpKey := *user.Email

	if err := s.store.SaveOTP(ctx, otpKey, otp, "password_reset", s.cfg.OTPExpiry); err != nil {
		return fmt.Errorf("save reset code: %w", err)
	}

	if s.email == nil {
		// Report the failure rather than returning success. A caller that
		// treats this as sent tells the user their code is on the way.
		return errors.New("password reset requires email delivery, which is not configured")
	}
	if err := s.email.Send(ctx, EmailMessage{
		To:      otpKey,
		Subject: "Your Momentum password reset code",
		TextBody: fmt.Sprintf(
			"Your password reset code is %s.\n\n"+
				"It expires in %d minutes. If you did not request a password reset, "+
				"you can ignore this email — your password has not been changed.\n",
			otp, int(s.cfg.OTPExpiry.Minutes())),
	}); err != nil {
		return fmt.Errorf("send reset code: %w", err)
	}
	s.log.Info("password reset code sent", "user_id", user.ID)
	return nil
}

// ResetPassword consumes the one-time code, sets the new password, and revokes
// every session — atomically.
//
// LB-5, two defects:
//
//  1. THE KEY MISMATCH. ForgotPassword stores the code under the EMAIL
//     (recovery is email-only at launch; SMS does not exist). This function
//     looked up `user.Phone` first and only fell back to email when the phone
//     was empty. So any account with BOTH a phone and an email could never
//     find its own emailed code: recovery was broken for exactly the users
//     most likely to have completed a full profile.
//
//  2. THE ORDERING. The code was deleted BEFORE the new password was
//     validated, so a weak-password rejection left the user with a consumed
//     code and no way to retry. And the password was committed BEFORE
//     sessions were revoked, so a failure in between left the account with a
//     new password and the attacker's session still live — the opposite of
//     what a reset is for.
//
// Everything now happens in one transaction, and the password is validated
// and hashed BEFORE the code is consumed.
func (s *Service) ResetPassword(ctx context.Context, identifier, code, newPassword string) error {
	// M4 (login audit): reset must not say whether an account exists.
	//
	// The password used to be validated AFTER the lookup, so a weak password
	// answered 400 RESET_FAILED for an unknown identifier and 422
	// WEAK_PASSWORD for a real one, and only real accounts paid for a bcrypt
	// hash. Now the password is validated and hashed FIRST, for every caller,
	// and an unknown account (or one without an email) pays the same code
	// comparison a real one does and gets the same error as a wrong code.
	// A rejected password still leaves a real user's code usable: nothing has
	// been read or consumed yet.
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.cfg.BcryptCost)
	if err != nil {
		return err
	}

	// Shape-based lookup, never phone-first (H2, registration audit).
	user, err := s.lookupUserByIdentifier(ctx, identifier)
	if err != nil {
		return err
	}

	// Recovery is EMAIL-ONLY, matching ForgotPassword. Keying off the phone
	// here is what broke recovery for every user who had both.
	if user == nil || user.Email == nil || strings.TrimSpace(*user.Email) == "" {
		_ = bcrypt.CompareHashAndPassword(dummyCodeHash(), []byte(code))
		return ErrResetCodeInvalid
	}
	otpKey := *user.Email

	// M5: the sessions the transaction below will revoke, so their live
	// access tokens can be killed in the revocation cache after it commits.
	live := s.liveSessionIDs(ctx, user.ID)

	// One transaction: consume the code, set the password, revoke sessions.
	err = s.store.ConsumeRecoveryAndSetPassword(
		ctx, user.ID, otpKey, "password_reset",
		func(storedHash string) bool {
			return bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(code)) == nil
		},
		string(hash),
		s.otpMaxAttempts(),
	)
	if err != nil {
		if errors.Is(err, store.ErrRecoveryCodeInvalid) {
			return ErrResetCodeInvalid
		}
		return err
	}
	s.cacheRevokeAll(ctx, live)

	// A16: wipe any in-flight pending-2FA sessions. An attacker who already
	// cleared step 1 and is sitting on a pending_token within its Redis TTL
	// must not complete step 2 with credentials that were just rotated.
	//
	// This is deliberately AFTER the transaction and best-effort: it lives in
	// Redis, cannot join a Postgres transaction, and a failure here must not
	// roll back a completed password reset. The durable revocation already
	// committed above, so a stale pending token cannot mint a session that
	// survives refresh.
	s.InvalidatePending2FASessions(ctx, user.ID)

	s.log.Info("password reset successful", "user_id", user.ID)
	return nil
}
