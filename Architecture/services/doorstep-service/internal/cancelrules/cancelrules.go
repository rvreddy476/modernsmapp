// Package cancelrules decides what cancelling a booking costs the customer,
// from doorstep.cancellation_rules (placeholders editable as data). Pure: the
// store loads the city's active rules.
//
// Stage comes from the booking status: nothing paid yet (pending_payment) is
// always free; confirmed is "unassigned" (free before assignment); assigned,
// en_route and arrived are their own stages; in_progress is the stage the
// seed refuses (no cancel after the start OTP). The first active rule —
// category-specific rules first, then sort_order, then id — whose stage
// matches and whose minutes_before_lt is NULL or greater than the minutes
// left to the slot decides. No matching rule is free (fail toward the
// customer, never toward a charge nobody configured). The fee never exceeds
// what was paid.
package cancelrules

import (
	"sort"
	"strconv"

	"github.com/google/uuid"
)

// Rule is one doorstep.cancellation_rules row.
type Rule struct {
	ID              uuid.UUID
	CategoryID      *uuid.UUID
	Stage           string
	MinutesBeforeLT *int
	FeePaise        int64
	Allowed         bool
	SortOrder       int
}

// Stages.
const (
	StageUnpaid     = "unpaid"
	StageUnassigned = "unassigned"
	StageAssigned   = "assigned"
	StageEnRoute    = "en_route"
	StageArrived    = "arrived"
	StageInProgress = "in_progress"
)

// Stage maps a booking status to its cancellation stage; ok is false for a
// status nobody may cancel from (completed, cancelled, expired, no-shows,
// awaiting extras payment).
func Stage(status string) (string, bool) {
	switch status {
	case "pending_payment":
		return StageUnpaid, true
	case "confirmed":
		return StageUnassigned, true
	case "assigned":
		return StageAssigned, true
	case "en_route":
		return StageEnRoute, true
	case "arrived":
		return StageArrived, true
	case "in_progress":
		return StageInProgress, true
	}
	return "", false
}

// Verdict is the cancellation preview.
type Verdict struct {
	Allowed     bool
	FeePaise    int64
	RefundPaise int64
	Rule        string
}

// Decide applies the rules. category is the booking's category, minutesLeft
// the whole minutes from now to the slot start (negative once it began), paid
// what the customer has paid and not had refunded.
func Decide(rules []Rule, category uuid.UUID, stage string, minutesLeft int, paid int64) Verdict {
	if paid < 0 {
		paid = 0
	}
	if stage == StageUnpaid {
		return Verdict{Allowed: true, Rule: "not_paid"}
	}
	ordered := make([]Rule, 0, len(rules))
	for _, r := range rules {
		if r.Stage != stage {
			continue
		}
		if r.CategoryID != nil && *r.CategoryID != category {
			continue
		}
		ordered = append(ordered, r)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		ci, cj := ordered[i].CategoryID != nil, ordered[j].CategoryID != nil
		if ci != cj {
			return ci
		}
		if ordered[i].SortOrder != ordered[j].SortOrder {
			return ordered[i].SortOrder < ordered[j].SortOrder
		}
		return ordered[i].ID.String() < ordered[j].ID.String()
	})
	for _, r := range ordered {
		if r.MinutesBeforeLT != nil && *r.MinutesBeforeLT <= minutesLeft {
			continue
		}
		if !r.Allowed {
			return Verdict{Allowed: false, Rule: stage}
		}
		fee := r.FeePaise
		if fee > paid {
			fee = paid
		}
		return Verdict{Allowed: true, FeePaise: fee, RefundPaise: paid - fee, Rule: name(stage, r)}
	}
	free := "free"
	if stage == StageUnassigned {
		free = "free_before_assignment"
	}
	return Verdict{Allowed: true, RefundPaise: paid, Rule: free}
}

// name is the rule label the customer app shows (contract examples:
// free_before_assignment, lt_3h, lt_1h, en_route, arrived).
func name(stage string, r Rule) string {
	switch {
	case stage == StageUnassigned && r.FeePaise == 0:
		return "free_before_assignment"
	case r.MinutesBeforeLT != nil:
		m := *r.MinutesBeforeLT
		if m%60 == 0 {
			return "lt_" + strconv.Itoa(m/60) + "h"
		}
		return "lt_" + strconv.Itoa(m) + "m"
	case r.FeePaise == 0:
		return "free"
	}
	return stage
}
