//go:build integration

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Partial profile writes against live PostgreSQL: a nil param keeps the
// stored column, a pointer to "" clears it.
//
//	PROFILE_IT_POSTGRES_DSN=postgres://.../identity_profile_it_test \
//	  go test -tags integration -p 1 ./internal/store/ -run ProfilePartialWrite -v

// absent marks a JSON key that must be missing (an omitempty nil) after a write.
type absentKey struct{}

// seedFullProfile gives every column UpdateProfile writes a non-empty value,
// so any column the statement overwrites shows up as a difference.
func seedFullProfile(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, *Profile) {
	t.Helper()
	ctx := context.Background()
	id := seedIdentityUser(t, pool, seedIdentity{firstName: "Asha", profileDOB: day("1990-03-17")})
	expires := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
		UPDATE profile.profiles SET
			username = $2, display_name = 'Asha K', last_name = 'Surname', preferred_name = 'Ash',
			pronouns = 'she/her', gender = 'female', bio = 'old bio', category = 'creator',
			profession = 'Engineer', website = 'https://example.test', location = 'Hyderabad',
			status_text = 'busy', status_emoji = 'x', status_expires_at = $3,
			profile_theme_color = '#112233', intro_media_url = 'https://example.test/i',
			intro_media_type = 'video', cta_label = 'Hire', cta_url = 'https://example.test/c',
			member_since_badge = TRUE, timezone = 'Asia/Kolkata'
		WHERE user_id = $1`, id, "it_"+uuid.NewString()[:8], expires); err != nil {
		t.Fatalf("seed full profile: %v", err)
	}
	before, err := New(pool).GetProfile(ctx, id)
	if err != nil || before == nil {
		t.Fatalf("GetProfile: %v", err)
	}
	return id, before
}

func profileJSONFields(t *testing.T, p *Profile) map[string]any {
	t.Helper()
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

// assertOnlyChanged fails for every response field that differs from before,
// except the keys in changed, which must hold exactly the given value (or be
// missing, for absentKey). Values are never printed: only field names.
func assertOnlyChanged(t *testing.T, before, after *Profile, changed map[string]any) {
	t.Helper()
	if after == nil {
		t.Fatal("no profile row after the write")
	}
	b, a := profileJSONFields(t, before), profileJSONFields(t, after)
	keys := map[string]bool{}
	for k := range b {
		keys[k] = true
	}
	for k := range a {
		keys[k] = true
	}
	for k := range changed {
		keys[k] = true
	}
	for k := range keys {
		got, present := a[k]
		if want, ok := changed[k]; ok {
			if _, mustBeAbsent := want.(absentKey); mustBeAbsent {
				if present {
					t.Errorf("%s: still set, want cleared", k)
				}
				continue
			}
			if !present || fmt.Sprint(got) != fmt.Sprint(want) {
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

func TestProfilePartialWrite_BioOnlyKeepsEveryOtherColumn(t *testing.T) {
	pool := profileWritePool(t)
	id, before := seedFullProfile(t, pool)
	after, err := New(pool).UpdateProfile(context.Background(), id, UpdateProfileParams{Bio: strPtr("new bio")})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	assertOnlyChanged(t, before, after, map[string]any{"bio": "new bio"})
}

func TestProfilePartialWrite_HandleChangeKeepsEveryOtherColumn(t *testing.T) {
	pool := profileWritePool(t)
	id, before := seedFullProfile(t, pool)
	handle := "it_" + uuid.NewString()[:8]
	after, err := New(pool).UpdateProfile(context.Background(), id, UpdateProfileParams{Username: &handle})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	assertOnlyChanged(t, before, after, map[string]any{"username": handle})
}

func TestProfilePartialWrite_EmptyValueClearsOnlyThatColumn(t *testing.T) {
	pool := profileWritePool(t)
	id, before := seedFullProfile(t, pool)
	empty := ""
	after, err := New(pool).UpdateProfile(context.Background(), id, UpdateProfileParams{
		LastName:             &empty,
		PreferredName:        &empty,
		Pronouns:             &empty,
		Gender:               &empty,
		ClearStatusExpiresAt: true,
	})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	assertOnlyChanged(t, before, after, map[string]any{
		"last_name": "", "preferred_name": "", "pronouns": "", "gender": "",
		"status_expires_at": absentKey{},
	})
}

func TestProfilePartialWrite_LastNameClearAlone(t *testing.T) {
	pool := profileWritePool(t)
	id, before := seedFullProfile(t, pool)
	after, err := New(pool).UpdateProfile(context.Background(), id, UpdateProfileParams{LastName: strPtr("")})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	assertOnlyChanged(t, before, after, map[string]any{"last_name": ""})
}

func TestProfilePartialWrite_SuppliedValuesAreWritten(t *testing.T) {
	pool := profileWritePool(t)
	id, before := seedFullProfile(t, pool)
	expires := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	after, err := New(pool).UpdateProfile(context.Background(), id, UpdateProfileParams{
		LastName:        strPtr("Kumar"),
		Gender:          strPtr("male"),
		Location:        strPtr("Pune"),
		StatusExpiresAt: &expires,
	})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if after == nil || after.StatusExpiresAt == nil || !after.StatusExpiresAt.Equal(expires) {
		t.Fatal("status_expires_at: not written as requested")
	}
	// The instant is checked above; the scan zone decides its JSON spelling.
	assertOnlyChanged(t, before, after, map[string]any{
		"last_name": "Kumar", "gender": "male", "location": "Pune",
		"status_expires_at": profileJSONFields(t, after)["status_expires_at"],
	})
}
