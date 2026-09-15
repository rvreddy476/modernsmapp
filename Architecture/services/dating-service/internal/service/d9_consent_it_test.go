// Lane D9 service tests: consent for religion, community, the biometric
// selfie check and Echoes. Setting a field or starting a check without
// consent is refused with *ConsentRequiredError and writes nothing; a
// withdrawal clears what the consent covered; the history is append-only and
// in the data export. Skipped without TEST_PG_DSN; refuses a database not
// named *_test.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

func d9Str(v string) *string { return &v }

func wantConsentRequired(t *testing.T, err error, consentType string) {
	t.Helper()
	var cr *ConsentRequiredError
	if !errors.As(err, &cr) || cr.ConsentType != consentType {
		t.Fatalf("err = %v, want ConsentRequiredError for %s", err, consentType)
	}
}

func TestD9_SensitiveFieldNeedsConsentAndWithdrawalClearsIt(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	u := uuid.New()
	seedActiveProfile(t, st, u)

	_, err := svc.UpsertProfile(ctx, u, store.UpsertProfileParams{Religion: d9Str("Parsi")})
	wantConsentRequired(t, err, ConsentReligion)
	if p, _ := st.GetProfile(ctx, u); p.Religion != nil {
		t.Fatalf("religion written without consent")
	}
	// Clearing needs no consent.
	if _, err := svc.UpsertProfile(ctx, u, store.UpsertProfileParams{Religion: d9Str("")}); err != nil {
		t.Fatalf("clearing religion without consent: %v", err)
	}
	_, err = svc.UpsertProfile(ctx, u, store.UpsertProfileParams{Community: d9Str("Nair")})
	wantConsentRequired(t, err, ConsentCommunity)

	if _, err := svc.SetConsent(ctx, u, ConsentReligion, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetConsent(ctx, u, ConsentReligion, true); err != nil { // repeat adds no entry
		t.Fatal(err)
	}
	p, err := svc.UpsertProfile(ctx, u, store.UpsertProfileParams{Religion: d9Str("Parsi")})
	if err != nil || p.Religion == nil || *p.Religion != "Parsi" {
		t.Fatalf("religion after consent = %v err=%v", p, err)
	}
	// Community is a separate consent.
	_, err = svc.UpsertProfile(ctx, u, store.UpsertProfileParams{Community: d9Str("Nair")})
	wantConsentRequired(t, err, ConsentCommunity)

	view, err := svc.SetConsent(ctx, u, ConsentReligion, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range view.Consents {
		if c.ConsentType == ConsentReligion && c.Granted {
			t.Fatalf("consents view still shows religion granted")
		}
	}
	if p, _ := st.GetProfile(ctx, u); p.Religion != nil {
		t.Fatalf("withdrawing consent left religion = %q", *p.Religion)
	}
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_profiles WHERE user_id = $1 AND (religion IS NOT NULL OR religion_sealed IS NOT NULL)`, u); n != 0 {
		t.Fatalf("withdrawn religion still stored")
	}
	_, err = svc.UpsertProfile(ctx, u, store.UpsertProfileParams{Religion: d9Str("Parsi")})
	wantConsentRequired(t, err, ConsentReligion)

	entries, err := st.ListConsentForUser(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	var grants, withdrawals int
	for _, e := range entries {
		if e.ConsentType == ConsentReligion {
			if e.Granted {
				grants++
			} else {
				withdrawals++
			}
		}
	}
	if grants != 1 || withdrawals != 1 {
		t.Fatalf("religion consent log = %d grants / %d withdrawals, want 1/1", grants, withdrawals)
	}

	// The export carries the consent history.
	raw, err := svc.BuildExportPayload(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	var doc UserDataExport
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range doc.ConsentLog {
		if e.ConsentType == ConsentReligion {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("export consent_log religion entries = %d, want 2", found)
	}
}

func TestD9_UnknownConsentTypeRefused(t *testing.T) {
	svc, _, _ := newD3Svc(t)
	if _, err := svc.SetConsent(context.Background(), uuid.New(), "marketing", true); !errors.Is(err, ErrUnknownConsentType) {
		t.Fatalf("err = %v, want ErrUnknownConsentType", err)
	}
}

func TestD9_SelfieCheckNeedsBiometricConsent(t *testing.T) {
	svc, st, pool := newSelfieSvc(t)
	ctx := context.Background()

	// Never granted: refused before anything else is looked at.
	stranger := uuid.New()
	seedBasicsProfile(t, st, stranger)
	_, err := svc.CreateSelfieChallenge(ctx, stranger)
	wantConsentRequired(t, err, ConsentBiometricSelfie)
	_, err = svc.SubmitSelfie(ctx, stranger, uuid.New(), uuid.New())
	wantConsentRequired(t, err, ConsentBiometricSelfie)

	// Granted (seedPendingSelfie grants it): a challenge is issued.
	u := uuid.New()
	seedPendingSelfie(t, st, u)
	ch, err := svc.CreateSelfieChallenge(ctx, u)
	if err != nil {
		t.Fatalf("challenge with consent: %v", err)
	}

	// Withdrawn: the open challenge is spent and no further check starts.
	if _, err := svc.SetConsent(ctx, u, ConsentBiometricSelfie, false); err != nil {
		t.Fatal(err)
	}
	var used bool
	if err := pool.QueryRow(ctx, `SELECT used_at IS NOT NULL FROM dating_selfie_challenges WHERE id = $1`, ch.ChallengeID).Scan(&used); err != nil || !used {
		t.Fatalf("open challenge after withdrawal: used=%v err=%v, want spent", used, err)
	}
	_, err = svc.SubmitSelfie(ctx, u, uuid.New(), ch.ChallengeID)
	wantConsentRequired(t, err, ConsentBiometricSelfie)
	_, err = svc.CreateSelfieChallenge(ctx, u)
	wantConsentRequired(t, err, ConsentBiometricSelfie)
	_, err = svc.ReviewSelfie(ctx, uuid.New(), u, "approve", "")
	wantConsentRequired(t, err, ConsentBiometricSelfie)
}

func TestD9_EchoesConsentThroughConsentsRoute(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	u := uuid.New()
	seedActiveProfile(t, st, u)
	if _, err := svc.SetConsent(ctx, u, EchoesConsentType, true); err != nil {
		t.Fatal(err)
	}
	if p, err := st.GetPrivacy(ctx, u); err != nil || !p.EchoesConsent {
		t.Fatalf("echoes after grant = %+v err=%v", p, err)
	}
	if err := st.UpsertEchoCache(ctx, u, []byte(`[]`), []byte(`[]`), []byte(`[]`), []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetConsent(ctx, u, EchoesConsentType, false); err != nil {
		t.Fatal(err)
	}
	if p, err := st.GetPrivacy(ctx, u); err != nil || p.EchoesConsent {
		t.Fatalf("echoes after withdrawal = %+v err=%v", p, err)
	}
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_echo_cache WHERE user_id = $1`, u); n != 0 {
		t.Fatalf("echo snapshot kept after withdrawal")
	}
}
