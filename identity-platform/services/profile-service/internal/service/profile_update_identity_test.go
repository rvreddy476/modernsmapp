package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
)

// Service.UpdateProfile over a fake store: the real validation, the real
// normalisation of params, and the audit log line.

type fakeProfileWrites struct {
	profile    *store.Profile
	registered *time.Time
	regErr     error
	updates    []store.UpdateProfileParams
}

func (f *fakeProfileWrites) GetProfile(context.Context, uuid.UUID) (*store.Profile, error) {
	if f.profile == nil {
		return nil, nil
	}
	cp := *f.profile
	return &cp, nil
}

func (f *fakeProfileWrites) GetRegistrationDOB(context.Context, uuid.UUID) (*time.Time, error) {
	return f.registered, f.regErr
}

// UpdateProfile applies the store's nil-means-unchanged rule for the two
// identity fields.
func (f *fakeProfileWrites) UpdateProfile(_ context.Context, _ uuid.UUID, p store.UpdateProfileParams) (*store.Profile, error) {
	f.updates = append(f.updates, p)
	next := *f.profile
	if p.DoB != nil {
		d := *p.DoB
		next.DoB = &d
	}
	if p.FirstName != nil {
		n := *p.FirstName
		next.FirstName = &n
	}
	f.profile = &next
	return &next, nil
}

var testUserID = uuid.MustParse("5b0c3a6e-1111-4c1d-9e2f-000000000001")

func dateOf(s string) *time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &d
}

func strOf(s string) *string { return &s }

func storedProfile() *store.Profile {
	return &store.Profile{UserID: testUserID, DisplayName: "Asha", FirstName: strOf("Asha"), DoB: dateOf("1995-05-05")}
}

func identityService(f *fakeProfileWrites) (*Service, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
	return New(nil, nil, nil, nil, logger).WithProfileWriteStore(f, func() time.Time { return refNow }), &buf
}

func storedDOB(f *fakeProfileWrites) string {
	if f.profile.DoB == nil {
		return ""
	}
	return f.profile.DoB.Format("2006-01-02")
}

func TestUpdateProfile_DOBRules(t *testing.T) {
	cases := []struct {
		name       string
		registered *time.Time
		dob        string
		wantCode   string
	}{
		{"valid 18+ date accepted", nil, "1990-01-01", ""},
		{"17 years and 364 days refused", nil, "2008-08-12", CodeDOBUnderMinimumAge},
		{"exactly 18 today accepted", nil, "2008-08-11", ""},
		{"future date refused", nil, "2026-08-12", CodeDOBInFuture},
		{"1899 refused", nil, "1899-12-31", CodeDOBTooEarly},
		{"more than a year from the registration DOB refused", dateOf("1995-05-05"), "1993-01-01", CodeDOBMismatchRegistration},
		{"more than a year later than the registration DOB refused", dateOf("1995-05-05"), "1996-05-06", CodeDOBMismatchRegistration},
		{"a three-month correction accepted", dateOf("1995-05-05"), "1995-08-05", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeProfileWrites{profile: storedProfile(), registered: tc.registered}
			svc, _ := identityService(f)
			_, err := svc.UpdateProfile(context.Background(), testUserID,
				store.UpdateProfileParams{DisplayName: strOf("Asha"), DoB: dateOf(tc.dob)})
			if got := fieldCode(t, err); got != tc.wantCode {
				t.Fatalf("got code %q, want %q", got, tc.wantCode)
			}
			if tc.wantCode == "" {
				if storedDOB(f) != tc.dob {
					t.Fatalf("stored DOB = %s, want %s", storedDOB(f), tc.dob)
				}
				return
			}
			if len(f.updates) != 0 {
				t.Fatalf("a refused DOB still reached the store (%d writes)", len(f.updates))
			}
			if storedDOB(f) != "1995-05-05" {
				t.Fatalf("stored DOB moved to %s on a refusal", storedDOB(f))
			}
		})
	}
}

