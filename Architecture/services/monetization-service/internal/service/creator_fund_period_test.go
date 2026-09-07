package service

import (
	"testing"
	"time"
)

// The period key is the new idempotency key, and it is also the only
// thing stored on a settlement row that says which days were paid. If it
// does not round-trip exactly, a re-run pays the wrong window.
func TestPeriodKeyRoundTrip(t *testing.T) {
	cases := []struct {
		key       string
		start     string
		end       string
		cadence   string
		wantLabel string
	}{
		{"2026-09", "2026-09-01", "2026-10-01", CadenceMonthly, "September 2026"},
		{"2026-02", "2026-02-01", "2026-03-01", CadenceMonthly, "February 2026"},
		{"2024-02", "2024-02-01", "2024-03-01", CadenceMonthly, "February 2024"}, // leap
		{"2026-09-H1", "2026-09-01", "2026-09-16", CadenceSemiMonthly, "1-15 September 2026"},
		{"2026-09-H2", "2026-09-16", "2026-10-01", CadenceSemiMonthly, "16-30 September 2026"},
		{"2026-02-H2", "2026-02-16", "2026-03-01", CadenceSemiMonthly, "16-28 February 2026"},
		{"2024-02-H2", "2024-02-16", "2024-03-01", CadenceSemiMonthly, "16-29 February 2024"},
		{"2026-12-H2", "2026-12-16", "2027-01-01", CadenceSemiMonthly, "16-31 December 2026"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			p, err := ParsePeriodKey(tc.key)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if p.Key != tc.key {
				t.Errorf("key round trip: got %q want %q", p.Key, tc.key)
			}
			if got := p.Start.Format("2006-01-02"); got != tc.start {
				t.Errorf("start: got %s want %s", got, tc.start)
			}
			if got := p.End.Format("2006-01-02"); got != tc.end {
				t.Errorf("end: got %s want %s", got, tc.end)
			}
			if p.Cadence != tc.cadence {
				t.Errorf("cadence: got %s want %s", p.Cadence, tc.cadence)
			}
			if p.Label() != tc.wantLabel {
				t.Errorf("label: got %q want %q", p.Label(), tc.wantLabel)
			}
		})
	}
}

func TestParsePeriodKeyRejectsGarbage(t *testing.T) {
	for _, key := range []string{"", "2026", "2026-13", "2026-00", "1999-01", "2026-09-H3", "2026-09-01", "september"} {
		if _, err := ParsePeriodKey(key); err == nil {
			t.Errorf("ParsePeriodKey(%q) accepted an invalid key", key)
		}
	}
}

// The two cadences must tile the calendar exactly: every day belongs to
// one period and no day belongs to two. A gap is a day nobody is paid
// for; an overlap is a day paid twice.
func TestPeriodsTileTheCalendarWithoutGapOrOverlap(t *testing.T) {
	for _, cadence := range []string{CadenceMonthly, CadenceSemiMonthly} {
		seen := map[string]string{}
		day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for day.Year() < 2027 {
			p := PeriodContaining(day, cadence)
			if day.Before(p.Start) || !day.Before(p.End) {
				t.Fatalf("%s: %s is not inside its own period %s [%s,%s)",
					cadence, day.Format("2006-01-02"), p.Key,
					p.Start.Format("2006-01-02"), p.End.Format("2006-01-02"))
			}
			if prev, ok := seen[day.Format("2006-01-02")]; ok && prev != p.Key {
				t.Fatalf("%s: %s claimed by both %s and %s", cadence, day, prev, p.Key)
			}
			seen[day.Format("2006-01-02")] = p.Key
			day = day.AddDate(0, 0, 1)
		}
		if len(seen) != 365 {
			t.Errorf("%s: covered %d days of 2026, want 365", cadence, len(seen))
		}
		// Every period's Days() must be exactly the days that map back to it.
		counted := map[string]int{}
		for _, key := range seen {
			counted[key]++
		}
		for key, n := range counted {
			p, err := ParsePeriodKey(key)
			if err != nil {
				t.Fatalf("period key %q from PeriodContaining does not parse: %v", key, err)
			}
			if len(p.Days()) != n {
				t.Errorf("%s: Days() has %d entries but %d days map to it", key, len(p.Days()), n)
			}
		}
	}
}

