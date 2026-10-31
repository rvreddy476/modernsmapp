// Audit 2026-10-30 M4 (founder decision 31 Oct) against a real Postgres: a
// trusted contact gets the exact panic location only after the member's
// explicit consent for that contact, recorded with its time, copy version
// and copy; an add never grants it; a revoke applies to the next panic.
// Skipped without TEST_PG_DSN (CI only, *_test databases).
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

type consentRow struct {
	share     bool
	at        *time.Time
	version   *string
	copyText  *string
	revokedAt *time.Time
}

func readConsentRow(t *testing.T, st *store.Store, user, contact uuid.UUID) consentRow {
	t.Helper()
	ctx := context.Background()
	tx, err := st.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var r consentRow
	if err := tx.QueryRow(ctx, `
		SELECT share_location_on_panic, location_consent_at, location_consent_version,
		       location_consent_copy, location_consent_revoked_at
		FROM dating_trusted_contacts WHERE user_id = $1 AND contact_id = $2`, user, contact).
		Scan(&r.share, &r.at, &r.version, &r.copyText, &r.revokedAt); err != nil {
		t.Fatalf("read consent row: %v", err)
	}
	return r
}

func panicPointFor(t *testing.T, svc *Service, incident, contact uuid.UUID) (*float64, *float64) {
	t.Helper()
	pc, err := svc.PanicNotifyContext(context.Background(), incident)
	if err != nil {
		t.Fatalf("notify context: %v", err)
	}
	for _, c := range pc.TrustedContacts {
		if c.UserID == contact {
			return c.Latitude, c.Longitude
		}
	}
	t.Fatalf("contact %s not paged: %+v", contact, pc.TrustedContacts)
	return nil, nil
}

func TestM4_PanicLocationNeedsExplicitPerContactConsent(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	user, contact := uuid.New(), uuid.New()
	seedActiveProfile(t, st, user)
	seedActiveProfile(t, st, contact)
	d3Match(t, svc, user, contact)

	// The current app's add always says true: stored not sharing, no consent.
	tc, err := svc.SetTrustedContact(ctx, user, contact, true)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if tc.ShareLocationOnPanic || tc.LocationConsentAt != nil {
		t.Fatalf("add granted location on panic: %+v", tc)
	}
	if r := readConsentRow(t, st, user, contact); r.share || r.at != nil || r.version != nil {
		t.Fatalf("add stored consent: %+v", r)
	}

	out, err := svc.RecordPanic(ctx, user, PanicRequest{Latitude: floatPtr(12.971599), Longitude: floatPtr(77.594566)})
	if err != nil {
		t.Fatalf("panic: %v", err)
	}
	if lat, lng := panicPointFor(t, svc, out.IncidentID, contact); lat != nil || lng != nil {
		t.Fatalf("contact without consent got the point")
	}

	// The consent action without, or with a stale, copy version is refused
	// and writes nothing.
	for _, v := range []string{"", "panic_location_v0"} {
		if _, err := svc.SetTrustedContactPanicLocation(ctx, user, contact, true, v); !errors.Is(err, ErrPanicLocationConsentRequired) {
			t.Fatalf("consent version %q: err=%v, want ErrPanicLocationConsentRequired", v, err)
		}
	}
	if r := readConsentRow(t, st, user, contact); r.share || r.at != nil {
		t.Fatalf("refused consent wrote: %+v", r)
	}

	// With the current version: recorded with time, version and copy.
	before := time.Now().Add(-time.Minute)
	tc, err = svc.SetTrustedContactPanicLocation(ctx, user, contact, true, store.PanicLocationConsentVersion)
	if err != nil {
		t.Fatalf("consent: %v", err)
	}
	if !tc.ShareLocationOnPanic || tc.LocationConsentAt == nil || tc.LocationConsentVersion == nil ||
		*tc.LocationConsentVersion != store.PanicLocationConsentVersion {
		t.Fatalf("consent response: %+v", tc)
	}
	r := readConsentRow(t, st, user, contact)
	if !r.share || r.at == nil || r.at.Before(before) || r.version == nil || *r.version != store.PanicLocationConsentVersion ||
		r.copyText == nil || *r.copyText != store.PanicLocationConsentCopy {
		t.Fatalf("consent record: %+v", r)
	}
	list, err := svc.ListTrustedContacts(ctx, user)
	if err != nil || len(list) != 1 || !list[0].ShareLocationOnPanic || list[0].LocationConsentAt == nil {
		t.Fatalf("list after consent: %+v err=%v", list, err)
	}
	if lat, lng := panicPointFor(t, svc, out.IncidentID, contact); lat == nil || lng == nil || *lat != 12.971599 || *lng != 77.594566 {
		t.Fatalf("consented contact lacks the point: %v %v", lat, lng)
	}

	// The app re-sending its add (true) does not reset the consent.
	if tc, err := svc.SetTrustedContact(ctx, user, contact, true); err != nil || !tc.ShareLocationOnPanic {
		t.Fatalf("re-add: %+v err=%v, want consent kept", tc, err)
	}

	// Revoke: off, the record cleared, revoked_at kept, and the next page
	// carries no point.
	tc, err = svc.SetTrustedContactPanicLocation(ctx, user, contact, false, "")
	if err != nil || tc.ShareLocationOnPanic || tc.LocationConsentAt != nil {
		t.Fatalf("revoke: %+v err=%v", tc, err)
	}
	if r := readConsentRow(t, st, user, contact); r.share || r.at != nil || r.version != nil || r.copyText != nil || r.revokedAt == nil {
		t.Fatalf("revoke record: %+v", r)
	}
	if lat, lng := panicPointFor(t, svc, out.IncidentID, contact); lat != nil || lng != nil {
		t.Fatalf("revoked contact still gets the point")
	}

	// An add with false on a consented contact also revokes.
	if _, err := svc.SetTrustedContactPanicLocation(ctx, user, contact, true, store.PanicLocationConsentVersion); err != nil {
		t.Fatalf("re-consent: %v", err)
	}
	if tc, err := svc.SetTrustedContact(ctx, user, contact, false); err != nil || tc.ShareLocationOnPanic {
		t.Fatalf("add with false: %+v err=%v, want revoked", tc, err)
	}
	if r := readConsentRow(t, st, user, contact); r.share || r.at != nil {
		t.Fatalf("add with false left consent: %+v", r)
	}
}

