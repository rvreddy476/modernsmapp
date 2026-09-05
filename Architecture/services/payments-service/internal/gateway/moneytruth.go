package gateway

// One provider-money validation policy, for every path that turns a
// provider's word into local money state.
//
// C3-LB-1. Before this file the same rule was written three times and the
// webhook path had a fourth, weaker version of it. Review 3 found two live
// holes that existed only because the rule was duplicated rather than shared:
//
//   - immediate CreateOrder ambiguity recovery attached the first non-blank
//     recovered order id WITHOUT comparing amount or currency, so a recovered
//     order belonging to a different intent could be attached to this one and
//     the buyer shown someone else's amount;
//   - webhook normalization ran the currency through `defaultINR`, so an
//     authentic-but-incomplete payload arrived carrying a currency the
//     provider never sent, and the comparison downstream then found it equal.
//
// Both are the same defect in different clothes: a check that cannot fire.
// One accepted a fact it never examined; the other manufactured the fact it
// was about to examine. So there is now exactly one implementation, and every
// money path calls it.
//
// The policy is deliberately total. There is no "unknown" or "assume" branch,
// because every such branch in this codebase has eventually been the bug: an
// amount without a currency is a number, and a signature proves who sent the
// bytes, never that an omitted field meant INR.

import (
	"errors"
	"fmt"
	"strings"
)

// ErrProviderMoneyUnverified means a provider fact could not be reconciled
// with what we hold locally. It is returned by VerifyProviderMoney and wrapped
// with the specific reason.
//
// Callers branch on it to distinguish "the provider disagrees with us" — which
// must never advance money state — from a transport failure, which may be
// retried.
var ErrProviderMoneyUnverified = errors.New("gateway: provider money fact does not verify")

// MoneyCheck is one provider fact awaiting verification against local record.
type MoneyCheck struct {
	// Operation names the path, so a refusal in the log says which of the
	// four callers refused. Free text; it appears only in the error.
	Operation string

	// IdentifierKind describes what Identifier is ("provider payment id",
	// "provider order id"), and Identifier is the value. The required
	// identifier differs per operation — recovery has an order, a capture
	// has a payment — so the caller names it rather than the policy
	// guessing which field should be populated.
	IdentifierKind string
	Identifier     string

	// Provider is what the PSP stated. Expected is what we hold locally.
	Provider Money
	Expected Money
}

// VerifyProviderMoney applies the whole policy, in one place.
//
// Every clause refuses rather than defaults:
//
//  1. the operation's identifier is present;
//  2. the provider named a positive amount;
//  3. it equals ours exactly — no tolerance, no rounding;
//  4. BOTH currencies are present, ours included;
//  5. they are equal after canonical case folding.
//
// Clause 4 is the one that keeps being lost. "The provider did not tell us"
// and "the provider said INR" are different facts, and every version of this
// code that collapsed them turned the comparison into a no-op.
func VerifyProviderMoney(c MoneyCheck) error {
	kind := c.IdentifierKind
	if kind == "" {
		kind = "provider identifier"
	}
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s: %s", ErrProviderMoneyUnverified, c.Operation,
			fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(c.Identifier) == "" {
		return fail("%s is blank", kind)
	}
	if c.Provider.Minor <= 0 {
		return fail("provider reported no amount (%d minor) for %s %q",
			c.Provider.Minor, kind, c.Identifier)
	}
	if c.Provider.Minor != c.Expected.Minor {
		return fail("provider amount %d does not equal our %d for %s %q",
			c.Provider.Minor, c.Expected.Minor, kind, c.Identifier)
	}
	if strings.TrimSpace(c.Provider.Currency) == "" {
		// Not a formality. An amount is a quantity until a currency says
		// what of: 118000 minor is ₹1,180 or $1,180, and the difference is
		// decided by the field this clause refuses to invent.
		return fail("provider stated no currency for %s %q", kind, c.Identifier)
	}
	if strings.TrimSpace(c.Expected.Currency) == "" {
		return fail("we hold no currency to compare %s %q against", kind, c.Identifier)
	}
	if !strings.EqualFold(strings.TrimSpace(c.Provider.Currency), strings.TrimSpace(c.Expected.Currency)) {
		return fail("provider currency %q does not equal our %q for %s %q",
			c.Provider.Currency, c.Expected.Currency, kind, c.Identifier)
	}
	return nil
}