func TestPreviousPeriodIsTheOneThatJustClosed(t *testing.T) {
	cases := []struct {
		now     string
		cadence string
		want    string
	}{
		{"2026-09-01T04:00:00Z", CadenceMonthly, "2026-08"},
		{"2026-09-30T23:00:00Z", CadenceMonthly, "2026-08"},
		{"2026-01-03T04:00:00Z", CadenceMonthly, "2025-12"},
		{"2026-09-16T04:00:00Z", CadenceSemiMonthly, "2026-09-H1"},
		{"2026-09-02T04:00:00Z", CadenceSemiMonthly, "2026-08-H2"},
		{"2026-01-01T04:00:00Z", CadenceSemiMonthly, "2025-12-H2"},
	}
	for _, tc := range cases {
		now, err := time.Parse(time.RFC3339, tc.now)
		if err != nil {
			t.Fatal(err)
		}
		if got := PreviousPeriod(now, tc.cadence).Key; got != tc.want {
			t.Errorf("PreviousPeriod(%s, %s) = %s, want %s", tc.now, tc.cadence, got, tc.want)
		}
	}
}

func TestNormalizeCadenceDefaultsToMonthly(t *testing.T) {
	for _, in := range []string{"", "weekly", "nonsense", "MONTHLY", " monthly "} {
		if got := NormalizeCadence(in); got != CadenceMonthly {
			t.Errorf("NormalizeCadence(%q) = %s, want monthly", in, got)
		}
	}
	for _, in := range []string{"semimonthly", "SemiMonthly", "twice-monthly", "fortnightly"} {
		if got := NormalizeCadence(in); got != CadenceSemiMonthly {
			t.Errorf("NormalizeCadence(%q) = %s, want semimonthly", in, got)
		}
	}
}

// ---------------------------------------------------------------------------
// The statement has to add up, and the double-pay invariant has to hold
// ---------------------------------------------------------------------------

func healthyStatement() *PeriodStatement {
	st := &PeriodStatement{
		PeriodKey:   "2026-09",
		PeriodLabel: "September 2026",
		FundViews:   12_000,
		Fund: StreamLine{
			Stream: "creator_fund", GrossPaise: 60_000,
			PlatformFeeBps: 3000, PlatformFeePaise: 18_000, NetPaise: 42_000,
			CreditedByThisRun: true,
		},
		Tips: StreamLine{Stream: "tips", Count: 4, GrossPaise: 25_000, PlatformFeeBps: 0, NetPaise: 25_000},
		Subs: StreamLine{Stream: "subscriptions", Count: 2, GrossPaise: 20_000, PlatformFeeBps: 0, NetPaise: 20_000},
	}
	st.GrossPaise = st.Fund.GrossPaise + st.Tips.GrossPaise + st.Subs.GrossPaise
	st.PlatformFeePaise = st.Fund.PlatformFeePaise
	st.NetPaise = st.Fund.NetPaise + st.Tips.NetPaise + st.Subs.NetPaise
	st.CreditedPaise = st.Fund.NetPaise
	st.NewlyCreditedPaise = st.Fund.NetPaise
	st.AlreadyCreditedPaise = st.Tips.NetPaise + st.Subs.NetPaise
	return st
}

func TestStatementArithmeticAcceptsAConsistentStatement(t *testing.T) {
	if err := CheckStatementArithmetic(healthyStatement()); err != nil {
		t.Fatalf("a consistent statement was rejected: %v", err)
	}
}

// This is the test that matters. Crediting tips a second time in the
// monthly run is the specific way this feature could quietly steal from
// the platform (or, with the sign flipped, from the creator). The
// arithmetic check must catch it.
func TestStatementArithmeticCatchesTipsCreditedTwice(t *testing.T) {
	st := healthyStatement()
	// A monthly run that "also credited the tips" would move
	// fund_net + tips into the wallet and still report tips as already
	// credited: credited + already_credited would then exceed net.
	st.CreditedPaise += st.Tips.NetPaise
	err := CheckStatementArithmetic(st)
	if err == nil {
		t.Fatal("a statement that paid the tips twice was accepted")
	}
	t.Logf("caught as expected: %v", err)
}

func TestStatementArithmeticCatchesAMissingStream(t *testing.T) {
	st := healthyStatement()
	st.Subs.GrossPaise = 0 // stream dropped from the total but not from gross
	if err := CheckStatementArithmetic(st); err == nil {
		t.Fatal("a statement whose streams do not sum to gross was accepted")
	}
}