func TestM4_PanicLocationConsentRefusals(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	user, contact, stranger := uuid.New(), uuid.New(), uuid.New()
	seedActiveProfile(t, st, user)
	seedActiveProfile(t, st, contact)
	seedActiveProfile(t, st, stranger)
	d3Match(t, svc, user, contact)

	// Not a trusted contact: neither grant nor revoke finds a row.
	if _, err := svc.SetTrustedContactPanicLocation(ctx, user, stranger, true, store.PanicLocationConsentVersion); err == nil {
		t.Fatalf("consent for a non-contact succeeded")
	}
	if _, err := svc.SetTrustedContactPanicLocation(ctx, user, stranger, false, ""); !errors.Is(err, store.ErrTrustedContactNotFound) {
		t.Fatalf("revoke for a non-contact: err=%v, want ErrTrustedContactNotFound", err)
	}

	if _, err := svc.SetTrustedContact(ctx, user, contact, false); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.SetTrustedContactPanicLocation(ctx, user, contact, true, store.PanicLocationConsentVersion); err != nil {
		t.Fatalf("consent: %v", err)
	}
	// After a block, consent cannot be given, but it can always be revoked.
	if err := st.BlockUser(ctx, contact, user); err != nil {
		t.Fatalf("block: %v", err)
	}
	if _, err := svc.SetTrustedContactPanicLocation(ctx, user, contact, true, store.PanicLocationConsentVersion); err == nil {
		t.Fatalf("consent across a block succeeded")
	}
	if tc, err := svc.SetTrustedContactPanicLocation(ctx, user, contact, false, ""); err != nil || tc.ShareLocationOnPanic {
		t.Fatalf("revoke across a block: %+v err=%v", tc, err)
	}
}
