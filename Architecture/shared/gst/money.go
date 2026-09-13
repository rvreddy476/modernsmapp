package gst

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// Paise is an amount in paise (1/100 rupee).
type Paise int64

// RateBP is a GST rate in basis points: 5% is 500, 18% is 1800.
type RateBP int32

const (
	bpDenominator = 10000

	// MaxAmount caps each line amount (1e14 paise, Rs 1 lakh crore). With
	// MaxLines lines and a rate of at most 100%, every intermediate sum stays
	// far inside int64.
	MaxAmount Paise = 100_000_000_000_000
	// MaxLines caps the number of lines in one computation.
	MaxLines = 1000
)

// extract splits a GST-inclusive amount into taxable value and tax.
//
//	taxable = floor(net * 10000 / (10000 + rate))
//	tax     = net - taxable
//
// Ported from commerce-service/internal/tax/gst.go:264-274. Deriving tax by
// subtraction is what guarantees taxable + tax == net for every input.
// big.Int carries the product so a large amount cannot overflow int64.
func extract(net Paise, rate RateBP) (taxable, tax Paise) {
	if rate == 0 || net == 0 {
		return net, 0
	}
	num := new(big.Int).Mul(big.NewInt(int64(net)), big.NewInt(bpDenominator))
	den := big.NewInt(int64(bpDenominator + int64(rate)))
	q := new(big.Int).Quo(num, den) // net >= 0, so truncation is floor
	taxable = Paise(q.Int64())
	tax = net - taxable
	return taxable, tax
}

// taxOnExclusive is the tax on a GST-exclusive taxable value, rounded half
// up to the paise: floor((taxable * rate + 5000) / 10000).
func taxOnExclusive(taxable Paise, rate RateBP) Paise {
	if rate == 0 || taxable == 0 {
		return 0
	}
	num := new(big.Int).Mul(big.NewInt(int64(taxable)), big.NewInt(int64(rate)))
	num.Add(num, big.NewInt(5000)) // half of 10000: round half up
	return Paise(num.Quo(num, big.NewInt(bpDenominator)).Int64())
}

// errAllocate is wrapped by every Allocate failure.
var errAllocate = errors.New("gst: allocation")

// Allocate spreads amount across weights by the largest-remainder method, so
// the parts sum to amount exactly.
//
// Ported from commerce-service/internal/tax/gst.go:289-353. Each part gets
// floor(amount * w / W); the shortfall (at most len-1 paise) goes one paise
// at a time to the largest fractional remainders, ties to the lower index,
// so the result is a pure function of the input order. When every weight is
// zero the whole amount goes to the first entry rather than being dropped.
func Allocate(amount Paise, weights []Paise) ([]Paise, error) {
	out := make([]Paise, len(weights))
	if len(weights) == 0 {
		if amount != 0 {
			return nil, fmt.Errorf("%w: cannot allocate across zero entries", errAllocate)
		}
		return out, nil
	}
	if amount == 0 {
		return out, nil
	}
	if amount < 0 {
		return nil, fmt.Errorf("%w: %w: amount", errAllocate, ErrNegativeAmount)
	}

	var total Paise
	for _, w := range weights {
		if w < 0 {
			return nil, fmt.Errorf("%w: %w: weight", errAllocate, ErrNegativeAmount)
		}
		total += w
	}
	if total == 0 {
		out[0] = amount
		return out, nil
	}

	type rem struct {
		idx int
		r   *big.Int
	}
	rems := make([]rem, 0, len(weights))
	amt := big.NewInt(int64(amount))
	tot := big.NewInt(int64(total))
	var assigned Paise
	for i, w := range weights {
		num := new(big.Int).Mul(amt, big.NewInt(int64(w)))
		q, r := new(big.Int).QuoRem(num, tot, new(big.Int))
		out[i] = Paise(q.Int64())
		assigned += out[i]
		rems = append(rems, rem{idx: i, r: r})
	}

	short := amount - assigned
	if short < 0 {
		return nil, fmt.Errorf("%w: overshot", errAllocate)
	}
	if short == 0 {
		return out, nil
	}
	sort.SliceStable(rems, func(a, b int) bool {
		c := rems[a].r.Cmp(rems[b].r)
		if c != 0 {
			return c > 0 // larger remainder first
		}
		return rems[a].idx < rems[b].idx // deterministic tie-break
	})
	for i := 0; i < int(short); i++ {
		out[rems[i%len(rems)].idx]++
	}
	return out, nil
}
