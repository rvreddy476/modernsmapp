//go:build integration

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"log/slog"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/identity-auth-service/database"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Module 3 CLB-3 — the real signup journey, over real HTTP, through the REAL
// auth middleware, against live PostgreSQL.
//
//	POSTGRES_DSN=postgres://... go test -tags integration ./internal/http/ -run Journey -v
//
// WHY THE EXISTING TESTS DID NOT COVER THIS
//
// handler_test.go installs noopMiddleware for auth and CSRF. That is fine for
// proving a handler's own logic, but it makes the middleware boundary
// invisible — and the boundary WAS the defect. /verify-email and
// /resend-verification sat behind the authenticated group and read the user
// from X-User-Id, which only a verified bearer session produces. Registration
// deliberately produces none. With noop middleware every one of those calls
// succeeded in tests and none of them could succeed in production.
//
// So this file wires the SAME middleware main.go wires —
// AuthMiddlewareWithKeys and RequireCSRFMiddleware — and drives the whole
// journey with an HTTP client that holds no session at all:
//
//	register -> no session -> verify (transaction + code) -> active -> login
//
// plus the resend path and every named negative control.

func journalLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// ── Live PostgreSQL ─────────────────────────────────────────────────────────

// journeyPool connects to the database this suite OWNS.
//
// It deliberately uses its own environment variable rather than POSTGRES_DSN.
// The store suite (internal/store/registration_integration_test.go) installs a
// hand-written subset of auth.users into whatever POSTGRES_DSN points at, and
// `go test ./...` runs packages concurrently — so sharing one database means
// this suite's `CREATE TABLE IF NOT EXISTS auth.users` can silently become a
// no-op over the other suite's narrower table, and the very next index fails on
// a column that the real schema has. A live test that depends on which package
// won a race is not evidence.
func journeyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AUTH_JOURNEY_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AUTH_JOURNEY_POSTGRES_DSN not set; live signup journey skipped. " +
			"Point it at a database this suite owns — it applies the whole of " +
			"database/setup.sql.")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := store.BootstrapSchema(context.Background(), pool, journeySchema); err != nil {
		t.Fatalf("install schema: %v", err)
	}
	return pool
}

// journeySchema is the REAL production schema, applied through the SAME
// BootstrapSchema entry point main.go calls at boot.
//
// An earlier version of this file hand-wrote a "subset the journey touches".
// That is how a live test goes vacuous: the hand-written auth.users had no
// two_factor_enabled column, so registration failed on a table the deployed
// service does not have — and had the column merely been spelled differently
// in a way PostgreSQL tolerated, the test would have passed against a schema
// that does not exist anywhere. Using database.SetupSQL means a DDL statement
// that would fail at boot fails here, and the journey runs against the columns
// production actually has.
//
// No statement is modified, skipped, or reordered. That includes
// `CREATE EXTENSION pgcrypto`: run this against a PostgreSQL that ships
// contrib (the docker-compose.infra.yml postgres container does) so an
// extension the deployed schema declares is actually installed here too. If
// this file ever starts editing the schema to make itself pass, the thing it
// proves stops being the thing that ships.
var journeySchema = database.SetupSQL

// ── The captured mailbox ────────────────────────────────────────────────────

// mailbox is the user's inbox. The code is READ OUT OF THE SENT MESSAGE
// rather than out of the database, so the journey depends on the same thing a
// real user depends on: an email that actually contains a usable code.
type mailbox struct {
	mu   sync.Mutex
	sent []service.EmailMessage
}

func (m *mailbox) Send(_ context.Context, msg service.EmailMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *mailbox) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

var otpPattern = regexp.MustCompile(`\b(\d{6})\b`)

// latestCode returns the code from the most recent message to an address.
func (m *mailbox) latestCode(t *testing.T, to string) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sent) - 1; i >= 0; i-- {
		if !strings.EqualFold(m.sent[i].To, to) {
			continue
		}
		if match := otpPattern.FindStringSubmatch(m.sent[i].TextBody); match != nil {
			return match[1]
		}
	}
	t.Fatalf("no verification code was ever emailed to %s (%d messages sent)", to, len(m.sent))
	return ""
}

