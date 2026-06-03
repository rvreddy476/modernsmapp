package pages

// legalTransitions encodes the page status state machine (spec §3). `disabled`
// is terminal. Any transition not listed here is illegal and must be rejected
// with 409 Conflict by callers.
var legalTransitions = map[string][]string{
	StatusDraft:         {StatusPendingReview, StatusDisabled},
	StatusPendingReview: {StatusApproved, StatusRejected, StatusDisabled},
	StatusRejected:      {StatusDraft, StatusDisabled},
	StatusApproved:      {StatusSuspended, StatusDisabled},
	StatusSuspended:     {StatusApproved, StatusDisabled},
	StatusDisabled:      {}, // terminal — irreversible
}

// CanTransition reports whether a page may move from `from` to `to`.
func CanTransition(from, to string) bool {
	for _, allowed := range legalTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// IsValidStatus reports whether s is a known page status.
func IsValidStatus(s string) bool {
	_, ok := legalTransitions[s]
	return ok
}

// IsTerminal reports whether a status can never transition further.
func IsTerminal(s string) bool {
	return s == StatusDisabled
}
