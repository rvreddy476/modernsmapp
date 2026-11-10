package service

// Email two-step verification for app users (twofa_email.go): unit tests
// over the in-memory admin-session store, an in-memory auth.otp_codes and
// miniredis. Codes are read from the captured email body, never logged.

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// email2FAStore adds a live auth.otp_codes table and real 2FA writes to the
// admin-session fake.
type email2FAStore struct {
	*adminSessionStore
	otps map[string]*store.OTP // key|purpose
}

func (e *email2FAStore) SaveOTP(_ context.Context, key, code, purpose string, ttl time.Duration) error {
	h, err := bcrypt.GenerateFromPassword([]byte(code), 4)
	if err != nil {
		return err
	}
	e.otps[key+"|"+purpose] = &store.OTP{ID: uuid.New(), Phone: key, Hash: string(h), Purpose: purpose, ExpiresAt: time.Now().Add(ttl)}
	return nil
}
func (e *email2FAStore) GetOTP(_ context.Context, key, purpose string) (*store.OTP, error) {
	o, ok := e.otps[key+"|"+purpose]
	if !ok || time.Now().After(o.ExpiresAt) {
		return nil, nil
	}
	cp := *o
	return &cp, nil
}
func (e *email2FAStore) IncrementOTPAttempts(_ context.Context, id uuid.UUID) (int, error) {
	for _, o := range e.otps {
		if o.ID == id {
			o.Attempts++
			return o.Attempts, nil
		}
	}
	return 0, errors.New("no such code")
}
func (e *email2FAStore) DeleteOTP(_ context.Context, id uuid.UUID) error {
	for k, o := range e.otps {
		if o.ID == id {
			delete(e.otps, k)
		}
	}
	return nil
}
func (e *email2FAStore) EnableEmail2FA(_ context.Context, id uuid.UUID) error {
	u := e.users[id]
	if u == nil || u.TwoFactorEnabled {
		return store.ErrTwoFactorStateChanged
	}
	u.TwoFactorEnabled, u.TwoFactorMethod = true, store.TwoFactorMethodEmail
	return nil
}
func (e *email2FAStore) Enable2FA(_ context.Context, id uuid.UUID, secret string) error {
	u := e.users[id]
	u.TwoFactorEnabled, u.TwoFactorMethod = true, store.TwoFactorMethodTOTP
	e.secrets[id] = secret
	return nil
}
func (e *email2FAStore) Disable2FA(_ context.Context, id uuid.UUID) error {
	u := e.users[id]
	u.TwoFactorEnabled, u.TwoFactorMethod = false, ""
	delete(e.secrets, id)
	return nil
}

// outbox captures every email the service sends.
type outbox struct{ msgs []EmailMessage }

func (o *outbox) Send(_ context.Context, m EmailMessage) error {
	o.msgs = append(o.msgs, m)
	return nil
}

var sixDigits = regexp.MustCompile(`\b(\d{6})\b`)

// lastCode is the code in the newest email, and that email's subject.
func (o *outbox) lastCode(t *testing.T) (string, string) {
	t.Helper()
	if len(o.msgs) == 0 {
		t.Fatal("no email was sent")
	}
	m := o.msgs[len(o.msgs)-1]
	c := sixDigits.FindString(m.TextBody)
	if c == "" {
		t.Fatal("email carries no 6-digit code")
	}
	return c, m.Subject
}

type email2FAFixture struct {
	*adminFixture
	es   *email2FAStore
	mail *outbox
}

func newEmail2FAFixture(t *testing.T) *email2FAFixture {
	t.Helper()
	f := newAdminFixture(t)
	es := &email2FAStore{adminSessionStore: f.st, otps: map[string]*store.OTP{}}
	f.svc.store = es
	mail := &outbox{}
	f.svc.WithEmailSender(mail)
	return &email2FAFixture{adminFixture: f, es: es, mail: mail}
}

