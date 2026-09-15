package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/service"
	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PUT /v1/profiles/me over the real router and the real service, with only
// storage faked. Covers the wire concerns the service cannot see: formats,
// an explicit null versus an absent field, and the 422 envelope.

type idwStore struct {
	profile    *store.Profile
	registered *time.Time
	updates    []store.UpdateProfileParams
}

func (s *idwStore) GetProfile(context.Context, uuid.UUID) (*store.Profile, error) {
	cp := *s.profile
	return &cp, nil
}

func (s *idwStore) GetRegistrationDOB(context.Context, uuid.UUID) (*time.Time, error) {
	return s.registered, nil
}

func (s *idwStore) UpdateProfile(_ context.Context, _ uuid.UUID, p store.UpdateProfileParams) (*store.Profile, error) {
	s.updates = append(s.updates, p)
	next := *s.profile
	if p.DoB != nil {
		d := *p.DoB
		next.DoB = &d
	}
	if p.FirstName != nil {
		n := *p.FirstName
		next.FirstName = &n
	}
	if p.Bio != nil {
		next.Bio = *p.Bio
	}
	s.profile = &next
	return &next, nil
}

var (
	idwNow  = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	idwUser = uuid.MustParse("5b0c3a6e-2222-4c1d-9e2f-000000000002")
)

func idwDay(s string) *time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &d
}

func idwSeed() *idwStore {
	first := "Asha"
	return &idwStore{profile: &store.Profile{UserID: idwUser, DisplayName: "Asha", FirstName: &first, DoB: idwDay("1995-05-05")}}
}

type idwEnvelope struct {
	Error *struct {
		Code    string `json:"code"`
		Details struct {
			Field string `json:"field"`
		} `json:"details"`
	} `json:"error"`
}

func idwPut(t *testing.T, st *idwStore, body string) (int, idwEnvelope) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(nil, nil, nil, nil, logger).WithProfileWriteStore(st, func() time.Time { return idwNow })
	pass := func(c *gin.Context) { c.Next() }
	New(svc, logger).RegisterRoutes(r, pass, pass)

	req := httptest.NewRequest(http.MethodPut, "/v1/profiles/me", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", idwUser.String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	var env idwEnvelope
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not the JSON envelope: %v", err)
	}
	return resp.Code, env
}

func idwStoredDOB(st *idwStore) string {
	if st.profile.DoB == nil {
		return ""
	}
	return st.profile.DoB.Format("2006-01-02")
}

func TestUpdateMe_DOBValidation(t *testing.T) {
	cases := []struct {
		name       string
		registered *time.Time
		dobJSON    string // "" means the key is absent
		wantStatus int
		wantCode   string
		wantStored string
	}{
		{"valid 18+ date accepted", nil, `"1990-01-01"`, 200, "", "1990-01-01"},
		{"UTC-midnight timestamp (Android) accepted", nil, `"1990-01-01T00:00:00Z"`, 200, "", "1990-01-01"},
		{"17 years and 364 days refused", nil, `"2008-08-12"`, 422, service.CodeDOBUnderMinimumAge, "1995-05-05"},
		{"exactly 18 today accepted", nil, `"2008-08-11"`, 200, "", "2008-08-11"},
		{"future date refused", nil, `"2026-08-12"`, 422, service.CodeDOBInFuture, "1995-05-05"},
		{"1899 refused", nil, `"1899-12-31"`, 422, service.CodeDOBTooEarly, "1995-05-05"},
		{"impossible date 2025-02-30 refused", nil, `"2025-02-30"`, 422, service.CodeDOBInvalid, "1995-05-05"},
		{"DD/MM/YYYY refused", nil, `"17/03/1990"`, 422, service.CodeDOBInvalid, "1995-05-05"},
		{"offset timestamp refused", nil, `"1990-03-17T00:00:00+05:30"`, 422, service.CodeDOBInvalid, "1995-05-05"},
		{"IST midnight rendered in UTC refused", nil, `"1990-03-16T18:30:00Z"`, 422, service.CodeDOBInvalid, "1995-05-05"},
		{"number refused", nil, `19900317`, 422, service.CodeDOBInvalid, "1995-05-05"},
		{"null on a profile with a DOB refused", nil, `null`, 422, service.CodeDOBRequired, "1995-05-05"},
		{"empty string refused", nil, `""`, 422, service.CodeDOBRequired, "1995-05-05"},
		{"absent dob leaves it unchanged", nil, "", 200, "", "1995-05-05"},
		{"more than a year from the registration DOB refused", idwDay("1995-05-05"), `"1993-01-01"`, 422, service.CodeDOBMismatchRegistration, "1995-05-05"},
		{"a three-month correction accepted", idwDay("1995-05-05"), `"1995-08-05"`, 200, "", "1995-08-05"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := idwSeed()
			st.registered = tc.registered
			body := `{"display_name":"Asha","bio":"hi"}`
			if tc.dobJSON != "" {
				body = `{"display_name":"Asha","bio":"hi","dob":` + tc.dobJSON + `}`
			}
			status, env := idwPut(t, st, body)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			if tc.wantCode != "" {
				if env.Error == nil || env.Error.Code != tc.wantCode || env.Error.Details.Field != "dob" {
					t.Fatalf("error = %+v, want code %s on field dob", env.Error, tc.wantCode)
				}
				if len(st.updates) != 0 {
					t.Fatal("a refused request still wrote")
				}
			}
			if got := idwStoredDOB(st); got != tc.wantStored {
				t.Fatalf("stored DOB = %q, want %q", got, tc.wantStored)
			}
			if tc.dobJSON == "" && st.updates[0].DoB != nil {
				t.Fatal("an absent dob reached the store as a value")
			}
		})
	}
}

