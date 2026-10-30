//go:build integration

package http

// CI-only (AUTH_JOURNEY_POSTGRES_DSN, full database/setup.sql): the SQL side
// of the registration / verification-code hardening.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// M1: the email is stored lowercased, a case variant cannot register again,
// and the database itself refuses a case-variant duplicate.
func TestJourneyEmailIsCaseInsensitive(t *testing.T) {
	e := newJourneyEnv(t)
	mixed := strings.ToUpper(e.email[:1]) + e.email[1:]
	e.email = mixed
	resp := e.register(t)
	if resp.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", resp.Code, resp.Body.String())
	}
	e.email = strings.ToLower(mixed) // what is stored; cleanup keys on it

	var stored string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT email FROM auth.users WHERE lower(email) = lower($1)`, mixed).Scan(&stored); err != nil {
		t.Fatalf("read: %v", err)
	}
	if stored != strings.ToLower(mixed) {
		t.Fatalf("stored %q, want the lowercased address", stored)
	}

	e.email = strings.ToUpper(mixed)
	again := e.register(t)
	e.email = strings.ToLower(mixed)
	if again.Code != http.StatusConflict {
		t.Fatalf("case-variant re-registration: %d %s, want 409", again.Code, again.Body.String())
	}

	_, err := e.pool.Exec(context.Background(),
		`INSERT INTO auth.users (user_id, email) VALUES ($1, $2)`, uuid.New(), strings.ToUpper(mixed))
	if err == nil || !strings.Contains(err.Error(), "uq_users_email_lower") {
		t.Fatalf("database accepted a case-variant duplicate email: %v", err)
	}
}

// H2: the phone sent at registration is not stored (registration cannot
// verify it, so it must not claim the unique column).
func TestJourneyRegisterDoesNotStoreUnverifiedPhone(t *testing.T) {
	e := newJourneyEnv(t)
	if resp := e.register(t); resp.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", resp.Code, resp.Body.String())
	}
	var phone *string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT phone FROM auth.users WHERE email = $1`, e.email).Scan(&phone); err != nil {
		t.Fatalf("read: %v", err)
	}
	if phone != nil {
		t.Fatalf("unverified phone %q was stored in the unique phone column", *phone)
	}
}

// H1 through HTTP and the real OTP table: five wrong codes delete the code,
// after which the emailed code no longer verifies.
func TestJourneyEmailCodeDiesAtTheAttemptCap(t *testing.T) {
	e := newJourneyEnv(t)
	resp := e.register(t)
	tx, _ := dataOf(t, resp)[VerificationTransactionField].(string)
	code := e.mail.latestCode(t, e.email)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := 0; i < 5; i++ {
		e.post(t, "/v1/auth/verify-email", map[string]any{VerificationTransactionField: tx, "code": wrong}, nil)
	}
	var n int
	_ = e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM auth.otp_codes WHERE phone = $1 AND purpose = 'email_verify'`, e.email).Scan(&n)
	if n != 0 {
		t.Fatalf("%d code row(s) left after 5 wrong guesses", n)
	}
	ok := e.post(t, "/v1/auth/verify-email", map[string]any{VerificationTransactionField: tx, "code": code}, nil)
	if ok.Code == http.StatusOK || e.accountStatus(t) != store.AccountStatusPendingVerification {
		t.Fatal("the emailed code activated the account after the attempt cap")
	}
}

// M2: concurrent saves leave exactly ONE code row, and it is the last one.
func TestJourneySaveOTPLeavesOneRow(t *testing.T) {
	e := newJourneyEnv(t)
	st := store.New(e.pool)
	key := e.email
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = st.SaveOTP(context.Background(), key, "12345"+string(rune('0'+i)), "email_verify", time.Minute)
		}(i)
	}
	wg.Wait()
	var n int
	_ = e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM auth.otp_codes WHERE phone = $1 AND purpose = 'email_verify'`, key).Scan(&n)
	if n != 1 {
		t.Fatalf("%d live codes after concurrent saves, want exactly 1", n)
	}
}

// M2/M4: a new job is not due until the fast path has had its chance; a
// claimed job is invisible to a second relay; abandoned jobs leave the queue.
func TestJourneyEmailJobClaimAndAbandon(t *testing.T) {
	e := newJourneyEnv(t)
	if resp := e.register(t); resp.Code != http.StatusCreated {
		t.Fatalf("register: %d", resp.Code)
	}
	ctx := context.Background()
	var uid uuid.UUID
	_ = e.pool.QueryRow(ctx, `SELECT user_id FROM auth.users WHERE email = $1`, e.email).Scan(&uid)
	t.Cleanup(func() { _, _ = e.pool.Exec(ctx, `DELETE FROM auth.email_delivery_jobs WHERE user_id = $1`, uid) })

	var delay float64
	_ = e.pool.QueryRow(ctx, `SELECT EXTRACT(EPOCH FROM next_attempt_at - created_at)
		FROM auth.email_delivery_jobs WHERE user_id = $1`, uid).Scan(&delay)
	if delay < store.EmailJobFirstAttemptDelay.Seconds()-1 {
		t.Fatalf("new job due after %.0fs, want >= %v", delay, store.EmailJobFirstAttemptDelay)
	}

	// Make it due and unsent, then claim from two "replicas" at once.
	_, _ = e.pool.Exec(ctx, `UPDATE auth.email_delivery_jobs SET sent_at = NULL,
		next_attempt_at = now() - interval '1 second' WHERE user_id = $1`, uid)
	st := store.New(e.pool)
	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jobs, err := st.FetchDueEmailJobs(ctx, 1000)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			mu.Lock()
			for _, j := range jobs {
				if j.UserID == uid {
					seen[j.ID]++
				}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	for id, n := range seen {
		if n > 1 {
			t.Fatalf("job %d claimed by %d relays at once", id, n)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("job claimed %d times in total, want 1", len(seen))
	}

	for id := range seen {
		if err := st.MarkEmailJobFailed(ctx, id, "gave up"); err != nil {
			t.Fatal(err)
		}
		_, _ = e.pool.Exec(ctx, `UPDATE auth.email_delivery_jobs SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, id)
		jobs, _ := st.FetchDueEmailJobs(ctx, 1000)
		for _, j := range jobs {
			if j.ID == id {
				t.Fatal("an abandoned job was claimed again")
			}
		}
	}
}
