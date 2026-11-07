// Rupee to paise conversion at the write boundary, and variant repricing:
// the rupee/paise column pairs and the normaliser that reconciles them.
// Moved out of store.go unchanged.

package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// ─── Rupee → paise, in exactly one place ─────────────────────────────
//
// The product/variant HTTP contract takes rupee floats (`mrp`,
// `selling_price`, `cost_price`) and the columns behind them are
// NUMERIC(12,2). Every pricing path downstream reads the integer `_minor`
// columns. Something has to convert, and it must happen once, at the write
// boundary, so a rounding choice made here cannot be made differently
// somewhere else.
//
// math.Round, not a truncating int64 cast: 12.99 arrives from JSON as
// 12.989999999999998, and `int64(12.989999999999998*100)` is 1298 — a paisa
// lost on a price nobody would ever suspect. Rounding gives 1299.

// rupeesToMinor converts a rupee amount to paise.
func rupeesToMinor(rupees float64) int64 { return int64(math.Round(rupees * 100)) }

// rupeesToMinorPtr converts an optional rupee amount, preserving nil.
//
// nil means "not stated" — a variant with no cost price — and must stay
// distinguishable from a stated zero.
func rupeesToMinorPtr(rupees *float64) *int64 {
	if rupees == nil {
		return nil
	}
	m := rupeesToMinor(*rupees)
	return &m
}

// anyRupeesToMinor converts a value arriving from an untyped JSON patch.
//
// `PATCH /variants/:id` binds into map[string]any, so a price is whatever
// encoding/json produced. A non-numeric value is refused rather than
// coerced: silently treating "1299" or true as zero would reprice the
// variant to free, which is the defect this whole helper exists to close.
func anyRupeesToMinor(v any) (any, error) {
	switch n := v.(type) {
	case nil:
		return nil, nil
	case float64:
		return rupeesToMinor(n), nil
	case float32:
		return rupeesToMinor(float64(n)), nil
	case int:
		return int64(n) * 100, nil
	case int64:
		return n * 100, nil
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return nil, fmt.Errorf("price %q is not a number", n.String())
		}
		return rupeesToMinor(f), nil
	default:
		return nil, fmt.Errorf("price must be a number, got %T", v)
	}
}

// orConvert prefers paise the caller supplied over converting their rupees.
//
// The conversion is the fallback, not the path. A price typed as paise that
// gets converted from a float on the way in has already lost whatever the
// float lost, and every exact figure computed from it downstream is exact
// about the wrong number.
func orConvert(minor *int64, rupees float64) int64 {
	if minor != nil {
		return *minor
	}
	return rupeesToMinor(rupees)
}

// orConvertPtr is orConvert for an optional amount. Absent stays absent — a
// nil cost price means "not recorded", never zero.
func orConvertPtr(minor *int64, rupees *float64) *int64 {
	if minor != nil {
		return minor
	}
	return rupeesToMinorPtr(rupees)
}

// ─── Variant repricing ───────────────────────────────────────────────

// variantMoneyPairs maps each rupee column to its paise column.
//
// Both names are accepted from a caller: `selling_price` is the legacy shape
// and `selling_price_minor` is what a client written today sends. They are
// resolved into one another before either is written.
var variantMoneyPairs = map[string]string{
	"mrp":                 "mrp_minor",
	"selling_price":       "selling_price_minor",
	"cost_price":          "cost_price_minor",
	"mrp_minor":           "mrp",
	"selling_price_minor": "selling_price",
	"cost_price_minor":    "cost_price",
}

// variantMoneyUpdate is one resolved amount, ready for both columns.
type variantMoneyUpdate struct {
	rupeeCol string
	minorCol string
	rupees   any // *float64-compatible: nil clears an optional amount
	minor    any
}

// ErrPriceDisagreement means a caller sent both shapes of the same amount and
// they do not describe the same money.
//
// Refused rather than resolved. Picking one silently decides what the buyer
// pays, and there is no reading of "selling_price: 1299, selling_price_minor:
// 99900" that is safe to guess at.
var ErrPriceDisagreement = errors.New("commerce: the rupee and paise forms of a price disagree")

