package service

// Regression tests for the registration / verification-code / rate-limit
// audit (audit-auth-registration H1-H3, M1, M2, M4; audit-auth-login H1, H4,
// M1, M4, M9). Each test names the finding it pins.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

// ── Identifiers (H2, M1) ────────────────────────────────────────────────────

func TestNormalizeEmail(t *testing.T) {
	ok := map[string]string{
		"alice@example.com":       "alice@example.com",
		"  Alice@Example.COM \t":  "alice@example.com",
		"a.b+tag@sub.example.org": "a.b+tag@sub.example.org",
	}
	for in, want := range ok {
		got, err := NormalizeEmail(in)
		if err != nil || got != want {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "   ", "foo", "foo@", "@example.com", "a@b", "a@@b.com", "a@b@c.com",
		"Bob <bob@example.com>", "bob@example.com.", "bob@.example.com",
		strings.Repeat("a", 250) + "@example.com",
	}
	for _, in := range bad {
		if got, err := NormalizeEmail(in); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want ErrInvalidEmail", in, got, err)
		}
	}
}

func TestNormalizePhoneE164(t *testing.T) {
	ok := map[string]string{
		"+919876543210":        "+919876543210",
		" +91 98765-43210 ":    "+919876543210",
		"+1 (415) 555.0100":    "+14155550100",
		"+4420":                "",
		"+123456789012345":     "+123456789012345",
		"+1234567890123456":    "",
		"9876543210":           "",
		"victim@gmail.com":     "",
		"+91abc6543210":        "",
		"+0919876543210":       "",
		"":                     "",
		"++919876543210":       "",
		"+91 9876 5432 10 ext": "",
	}
	for in, want := range ok {
		got, err := NormalizePhoneE164(in)
		if want == "" {
			if !errors.Is(err, ErrInvalidPhone) {
				t.Errorf("NormalizePhoneE164(%q) = %q, %v; want ErrInvalidPhone", in, got, err)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("NormalizePhoneE164(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// phoneSquatStore answers EVERY phone lookup with the attacker's account, the
// way the phone column did once an attacker had registered the victim's email
// address as their "phone".
type phoneSquatStore struct {
	*fakeAnomalyStore
	attacker    *store.User
	phoneLookup []string
	emailLookup []string
}

func (p *phoneSquatStore) GetUserByPhone(_ context.Context, phone string) (*store.User, error) {
	p.phoneLookup = append(p.phoneLookup, phone)
	return p.attacker, nil
}

func (p *phoneSquatStore) GetUserByEmail(ctx context.Context, email string) (*store.User, error) {
	p.emailLookup = append(p.emailLookup, email)
	return p.fakeAnomalyStore.GetUserByEmail(ctx, email)
}

func newPhoneSquat(t *testing.T) (*Service, *phoneSquatStore, *store.User) {
	t.Helper()
	svc, f, _ := newTestService(t, "shadow")
	hash, _ := bcrypt.GenerateFromPassword([]byte(lifecycleTestPassword), 4)
	victimEmail := "victim@example.com"
	victim := &store.User{ID: uuid.New(), Email: &victimEmail, PasswordHash: string(hash),
		AccountStatus: store.AccountStatusActive}
	attackerEmail := "attacker@example.com"
	attacker := &store.User{ID: uuid.New(), Email: &attackerEmail, PasswordHash: "x",
		Phone: "victim@example.com", AccountStatus: store.AccountStatusActive}
	f.users = map[uuid.UUID]*store.User{victim.ID: victim, attacker.ID: attacker}
	p := &phoneSquatStore{fakeAnomalyStore: f, attacker: attacker}
	svc.store = p
	return svc, p, victim
}

// H2: an email-shaped identifier is never matched against the phone column,
// and is normalised before the email lookup.
func TestLookupEmailShapedIdentifierNeverTriesPhone(t *testing.T) {
	svc, p, victim := newPhoneSquat(t)
	u, err := svc.lookupUserByIdentifier(context.Background(), "  Victim@Example.com ")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.phoneLookup) != 0 {
		t.Fatalf("email-shaped identifier was looked up in the phone column: %v", p.phoneLookup)
	}
	if u == nil || u.ID != victim.ID {
		t.Fatalf("resolved to %v, want the victim", u)
	}
	if len(p.emailLookup) != 1 || p.emailLookup[0] != "victim@example.com" {
		t.Fatalf("email lookup used %v, want the normalised address", p.emailLookup)
	}
}

func TestLookupPhoneIdentifierUsesE164AndNeverEmail(t *testing.T) {
	svc, p, _ := newPhoneSquat(t)
	if _, err := svc.lookupUserByIdentifier(context.Background(), "+91 98765 43210"); err != nil {
		t.Fatal(err)
	}
	if len(p.emailLookup) != 0 {
		t.Fatalf("phone identifier was looked up in the email column: %v", p.emailLookup)
	}
	if len(p.phoneLookup) != 1 || p.phoneLookup[0] != "+919876543210" {
		t.Fatalf("phone lookup used %v, want the E.164 form", p.phoneLookup)
	}
}

// H2 scenario A end to end through LoginWithPassword: the victim signs in with
// their own email and correct password and must reach THEIR account.
func TestLoginWithEmailIsNotHijackedByPhoneSquat(t *testing.T) {
	svc, p, _ := newPhoneSquat(t)
	_, err := svc.LoginWithPassword(context.Background(), "victim@example.com",
		lifecycleTestPassword, "dev", "android", "1.2.3.4", "ua")
	if err != nil && strings.Contains(err.Error(), "invalid credentials") {
		t.Fatalf("victim's own email+password was refused (resolved to the squatter): %v", err)
	}
	if len(p.phoneLookup) != 0 {
		t.Fatalf("login looked the email up in the phone column: %v", p.phoneLookup)
	}
}

// ── Email verification cap (H1) ─────────────────────────────────────────────

// otpStore holds one live code the way auth.otp_codes does.
type otpStore struct {
	*fakeAnomalyStore
	uid       uuid.UUID
	otp       *store.OTP
	activated bool
	incErr    error
	jobsShut  int
}

func (p *otpStore) GetOTP(_ context.Context, _, _ string) (*store.OTP, error) {
	if p.otp == nil {
		return nil, nil
	}
	cp := *p.otp
	return &cp, nil
}
func (p *otpStore) IncrementOTPAttempts(_ context.Context, _ uuid.UUID) (int, error) {
	if p.incErr != nil {
		return 0, p.incErr
	}
	p.otp.Attempts++
	return p.otp.Attempts, nil
}
func (p *otpStore) DeleteOTP(_ context.Context, _ uuid.UUID) error { p.otp = nil; return nil }
func (p *otpStore) LookupVerificationTransaction(_ context.Context, _, _ string) (uuid.UUID, error) {
	return p.uid, nil
}
func (p *otpStore) ConsumeVerificationTransaction(_ context.Context, _, _ string) (uuid.UUID, error) {
	return p.uid, nil
}
func (p *otpStore) ActivateVerifiedAccount(_ context.Context, _ uuid.UUID) error {
	p.activated = true
	return nil
}
func (p *otpStore) MarkUserEmailJobsSent(_ context.Context, _ uuid.UUID) error {
	p.jobsShut++
	return nil
}

// issue stores a fresh code with attempts=0, which is what SaveOTP does on a
// resend.
func (p *otpStore) issue(t *testing.T, code string) {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(code), 4)
	p.otp = &store.OTP{ID: uuid.New(), Hash: string(hash), ExpiresAt: time.Now().Add(5 * time.Minute)}
}

func newOTPFixture(t *testing.T) (*Service, *otpStore, *miniredis.Miniredis) {
	t.Helper()
	svc, f, mr := newTestService(t, "shadow")
	uid := uuid.New()
	email := "pending@example.com"
	f.users = map[uuid.UUID]*store.User{uid: {ID: uid, Email: &email, AccountStatus: "pending_verification"}}
	p := &otpStore{fakeAnomalyStore: f, uid: uid}
	svc.store = p
	return svc, p, mr
}

// The audit probe, made a regression test: 50 wrong guesses used to leave the
// code live and the correct code still activated the account.
func TestEmailVerifyCodeIsDeletedAtTheAttemptCap(t *testing.T) {
	svc, p, _ := newOTPFixture(t)
	p.issue(t, "424242")
	ctx := context.Background()

	for i := 0; i < svc.otpMaxAttempts(); i++ {
		if err := svc.VerifyEmailWithTransaction(ctx, "tok", "000000"); err == nil {
			t.Fatalf("wrong code %d accepted", i+1)
		}
	}
	if p.otp != nil {
		t.Fatalf("code still live after %d wrong guesses (attempts=%d)", svc.otpMaxAttempts(), p.otp.Attempts)
	}
	if err := svc.VerifyEmailWithTransaction(ctx, "tok", "424242"); err == nil || p.activated {
		t.Fatal("the correct code activated the account after the cap was reached")
	}
}

// A code row that already reached the cap (left by an older build) is refused
// and deleted without comparing.
func TestEmailVerifyRefusesASpentCode(t *testing.T) {
	svc, p, _ := newOTPFixture(t)
	p.issue(t, "424242")
	p.otp.Attempts = svc.otpMaxAttempts()
	if err := svc.VerifyEmailWithTransaction(context.Background(), "tok", "424242"); err == nil {
		t.Fatal("a code at the attempt cap was accepted")
	}
	if p.otp != nil || p.activated {
		t.Fatal("spent code not deleted, or account activated")
	}
}

// A failure to count a wrong guess refuses rather than allowing an uncounted
// guess.
func TestEmailVerifyCountFailureRefuses(t *testing.T) {
	svc, p, _ := newOTPFixture(t)
	p.issue(t, "424242")
	p.incErr = errors.New("db down")
	err := svc.VerifyEmailWithTransaction(context.Background(), "tok", "000000")
	if err == nil || !strings.Contains(err.Error(), "count verification attempt") {
		t.Fatalf("got %v, want a refusal naming the count failure", err)
	}
}

// H1: a resend stores a fresh code with attempts=0, so the per-code cap alone
// would hand out OTP_MAX_ATTEMPTS more guesses per resend. The per-account
// budget must keep counting across resends and refuse even the right code.
func TestEmailVerifyPerAccountCapSurvivesResend(t *testing.T) {
	svc, p, _ := newOTPFixture(t)
	ctx := context.Background()
	guesses := 0
	for guesses < int(emailVerifyPerHour) {
		p.issue(t, "424242") // a resend: fresh code, attempts=0
		for i := 0; i < svc.otpMaxAttempts()-1 && guesses < int(emailVerifyPerHour); i++ {
			if err := svc.VerifyEmailWithTransaction(ctx, "tok", "000000"); err == nil {
				t.Fatal("wrong code accepted")
			}
			guesses++
		}
	}
	p.issue(t, "424242")
	err := svc.VerifyEmailWithTransaction(ctx, "tok", "424242")
	if _, ok := AsThrottled(err); !ok {
		t.Fatalf("after %d guesses across resends the correct code got %v; want throttled", guesses, err)
	}
	if p.activated {
		t.Fatal("account activated past the per-account attempt budget")
	}
}

// Within budget the flow still works, and activation closes queued jobs (M2).
func TestEmailVerifySucceedsAndClosesQueuedJobs(t *testing.T) {
	svc, p, _ := newOTPFixture(t)
	p.issue(t, "424242")
	_ = svc.VerifyEmailWithTransaction(context.Background(), "tok", "000000")
	if err := svc.VerifyEmailWithTransaction(context.Background(), "tok", "424242"); err != nil {
		t.Fatalf("correct code refused: %v", err)
	}
	if !p.activated {
		t.Fatal("not activated")
	}
	if p.jobsShut == 0 {
		t.Fatal("activation left the queued verification job open; the relay would mail another code")
	}
}

// M4: RequestEmailVerification reports "already verified" and "undeliverable"
// as sentinels the relay can act on.
func TestRequestEmailVerificationSentinels(t *testing.T) {
	svc, f, _ := newTestService(t, "shadow")
	verified, junk := uuid.New(), uuid.New()
	e1, e2 := "done@example.com", "foo"
	f.users = map[uuid.UUID]*store.User{
		verified: {ID: verified, Email: &e1, EmailVerified: true},
		junk:     {ID: junk, Email: &e2},
	}
	if err := svc.RequestEmailVerification(context.Background(), verified); !errors.Is(err, ErrEmailAlreadyVerified) {
		t.Fatalf("verified account: %v, want ErrEmailAlreadyVerified", err)
	}
	if err := svc.RequestEmailVerification(context.Background(), junk); !errors.Is(err, ErrEmailUndeliverable) {
		t.Fatalf("junk address: %v, want ErrEmailUndeliverable", err)
	}
	if err := svc.RequestEmailVerification(context.Background(), uuid.New()); !errors.Is(err, ErrEmailUndeliverable) {
		t.Fatalf("missing account: %v, want ErrEmailUndeliverable", err)
	}
}

// ── Password reset (H3 / login H1, login M4) ────────────────────────────────

type resetStore struct {
	*fakeAnomalyStore
	calls       int
	maxAttempts int
	err         error
}

func (r *resetStore) ConsumeRecoveryAndSetPassword(_ context.Context, _ uuid.UUID, _, _ string, _ func(string) bool, _ string, maxAttempts int) error {
	r.calls++
	r.maxAttempts = maxAttempts
	return r.err
}

func newResetFixture(t *testing.T) (*Service, *resetStore) {
	t.Helper()
	svc, f, _ := newTestService(t, "shadow")
	email := "owner@example.com"
	uid := uuid.New()
	f.users = map[uuid.UUID]*store.User{uid: {ID: uid, Email: &email, AccountStatus: store.AccountStatusActive}}
	r := &resetStore{fakeAnomalyStore: f}
	svc.store = r
	return svc, r
}

// The store is given the configured cap, so wrong codes are counted and the
// code deleted under the row lock (store side proved in CI integration).
func TestResetPasswordPassesTheAttemptCapToTheStore(t *testing.T) {
	svc, r := newResetFixture(t)
	r.err = store.ErrRecoveryCodeInvalid
	err := svc.ResetPassword(context.Background(), "Owner@Example.com", "000000", "N3w!password")
	if !errors.Is(err, ErrResetCodeInvalid) {
		t.Fatalf("got %v, want ErrResetCodeInvalid", err)
	}
	if r.calls != 1 || r.maxAttempts != svc.otpMaxAttempts() {
		t.Fatalf("store called %d times with maxAttempts=%d, want 1 call with %d",
			r.calls, r.maxAttempts, svc.otpMaxAttempts())
	}
}

// M4 (login audit): a weak password is reported the same way whether or not
// the account exists, and an unknown account gets exactly the wrong-code error.
func TestResetPasswordDoesNotRevealAccountExistence(t *testing.T) {
	svc, r := newResetFixture(t)
	ctx := context.Background()

	for _, id := range []string{"owner@example.com", "nobody@example.com", "+919876543210"} {
		if err := svc.ResetPassword(ctx, id, "123456", "weak"); !errors.Is(err, ErrPasswordTooShort) {
			t.Errorf("weak password for %q: %v, want ErrPasswordTooShort regardless of existence", id, err)
		}
	}
	if r.calls != 0 {
		t.Fatal("a weak password reached the store")
	}

	r.err = store.ErrRecoveryCodeInvalid
	known := svc.ResetPassword(ctx, "owner@example.com", "000000", "N3w!password")
	unknown := svc.ResetPassword(ctx, "nobody@example.com", "000000", "N3w!password")
	if known == nil || unknown == nil || known.Error() != unknown.Error() ||
		!errors.Is(unknown, ErrResetCodeInvalid) {
		t.Fatalf("known=%v unknown=%v: the two must be the same error", known, unknown)
	}
}

// ── 2FA verify and password re-checks (login H4, M9) ────────────────────────

func TestVerify2FAPerAccountCap(t *testing.T) {
	f := newAdminFixture(t)
	u := f.addUser(t, true)
	ctx := context.Background()

	var last error
	for i := 0; i <= int(twoFAVerifyLimit); i++ {
		// A fresh pending token each time: holding the password mints them
		// freely, so a per-token cap alone is not enough.
		tok, err := f.svc.StorePending2FASession(ctx, u.ID, "d", "web", "1.2.3.4", "ua", AMRPassword)
		if err != nil {
			t.Fatal(err)
		}
		_, last = f.svc.Verify2FA(ctx, u.ID, "000000", tok)
	}
	if _, ok := AsThrottled(last); !ok {
		t.Fatalf("attempt %d with fresh pending tokens got %v; want throttled", twoFAVerifyLimit+1, last)
	}
	tok, _ := f.svc.StorePending2FASession(ctx, u.ID, "d", "web", "1.2.3.4", "ua", AMRPassword)
	if _, err := f.svc.Verify2FA(ctx, u.ID, f.code(t, u, time.Now()), tok); err == nil {
		t.Fatal("the correct TOTP signed in past the per-account cap")
	}
}

// The cap is charged only after the pending token resolves to the account:
// someone who merely knows a user id cannot spend it.
func TestVerify2FAUnknownPendingTokenDoesNotSpendBudget(t *testing.T) {
	f := newAdminFixture(t)
	u := f.addUser(t, true)
	ctx := context.Background()
	for i := 0; i < int(twoFAVerifyLimit)+5; i++ {
		_, _ = f.svc.Verify2FA(ctx, u.ID, "000000", "not-a-token")
	}
	tok, _ := f.svc.StorePending2FASession(ctx, u.ID, "d", "web", "1.2.3.4", "ua", AMRPassword)
	_, err := f.svc.Verify2FA(ctx, u.ID, "000000", tok)
	if _, ok := AsThrottled(err); ok {
		t.Fatal("garbage pending tokens spent the account's 2FA budget")
	}
}

// A pending token is burned after twoFAPendingFailLimit wrong codes.
func TestVerify2FABurnsPendingTokenAfterFailures(t *testing.T) {
	f := newAdminFixture(t)
	u := f.addUser(t, true)
	ctx := context.Background()
	tok, _ := f.svc.StorePending2FASession(ctx, u.ID, "d", "web", "1.2.3.4", "ua", AMRPassword)
	for i := 0; i < int(twoFAPendingFailLimit); i++ {
		_, _ = f.svc.Verify2FA(ctx, u.ID, "000000", tok)
	}
	if f.mr.Exists(pendingSessionPrefix + tok) {
		t.Fatalf("pending token survived %d wrong codes", twoFAPendingFailLimit)
	}
}

// M9: Disable2FA gives one answer for either wrong factor.
func TestDisable2FADoesNotSayWhichFactorFailed(t *testing.T) {
	f := newAdminFixture(t)
	u := f.addUser(t, true)
	ctx := context.Background()
	good := f.code(t, u, time.Now())

	wrongPassword := f.svc.Disable2FA(ctx, u.ID, "Wrong!pass1", good)
	wrongCode := f.svc.Disable2FA(ctx, u.ID, adminTestPassword, "000000")
	if !errors.Is(wrongPassword, ErrInvalidCredentials) || !errors.Is(wrongCode, ErrInvalidCredentials) {
		t.Fatalf("wrong password -> %v, wrong code -> %v; both must be ErrInvalidCredentials",
			wrongPassword, wrongCode)
	}
}

// M9: password re-checks share one per-account budget.
func TestPasswordReverifyIsRateLimitedPerAccount(t *testing.T) {
	f := newAdminFixture(t)
	u := f.addUser(t, true)
	ctx := context.Background()
	for i := 0; i < int(passwordReverifyLimit); i++ {
		if _, err := f.svc.reverifyPassword(ctx, u.ID, "Wrong!pass1"); !errors.Is(err, ErrInvalidPassword) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	if _, err := f.svc.reverifyPassword(ctx, u.ID, adminTestPassword); err == nil {
		t.Fatal("the correct password was accepted past the re-check budget")
	} else if _, ok := AsThrottled(err); !ok {
		t.Fatalf("got %v, want throttled", err)
	}
	// Disable2FA draws on the same budget.
	if err := f.svc.Disable2FA(ctx, u.ID, adminTestPassword, f.code(t, u, time.Now())); err == nil {
		t.Fatal("Disable2FA bypassed the shared password re-check budget")
	} else if _, ok := AsThrottled(err); !ok {
		t.Fatalf("Disable2FA got %v, want throttled", err)
	}
}

// ── Fixed window (login M1) ─────────────────────────────────────────────────

func TestRedisThrottleWindowDoesNotSlide(t *testing.T) {
	mr := miniredis.RunT(t)
	th := NewRedisThrottle(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	ctx := context.Background()
	const key, limit, window = "k", int64(2), 15 * time.Minute

	for i := 0; i < 3; i++ {
		_, _, _ = th.Allow(ctx, key, limit, window)
	}
	// One request every 14 minutes used to re-arm the full window forever.
	mr.FastForward(14 * time.Minute)
	if ok, _, _ := th.Allow(ctx, key, limit, window); ok {
		t.Fatal("over-limit request allowed inside the window")
	}
	mr.FastForward(2 * time.Minute) // 16 min after the first request
	if ok, _, err := th.Allow(ctx, key, limit, window); !ok || err != nil {
		t.Fatalf("still locked 16 minutes after the window opened (ok=%v err=%v)", ok, err)
	}
}

// H2/M1 at the service layer, so a second handler cannot skip the checks.
func TestRegisterWithConsentRejectsBadEmailAndPhoneBeforeAnyWrite(t *testing.T) {
	svc, _, _ := newTestService(t, "shadow")
	consent := RegistrationConsent{Accepted: true, Version: CurrentTermsVersion}
	_, err := svc.RegisterWithConsent(context.Background(), "", "foo", "Str0ng!pass",
		"A", "B", "1990-01-01", "", consent)
	if !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("junk email: %v, want ErrInvalidEmail", err)
	}
	_, err = svc.RegisterWithConsent(context.Background(), "victim@gmail.com", "a@example.com", "Str0ng!pass",
		"A", "B", "1990-01-01", "", consent)
	if !errors.Is(err, ErrInvalidPhone) {
		t.Fatalf("email-shaped phone: %v, want ErrInvalidPhone", err)
	}
}

type captureMail struct{ n int }

func (c *captureMail) Send(context.Context, EmailMessage) error { c.n++; return nil }

// M2: a successful resend closes the queued job, so the relay cannot mail a
// second code that replaces the one the user just received.
func TestResendClosesQueuedJobs(t *testing.T) {
	svc, p, _ := newOTPFixture(t)
	mail := &captureMail{}
	svc.WithEmailSender(mail)
	if err := svc.ResendVerificationWithTransaction(context.Background(), "tok"); err != nil {
		t.Fatalf("resend: %v", err)
	}
	if mail.n != 1 || p.jobsShut != 1 {
		t.Fatalf("sent=%d jobsClosed=%d, want 1 and 1", mail.n, p.jobsShut)
	}
}

// An unset OTP_MAX_ATTEMPTS means the default cap, never "refuse every code".
func TestOTPMaxAttemptsDefaultsWhenUnset(t *testing.T) {
	svc, _, _ := newTestService(t, "shadow")
	svc.cfg.OTPMaxAttempts = 0
	if got := svc.otpMaxAttempts(); got != defaultOTPMaxAttempts {
		t.Fatalf("otpMaxAttempts() = %d with config 0, want %d", got, defaultOTPMaxAttempts)
	}
}
