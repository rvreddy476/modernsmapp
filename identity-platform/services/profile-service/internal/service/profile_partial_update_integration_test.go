//go:build integration

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Partial profile writes and handle changes end to end: real Service, real
// Store, identity_profile_it_test.
//
//	PROFILE_IT_POSTGRES_DSN=postgres://.../identity_profile_it_test \
//	  go test -tags integration -p 1 ./internal/service/ -v

// Matches profile-service/database/setup.sql.
const handleHistoryITSchema = `
CREATE TABLE IF NOT EXISTS profile.handle_history (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    old_username   TEXT NOT NULL,
    new_username   TEXT NOT NULL,
    changed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    cooldown_until TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days')
);
`

// seedITFullProfile gives every column UpdateProfile writes a non-empty value.
func seedITFullProfile(t *testing.T, pool *pgxpool.Pool, st *store.Store) (uuid.UUID, *store.Profile) {
	t.Helper()
	ctx := context.Background()
	id := seedITAccount(t, pool, itDate("1990-03-17"))
	if _, err := pool.Exec(ctx, `
		UPDATE profile.profiles SET
			username = $2, display_name = 'Asha K', last_name = 'Surname', preferred_name = 'Ash',
			pronouns = 'she/her', gender = 'female', bio = 'old bio', category = 'creator',
			profession = 'Engineer', website = 'https://example.test', location = 'Hyderabad',
			status_text = 'busy', status_emoji = 'x', status_expires_at = $3,
			profile_theme_color = '#112233', intro_media_url = 'https://example.test/i',
			intro_media_type = 'video', cta_label = 'Hire', cta_url = 'https://example.test/c',
			member_since_badge = TRUE, timezone = 'Asia/Kolkata'
		WHERE user_id = $1`, id, "it_"+uuid.NewString()[:8], time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed full profile: %v", err)
	}
	before, err := st.GetProfile(ctx, id)
	if err != nil || before == nil {
		t.Fatalf("GetProfile: %v", err)
	}
	return id, before
}

// itAssertOnlyChanged fails for every profile field that differs from before,
// except the keys in changed, which must hold the given value. Field names
// only, never values.
func itAssertOnlyChanged(t *testing.T, before, after *store.Profile, changed map[string]string) {
	t.Helper()
	fields := func(p *store.Profile) map[string]any {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal profile: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal profile: %v", err)
		}
		delete(m, "updated_at")
		return m
	}
	if after == nil {
		t.Fatal("no profile row after the write")
	}
	b, a := fields(before), fields(after)
	keys := map[string]bool{}
	for k := range b {
		keys[k] = true
	}
	for k := range a {
		keys[k] = true
	}
	for k := range keys {
		got, present := a[k]
		if want, ok := changed[k]; ok {
			if !present || fmt.Sprint(got) != want {
				t.Errorf("%s: not written as requested", k)
			}
			continue
		}
		prev, wasPresent := b[k]
		if present != wasPresent || fmt.Sprint(got) != fmt.Sprint(prev) {
			t.Errorf("%s: changed by a write that did not carry it", k)
		}
	}
}

func TestIntegration_ChangeHandleKeepsEveryProfileField(t *testing.T) {
	pool := serviceITPool(t)
	if _, err := pool.Exec(context.Background(), handleHistoryITSchema); err != nil {
		t.Fatalf("install handle_history: %v", err)
	}
	svc, st := itService(pool)
	id, before := seedITFullProfile(t, pool, st)
	handle := "it_" + uuid.NewString()[:8]

	returned, err := svc.ChangeHandle(context.Background(), id, "@"+handle)
	if err != nil {
		t.Fatalf("ChangeHandle: %v", err)
	}
	stored, err := st.GetProfile(context.Background(), id)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	itAssertOnlyChanged(t, before, stored, map[string]string{"username": handle})
	itAssertOnlyChanged(t, before, returned, map[string]string{"username": handle})

	var history int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM profile.handle_history WHERE user_id = $1`, id).Scan(&history); err != nil {
		t.Fatalf("count handle_history: %v", err)
	}
	if history != 1 {
		t.Fatalf("handle_history rows = %d, want 1", history)
	}
}

func TestIntegration_BioOnlyUpdateKeepsEveryOtherField(t *testing.T) {
	pool := serviceITPool(t)
	svc, st := itService(pool)
	id, before := seedITFullProfile(t, pool, st)
	if _, err := svc.UpdateProfile(context.Background(), id, store.UpdateProfileParams{Bio: strOf("new bio")}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	after, err := st.GetProfile(context.Background(), id)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	itAssertOnlyChanged(t, before, after, map[string]string{"bio": "new bio"})
}

// A first name stored before the rule existed (60 characters, digits), sent
// back unchanged with a bio edit, as the Android form does on every save.
func TestIntegration_UnchangedLegacyFirstNameDoesNotBlockABioSave(t *testing.T) {
	pool := serviceITPool(t)
	svc, st := itService(pool)
	id, _ := seedITFullProfile(t, pool, st)
	legacy := strings.Repeat("Asha1", 12)
	if _, err := pool.Exec(context.Background(),
		`UPDATE profile.profiles SET first_name = $2 WHERE user_id = $1`, id, legacy); err != nil {
		t.Fatalf("seed legacy first name: %v", err)
	}
	before, err := st.GetProfile(context.Background(), id)
	if err != nil || before == nil {
		t.Fatalf("GetProfile: %v", err)
	}

	if _, err := svc.UpdateProfile(context.Background(), id,
		store.UpdateProfileParams{Bio: strOf("new bio"), FirstName: strOf(legacy)}); err != nil {
		t.Fatalf("an unchanged legacy first name blocked the save: %v", err)
	}
	after, err := st.GetProfile(context.Background(), id)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	itAssertOnlyChanged(t, before, after, map[string]string{"bio": "new bio"})

	// Changing it is still a first-name write, and still validated.
	if got := fieldCode(t, func() error {
		_, err := svc.UpdateProfile(context.Background(), id,
			store.UpdateProfileParams{Bio: strOf("newer bio"), FirstName: strOf(legacy + "2")})
		return err
	}()); got != CodeFirstNameInvalid {
		t.Fatalf("changed to another invalid value: got %q, want %s", got, CodeFirstNameInvalid)
	}
	unchanged, err := st.GetProfile(context.Background(), id)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	itAssertOnlyChanged(t, after, unchanged, map[string]string{})
}

func TestIntegration_ClearedLastNameClearsOnlyLastName(t *testing.T) {
	pool := serviceITPool(t)
	svc, st := itService(pool)
	id, before := seedITFullProfile(t, pool, st)
	if _, err := svc.UpdateProfile(context.Background(), id, store.UpdateProfileParams{LastName: strOf("")}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	after, err := st.GetProfile(context.Background(), id)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	itAssertOnlyChanged(t, before, after, map[string]string{"last_name": ""})
}