// ErrPriceNotPositive means a repricing would make a variant free.
//
// The catalogue has no concept of a giveaway, and checkout happily charges
// zero. A seller who typed a price wrong should be told, not have their stock
// taken for nothing.
var ErrPriceNotPositive = errors.New("commerce: a price must be greater than zero")

// normaliseVariantMoney resolves every money field the caller supplied into a
// matched (rupees, paise) pair.
func normaliseVariantMoney(updates map[string]any) ([]variantMoneyUpdate, error) {
	out := []variantMoneyUpdate{}
	for _, rupeeCol := range []string{"mrp", "selling_price", "cost_price"} {
		minorCol := variantMoneyPairs[rupeeCol]
		rawRupees, hasRupees := updates[rupeeCol]
		rawMinor, hasMinor := updates[minorCol]
		if !hasRupees && !hasMinor {
			continue
		}

		// An explicit null clears an optional amount. Only cost price is
		// optional; a null selling price would make the variant unpriceable
		// rather than free, and the NOT NULL column would reject it anyway.
		if (hasMinor && rawMinor == nil) || (hasRupees && rawRupees == nil) {
			if rupeeCol != "cost_price" {
				return nil, fmt.Errorf("%w: %s", ErrPriceNotPositive, rupeeCol)
			}
			out = append(out, variantMoneyUpdate{rupeeCol, minorCol, nil, nil})
			continue
		}

		var minor int64
		var err error
		switch {
		case hasMinor:
			minor, err = anyToMinor(rawMinor)
		default:
			minor, err = rupeesToMinorStrict(rawRupees)
		}
		if err != nil {
			return nil, fmt.Errorf("commerce: %s: %w", rupeeCol, err)
		}

		// Both sent: they must agree. This is the one case where guessing
		// changes what a buyer is charged.
		if hasMinor && hasRupees {
			fromRupees, convErr := rupeesToMinorStrict(rawRupees)
			if convErr != nil {
				return nil, fmt.Errorf("commerce: %s: %w", rupeeCol, convErr)
			}
			if fromRupees != minor {
				return nil, fmt.Errorf("%w: %s says %d paise, %s says %d",
					ErrPriceDisagreement, rupeeCol, fromRupees, minorCol, minor)
			}
		}

		if minor <= 0 && rupeeCol != "cost_price" {
			return nil, fmt.Errorf("%w: %s", ErrPriceNotPositive, rupeeCol)
		}
		out = append(out, variantMoneyUpdate{
			rupeeCol: rupeeCol,
			minorCol: minorCol,
			// The rupee column is a mirror of the paise, written for the
			// analytics readers that still scan it.
			rupees: float64(minor) / 100.0, // money-exempt: NUMERIC mirror of the minor column
			minor:  minor,
		})
	}
	return out, nil
}

// anyToMinor reads a paise value out of a decoded JSON body.
//
// encoding/json gives float64 for every number, so an integer count of paise
// arrives as a float and has to come back out. It must be a WHOLE number —
// there is no such thing as a fraction of a paise, and accepting one would
// reintroduce the rounding this path exists to remove.
func anyToMinor(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case float64:
		if n != math.Trunc(n) {
			return 0, fmt.Errorf("paise must be a whole number, got %v", n)
		}
		return int64(n), nil
	case json.Number:
		return n.Int64()
	default:
		return 0, fmt.Errorf("expected a number of paise, got %T", v)
	}
}

// rupeesToMinorStrict is anyRupeesToMinor with a concrete return type.
//
// anyRupeesToMinor returns `any` because it feeds a query-argument slice,
// where a nil has to stay a nil. The repricing path needs to COMPARE two
// amounts, so it needs the number.
func rupeesToMinorStrict(v any) (int64, error) {
	converted, err := anyRupeesToMinor(v)
	if err != nil {
		return 0, err
	}
	switch n := converted.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	default:
		return 0, fmt.Errorf("expected a rupee amount, got %T", v)
	}
}