func TestUpdateProfile_AbsentDOBLeavesItUnchanged(t *testing.T) {
	f := &fakeProfileWrites{profile: storedProfile()}
	svc, _ := identityService(f)
	if _, err := svc.UpdateProfile(context.Background(), testUserID,
		store.UpdateProfileParams{DisplayName: strOf("Asha"), Bio: strOf("hello")}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if len(f.updates) != 1 || f.updates[0].DoB != nil {
		t.Fatalf("an absent dob must reach the store as nil (unchanged), got %+v", f.updates)
	}
	if storedDOB(f) != "1995-05-05" {
		t.Fatalf("stored DOB = %q, want it unchanged", storedDOB(f))
	}
}

// Android resends the whole form on every save. Resending the stored date is
// not a DOB write, so an account holding an older out-of-policy value can
// still edit its bio, and nothing is audited as a change.
func TestUpdateProfile_UnchangedDOBResendIsNotAWrite(t *testing.T) {
	f := &fakeProfileWrites{profile: storedProfile()}
	f.profile.DoB = dateOf("2010-01-01")
	svc, logs := identityService(f)
	if _, err := svc.UpdateProfile(context.Background(), testUserID,
		store.UpdateProfileParams{DisplayName: strOf("Asha"), DoB: dateOf("2010-01-01")}); err != nil {
		t.Fatalf("resending the stored DOB was refused: %v", err)
	}
	if f.updates[0].DoB != nil {
		t.Fatal("an unchanged DOB was written")
	}
	if strings.Contains(logs.String(), "date of birth changed") {
		t.Fatal("an unchanged DOB was audited as a change")
	}
}

func TestUpdateProfile_DOBChangeIsAuditedWithYearsOnly(t *testing.T) {
	f := &fakeProfileWrites{profile: storedProfile()}
	svc, logs := identityService(f)
	if _, err := svc.UpdateProfile(context.Background(), testUserID,
		store.UpdateProfileParams{DisplayName: strOf("Asha"), DoB: dateOf("1990-01-01")}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	out := logs.String()
	for _, want := range []string{"level=INFO", "date of birth changed", "user_id=" + testUserID.String(), "old_year=1995", "new_year=1990"} {
		if !strings.Contains(out, want) {
			t.Errorf("audit line missing %q", want)
		}
	}
	for _, leak := range []string{"1990-01-01", "1995-05-05", "05-05", "01-01"} {
		if strings.Contains(out, leak) {
			t.Errorf("audit log leaked a full date fragment %q", leak)
		}
	}
}

func TestUpdateProfile_RegistrationReadFailureFailsClosed(t *testing.T) {
	f := &fakeProfileWrites{profile: storedProfile(), regErr: errors.New("db down")}
	svc, _ := identityService(f)
	_, err := svc.UpdateProfile(context.Background(), testUserID,
		store.UpdateProfileParams{DisplayName: strOf("Asha"), DoB: dateOf("1995-08-05")})
	if err == nil {
		t.Fatal("an unreadable registration record let the DOB change through")
	}
	if _, isField := IsFieldError(err); isField {
		t.Fatalf("a storage failure was reported as a validation failure: %v", err)
	}
	if len(f.updates) != 0 {
		t.Fatal("the write happened anyway")
	}
}

func TestUpdateProfile_FirstNameRules(t *testing.T) {
	for _, bad := range []string{"", "   ", "Asha\x00", "As\tha", "\t"} {
		f := &fakeProfileWrites{profile: storedProfile()}
		svc, _ := identityService(f)
		_, err := svc.UpdateProfile(context.Background(), testUserID,
			store.UpdateProfileParams{DisplayName: strOf("Asha"), FirstName: strOf(bad)})
		if got := fieldCode(t, err); got != CodeFirstNameInvalid {
			t.Errorf("first_name %q: got %q, want %s", bad, got, CodeFirstNameInvalid)
		}
		if len(f.updates) != 0 {
			t.Errorf("first_name %q reached the store", bad)
		}
	}

	f := &fakeProfileWrites{profile: storedProfile()}
	svc, _ := identityService(f)
	if _, err := svc.UpdateProfile(context.Background(), testUserID,
		store.UpdateProfileParams{DisplayName: strOf("Asha"), FirstName: strOf("  Asha K  ")}); err != nil {
		t.Fatalf("valid first name refused: %v", err)
	}
	if got := *f.profile.FirstName; got != "Asha K" {
		t.Fatalf("first name stored as %q, want trimmed", got)
	}
}

// The Android form sends first_name "" for an account that never had one.
// That is not clearing anything, so it must not block the rest of the save.
func TestUpdateProfile_EmptyFirstNameOnAnAccountWithoutOneIsUnchanged(t *testing.T) {
	for name, stored := range map[string]*string{"empty": strOf(""), "null": nil} {
		t.Run(name, func(t *testing.T) {
			f := &fakeProfileWrites{profile: storedProfile()}
			f.profile.FirstName = stored
			svc, _ := identityService(f)
			if _, err := svc.UpdateProfile(context.Background(), testUserID,
				store.UpdateProfileParams{DisplayName: strOf("Asha"), Bio: strOf("hi"), FirstName: strOf("")}); err != nil {
				t.Fatalf("refused: %v", err)
			}
			if f.updates[0].FirstName != nil {
				t.Fatal("an empty first name was written")
			}
		})
	}
}
