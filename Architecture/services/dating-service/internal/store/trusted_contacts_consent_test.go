package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// Audit 2026-10-30 M4: the panic-time consent gate. No database needed.
func TestTrustedContact_SharesPanicLocationNeedsFlagConsentAndCurrentVersion(t *testing.T) {
	now := time.Now()
	current, stale := PanicLocationConsentVersion, "panic_location_v0"
	cases := []struct {
		name string
		tc   *TrustedContact
		want bool
	}{
		{"nil", nil, false},
		{"flag off", &TrustedContact{ShareLocationOnPanic: false, LocationConsentAt: &now, LocationConsentVersion: &current}, false},
		{"legacy flag, no consent record", &TrustedContact{ShareLocationOnPanic: true}, false},
		{"flag and time, no version", &TrustedContact{ShareLocationOnPanic: true, LocationConsentAt: &now}, false},
		{"flag and version, no time", &TrustedContact{ShareLocationOnPanic: true, LocationConsentVersion: &current}, false},
		{"stale copy version", &TrustedContact{ShareLocationOnPanic: true, LocationConsentAt: &now, LocationConsentVersion: &stale}, false},
		{"consented to the current copy", &TrustedContact{ShareLocationOnPanic: true, LocationConsentAt: &now, LocationConsentVersion: &current}, true},
	}
	for _, c := range cases {
		if got := c.tc.SharesPanicLocation(); got != c.want {
			t.Errorf("%s: SharesPanicLocation() = %v, want %v", c.name, got, c.want)
		}
	}
}

// What the app is shown is the effective state: a legacy flag reads off,
// and a consent that no longer counts is not echoed back.
func TestTrustedContact_NormaliseServesTheEffectiveOptIn(t *testing.T) {
	now := time.Now()
	current, stale := PanicLocationConsentVersion, "panic_location_v0"

	legacy := (&TrustedContact{ContactID: uuid.New(), ShareLocationOnPanic: true}).normalise()
	if legacy.ShareLocationOnPanic || legacy.LocationConsentAt != nil || legacy.LocationConsentVersion != nil {
		t.Fatalf("legacy row served as %+v, want off with no consent", legacy)
	}
	old := (&TrustedContact{ShareLocationOnPanic: true, LocationConsentAt: &now, LocationConsentVersion: &stale}).normalise()
	if old.ShareLocationOnPanic || old.LocationConsentAt != nil || old.LocationConsentVersion != nil {
		t.Fatalf("stale consent served as %+v, want off with no consent", old)
	}
	on := (&TrustedContact{ShareLocationOnPanic: true, LocationConsentAt: &now, LocationConsentVersion: &current}).normalise()
	if !on.ShareLocationOnPanic || on.LocationConsentAt == nil || *on.LocationConsentVersion != current {
		t.Fatalf("consented row served as %+v, want on with its consent", on)
	}
}

func TestPanicLocationConsentAcceptsOnlyTheCurrentVersion(t *testing.T) {
	if !PanicLocationConsentAccepted(PanicLocationConsentVersion) {
		t.Fatal("the current version is not accepted")
	}
	for _, v := range []string{"", "panic_location_v0", " " + PanicLocationConsentVersion, "PANIC_LOCATION_V1"} {
		if PanicLocationConsentAccepted(v) {
			t.Errorf("version %q accepted", v)
		}
	}
	if PanicLocationConsentCopy == "" {
		t.Fatal("no consent copy to show")
	}
}