func TestStatementArithmeticCatchesAWrongFeeSplit(t *testing.T) {
	st := healthyStatement()
	st.Fund.PlatformFeePaise = 17_999 // gross - fee no longer equals net
	st.PlatformFeePaise = 17_999
	if err := CheckStatementArithmetic(st); err == nil {
		t.Fatal("a statement whose fee split does not reconcile was accepted")
	}
}

// Settling an overlapping period — 2026-09-H1 after 2026-09 has already
// paid those days — is legitimate and must not look like missing money.
// The fund line is still reported; it is just reported as already paid.
func TestStatementForAPeriodWhoseFundDaysWerePaidElsewhere(t *testing.T) {
	st := healthyStatement()
	st.CreditedPaise = 0
	st.NewlyCreditedPaise = 0
	st.AlreadyCreditedPaise = st.Tips.NetPaise + st.Subs.NetPaise + st.Fund.NetPaise
	if err := CheckStatementArithmetic(st); err != nil {
		t.Fatalf("overlapping-period statement rejected: %v", err)
	}
	if st.NewlyCreditedPaise != 0 {
		t.Error("an overlapping period claimed to have moved money")
	}
}

// Money that is owed and has not moved is reported, not silently dropped
// into one of the other two buckets.
func TestStatementReportsPendingRatherThanLosingIt(t *testing.T) {
	st := healthyStatement()
	st.CreditedPaise = 0
	st.NewlyCreditedPaise = 0
	st.PendingPaise = st.Fund.NetPaise
	if err := CheckStatementArithmetic(st); err != nil {
		t.Fatalf("statement with pending money rejected: %v", err)
	}
	if !contains(ExplainStatement(st), "still pending") {
		t.Errorf("pending money is not mentioned to the creator:\n%s", ExplainStatement(st))
	}
	// Dropping it instead of reporting it must fail.
	st.PendingPaise = 0
	if err := CheckStatementArithmetic(st); err == nil {
		t.Fatal("a statement that lost the unpaid fund money was accepted")
	}
}

// A run cannot report moving more than the settlement is responsible for.
func TestStatementCatchesARunClaimingMoreThanTheSettlementHolds(t *testing.T) {
	st := healthyStatement()
	st.NewlyCreditedPaise = st.CreditedPaise + 1
	if err := CheckStatementArithmetic(st); err == nil {
		t.Fatal("a run that credited more than the settlement total was accepted")
	}
}

// A creator who was tipped but published nothing still has a statement,
// their net is the tips, and the settlement credits zero because the
// money is already theirs.
func TestStatementForTipsOnlyCreatorCreditsNothingNew(t *testing.T) {
	st := &PeriodStatement{
		PeriodKey: "2026-09", PeriodLabel: "September 2026",
		Fund: StreamLine{Stream: "creator_fund", PlatformFeeBps: 3000, CreditedByThisRun: true},
		Tips: StreamLine{Stream: "tips", Count: 3, GrossPaise: 150_000, NetPaise: 150_000},
		Subs: StreamLine{Stream: "subscriptions"},
	}
	st.GrossPaise = 150_000
	st.NetPaise = 150_000
	st.AlreadyCreditedPaise = 150_000
	st.CreditedPaise = 0
	if err := CheckStatementArithmetic(st); err != nil {
		t.Fatalf("tips-only statement rejected: %v", err)
	}
	if st.CreditedPaise != 0 {
		t.Errorf("tips-only settlement moved %d paise; tips were already paid", st.CreditedPaise)
	}
	if st.NetPaise != st.Tips.NetPaise {
		t.Errorf("tips-only creator's net is %d, want the tips total %d", st.NetPaise, st.Tips.NetPaise)
	}
}

// The explanation is what a creator actually reads. It has to name the
// period and say which half of the money is arriving now, otherwise the
// statement total and the wallet movement look like a discrepancy.
func TestExplainStatementNamesThePeriodAndSplitsTheCredit(t *testing.T) {
	got := ExplainStatement(healthyStatement())
	for _, want := range []string{"September 2026", "creator fund", "tip", "subscription", "30%", "already in your wallet"} {
		if !contains(got, want) {
			t.Errorf("explanation is missing %q:\n%s", want, got)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
