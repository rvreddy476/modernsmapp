package service

import (
	"errors"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// GET /v1/dating/safety/alerts/:id, no database: who may open a panic
// alert, for how long, and when it carries the exact point.

func alertFixture() (*store.PanicIncident, *store.TrustedContact, *store.TrustedContact, time.Time) {
	lat, lng := 12.971599, 77.594566
	created := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	inc := &store.PanicIncident{ID: uuid.New(), UserID: uuid.New(), Status: store.PanicStatusOpen,
		Latitude: &lat, Longitude: &lng, CreatedAt: created}
	version := store.PanicLocationConsentVersion
	consented := &store.TrustedContact{ContactID: uuid.New(), ShareLocationOnPanic: true,
		LocationConsentAt: &created, LocationConsentVersion: &version}
	plain := &store.TrustedContact{ContactID: uuid.New()}
	return inc, consented, plain, created
}

func TestSafetyAlertFor_OnlyTheMembersTrustedContacts(t *testing.T) {
	inc, consented, plain, created := alertFixture()
	contacts := []*store.TrustedContact{consented, plain}
	now := created.Add(time.Minute)
	for name, who := range map[string]uuid.UUID{
		"stranger":   uuid.New(),
		"the member": inc.UserID,
		"nil caller": uuid.Nil,
	} {
		if _, err := safetyAlertFor(inc, contacts, who, now, time.Hour); !errors.Is(err, ErrSafetyAlertNotFound) {
			t.Fatalf("%s: err=%v, want ErrSafetyAlertNotFound", name, err)
		}
	}
	// Backstop: the store never lets a member be their own contact, but a
	// row that said so still would not let them read their own alert.
	self := &store.TrustedContact{ContactID: inc.UserID}
	if _, err := safetyAlertFor(inc, []*store.TrustedContact{self}, inc.UserID, now, time.Hour); !errors.Is(err, ErrSafetyAlertNotFound) {
		t.Fatalf("member listed as own contact: err=%v, want ErrSafetyAlertNotFound", err)
	}
	// A removed (or blocked, which the store leaves out) contact is a stranger.
	if _, err := safetyAlertFor(inc, []*store.TrustedContact{plain}, consented.ContactID, now, time.Hour); !errors.Is(err, ErrSafetyAlertNotFound) {
		t.Fatalf("removed contact: err=%v, want ErrSafetyAlertNotFound", err)
	}
	for _, tc := range contacts {
		a, err := safetyAlertFor(inc, contacts, tc.ContactID, now, time.Hour)
		if err != nil || a.AlertID != inc.ID || a.Status != store.PanicStatusOpen {
			t.Fatalf("contact %s: alert=%+v err=%v", tc.ContactID, a, err)
		}
	}
}

func TestSafetyAlertFor_NotPagedIncidentsAreNotFound(t *testing.T) {
	inc, consented, _, created := alertFixture()
	contacts := []*store.TrustedContact{consented}
	for name, mut := range map[string]func(*store.PanicIncident){
		"suspected abuse": func(p *store.PanicIncident) { p.SuspectedAbuse = true },
		"anonymised":      func(p *store.PanicIncident) { p.Anonymised = true },
	} {
		cp := *inc
		mut(&cp)
		if _, err := safetyAlertFor(&cp, contacts, consented.ContactID, created, time.Hour); !errors.Is(err, ErrSafetyAlertNotFound) {
			t.Fatalf("%s: err=%v, want ErrSafetyAlertNotFound", name, err)
		}
	}
}

func TestSafetyAlertFor_RefusedAfterTheWindow(t *testing.T) {
	inc, consented, _, created := alertFixture()
	contacts := []*store.TrustedContact{consented}
	window := 24 * time.Hour
	a, err := safetyAlertFor(inc, contacts, consented.ContactID, created.Add(window-time.Second), window)
	if err != nil || !a.ExpiresAt.Equal(created.Add(window)) || !a.TriggeredAt.Equal(created) {
		t.Fatalf("inside the window: alert=%+v err=%v", a, err)
	}
	for _, at := range []time.Time{created.Add(window), created.Add(window + time.Hour)} {
		if _, err := safetyAlertFor(inc, contacts, consented.ContactID, at, window); !errors.Is(err, ErrSafetyAlertExpired) {
			t.Fatalf("at %s: err=%v, want ErrSafetyAlertExpired", at, err)
		}
	}
	// A stranger after the window still learns nothing.
	if _, err := safetyAlertFor(inc, contacts, uuid.New(), created.Add(2*window), window); !errors.Is(err, ErrSafetyAlertNotFound) {
		t.Fatalf("stranger after the window: err=%v, want ErrSafetyAlertNotFound", err)
	}
}

func TestSafetyAlertFor_ExactPointOnlyWithConsent(t *testing.T) {
	inc, consented, plain, created := alertFixture()
	contacts := []*store.TrustedContact{consented, plain}
	now := created.Add(time.Minute)

	a, err := safetyAlertFor(inc, contacts, consented.ContactID, now, time.Hour)
	if err != nil || !a.LocationShared || a.Location == nil ||
		a.Location.Latitude != *inc.Latitude || a.Location.Longitude != *inc.Longitude {
		t.Fatalf("consented contact: alert=%+v err=%v, want the exact point", a, err)
	}
	a, err = safetyAlertFor(inc, contacts, plain.ContactID, now, time.Hour)
	if err != nil || a.LocationShared || a.Location != nil {
		t.Fatalf("contact without consent: alert=%+v err=%v, want no location", a, err)
	}
	stale := "panic_location_v0"
	staleTC := &store.TrustedContact{ContactID: uuid.New(), ShareLocationOnPanic: true, LocationConsentAt: &created, LocationConsentVersion: &stale}
	a, err = safetyAlertFor(inc, []*store.TrustedContact{staleTC}, staleTC.ContactID, now, time.Hour)
	if err != nil || a.Location != nil {
		t.Fatalf("stale consent copy: alert=%+v err=%v, want no location", a, err)
	}

	resolved := *inc
	resolved.Status = store.PanicStatusResolved
	a, err = safetyAlertFor(&resolved, contacts, consented.ContactID, now, time.Hour)
	if err != nil || a.Status != store.PanicStatusResolved || a.LocationShared || a.Location != nil {
		t.Fatalf("resolved alert: alert=%+v err=%v, want the status and no location", a, err)
	}

	noPoint := *inc
	noPoint.Latitude, noPoint.Longitude = nil, nil
	a, err = safetyAlertFor(&noPoint, contacts, consented.ContactID, now, time.Hour)
	if err != nil || a.LocationShared || a.Location != nil {
		t.Fatalf("incident without a point: alert=%+v err=%v", a, err)
	}
}

func TestGetSafetyAlert_NilIDsRefusedBeforeTheStore(t *testing.T) {
	svc := New(nil, nil) // any store call would panic
	if _, err := svc.GetSafetyAlert(t.Context(), uuid.Nil, uuid.New()); !errors.Is(err, ErrSafetyAlertNotFound) {
		t.Fatalf("nil caller: err=%v", err)
	}
	if _, err := svc.GetSafetyAlert(t.Context(), uuid.New(), uuid.Nil); !errors.Is(err, ErrSafetyAlertNotFound) {
		t.Fatalf("nil alert: err=%v", err)
	}
}

func TestSafetyConfig_AlertWindowDefault(t *testing.T) {
	svc := New(nil, nil)
	if got := svc.safetyConfig().PanicAlertWindow; got != DefaultPanicAlertWindow {
		t.Fatalf("default window = %s", got)
	}
	svc.SetSafetyConfig(SafetyConfig{PanicAlertWindow: 3 * time.Hour})
	if got := svc.safetyConfig().PanicAlertWindow; got != 3*time.Hour {
		t.Fatalf("configured window = %s", got)
	}
}