// nullProducer stands in for the Kafka producer. Registration goes through the
// transactional outbox rather than the producer, so nothing this journey
// asserts depends on it; login publishes a user-logged-in event through it,
// and main.go always supplies one.
type nullProducer struct{}

func (nullProducer) PublishUserRegistered(context.Context, uuid.UUID, string, *string, string, string, string, string) error {
	return nil
}

func (nullProducer) PublishUserLoggedIn(context.Context, uuid.UUID, uuid.UUID, string, string, string) error {
	return nil
}

func (nullProducer) PublishRaw(context.Context, string, string, json.RawMessage) error { return nil }

// ── The service under test, wired as main.go wires it ───────────────────────

type journeyEnv struct {
	router *gin.Engine
	pool   *pgxpool.Pool
	mail   *mailbox
	email  string
	phone  string
	pass   string
}

const journeyJWTSecret = "clb3-journey-secret-not-a-production-key"

// newJourneyEnv wires the service against a real in-process Redis, so the
// rate limiters and the login-anomaly probe run for real rather than being
// short-circuited by a nil client.
func newJourneyEnv(t *testing.T) *journeyEnv {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return newJourneyEnvWithRedis(t, rdb)
}

// newJourneyEnvWithRedis builds the router against the given Redis. The
// rate-limit control below passes an UNREACHABLE client to prove the limiter
// is genuinely in the chain (it fails closed).
func newJourneyEnvWithRedis(t *testing.T, rdb *redis.Client) *journeyEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	pool := journeyPool(t)

	cfg := &config.Config{
		JWTSecret:       journeyJWTSecret,
		JWTKID:          "journey",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour,
		OTPExpiry:       10 * time.Minute,
		OTPDigits:       6,
		BcryptCost:      4, // fast on purpose; this test is about flow, not cost
		JWTIssuer:       "atpost-auth",
		JWTAudience:     "atpost-api",
	}

	st := store.New(pool)
	mail := &mailbox{}
	svc := service.New(st, nullProducer{}, cfg, journalLogger(), rdb, nil)
	svc.WithEmailSender(mail)

	h := New(svc, cfg, journalLogger(), rdb)

	// THE POINT OF THIS FILE: the real middleware, not a noop.
	authMW := AuthMiddlewareWithKeys(JWTKeySet{
		ActiveKID:    cfg.JWTKID,
		ActiveSecret: cfg.JWTSecret,
	}, nil)
	csrfMW := RequireCSRFMiddleware()

	r := gin.New()
	h.RegisterRoutes(r, authMW, csrfMW)

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	env := &journeyEnv{
		router: r,
		pool:   pool,
		mail:   mail,
		email:  "clb3-" + suffix + "@example.test",
		phone:  "+9199" + suffix[:8],
		pass:   "Journey!Passw0rd-" + suffix[:6],
	}
	t.Cleanup(func() { env.cleanup(t) })
	return env
}

func (e *journeyEnv) cleanup(t *testing.T) {
	ctx := context.Background()
	var userID uuid.UUID
	if err := e.pool.QueryRow(ctx,
		`SELECT user_id FROM auth.users WHERE email = $1`, e.email).Scan(&userID); err != nil {
		return
	}
	for _, q := range []string{
		`DELETE FROM auth.verification_transactions WHERE user_id = $1`,
		`DELETE FROM auth.sessions WHERE user_id = $1`,
		`DELETE FROM auth.registration_consents WHERE user_id = $1`,
		`DELETE FROM profile.profiles WHERE user_id = $1`,
		`DELETE FROM auth.users WHERE user_id = $1`,
	} {
		if _, err := e.pool.Exec(ctx, q, userID); err != nil {
			t.Logf("cleanup %s: %v", q, err)
		}
	}
	_, _ = e.pool.Exec(ctx, `DELETE FROM auth.otp_codes WHERE phone = $1`, e.email)
}

