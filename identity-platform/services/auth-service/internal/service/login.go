// Sign-in flows: OTP request and verification, password login (with the
// dummy password hash compared when no account matches), and
// createSessionForUser, which issues the session and token pair every
// successful sign-in ends in. Pure move out of auth.go; behaviour is
// unchanged.

package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/events"
	"golang.org/x/crypto/bcrypt"
)

// RequestOTP generates and saves an OTP.
func (s *Service) RequestOTP(ctx context.Context, phone, purpose string) error {
	otp, err := s.generateOTP()
	if err != nil {
		return fmt.Errorf("failed to generate otp: %w", err)
	}

	s.log.Debug("otp generated", "phone", maskPhone(phone), "purpose", purpose)
	return s.store.SaveOTP(ctx, phone, otp, purpose, s.cfg.OTPExpiry)
}

// VerifyOTP validates OTP and logs the user in.
func (s *Service) VerifyOTP(ctx context.Context, phone, code, purpose, deviceID, platform, ip, userAgent string) (*AuthResponse, error) {
	if s.cfg.OTPBypassCode != "" && code == s.cfg.OTPBypassCode {
		// Bypass for dev/test environments only
	} else {
		otp, err := s.store.GetOTP(ctx, phone, purpose)
		if err != nil {
			return nil, err
		}
		if otp == nil {
			return nil, errors.New("invalid or expired otp")
		}
		if otp.Attempts >= s.cfg.OTPMaxAttempts {
			return nil, errors.New("invalid or expired otp")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(otp.Hash), []byte(code)); err != nil {
			attempts, incErr := s.store.IncrementOTPAttempts(ctx, otp.ID)
			if incErr != nil {
				return nil, incErr
			}
			if attempts >= s.cfg.OTPMaxAttempts {
				_ = s.store.DeleteOTP(ctx, otp.ID)
			}
			return nil, errors.New("invalid or expired otp")
		}
		if err := s.store.DeleteOTP(ctx, otp.ID); err != nil {
			return nil, err
		}
	}

	user, err := s.store.GetUserByPhone(ctx, phone)
	if err != nil {
		return nil, err
	}
	created := false
	if user == nil {
		// Transactional: create auth user + usr record + profile + outbox event
		tx, err := s.store.DB().Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback(ctx)

		user, err = s.store.CreateUserTx(ctx, tx, phone)
		if err != nil {
			return nil, err
		}

		if err := s.store.CreateUserRecordTx(ctx, tx, user.ID); err != nil {
			return nil, fmt.Errorf("failed to create user record: %w", err)
		}

		displayName := "User " + user.ID.String()[:8]
		if err := s.store.CreateProfileTx(ctx, tx, user.ID, displayName, "", "", "", ""); err != nil {
			return nil, fmt.Errorf("failed to create profile: %w", err)
		}

		// No email and no name on this path, so the handle comes from the
		// "member" base plus a suffix. It is still a handle: an account with
		// a NULL username has no profile address at all.
		username, err := s.assignHandleTx(ctx, tx, user.ID, "", "", "")
		if err != nil {
			return nil, fmt.Errorf("failed to assign username: %w", err)
		}

		outboxPayload := events.UserRegisteredPayload{
			UserID:    user.ID.String(),
			Username:  username,
			Phone:     phone,
			CreatedAt: time.Now(),
		}
		if err := s.store.InsertOutboxEventTx(ctx, tx, events.UserRegistered, user.ID.String(), outboxPayload); err != nil {
			return nil, fmt.Errorf("failed to insert outbox event: %w", err)
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("failed to commit transaction: %w", err)
		}
		created = true
	}

	if created {
		s.log.Info("user registered via OTP", "user_id", user.ID)
	}

	// Route through createSessionForUser so the 2FA gate AND the A13
	// anomaly gate are applied in one place. The step-up sentinel is
	// re-wrapped as an AuthResponse with RequiresStepUp set so the
	// handler can render it.
	resp, err := s.createSessionForUser(ctx, user, []string{AMRSMS}, deviceID, platform, ip, userAgent)
	if err != nil {
		if errors.Is(err, ErrAnomalyStepUpRequired) {
			// Surface the envelope without the sentinel — handler will
			// inspect RequiresStepUp to format the 401.
			return resp, err
		}
		return nil, err
	}

	// Publish the user-logged-in event only for the success path
	// (no pending 2FA / step-up). The TokenPair is zero-value when a
	// pending response is returned, so checking AccessToken keeps the
	// publish on the real-session case.
	if resp != nil && resp.Tokens.AccessToken != "" {
		if pErr := s.producer.PublishUserLoggedIn(ctx, user.ID, resp.SessionID, deviceID, platform, ip); pErr != nil {
			s.log.Warn("failed to publish user logged in event", "err", pErr, "user_id", user.ID, "session_id", resp.SessionID)
		}
	}

	return resp, nil
}