// consumer is an active app user with a verified email and no 2FA.
func (f *email2FAFixture) consumer(t *testing.T) *store.User {
	u := f.addUser(t, false)
	u.EmailVerified = true
	return u
}

// withEmail2FA is consumer() with email 2FA already on.
func (f *email2FAFixture) withEmail2FA(t *testing.T) *store.User {
	u := f.consumer(t)
	u.TwoFactorEnabled, u.TwoFactorMethod = true, store.TwoFactorMethodEmail
	return u
}

// wrongCode is a 6-digit code that is not code.
func wrongCode(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

func (f *email2FAFixture) inbox(uid uuid.UUID, kind string) []store.LoginAnomaly {
	var out []store.LoginAnomaly
	for _, a := range f.st.anomalies {
		if a.UserID == uid && a.AnomalyType == kind {
			out = append(out, a)
		}
	}
	return out
}

// The founder's security reason: a token alone must not change 2FA. The
// password is checked before anything is mailed.
func TestEmail2FAStartNeedsTheCorrectPassword(t *testing.T) {
	f := newEmail2FAFixture(t)
	u := f.consumer(t)
	ctx := context.Background()

	if _, err := f.svc.StartEmail2FA(ctx, u.ID, "wrong-password", TwoFAPurposeEnable); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v, want ErrInvalidCredentials", err)
	}
	if len(f.mail.msgs) != 0 {
		t.Fatal("a code was mailed for a wrong password")
	}

	ch, err := f.svc.StartEmail2FA(ctx, u.ID, adminTestPassword, TwoFAPurposeEnable)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if ch.ChallengeToken == "" || !ch.ExpiresAt.After(time.Now()) {
		t.Fatalf("challenge = %+v", ch)
	}
	if want := MaskEmail(*u.Email); ch.EmailMasked != want || !strings.Contains(ch.EmailMasked, "***@") {
		t.Fatalf("email_masked = %q, want %q", ch.EmailMasked, want)
	}
	if _, subject := f.mail.lastCode(t); subject != "Confirm two-step verification" {
		t.Fatalf("subject = %q", subject)
	}
	if f.mail.msgs[0].To != *u.Email {
		t.Fatal("code went to another address")
	}
}

func TestEmail2FAStartRefusals(t *testing.T) {
	f := newEmail2FAFixture(t)
	ctx := context.Background()

	unverified := f.addUser(t, false)
	if _, err := f.svc.StartEmail2FA(ctx, unverified.ID, adminTestPassword, TwoFAPurposeEnable); !errors.Is(err, ErrTwoFactorEmailUnverified) {
		t.Fatalf("unverified email: %v", err)
	}
	on := f.withEmail2FA(t)
	if _, err := f.svc.StartEmail2FA(ctx, on.ID, adminTestPassword, TwoFAPurposeEnable); !errors.Is(err, ErrTwoFactorAlreadyEnabled) {
		t.Fatalf("enable while on: %v", err)
	}
	off := f.consumer(t)
	if _, err := f.svc.StartEmail2FA(ctx, off.ID, adminTestPassword, TwoFAPurposeDisable); !errors.Is(err, ErrTwoFactorNotEnabled) {
		t.Fatalf("disable while off: %v", err)
	}
	// A TOTP account must not be downgraded by an emailed code.
	legacy := f.addUser(t, true)
	legacy.EmailVerified = true
	if _, err := f.svc.StartEmail2FA(ctx, legacy.ID, adminTestPassword, TwoFAPurposeDisable); !errors.Is(err, ErrTwoFactorMethodMismatch) {
		t.Fatalf("email disable of a TOTP account: %v", err)
	}
	if _, err := f.svc.StartEmail2FA(ctx, off.ID, adminTestPassword, "sms"); !errors.Is(err, ErrInvalidTwoFAPurpose) {
		t.Fatalf("bad purpose: %v", err)
	}
	if len(f.mail.msgs) != 0 {
		t.Fatalf("%d codes mailed for refused starts", len(f.mail.msgs))
	}
}