// post issues a request with NO session, NO cookies, and NO X-User-Id unless
// the caller explicitly forges one.
func (e *journeyEnv) post(t *testing.T, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp := httptest.NewRecorder()
	e.router.ServeHTTP(resp, req)
	return resp
}

func decode(t *testing.T, resp *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", resp.Body.String(), err)
	}
	return out
}

// dataOf digs past the envelope the api package wraps successful bodies in.
func dataOf(t *testing.T, resp *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	body := decode(t, resp)
	if d, ok := body["data"].(map[string]any); ok {
		return d
	}
	return body
}

func (e *journeyEnv) register(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return e.post(t, "/v1/auth/register", map[string]any{
		"phone":            e.phone,
		"email":            e.email,
		"password":         e.pass,
		"first_name":       "Journey",
		"last_name":        "Tester",
		"dob":              "1990-01-01",
		"gender":           "other",
		"accepted_terms":   true,
		"accepted_privacy": true,
		"terms_version":    service.CurrentTermsVersion,
	}, nil)
}

func (e *journeyEnv) accountStatus(t *testing.T) string {
	t.Helper()
	var status string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT account_status FROM auth.users WHERE email = $1`, e.email).Scan(&status); err != nil {
		t.Fatalf("read account_status: %v", err)
	}
	return status
}

func (e *journeyEnv) sessionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM auth.sessions s
		   JOIN auth.users u ON u.user_id = s.user_id
		  WHERE u.email = $1`, e.email).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

