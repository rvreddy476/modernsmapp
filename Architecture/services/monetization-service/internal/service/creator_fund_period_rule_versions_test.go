package service

import (
	"testing"

	"github.com/atpost/monetization-service/internal/store/postgres"
)

// One accrual rule per period is the normal case. A period whose fund
// rows were priced by two different rules (a rule change mid-month, or
// pre-019 'cf-0' rows beside 'cf-1' ones) is allowed, but it must be
// visible on the statement: rule_versions lists every version the rows
// carry, or the statement is refused (plan Phase 5C).
func TestStatementRefusesMixedRuleVersions(t *testing.T) {
	mixed := func() *PeriodStatement {
		st := healthyStatement()
		st.Fund.Count = 2
		st.FundBreakdown = []postgres.EarningsDailyBreakdown{
			{ContentType: "flick", GrossPaise: 30_000, NetPaise: 21_000, RuleVersion: "cf-1"},
			{ContentType: "flick", GrossPaise: 30_000, NetPaise: 21_000, RuleVersion: "cf-0"},
		}
		return st
	}

	st := mixed()
	if err := CheckStatementArithmetic(st); err == nil {
		t.Fatal("a statement whose fund rows mix cf-0 and cf-1 with no rule_versions was accepted")
	} else {
		t.Logf("refused as expected: %v", err)
	}

	st = mixed()
	st.RuleVersions = []string{"cf-1"}
	if err := CheckStatementArithmetic(st); err == nil {
		t.Fatal("rule_versions that omits one of the versions present was accepted")
	}

	st = mixed()
	st.RuleVersions = []string{"cf-0", "cf-1"}
	if err := CheckStatementArithmetic(st); err != nil {
		t.Fatalf("a mix that the statement lists in full was refused: %v", err)
	}

	// The normal case: one rule, listed or not, is fine either way.
	st = healthyStatement()
	st.Fund.Count = 1
	st.FundBreakdown = []postgres.EarningsDailyBreakdown{{ContentType: "flick", GrossPaise: 60_000, NetPaise: 42_000, RuleVersion: "cf-1"}}
	if err := CheckStatementArithmetic(st); err != nil {
		t.Fatalf("a single-rule statement was refused: %v", err)
	}
	st.RuleVersions = []string{"cf-1"}
	if err := CheckStatementArithmetic(st); err != nil {
		t.Fatalf("a single-rule statement that lists its rule was refused: %v", err)
	}
}