// dummyPasswordHashValue is a bcrypt hash at this service's password cost,
// made once, that no password matches.
func (s *Service) dummyPasswordHashValue() []byte {
	s.dummyPasswordHashOnce.Do(func() {
		h, err := bcrypt.GenerateFromPassword([]byte("no-account-has-this-password"), s.cfg.BcryptCost)
		if err != nil {
			panic(fmt.Sprintf("dummy password hash: %v", err))
		}
		s.dummyPasswordHash = h
	})
	return s.dummyPasswordHash
}

func (s *Service) LoginWithPassword(ctx context.Context, identifier, password, deviceID, platform, ip, userAgent string) (*AuthResponse, error) {
	// The column is chosen from the identifier's shape: an email-shaped value
	// is never matched against the phone column (H2, registration audit).
	user, err := s.lookupUserByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}

	if user == nil || user.PasswordHash == "" {
		// No account, or one without a password (OAuth only): compare anyway,
		// so the refusal takes as long as a wrong password and its timing does
		// not say which addresses have accounts here.
		_ = s.comparePassword(s.dummyPasswordHashValue(), []byte(password))
		if user == nil {
			return nil, errors.New("invalid credentials")
		}
		return nil, errors.New("user has no password set (try OTP login)")
	}

	// LB-5: an unverified account must not be able to log in.
	//
	// Registration creates the account PENDING and issues no session. Without
	// this check the pending state would be decorative: a caller could simply
	// register and then log in, and an unverified email address would still be
	// a usable identity.
	//
	// CLB-3 — THE PASSWORD IS CHECKED FIRST, AND THAT IS THE SECURE ORDER.
	//
	// This check used to run BEFORE the password comparison, on the reasoning
	// that it avoided leaking whether the password was right. It leaked more
	// than it saved: anyone could learn "this address has a pending account
	// here" by submitting any password at all, and — worse — a real user who
	// closed the app after registering had NO way back. The verification
	// transaction was gone with the process, and resend needed a session they
	// could never get.
	//
	// With the password checked first, a wrong password is the same generic
	// "invalid credentials" whether the account is pending, active, or absent,
	// and a user who proves they own the account gets a fresh verification
	// transaction instead of a dead end. No session is issued either way.
	if err := s.comparePassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid credentials")
	}

	if pending, perr := s.store.IsAccountPendingVerification(ctx, user.ID); perr != nil {
		return nil, fmt.Errorf("check account status: %w", perr)
	} else if pending {
		vt, verr := s.store.CreateVerificationTransaction(ctx, user.ID,
			store.VerificationPurposeEmail, store.VerificationTransactionTTL)
		if verr != nil {
			s.log.Error("pending login: verification transaction not issued",
				"err", verr, "user_id", user.ID)
			return nil, ErrEmailNotVerified
		}
		// A resumption path, not a session: no access token, no refresh token,
		// no session row.
		return &AuthResponse{
			User:                  user,
			RequiresVerification:  true,
			VerificationToken:     vt.Token,
			VerificationExpiresAt: &vt.ExpiresAt,
		}, ErrEmailNotVerified
	}

	// Account-control state machine (deactivate / delete / purge). Runs
	// AFTER the verification gate and the password check, so only the
	// account's owner can trigger a transition, and a wrong password stays
	// the same generic "invalid credentials" regardless of lifecycle state.
	//
	//	deactivated                     → reactivate, continue login
	//	pending_deletion, window open   → cancel deletion, continue login
	//	pending_deletion, window closed → ErrAccountPendingPurge (403)
	//	purged                          → ErrAccountPurged (403)
	if err := s.applyLoginLifecycle(ctx, user); err != nil {
		return nil, err
	}

	// Route through createSessionForUser so both the 2FA gate (A6) and
	// the A13 anomaly gate are applied centrally. The step-up sentinel
	// is forwarded to the handler so the 401-with-body can render.
	resp, err := s.createSessionForUser(ctx, user, []string{AMRPassword}, deviceID, platform, ip, userAgent)
	if err != nil {
		if errors.Is(err, ErrAnomalyStepUpRequired) {
			return resp, err
		}
		return nil, err
	}

	// Side effects only run on the real-session branch (TokenPair
	// populated). 2FA-pending and step-up-pending responses skip these
	// so the user-logged-in event doesn't fire before the session
	// actually exists.
	if resp != nil && resp.Tokens.AccessToken != "" {
		if uErr := s.store.UpdateLastLogin(ctx, user.ID); uErr != nil {
			s.log.Warn("failed to update last_login_at", "err", uErr, "user_id", user.ID)
		}
		if pErr := s.producer.PublishUserLoggedIn(ctx, user.ID, resp.SessionID, deviceID, platform, ip); pErr != nil {
			s.log.Warn("failed to publish user logged in event", "err", pErr, "user_id", user.ID, "session_id", resp.SessionID)
		}
	}

	return resp, nil
}

