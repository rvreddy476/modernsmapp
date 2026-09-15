package http

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
)

// PUT /v1/profiles/me is a partial update. For each field: an absent key must
// reach the store as "unchanged", an explicit null or "" as a clear, and a
// value as that value. Runs over the real router and service (idwPut).

func TestUpdateMe_EmptyBodyChangesNothing(t *testing.T) {
	st := idwSeed()
	if status, env := idwPut(t, st, `{}`); status != 200 {
		t.Fatalf("status=%d error=%+v", status, env.Error)
	}
	v := reflect.ValueOf(st.updates[0])
	for i := 0; i < v.NumField(); i++ {
		if !v.Field(i).IsZero() {
			t.Errorf("an empty body set %s; every absent field must reach the store unset", v.Type().Field(i).Name)
		}
	}
}

func TestUpdateMe_OptionalTextFieldsAbsentNullEmptyValue(t *testing.T) {
	fields := []struct {
		key       string
		get       func(store.UpdateProfileParams) *string
		send      string
		wantValue string
		// What a null or "" reaches the store as.
		wantCleared string
	}{
		{"display_name", func(p store.UpdateProfileParams) *string { return p.DisplayName }, "Asha K", "Asha K", ""},
		{"bio", func(p store.UpdateProfileParams) *string { return p.Bio }, "hello", "hello", ""},
		{"last_name", func(p store.UpdateProfileParams) *string { return p.LastName }, "Surname", "Surname", ""},
		{"preferred_name", func(p store.UpdateProfileParams) *string { return p.PreferredName }, "Ash", "Ash", ""},
		{"pronouns", func(p store.UpdateProfileParams) *string { return p.Pronouns }, "she/her", "she/her", ""},
		{"gender", func(p store.UpdateProfileParams) *string { return p.Gender }, "female", "female", ""},
		{"category", func(p store.UpdateProfileParams) *string { return p.Category }, "creator", "creator", ""},
		{"profession", func(p store.UpdateProfileParams) *string { return p.Profession }, "Engineer", "Engineer", ""},
		{"website", func(p store.UpdateProfileParams) *string { return p.Website }, "example.test/me", "https://example.test/me", ""},
		{"location", func(p store.UpdateProfileParams) *string { return p.Location }, "Hyderabad", "Hyderabad", ""},
		{"status_text", func(p store.UpdateProfileParams) *string { return p.StatusText }, "busy", "busy", ""},
		{"status_emoji", func(p store.UpdateProfileParams) *string { return p.StatusEmoji }, "x", "x", ""},
		{"profile_theme_color", func(p store.UpdateProfileParams) *string { return p.ProfileThemeColor }, "#112233", "#112233", defaultProfileThemeColor},
		{"intro_media_url", func(p store.UpdateProfileParams) *string { return p.IntroMediaURL }, "https://example.test/i", "https://example.test/i", ""},
		{"intro_media_type", func(p store.UpdateProfileParams) *string { return p.IntroMediaType }, "video", "video", ""},
		{"cta_label", func(p store.UpdateProfileParams) *string { return p.CTALabel }, "Hire", "Hire", ""},
		{"cta_url", func(p store.UpdateProfileParams) *string { return p.CTAURL }, "example.test/c", "https://example.test/c", ""},
		{"timezone", func(p store.UpdateProfileParams) *string { return p.Timezone }, "Asia/Kolkata", "Asia/Kolkata", ""},
	}
	for _, f := range fields {
		cases := []struct {
			name string
			json string // "" means the key is absent
			want *string
		}{
			{"absent", "", nil},
			{"null", "null", &f.wantCleared},
			{"empty string", `""`, &f.wantCleared},
			{"value", strconv.Quote(f.send), &f.wantValue},
		}
		for _, tc := range cases {
			t.Run(f.key+"/"+tc.name, func(t *testing.T) {
				st := idwSeed()
				body := `{}`
				if tc.json != "" {
					body = `{"` + f.key + `":` + tc.json + `}`
				}
				if status, env := idwPut(t, st, body); status != 200 {
					t.Fatalf("status=%d error=%+v", status, env.Error)
				}
				got := f.get(st.updates[0])
				switch {
				case tc.want == nil && got != nil:
					t.Fatalf("an absent %s reached the store as a value; it must be left unchanged", f.key)
				case tc.want != nil && got == nil:
					t.Fatalf("%s %s reached the store as unchanged", f.key, tc.name)
				case tc.want != nil && *got != *tc.want:
					t.Fatalf("%s %s reached the store as %q, want %q", f.key, tc.name, *got, *tc.want)
				}
			})
		}
	}
}

