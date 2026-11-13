package premiumtest

// The document half of the fake store (migration 029), mirroring the
// Postgres store: premium.DocumentFor decides, one invoice and one credit
// note per pass at most, numbers consecutive per series and financial year
// and taken only when the document is written.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/atpost/monetization-service/internal/events"
	"github.com/atpost/monetization-service/internal/premium"
	"github.com/google/uuid"
)

func counterKey(series, fy string) string { return series + "|" + fy }

// issueLocked runs under s.mu inside ApplyPremiumPaymentEvent.
func (s *Store) issueLocked(d premium.Decision, pass premium.Period, sup premium.Supplier, now time.Time) (*premium.Invoice, error) {
	var inv, cn *premium.Invoice
	for _, i := range s.Invoices {
		if i.PassID != pass.ID {
			continue
		}
		if i.Kind == premium.KindInvoice {
			inv = i
		} else {
			cn = i
		}
	}
	var doc premium.Invoice
	var err error
	switch premium.DocumentFor(d, inv != nil, cn != nil) {
	case premium.KindInvoice:
		if pass.Tax == nil {
			return nil, nil
		}
		fy := premium.FinancialYear(now)
		doc, err = premium.NewInvoice(pass, sup, s.Buyers[pass.ID], now, s.Counters[counterKey(premium.SeriesInvoice, fy)]+1)
	case premium.KindCreditNote:
		fy := premium.FinancialYear(now)
		doc, err = premium.NewCreditNote(*inv, now, s.Counters[counterKey(premium.SeriesCreditNote, fy)]+1)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("premium document for pass %s: %w", pass.ID, err)
	}
	for _, i := range s.Invoices {
		if i.Number == doc.Number {
			return nil, fmt.Errorf("duplicate document number %s", doc.Number)
		}
	}
	s.Counters[counterKey(doc.Series, doc.FY)] = doc.Seq
	c := doc
	s.Invoices[doc.ID] = &c
	out := doc
	return &out, nil
}

func (s *Store) SetPremiumPeriodBuyer(_ context.Context, periodID uuid.UUID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.Periods[periodID]; ok && (p.Status == premium.StatusCreated || p.Status == premium.StatusAbandoned) {
		s.Buyers[periodID] = name
	}
	return nil
}

func (s *Store) sorted(keep func(*premium.Invoice) bool) []premium.Invoice {
	out := []premium.Invoice{}
	for _, i := range s.Invoices {
		if keep(i) {
			out = append(out, *i)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].IssuedAt.Equal(out[b].IssuedAt) {
			return out[a].IssuedAt.After(out[b].IssuedAt)
		}
		// As the Postgres store: a credit note first on a tie.
		if ca, cb := out[a].Kind == premium.KindCreditNote, out[b].Kind == premium.KindCreditNote; ca != cb {
			return ca
		}
		return out[a].Seq > out[b].Seq
	})
	return out
}

func (s *Store) ListPremiumInvoices(_ context.Context, userID uuid.UUID) ([]premium.Invoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sorted(func(i *premium.Invoice) bool { return i.UserID == userID }), nil
}

// GetPremiumInvoice answers by id alone, as the Postgres store does: the
// owner check is the service's.
func (s *Store) GetPremiumInvoice(_ context.Context, id uuid.UUID) (*premium.Invoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, ok := s.Invoices[id]; ok {
		c := *i
		return &c, nil
	}
	return nil, nil
}

func (s *Store) PremiumInvoiceIDForPass(_ context.Context, passID uuid.UUID) (*uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, i := range s.Invoices {
		if i.PassID == passID && i.Kind == premium.KindInvoice {
			id := i.ID
			return &id, nil
		}
	}
	return nil, nil
}

func (s *Store) PremiumGSTRegister(_ context.Context, from, to string) ([]premium.Invoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.sorted(func(i *premium.Invoice) bool { return i.InvoiceDate >= from && i.InvoiceDate <= to })
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Kind != out[b].Kind {
			return out[a].Kind > out[b].Kind // invoice before credit_note
		}
		if out[a].FY != out[b].FY {
			return out[a].FY < out[b].FY
		}
		return out[a].Seq < out[b].Seq
	})
	return out, nil
}

func (s *Store) UnpublishedPremiumInvoices(_ context.Context, before time.Time, limit int) ([]premium.Invoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.sorted(func(i *premium.Invoice) bool {
		_, done := s.Published[i.ID]
		return !done && i.IssuedAt.Before(before)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) MarkPremiumInvoicePublished(_ context.Context, id uuid.UUID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, done := s.Published[id]; !done {
		s.Published[id] = at
	}
	return nil
}

// PublishedDocument is one premium document event.
type PublishedDocument struct {
	Type    string
	Payload events.PremiumInvoicePayload
}

func (p *Publisher) PublishPremiumInvoiceEvent(_ context.Context, eventType string, payload events.PremiumInvoicePayload) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Fail != nil {
		return p.Fail
	}
	p.Documents = append(p.Documents, PublishedDocument{Type: eventType, Payload: payload})
	return nil
}