func TestUpdateMe_FirstNameValidation(t *testing.T) {
	for name, firstJSON := range map[string]string{
		"empty":              `""`,
		"whitespace only":    `"   "`,
		"NUL control char":   `"Asha` + `\` + `u0000"`,
		"tab control char":   `"As\tha"`,
		"newline prefix":     `"\nAsha"`,
		"digits":             `"Asha2"`,
		"over 50 characters": `"` + strings.Repeat("a", 51) + `"`,
	} {
		t.Run(name, func(t *testing.T) {
			st := idwSeed()
			status, env := idwPut(t, st, `{"display_name":"Asha","first_name":`+firstJSON+`}`)
			if status != 422 || env.Error == nil || env.Error.Code != service.CodeFirstNameInvalid || env.Error.Details.Field != "first_name" {
				t.Fatalf("status=%d error=%+v, want 422 %s on first_name", status, env.Error, service.CodeFirstNameInvalid)
			}
			if *st.profile.FirstName != "Asha" || len(st.updates) != 0 {
				t.Fatal("a refused first name was written")
			}
		})
	}

	st := idwSeed()
	if status, env := idwPut(t, st, `{"display_name":"Asha","first_name":"  Asha K  "}`); status != 200 {
		t.Fatalf("valid first name: status=%d error=%+v", status, env.Error)
	}
	if *st.profile.FirstName != "Asha K" {
		t.Fatalf("stored first name = %q, want trimmed", *st.profile.FirstName)
	}
}

// A stored first name from before the rule (60 characters, digits) must not
// 422 every save: Android always resends first_name with the rest of the form.
func TestUpdateMe_UnchangedLegacyFirstName(t *testing.T) {
	legacy := strings.Repeat("Asha1", 12)
	seed := func() *idwStore {
		st := idwSeed()
		st.profile.FirstName = &legacy
		return st
	}
	cases := []struct {
		name       string
		stored     func() *idwStore
		firstName  string
		wantStatus int
		wantCode   string
		wantStored string
		wantBio    string
	}{
		{"unchanged legacy name with a bio change", seed, legacy, 200, "", legacy, "new bio"},
		{"changed to another invalid value", seed, legacy + "2", 422, service.CodeFirstNameInvalid, legacy, ""},
		{"changed to a valid value", seed, "Asha K", 200, "", "Asha K", "new bio"},
		{"unchanged valid name", idwSeed, "Asha", 200, "", "Asha", "new bio"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.stored()
			status, env := idwPut(t, st, `{"bio":"new bio","first_name":"`+tc.firstName+`"}`)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d (error %+v)", status, tc.wantStatus, env.Error)
			}
			if tc.wantCode != "" {
				if env.Error == nil || env.Error.Code != tc.wantCode || env.Error.Details.Field != "first_name" {
					t.Fatalf("error = %+v, want %s on first_name", env.Error, tc.wantCode)
				}
				if len(st.updates) != 0 {
					t.Fatal("a refused request still wrote")
				}
			}
			if *st.profile.FirstName != tc.wantStored {
				t.Fatal("stored first name is not the expected one")
			}
			if st.profile.Bio != tc.wantBio {
				t.Fatalf("bio = %q, want %q", st.profile.Bio, tc.wantBio)
			}
		})
	}
}