// Wrong codes are refused and capped at OTP_MAX_ATTEMPTS: past the cap the
// right code is dead too. A fresh challenge (after the cooldown) works.
func TestEmail2FAEnableRightAndWrongCode(t *testing.T) {
	f := newEmail2FAFixture(t)
	u := f.consumer(t)
	ctx := context.Background()

	ch, err := f.svc.StartEmail2FA(ctx, u.ID, adminTestPassword, TwoFAPurposeEnable)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := f.mail.lastCode(t)
	for i := 0; i < f.svc.otpMaxAttempts(); i++ {
		if err := f.svc.EnableEmail2FA(ctx, u.ID, ch.ChallengeToken, wrongCode(code)); !errors.Is(err, ErrVerifyCodeInvalid) {
			t.Fatalf("wrong code %d: %v, want ErrVerifyCodeInvalid", i+1, err)
		}
	}
	if err := f.svc.EnableEmail2FA(ctx, u.ID, ch.ChallengeToken, code); !errors.Is(err, ErrVerifyCodeInvalid) {
		t.Fatalf("right code after the cap: %v, want refused", err)
	}
	if u.TwoFactorEnabled {
		t.Fatal("2FA turned on by a capped code")
	}

	// Another start inside the cooldown is refused, after it works.
	if _, err := f.svc.StartEmail2FA(ctx, u.ID, adminTestPassword, TwoFAPurposeEnable); err == nil {
		t.Fatal("second code inside the 60 s cooldown")
	} else if _, ok := AsThrottled(err); !ok {
		t.Fatalf("cooldown: %v, want throttled", err)
	}
	f.mr.FastForward(EmailCodeResendCooldown + time.Second)
	ch, err = f.svc.StartEmail2FA(ctx, u.ID, adminTestPassword, TwoFAPurposeEnable)
	if err != nil {
		t.Fatal(err)
	}
	code, _ = f.mail.lastCode(t)

	// Someone else's session cannot use this challenge.
	other := f.consumer(t)
	if err := f.svc.EnableEmail2FA(ctx, other.ID, ch.ChallengeToken, code); !errors.Is(err, ErrEmailChallengeInvalid) {
		t.Fatalf("foreign challenge: %v", err)
	}

	if err := f.svc.EnableEmail2FA(ctx, u.ID, ch.ChallengeToken, code); err != nil {
		t.Fatalf("right code: %v", err)
	}
	if !u.TwoFactorEnabled || u.SecondFactorMethod() != store.TwoFactorMethodEmail {
		t.Fatalf("after enable: enabled=%v method=%q", u.TwoFactorEnabled, u.TwoFactorMethod)
	}
	if got := f.inbox(u.ID, "two_factor_enabled"); len(got) != 1 || got[0].Metadata["method"] != "email" {
		t.Fatalf("security inbox = %+v", got)
	}
	// The challenge is spent.
	if err := f.svc.EnableEmail2FA(ctx, u.ID, ch.ChallengeToken, code); err == nil {
		t.Fatal("challenge replayed")
	}
}

