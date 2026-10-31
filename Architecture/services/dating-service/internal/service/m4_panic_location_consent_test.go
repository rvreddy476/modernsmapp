package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Audit 2026-10-30 M4, no database: the paging entry carries the exact
// point only for a contact with a recorded, current consent.
func TestPanicNotifyContact_PointOnlyWithRecordedConsent(t *testing.T) {
	lat, lng := 12.971599, 77.594566
	inc := &store.PanicIncident{Latitude: &lat, Longitude: &lng}
	now := time.Now()
	current, stale := store.PanicLocationConsentVersion, "panic_location_v0"
	contact := uuid.New()

	consented := &store.TrustedContact{ContactID: contact, ShareLocationOnPanic: true, LocationConsentAt: &now, LocationConsentVersion: &current}
	c := panicNotifyContact(consented, inc)
	if c.UserID != contact || c.Latitude == nil || *c.Latitude != lat || c.Longitude == nil || *c.Longitude != lng {
		t.Fatalf("consented contact = %+v, want the point", c)
	}
	for name, tc := range map[string]*store.TrustedContact{
		"not opted in":       {ContactID: contact},
		"legacy flag only":   {ContactID: contact, ShareLocationOnPanic: true},
		"stale copy version": {ContactID: contact, ShareLocationOnPanic: true, LocationConsentAt: &now, LocationConsentVersion: &stale},
		"revoked":            {ContactID: contact, ShareLocationOnPanic: false, LocationConsentAt: nil},
	} {
		c := panicNotifyContact(tc, inc)
		if c.UserID != contact {
			t.Fatalf("%s: not paged: %+v", name, c)
		}
		if c.Latitude != nil || c.Longitude != nil {
			t.Fatalf("%s: got the point: %+v", name, c)
		}
	}
	// No point on the incident: nothing to send even with consent.
	if c := panicNotifyContact(consented, &store.PanicIncident{}); c.Latitude != nil || c.Longitude != nil {
		t.Fatalf("incident without a point leaked one: %+v", c)
	}
}

// Turning sharing on without consent to the current copy is refused before
// anything is read or written (the store is nil here, so any store call
// would panic).
func TestSetTrustedContactPanicLocation_RefusesWithoutCurrentConsent(t *testing.T) {
	svc := New(nil, nil)
	user, contact := uuid.New(), uuid.New()
	for _, v := range []string{"", "panic_location_v0", "yes"} {
		if _, err := svc.SetTrustedContactPanicLocation(context.Background(), user, contact, true, v); !errors.Is(err, ErrPanicLocationConsentRequired) {
			t.Fatalf("consent version %q: err=%v, want ErrPanicLocationConsentRequired", v, err)
		}
	}
	if _, err := svc.SetTrustedContactPanicLocation(context.Background(), uuid.Nil, contact, false, ""); err == nil {
		t.Fatal("nil user accepted")
	}
}