// ─────────────────────────────────────────────────────────────────────────────
// THE CLOSURE PROOF — register -> no session -> verify -> active -> login.
// ─────────────────────────────────────────────────────────────────────────────
func TestJourneyRegisterVerifyActivateLogin(t *testing.T) {
	e := newJourneyEnv(t)

	// ── 1. Register. ──
	resp := e.register(t)
	if resp.Code != http.StatusCreated && resp.Code != http.StatusOK {
		t.Fatalf("register: status %d: %s", resp.Code, resp.Body.String())
	}
	data := dataOf(t, resp)

	// No session, by any route: no tokens in the body, no cookies, no row.
	if _, ok := data["tokens"]; ok {
		if tokens, _ := data["tokens"].(map[string]any); tokens["access_token"] != nil && tokens["access_token"] != "" {
			t.Fatal("registration returned an access token; an unverified address is a usable identity")
		}
	}
	if len(resp.Result().Cookies()) != 0 {
		t.Fatalf("registration set cookies: %v", resp.Result().Cookies())
	}
	if n := e.sessionCount(t); n != 0 {
		t.Fatalf("registration created %d session rows; it must create none", n)
	}
	if s := e.accountStatus(t); s != store.AccountStatusPendingVerification {
		t.Fatalf("account_status is %q, want %q", s, store.AccountStatusPendingVerification)
	}

	// ── 2. The verification transaction is returned and the code is sent. ──
	transaction, _ := data[VerificationTransactionField].(string)
	if transaction == "" {
		t.Fatal("registration returned no verification transaction. The account is " +
			"pending, has no session, and both verify and resend need one — the user " +
			"is stuck at 'check your email' forever.")
	}
	if e.mail.count() == 0 {
		t.Fatal("no verification email was sent")
	}
	code := e.mail.latestCode(t, e.email)

	// ── 3. Resend works with NO session. ──
	resend := e.post(t, "/v1/auth/resend-verification", map[string]any{
		"type":                       "email",
		VerificationTransactionField: transaction,
	}, nil)
	if resend.Code != http.StatusOK {
		t.Fatalf("resend with no session: status %d: %s", resend.Code, resend.Body.String())
	}
	if e.mail.count() < 2 {
		t.Fatalf("resend reported success but sent nothing (%d messages)", e.mail.count())
	}
	// The resend supersedes the earlier code, so use the newest one.
	code = e.mail.latestCode(t, e.email)

	// ── 4. Verify with the transaction + code, still with NO session. ──
	verify := e.post(t, "/v1/auth/verify-email", map[string]any{
		VerificationTransactionField: transaction,
		"code":                       code,
	}, nil)
	if verify.Code != http.StatusOK {
		t.Fatalf("verify with no session: status %d: %s", verify.Code, verify.Body.String())
	}
	if s := e.accountStatus(t); s != store.AccountStatusActive {
		t.Fatalf("account_status is %q after verification, want %q", s, store.AccountStatusActive)
	}

	// ── 5. Normal login now succeeds and issues a real session. ──
	login := e.post(t, "/v1/auth/login", map[string]any{
		"identifier": e.email,
		"password":   e.pass,
	}, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login after activation: status %d: %s", login.Code, login.Body.String())
	}
	loginData := dataOf(t, login)
	tokens, _ := loginData["tokens"].(map[string]any)
	access, _ := tokens["access_token"].(string)
	if access == "" {
		t.Fatalf("login returned no access token: %s", login.Body.String())
	}
	if n := e.sessionCount(t); n != 1 {
		t.Fatalf("login created %d sessions, want 1", n)
	}

	// ── 6. The token the journey produced is accepted by the REAL middleware. ──
	me := httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	me.Header.Set("Authorization", "Bearer "+access)
	meResp := httptest.NewRecorder()
	e.router.ServeHTTP(meResp, me)
	if meResp.Code != http.StatusOK {
		t.Fatalf("the session from the completed journey was rejected by the real auth "+
			"middleware: status %d: %s", meResp.Code, meResp.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NEGATIVE CONTROL — a raw user id must not activate anything.
//
// This is the control for "accepting a raw user ID". If the handler ever reads
// X-User-Id again, or the request body grows a user_id field, this passes and
// anyone can grind codes against any account they can name.
// ─────────────────────────────────────────────────────────────────────────────
func TestJourneyRawUserIDCannotVerify(t *testing.T) {
	e := newJourneyEnv(t)
	if resp := e.register(t); resp.Code >= 300 {
		t.Fatalf("register: %d %s", resp.Code, resp.Body.String())
	}
	code := e.mail.latestCode(t, e.email)

	var userID uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`SELECT user_id FROM auth.users WHERE email = $1`, e.email).Scan(&userID); err != nil {
		t.Fatalf("read user id: %v", err)
	}

	for _, attempt := range []struct {
		name string
		body map[string]any
		hdrs map[string]string
	}{
		{"X-User-Id header, correct code",
			map[string]any{"code": code},
			map[string]string{"X-User-Id": userID.String()}},
		{"user_id in body, correct code",
			map[string]any{"code": code, "user_id": userID.String()},
			nil},
		{"user_id as the transaction",
			map[string]any{VerificationTransactionField: userID.String(), "code": code},
			nil},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			resp := e.post(t, "/v1/auth/verify-email", attempt.body, attempt.hdrs)
			if resp.Code == http.StatusOK {
				t.Fatalf("a caller-supplied user id activated an account (%d): %s",
					resp.Code, resp.Body.String())
			}
			if s := e.accountStatus(t); s != store.AccountStatusPendingVerification {
				t.Fatalf("account_status became %q; a caller-supplied id changed account state", s)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NEGATIVE CONTROL — every invalid transaction shape.
//
// Expired, wrong-purpose, forged, replayed, and another user's credential must
// all fail, and none of them may activate an account.
// ─────────────────────────────────────────────────────────────────────────────
func TestJourneyInvalidTransactionsCannotActivate(t *testing.T) {
	e := newJourneyEnv(t)
	if resp := e.register(t); resp.Code >= 300 {
		t.Fatalf("register: %d %s", resp.Code, resp.Body.String())
	}
	code := e.mail.latestCode(t, e.email)

	var userID uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`SELECT user_id FROM auth.users WHERE email = $1`, e.email).Scan(&userID); err != nil {
		t.Fatalf("read user id: %v", err)
	}
	st := store.New(e.pool)
	ctx := context.Background()

	// A credential minted for a DIFFERENT purpose.
	wrongPurpose, err := st.CreateVerificationTransaction(ctx, userID, "password_reset", time.Hour)
	if err != nil {
		t.Fatalf("mint wrong-purpose transaction: %v", err)
	}

	// An EXPIRED credential.
	expired, err := st.CreateVerificationTransaction(ctx, userID, store.VerificationPurposeEmail, time.Hour)
	if err != nil {
		t.Fatalf("mint transaction: %v", err)
	}
	if _, err := e.pool.Exec(ctx,
		`UPDATE auth.verification_transactions SET expires_at = NOW() - INTERVAL '1 minute'
		  WHERE user_id = $1 AND purpose = $2`, userID, store.VerificationPurposeEmail); err != nil {
		t.Fatalf("expire transaction: %v", err)
	}

	for _, tc := range []struct{ name, token string }{
		{"forged", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{"empty", ""},
		{"wrong purpose", wrongPurpose.Token},
		{"expired", expired.Token},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := e.post(t, "/v1/auth/verify-email", map[string]any{
				VerificationTransactionField: tc.token,
				"code":                       code,
			}, nil)
			if resp.Code == http.StatusOK {
				t.Fatalf("a %s transaction activated an account: %s", tc.name, resp.Body.String())
			}
			if s := e.accountStatus(t); s != store.AccountStatusPendingVerification {
				t.Fatalf("account_status became %q after a %s transaction", s, tc.name)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Another user's credential must not activate this account, and a replay of a
// SUCCESSFUL verification must not re-enter activation.
// ─────────────────────────────────────────────────────────────────────────────
func TestJourneyOtherUsersTransactionAndReplayAreRejected(t *testing.T) {
	victim := newJourneyEnv(t)
	if resp := victim.register(t); resp.Code >= 300 {
		t.Fatalf("register victim: %d %s", resp.Code, resp.Body.String())
	}
	victimCode := victim.mail.latestCode(t, victim.email)

	attacker := newJourneyEnvWithRedis(t, nil)
	// Share the victim's router so both accounts live in one service instance.
	attacker.router = victim.router
	if resp := attacker.register(t); resp.Code >= 300 {
		t.Fatalf("register attacker: %d %s", resp.Code, resp.Body.String())
	}

	// The attacker registered through the victim's router, so the mail landed
	// in the victim's mailbox.
	attackerTransaction := ""
	{
		resp := attacker.post(t, "/v1/auth/login", map[string]any{
			"identifier": attacker.email,
			"password":   attacker.pass,
		}, nil)
		if resp.Code != http.StatusForbidden {
			t.Fatalf("pending login: status %d, want 403: %s", resp.Code, resp.Body.String())
		}
		body := decode(t, resp)
		errObj, _ := body["error"].(map[string]any)
		details, _ := errObj["details"].(map[string]any)
		attackerTransaction, _ = details[VerificationTransactionField].(string)
	}
	if attackerTransaction == "" {
		t.Fatal("pending login returned no verification transaction; a user who closed " +
			"the app after registering has no way back in")
	}

	// The attacker's credential must not activate the VICTIM's account, even
	// with the victim's real code.
	resp := victim.post(t, "/v1/auth/verify-email", map[string]any{
		VerificationTransactionField: attackerTransaction,
		"code":                       victimCode,
	}, nil)
	if resp.Code == http.StatusOK && victim.accountStatus(t) == store.AccountStatusActive {
		t.Fatal("one user's verification transaction activated ANOTHER user's account")
	}
	if s := victim.accountStatus(t); s != store.AccountStatusPendingVerification {
		t.Fatalf("victim account_status became %q via another user's transaction", s)
	}

	// Now complete the victim's own journey, then replay it.
	var victimTransaction string
	{
		resp := victim.post(t, "/v1/auth/login", map[string]any{
			"identifier": victim.email,
			"password":   victim.pass,
		}, nil)
		body := decode(t, resp)
		errObj, _ := body["error"].(map[string]any)
		details, _ := errObj["details"].(map[string]any)
		victimTransaction, _ = details[VerificationTransactionField].(string)
	}
	if victimTransaction == "" {
		t.Fatal("pending login did not return a fresh transaction for the victim")
	}
	// A fresh transaction supersedes the old one, so re-request the code.
	if r := victim.post(t, "/v1/auth/resend-verification", map[string]any{
		"type":                       "email",
		VerificationTransactionField: victimTransaction,
	}, nil); r.Code != http.StatusOK {
		t.Fatalf("resend: %d %s", r.Code, r.Body.String())
	}
	freshCode := victim.mail.latestCode(t, victim.email)

	ok := victim.post(t, "/v1/auth/verify-email", map[string]any{
		VerificationTransactionField: victimTransaction,
		"code":                       freshCode,
	}, nil)
	if ok.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", ok.Code, ok.Body.String())
	}
	if s := victim.accountStatus(t); s != store.AccountStatusActive {
		t.Fatalf("account_status is %q after successful verification", s)
	}

	// REPLAY: the same transaction and code, submitted again.
	replay := victim.post(t, "/v1/auth/verify-email", map[string]any{
		VerificationTransactionField: victimTransaction,
		"code":                       freshCode,
	}, nil)
	if replay.Code == http.StatusOK {
		t.Fatal("a replayed verification succeeded; the transaction is not single-use")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NEGATIVE CONTROL — the pending-login recovery path must exist.
//
// A wrong password stays a generic invalid-credentials answer, and a correct
// one on a pending account returns a fresh transaction and NO session.
// ─────────────────────────────────────────────────────────────────────────────
func TestJourneyPendingLoginIsTheRecoveryPathAndLeaksNothing(t *testing.T) {
	e := newJourneyEnv(t)
	if resp := e.register(t); resp.Code >= 300 {
		t.Fatalf("register: %d %s", resp.Code, resp.Body.String())
	}

	// Wrong password on a PENDING account: generic, no transaction.
	wrong := e.post(t, "/v1/auth/login", map[string]any{
		"identifier": e.email,
		"password":   "not-the-password",
	}, nil)
	if wrong.Code == http.StatusOK {
		t.Fatal("a wrong password logged in")
	}
	if strings.Contains(wrong.Body.String(), VerificationTransactionField) {
		t.Fatalf("a WRONG password was handed a verification transaction: %s", wrong.Body.String())
	}
	if strings.Contains(strings.ToUpper(wrong.Body.String()), "EMAIL_NOT_VERIFIED") {
		t.Fatalf("a wrong password revealed that the account is pending, which lets "+
			"anyone enumerate pending accounts with no credential at all: %s",
			wrong.Body.String())
	}

	// Correct password on a PENDING account: a fresh transaction, no session.
	right := e.post(t, "/v1/auth/login", map[string]any{
		"identifier": e.email,
		"password":   e.pass,
	}, nil)
	if right.Code != http.StatusForbidden {
		t.Fatalf("pending login: status %d, want 403: %s", right.Code, right.Body.String())
	}
	body := decode(t, right)
	errObj, _ := body["error"].(map[string]any)
	details, _ := errObj["details"].(map[string]any)
	transaction, _ := details[VerificationTransactionField].(string)
	if transaction == "" {
		t.Fatalf("pending login returned no verification transaction: %s", right.Body.String())
	}
	if len(right.Result().Cookies()) != 0 {
		t.Fatalf("pending login set cookies: %v", right.Result().Cookies())
	}
	if n := e.sessionCount(t); n != 0 {
		t.Fatalf("pending login created %d session rows; it must create none", n)
	}
	if strings.Contains(right.Body.String(), "access_token") {
		t.Fatalf("pending login returned an access token: %s", right.Body.String())
	}

	// And that transaction must actually work.
	if r := e.post(t, "/v1/auth/resend-verification", map[string]any{
		"type":                       "email",
		VerificationTransactionField: transaction,
	}, nil); r.Code != http.StatusOK {
		t.Fatalf("the recovery transaction could not resend: %d %s", r.Code, r.Body.String())
	}
	code := e.mail.latestCode(t, e.email)
	if v := e.post(t, "/v1/auth/verify-email", map[string]any{
		VerificationTransactionField: transaction,
		"code":                       code,
	}, nil); v.Code != http.StatusOK {
		t.Fatalf("the recovery transaction could not verify: %d %s", v.Code, v.Body.String())
	}
	if s := e.accountStatus(t); s != store.AccountStatusActive {
		t.Fatalf("account_status is %q after recovery verification", s)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NEGATIVE CONTROL — verify/resend must not be reachable behind an access
// token requirement, and must be rate limited.
//
// The first half is what CLB-3 fixed: with the routes back under the auth
// middleware, a sessionless request gets 401 and the journey is dead. The
// second half proves the limiter is really in the chain — an unreachable Redis
// makes it fail closed, which cannot happen if it is not there.
// ─────────────────────────────────────────────────────────────────────────────
func TestJourneyVerifyRoutesArePublicAndRateLimited(t *testing.T) {
	e := newJourneyEnv(t)
	if resp := e.register(t); resp.Code >= 300 {
		t.Fatalf("register: %d %s", resp.Code, resp.Body.String())
	}

	// Public: no session, and specifically NOT 401 from the auth middleware.
	for _, path := range []string{"/v1/auth/verify-email", "/v1/auth/resend-verification"} {
		resp := e.post(t, path, map[string]any{
			"type":                       "email",
			VerificationTransactionField: "obviously-invalid",
			"code":                       "000000",
		}, nil)
		if resp.Code == http.StatusUnauthorized {
			t.Fatalf("%s answered 401 to a sessionless request. A pending account has no "+
				"session by design, so this route is unreachable by the only users who "+
				"need it.", path)
		}
	}

	// Rate limited: an unreachable Redis fails the limiter closed.
	unreachable := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 200 * time.Millisecond,
		ReadTimeout: 200 * time.Millisecond,
	})
	defer unreachable.Close()

	limited := newJourneyEnvWithRedis(t, unreachable)
	for _, path := range []string{"/v1/auth/verify-email", "/v1/auth/resend-verification"} {
		resp := limited.post(t, path, map[string]any{
			"type":                       "email",
			VerificationTransactionField: "obviously-invalid",
			"code":                       "000000",
		}, nil)
		if resp.Code != http.StatusTooManyRequests {
			t.Errorf("%s returned %d with the rate limiter's store unreachable; want 429. "+
				"The limiter fails closed, so any other answer means it is not in this "+
				"route's chain and the endpoint is an unthrottled code-grinding surface.",
				path, resp.Code)
		}
	}
}

// A short guard that the credential is stored hashed, not in the clear.
func TestJourneyTransactionIsNotStoredInPlaintext(t *testing.T) {
	e := newJourneyEnv(t)
	resp := e.register(t)
	if resp.Code >= 300 {
		t.Fatalf("register: %d %s", resp.Code, resp.Body.String())
	}
	transaction, _ := dataOf(t, resp)[VerificationTransactionField].(string)
	if transaction == "" {
		t.Fatal("no verification transaction was issued")
	}

	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM auth.verification_transactions WHERE token_hash = $1`,
		transaction).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Fatalf("the verification transaction is stored in plaintext (%d rows matched the "+
			"raw value); a database dump would yield usable credentials", n)
	}

	// ...and the digest that IS stored resolves back to the account.
	var stored int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM auth.verification_transactions t
		   JOIN auth.users u ON u.user_id = t.user_id
		  WHERE u.email = $1 AND t.purpose = $2`,
		e.email, store.VerificationPurposeEmail).Scan(&stored); err != nil {
		t.Fatalf("query: %v", err)
	}
	if stored != 1 {
		t.Fatalf("found %d stored verification transactions for the account, want 1", stored)
	}
}