func TestUpdateMe_StatusExpiresAtAbsentNullValue(t *testing.T) {
	at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		json      string
		wantClear bool
		wantAt    *time.Time
	}{
		{"absent", "", false, nil},
		{"null", "null", true, nil},
		{"empty string", `""`, true, nil},
		{"value", `"2026-09-20T10:00:00Z"`, false, &at},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := idwSeed()
			body := `{}`
			if tc.json != "" {
				body = `{"status_expires_at":` + tc.json + `}`
			}
			if status, env := idwPut(t, st, body); status != 200 {
				t.Fatalf("status=%d error=%+v", status, env.Error)
			}
			p := st.updates[0]
			if p.ClearStatusExpiresAt != tc.wantClear {
				t.Fatalf("clear = %v, want %v", p.ClearStatusExpiresAt, tc.wantClear)
			}
			if (p.StatusExpiresAt == nil) != (tc.wantAt == nil) ||
				(tc.wantAt != nil && !p.StatusExpiresAt.Equal(*tc.wantAt)) {
				t.Fatalf("status_expires_at param = %v, want %v", p.StatusExpiresAt, tc.wantAt)
			}
		})
	}
}

// first_name and member_since_badge cannot be cleared: a null is "unchanged".
func TestUpdateMe_NonClearableFieldsTreatNullAsAbsent(t *testing.T) {
	for _, key := range []string{"first_name", "member_since_badge"} {
		for _, body := range []string{`{}`, `{"` + key + `":null}`} {
			st := idwSeed()
			if status, env := idwPut(t, st, body); status != 200 {
				t.Fatalf("%s: status=%d error=%+v", body, status, env.Error)
			}
			if st.updates[0].FirstName != nil || st.updates[0].MemberSinceBadge != nil {
				t.Fatalf("%s reached the store as a value", body)
			}
		}
	}
	st := idwSeed()
	if status, env := idwPut(t, st, `{"first_name":"Asha K","member_since_badge":true}`); status != 200 {
		t.Fatalf("status=%d error=%+v", status, env.Error)
	}
	if p := st.updates[0]; p.FirstName == nil || *p.FirstName != "Asha K" || p.MemberSinceBadge == nil || !*p.MemberSinceBadge {
		t.Fatal("supplied first_name / member_since_badge not passed to the store")
	}
}

func TestUpdateMe_OptionalTextWrongTypeIsRefused(t *testing.T) {
	st := idwSeed()
	if status, _ := idwPut(t, st, `{"last_name":5}`); status != 400 {
		t.Fatalf("status = %d, want 400", status)
	}
	if len(st.updates) != 0 {
		t.Fatal("a malformed body still wrote")
	}
}

// The validation that existed before still applies to a present value.
func TestUpdateMe_PresentValuesAreStillValidated(t *testing.T) {
	for _, body := range []string{
		`{"profile_theme_color":"blue"}`,
		`{"website":"javascript:alert(1)"}`,
		`{"bio":"` + strings.Repeat("a", maxBio+1) + `"}`,
	} {
		st := idwSeed()
		if status, _ := idwPut(t, st, body); status != 400 {
			t.Fatalf("status = %d, want 400 for an invalid present value", status)
		}
		if len(st.updates) != 0 {
			t.Fatal("an invalid value still wrote")
		}
	}
}