// createSessionForUser is a helper to create a full session (shared logic).
// amr names how the caller authenticated so far (e.g. ["pwd"]); it is stored
// on the session and becomes the token's amr claim. When it already contains
// AMROTP (Verify2FA calling back in after the TOTP check) the 2FA gate and
// the anomaly enforcement are both satisfied and skipped: re-entering them
// asked a TOTP user for a second pending token forever.
func (s *Service) createSessionForUser(ctx context.Context, user *store.User, amr []string, deviceID, platform, ip, userAgent string) (*AuthResponse, error) {
	// "email" counts only for an email-2FA account: Verify2FA adds it after
	// checking the mailed code. It never satisfies a TOTP account.
	secondFactorDone := hasAMR(amr, AMROTP) || hasAMR(amr, AMRRecovery) ||
		(hasAMR(amr, AMREmail) && user.SecondFactorMethod() == store.TwoFactorMethodEmail)
	// Audit A6: enforce 2FA at every full-session entry point, not just
	// the password-login path. Previously OAuth (Google/Apple) called
	// this directly and skipped the TwoFactorEnabled check that
	// auth.go:213 enforces for password logins — a 2FA-enabled user
	// could still sign in with only a stolen OAuth refresh token.
	// Returning a pending 2FA session here funnels every login flow
	// through the same second-factor gate.
	if user.TwoFactorEnabled && !secondFactorDone {
		pendingToken, err := s.StorePending2FASession(ctx, user.ID, deviceID, platform, ip, userAgent, amr...)
		if err != nil {
			return nil, fmt.Errorf("failed to create pending 2FA session: %w", err)
		}
		resp := &AuthResponse{
			Requires2FA:     true,
			PendingToken:    pendingToken,
			User:            user,
			TwoFactorMethod: user.SecondFactorMethod(),
		}
		if resp.TwoFactorMethod == store.TwoFactorMethodEmail {
			// The code goes out now; /2fa/verify takes it, /2fa/resend
			// sends another.
			if to, ok := verifiedEmail(user); ok {
				resp.EmailMasked = MaskEmail(to)
			}
			s.sendLoginCode(ctx, user)
		}
		return resp, nil
	}

	// A13 — anomaly enforcement. Probe BEFORE minting tokens so we can
	// either (a) gate the session behind step-up when the risk band is
	// high AND enforcement is on, or (b) fall through to the normal
	// path and run shadow side effects. The probe is cheap (one Redis
	// GET + one trusted-devices read); it runs unconditionally so the
	// shadow-mode telemetry continues to be accurate.
	//
	// Sub-policy: suspended accounts are rejected outright (the
	// existing suspended-account gate lives at the GetUserByX layer for
	// password login but not here, so re-assert it). paused / restricted
	// users CAN attempt step-up so they have a path to clear the flag.
	if user.AccountStatus == "suspended" {
		return nil, errors.New("account suspended")
	}

	probe := s.probeLoginAnomaly(ctx, user.ID.String(), ip, deviceID)
	risk := classifyAnomalyRisk(probe.LastIP, ip, probe.IsNewIP, probe.IsNewDevice, probe.UAFamilyChanged)

	if risk == anomalyHigh && s.cfg.LoginAnomalyEnforce == "enforce" && !secondFactorDone {
		// High-risk + enforce mode: refuse to mint tokens. Stash the
		// pending state, dispatch the email-OTP if available, and
		// return a step-up envelope. The handler maps the sentinel to
		// a 401 with body.
		//
		// applyAnomalySideEffects is intentionally NOT called here: we
		// don't want to advance last_ip until the user proves they own
		// the account (otherwise the second login attempt would see
		// the new IP as already-known and bypass the gate).
		resp, err := s.startAnomalyStepUp(ctx, user, amr, deviceID, platform, ip, userAgent)
		if err == nil {
			// startAnomalyStepUp returns (nil, ErrAnomalyStepUpRequired)
			// on success; landing here means an unexpected nil-error
			// success path. Fall through to issue session so we don't
			// strand the user.
			return resp, nil
		}
		// ErrAnomalyStepUpRequired is the expected "happy path" for
		// the enforce branch — return both the envelope and the
		// sentinel so the handler can format the 401-with-body.
		if errors.Is(err, ErrAnomalyStepUpRequired) {
			return resp, err
		}
		// Step-up unavailable (no email_verified, no 2FA) → refuse
		// the login. The user must contact support to recover.
		if errors.Is(err, ErrAnomalyStepUpUnavailable) {
			s.log.Warn("anomaly enforce: no step-up channel; refusing login",
				"user_id", user.ID, "ip", ip)
			return nil, err
		}
		// Any other startAnomalyStepUp failure (Redis down,
		// marshaling, etc.): log and fall through to shadow behaviour
		// rather than locking everybody out. Better to issue the
		// session and rely on the security inbox + push notification
		// than to brick logins on infra hiccups.
		s.log.Error("anomaly enforce: step-up setup failed; falling back to shadow",
			"err", err, "user_id", user.ID)
	}

	// Shadow (or low/medium risk under enforce mode): record the
	// anomaly + emit Kafka if there's anything to report, then mint
	// the session.
	s.applyAnomalySideEffects(ctx, user.ID.String(), ip, deviceID, platform, userAgent, probe)

	return s.startSession(ctx, user, deviceID, platform, ip, userAgent, amr)
}