// Sign-in of an email-2FA account: requires_2fa with method email and the
// masked address, the code mailed at once, /2fa/verify takes it.
func TestEmail2FALoginReturnsMethodEmailAndVerifyAcceptsTheCode(t *testing.T) {
	f := newEmail2FAFixture(t)
	u := f.withEmail2FA(t)
	ctx := context.Background()

	resp, err := f.svc.LoginWithPassword(ctx, *u.Email, adminTestPassword, "dev", "android", "10.0.0.5", "okhttp")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !resp.Requires2FA || resp.PendingToken == "" || resp.Tokens.AccessToken != "" {
		t.Fatalf("login = %+v, want a pending 2FA envelope", resp)
	}
	if resp.TwoFactorMethod != "email" || resp.EmailMasked != MaskEmail(*u.Email) {
		t.Fatalf("method=%q masked=%q", resp.TwoFactorMethod, resp.EmailMasked)
	}
	code, subject := f.mail.lastCode(t)
	if subject != "Your sign-in code" {
		t.Fatalf("subject = %q", subject)
	}

	if _, err := f.svc.Verify2FA(ctx, u.ID, wrongCode(code), resp.PendingToken); err == nil {
		t.Fatal("wrong emailed code signed in")
	}
	done, err := f.svc.Verify2FA(ctx, u.ID, code, resp.PendingToken)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if done.Tokens.AccessToken == "" {
		t.Fatal("no session after the right code")
	}
	c := parseClaims(t, done.Tokens.AccessToken)
	if !hasAMR(c.AMR, AMREmail) || hasAMR(c.AMR, AMROTP) || c.AdminMFA {
		t.Fatalf("amr=%v admin_mfa=%v: an emailed code must never count as otp", c.AMR, c.AdminMFA)
	}
	// The code is single-use and the pending token is spent.
	if _, err := f.svc.Verify2FA(ctx, u.ID, code, resp.PendingToken); err == nil {
		t.Fatal("pending token replayed")
	}
}

// A legacy consumer enrolled in TOTP keeps signing in with it and is told so.
func TestLegacyTOTPConsumerKeepsTOTP(t *testing.T) {
	f := newEmail2FAFixture(t)
	u := f.addUser(t, true) // TOTP, no admin role
	ctx := context.Background()

	resp, err := f.svc.LoginWithPassword(ctx, *u.Email, adminTestPassword, "dev", "android", "10.0.0.5", "okhttp")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Requires2FA || resp.TwoFactorMethod != "totp" || resp.EmailMasked != "" {
		t.Fatalf("login = %+v, want requires_2fa with method totp", resp)
	}
	if len(f.mail.msgs) != 0 {
		t.Fatal("a TOTP sign-in mailed a code")
	}
	if _, err := f.svc.Resend2FACode(ctx, resp.PendingToken); !errors.Is(err, ErrTwoFactorMethodMismatch) {
		t.Fatalf("resend for a TOTP sign-in: %v, want ErrTwoFactorMethodMismatch", err)
	}
	if _, err := f.svc.Verify2FA(ctx, u.ID, f.code(t, u, time.Now()), resp.PendingToken); err != nil {
		t.Fatalf("TOTP verify: %v", err)
	}
	if _, err := f.svc.Resend2FACode(ctx, resp.PendingToken); err == nil {
		t.Fatal("resend for a spent pending token")
	}
}

func TestEmail2FAResendCooldown(t *testing.T) {
	f := newEmail2FAFixture(t)
	u := f.withEmail2FA(t)
	ctx := context.Background()

	resp, err := f.svc.LoginWithPassword(ctx, *u.Email, adminTestPassword, "dev", "android", "10.0.0.5", "okhttp")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := f.mail.lastCode(t)

	_, err = f.svc.Resend2FACode(ctx, resp.PendingToken)
	th, ok := AsThrottled(err)
	if !ok || th.RetryAfter <= 0 || th.RetryAfter > EmailCodeResendCooldown {
		t.Fatalf("resend inside the cooldown: %v", err)
	}
	if len(f.mail.msgs) != 1 {
		t.Fatal("a refused resend mailed a code")
	}

	f.mr.FastForward(EmailCodeResendCooldown + time.Second)
	sent, err := f.svc.Resend2FACode(ctx, resp.PendingToken)
	if err != nil {
		t.Fatalf("resend after the cooldown: %v", err)
	}
	if !sent.Sent || sent.ResendAfterSeconds != 60 || sent.EmailMasked != MaskEmail(*u.Email) {
		t.Fatalf("resend = %+v", sent)
	}
	second, _ := f.mail.lastCode(t)
	if len(f.mail.msgs) != 2 {
		t.Fatalf("%d emails, want 2", len(f.mail.msgs))
	}
	if first != second {
		// The resend replaced the live code.
		if _, err := f.svc.Verify2FA(ctx, u.ID, first, resp.PendingToken); err == nil {
			t.Fatal("the replaced code still signs in")
		}
	}
	if _, err := f.svc.Verify2FA(ctx, u.ID, second, resp.PendingToken); err != nil {
		t.Fatalf("resent code: %v", err)
	}

	if _, err := f.svc.Resend2FACode(ctx, "not-a-pending-token"); !errors.Is(err, ErrPending2FAInvalid) {
		t.Fatalf("unknown pending token: %v", err)
	}
}

