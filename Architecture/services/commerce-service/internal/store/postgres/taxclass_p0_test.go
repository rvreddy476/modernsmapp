package postgres

// B9 — missing or invalid GST tax classes silently became a 0% rate.
//
// rateFromClass used to return a bare tax.RateBP whose every failure path
// fell through to `return 0`. Its doc comment claimed the product would be
// rejected; nothing rejected it. A dangling tax_class_id FK, a NULL
// percentage, or a percentage the tax package refuses all produced a 0% line,
// GST was under-collected in paise on every affected order, and the caller
// could not distinguish that from a legitimately zero-rated good.
//
// The review's requirement was a table covering "every accepted rate and
// rejection of missing/invalid tax configuration". The DB-backed half (a real
// dangling FK through lockAndPriceLines) is listed as unexecuted in the
// handover; this covers the resolution rule itself, which is where the
// silent zero was produced.

import (
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/tax"
	"github.com/google/uuid"
)

func f(v float64) *float64 { return &v }

func TestRateFromClass(t *testing.T) {
	someClass := uuid.New()

	cases := []struct {
		name             string
		classID          *uuid.UUID
		cgst, sgst, igst *float64
		wantBP           int
		wantErr          error
	}{
		// ── Accepted: the real Indian GST slabs, both ways of stating them.
		{"igst 5%", &someClass, nil, nil, f(5), 500, nil},
		{"igst 12%", &someClass, nil, nil, f(12), 1200, nil},
		{"igst 18%", &someClass, nil, nil, f(18), 1800, nil},
		{"igst 28%", &someClass, nil, nil, f(28), 2800, nil},
		{"cgst+sgst 2.5+2.5 = 5%", &someClass, f(2.5), f(2.5), nil, 500, nil},
		{"cgst+sgst 6+6 = 12%", &someClass, f(6), f(6), nil, 1200, nil},
		{"cgst+sgst 9+9 = 18%", &someClass, f(9), f(9), nil, 1800, nil},
		{"cgst+sgst 14+14 = 28%", &someClass, f(14), f(14), nil, 2800, nil},

		// ── Accepted: a genuine, explicitly-stated zero rate. This is the
		// case that must stay distinguishable from the broken ones.
		{"explicit zero-rated class", &someClass, nil, nil, f(0), 0, nil},

		// ── Rejected: the silent-zero paths.
		{"no tax class at all", nil, nil, nil, nil, 0, ErrTaxClassMissing},
		{"dangling FK: class id set, all percentages NULL", &someClass, nil, nil, nil, 0, ErrTaxClassMissing},
		{"only cgst, no sgst", &someClass, f(9), nil, nil, 0, ErrTaxClassInvalid},
		{"only sgst, no cgst", &someClass, nil, f(9), nil, 0, ErrTaxClassInvalid},
		{"nonsense igst", &someClass, nil, nil, f(101), 0, ErrTaxClassInvalid},
		{"negative igst", &someClass, nil, nil, f(-5), 0, ErrTaxClassInvalid},
		{"cgst+sgst out of range", &someClass, f(80), f(80), nil, 0, ErrTaxClassInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bp, err := rateFromClass(tc.classID, tc.cgst, tc.sgst, tc.igst)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("want %v, got rate %d with no error — this is the silent zero", tc.wantErr, bp)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if int(bp) != tc.wantBP {
				t.Fatalf("rate = %d basis points, want %d", bp, tc.wantBP)
			}
		})
	}
}

// ─── Negative control (review §4) ────────────────────────────────────
//
// Restore the old fall-through and prove every rejected case above becomes a
// silent 0% instead. If this control stops producing zeros, the table above
// is passing for some reason other than the check it claims to exercise.
func TestNegativeControl_FallThroughMakesBadTaxDataZeroRated(t *testing.T) {
	someClass := uuid.New()

	// Verbatim the previous implementation.
	old := func(cgst, sgst, igst *float64) int {
		if igst != nil && *igst > 0 {
			if bp, err := tax.RateFromPercent(*igst); err == nil {
				return int(bp)
			}
		}
		if cgst != nil && sgst != nil {
			if bp, err := tax.RateFromPercent(*cgst + *sgst); err == nil {
				return int(bp)
			}
		}
		return 0
	}

	broken := []struct {
		name             string
		cgst, sgst, igst *float64
	}{
		{"dangling FK", nil, nil, nil},
		{"only cgst", f(9), nil, nil},
		{"nonsense igst", nil, nil, f(101)},
		{"negative igst", nil, nil, f(-5)},
	}
	for _, b := range broken {
		if got := old(b.cgst, b.sgst, b.igst); got != 0 {
			t.Fatalf("negative control did not reproduce the defect for %q: old code returned %d, want 0",
				b.name, got)
		}
		// And the corrected implementation must refuse the same input.
		if _, err := rateFromClass(&someClass, b.cgst, b.sgst, b.igst); err == nil {
			t.Fatalf("%q: the corrected resolver accepted input the old one zero-rated", b.name)
		}
	}
	t.Log("negative control reproduced the original defect: missing, partial and invalid GST " +
		"configuration all resolved to a 0% rate under the previous fall-through")
}