// The per-account resend caps still apply after the cooldown.
func TestEmail2FAResendQuota(t *testing.T) {
	f := newEmail2FAFixture(t)
	u := f.withEmail2FA(t)
	ctx := context.Background()
	resp, err := f.svc.LoginWithPassword(ctx, *u.Email, adminTestPassword, "dev", "android", "10.0.0.5", "okhttp")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(resendPerHour); i++ {
		f.mr.FastForward(EmailCodeResendCooldown + time.Second)
		if _, err := f.svc.Resend2FACode(ctx, resp.PendingToken); err != nil {
			t.Fatalf("resend %d: %v", i+1, err)
		}
	}
	f.mr.FastForward(EmailCodeResendCooldown + time.Second)
	if _, err := f.svc.Resend2FACode(ctx, resp.PendingToken); err == nil {
		t.Fatal("resend past the hourly cap")
	}
}

func TestTOTPSetupRefusedForNonAdminAllowedForAdmin(t *testing.T) {
	f := newEmail2FAFixture(t)
	f.svc.cfg.TwoFAIssuer = "Momentum-test"
	ctx := context.Background()

	consumer := f.consumer(t)
	if _, err := f.svc.Setup2FA(ctx, consumer.ID); !errors.Is(err, ErrTOTPNotOffered) {
		t.Fatalf("consumer setup: %v, want ErrTOTPNotOffered", err)
	}
	if err := f.svc.Verify2FASetup(ctx, consumer.ID, "123456"); !errors.Is(err, ErrTOTPNotOffered) {
		t.Fatalf("consumer verify-setup: %v, want ErrTOTPNotOffered", err)
	}

	admin := f.consumer(t)
	f.st.platformRoles[admin.ID] = []string{"admin"}
	setup, err := f.svc.Setup2FA(ctx, admin.ID)
	if err != nil {
		t.Fatalf("admin setup: %v", err)
	}
	if setup.Secret == "" || len(setup.RecoveryCodes) == 0 {
		t.Fatalf("setup = %+v", setup)
	}
	f.st.secrets[admin.ID] = setup.Secret
	if err := f.svc.Verify2FASetup(ctx, admin.ID, f.code(t, admin, time.Now())); err != nil {
		t.Fatalf("admin verify-setup: %v", err)
	}
	if !admin.HasTOTP() {
		t.Fatal("admin has no TOTP after setup")
	}
	if got := f.inbox(admin.ID, "two_factor_enabled"); len(got) != 1 || got[0].Metadata["method"] != "totp" {
		t.Fatalf("security inbox = %+v", got)
	}
}

// signIn completes an email-2FA sign-in and returns the session.
func (f *email2FAFixture) signIn(t *testing.T, u *store.User) *AuthResponse {
	t.Helper()
	ctx := context.Background()
	resp, err := f.svc.LoginWithPassword(ctx, *u.Email, adminTestPassword, "dev", "android", "10.0.0.5", "okhttp")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := f.mail.lastCode(t)
	done, err := f.svc.Verify2FA(ctx, u.ID, code, resp.PendingToken)
	if err != nil {
		t.Fatal(err)
	}
	return done
}

func TestEmailStepUpIssuesStepUpAt(t *testing.T) {
	f := newEmail2FAFixture(t)
	u := f.withEmail2FA(t)
	ctx := context.Background()
	sess := f.signIn(t, u)
	f.mr.FastForward(EmailCodeResendCooldown + time.Second)

	// The TOTP step-up has nothing to check for this account.
	if _, err := f.svc.StepUp(ctx, u.ID, sess.SessionID, "123456"); !errors.Is(err, ErrTOTPNotEnrolled) {
		t.Fatalf("TOTP step-up for an email account: %v", err)
	}

	ch, err := f.svc.StartStepUpEmail(ctx, u.ID, sess.SessionID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	code, subject := f.mail.lastCode(t)
	if subject != "Confirm it's you" || ch.EmailMasked != MaskEmail(*u.Email) {
		t.Fatalf("subject=%q masked=%q", subject, ch.EmailMasked)
	}

	// Bound to the session that asked.
	other := f.signIn(t, u)
	if _, err := f.svc.StepUpEmail(ctx, u.ID, other.SessionID, ch.ChallengeToken, code); !errors.Is(err, ErrEmailChallengeInvalid) {
		t.Fatalf("challenge on another session: %v", err)
	}
	if _, err := f.svc.StepUpEmail(ctx, u.ID, sess.SessionID, ch.ChallengeToken, wrongCode(code)); !errors.Is(err, ErrVerifyCodeInvalid) {
		t.Fatalf("wrong code: %v", err)
	}

	before := time.Now().Add(-time.Second)
	up, err := f.svc.StepUpEmail(ctx, u.ID, sess.SessionID, ch.ChallengeToken, code)
	if err != nil {
		t.Fatalf("step-up: %v", err)
	}
	if up.StepUpAt.Before(before) || up.AdminMFA || up.AccessToken == "" {
		t.Fatalf("step-up = %+v", up)
	}
	c := parseClaims(t, up.AccessToken)
	if c.StepUpAt == 0 || c.AdminMFA || hasAMR(c.AMR, AMROTP) {
		t.Fatalf("claims step_up_at=%d admin_mfa=%v amr=%v", c.StepUpAt, c.AdminMFA, c.AMR)
	}

	// A TOTP account cannot start an email step-up.
	legacy := f.addUser(t, true)
	legacy.EmailVerified = true
	lsess := f.login(t, legacy)
	if _, err := f.svc.StartStepUpEmail(ctx, legacy.ID, lsess.SessionID); !errors.Is(err, ErrTwoFactorMethodMismatch) {
		t.Fatalf("email step-up for TOTP account: %v", err)
	}
}

func TestEmail2FADisableWithChallenge(t *testing.T) {
	f := newEmail2FAFixture(t)
	u := f.withEmail2FA(t)
	ctx := context.Background()

	// The TOTP disable path refuses an email account.
	if err := f.svc.Disable2FA(ctx, u.ID, adminTestPassword, "123456"); !errors.Is(err, ErrTwoFactorMethodMismatch) {
		t.Fatalf("TOTP disable of an email account: %v", err)
	}

	ch, err := f.svc.StartEmail2FA(ctx, u.ID, adminTestPassword, TwoFAPurposeDisable)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := f.mail.lastCode(t)
	// An enable challenge (or any other purpose) cannot be used to disable.
	if err := f.svc.EnableEmail2FA(ctx, u.ID, ch.ChallengeToken, code); !errors.Is(err, ErrEmailChallengeInvalid) {
		t.Fatalf("disable challenge used for enable: %v", err)
	}
	if err := f.svc.DisableEmail2FA(ctx, u.ID, ch.ChallengeToken, wrongCode(code)); !errors.Is(err, ErrVerifyCodeInvalid) {
		t.Fatalf("wrong code: %v", err)
	}
	if !u.TwoFactorEnabled {
		t.Fatal("disabled by a wrong code")
	}
	if err := f.svc.DisableEmail2FA(ctx, u.ID, ch.ChallengeToken, code); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if u.TwoFactorEnabled || u.SecondFactorMethod() != "" {
		t.Fatalf("after disable: enabled=%v method=%q", u.TwoFactorEnabled, u.TwoFactorMethod)
	}
	if got := f.inbox(u.ID, "two_factor_disabled"); len(got) != 1 {
		t.Fatalf("security inbox = %+v", got)
	}

	// With 2FA off the account signs in with the password alone.
	resp, err := f.svc.LoginWithPassword(ctx, *u.Email, adminTestPassword, "dev", "android", "10.0.0.5", "okhttp")
	if err != nil || resp.Requires2FA || resp.Tokens.AccessToken == "" {
		t.Fatalf("login after disable: %+v, %v", resp, err)
	}
}

// A sign-in code is bound to sign-in: it cannot enable 2FA, and an enable
// code cannot finish a sign-in.
func TestEmailCodesAreBoundToTheirPurpose(t *testing.T) {
	f := newEmail2FAFixture(t)
	u := f.consumer(t)
	ctx := context.Background()

	ch, err := f.svc.StartEmail2FA(ctx, u.ID, adminTestPassword, TwoFAPurposeEnable)
	if err != nil {
		t.Fatal(err)
	}
	enableCode, _ := f.mail.lastCode(t)
	if err := f.svc.EnableEmail2FA(ctx, u.ID, ch.ChallengeToken, enableCode); err != nil {
		t.Fatal(err)
	}

	resp, err := f.svc.LoginWithPassword(ctx, *u.Email, adminTestPassword, "dev", "android", "10.0.0.5", "okhttp")
	if err != nil {
		t.Fatal(err)
	}
	loginCode, _ := f.mail.lastCode(t)
	if loginCode != enableCode {
		if _, err := f.svc.Verify2FA(ctx, u.ID, enableCode, resp.PendingToken); err == nil {
			t.Fatal("the enable code finished a sign-in")
		}
	}
	if _, ok := f.es.otps[emailOTPKey(u.ID)+"|"+emailOTPPurpose(emailChallengeEnable)]; ok {
		t.Fatal("the enable code is still live after use")
	}
}

// Early resends are refused by the cooldown without spending the hourly
// resend quota, so the user is not locked out by impatient tapping.
func TestEmail2FAEarlyResendCostsNoQuota(t *testing.T) {
	f := newEmail2FAFixture(t)
	u := f.withEmail2FA(t)
	ctx := context.Background()
	resp, err := f.svc.LoginWithPassword(ctx, *u.Email, adminTestPassword, "dev", "android", "10.0.0.5", "okhttp")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(resendPerHour)+2; i++ {
		if _, err := f.svc.Resend2FACode(ctx, resp.PendingToken); err == nil {
			t.Fatalf("early resend %d sent", i+1)
		}
	}
	f.mr.FastForward(EmailCodeResendCooldown + time.Second)
	if _, err := f.svc.Resend2FACode(ctx, resp.PendingToken); err != nil {
		t.Fatalf("resend after the cooldown: %v (early taps spent the quota)", err)
	}
}

// "email" in amr satisfies the 2FA gate only for an email-2FA account: it
// can never stand in for a TOTP account's authenticator.
func TestEmailAMRDoesNotSatisfyTOTPGate(t *testing.T) {
	f := newEmail2FAFixture(t)
	u := f.addUser(t, true)
	resp, err := f.svc.createSessionForUser(context.Background(), u, []string{AMRPassword, AMREmail}, "dev", "android", "10.0.0.5", "okhttp")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Requires2FA || resp.Tokens.AccessToken != "" {
		t.Fatalf("TOTP account got a session from an email amr: %+v", resp)
	}
}
