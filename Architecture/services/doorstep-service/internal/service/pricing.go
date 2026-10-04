package service

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/catalogue"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/google/uuid"
)

// Professionals' own prices (B1, 4 Oct 2026): a professional prices the
// options and add-ons of the services whose skill they declared; every new
// or changed price waits for an admin (nothing a professional submits is
// approved by itself); only an approved, live price is ever listed, quoted
// or booked.

// PricingStore is what pricing needs from the store.
type PricingStore interface {
	ProPricing(ctx context.Context, proID uuid.UUID, at time.Time) (*store.PricingFacts, []model.ProServicePricing, error)
	SubmitProPrice(ctx context.Context, in store.SubmitPrice) (*model.ProPrice, error)
	WithdrawProPrice(ctx context.Context, proID, priceID uuid.UUID, at time.Time) (*model.ProPrice, error)
	SetSameDay(ctx context.Context, proID, serviceID uuid.UUID, enabled bool, at time.Time) (*model.SameDaySetting, error)
	ProItemPrices(ctx context.Context, city string, at time.Time, proIDs, itemIDs []uuid.UUID) (map[uuid.UUID]catalogue.ProPrices, error)
	ListPriceReviews(ctx context.Context, status, city string, proID *uuid.UUID, after *store.PriceCursor, limit int) ([]model.AdminProPrice, error)
	DecideProPrice(ctx context.Context, a store.Actor, id uuid.UUID, approve bool, reason *string, at time.Time) (*model.AdminProPrice, error)
}

// WithPricing wires professional pricing (B1).
func (s *Service) WithPricing(p PricingStore) *Service {
	s.pr = p
	return s
}

func (s *Service) pricing(ctx context.Context) (PricingStore, error) {
	if s.pr == nil {
		return nil, internal(ctx, "pricing store", errors.New("professional pricing is not wired"))
	}
	return s.pr, nil
}

// MaxPricePaise bounds one price (₹1,00,000 per unit).
const MaxPricePaise = 10_000_000

func skillRequired(code string) *apperr.Error {
	e := apperr.New(http.StatusForbidden, apperr.CodeSkillRequired, "declare this service's skill before pricing it")
	if code != "" {
		e = e.WithDetails(map[string]any{"skill_code": code})
	}
	return e
}

// ProPrices lists the services the professional may price, with every
// item's live approved price, pending submission and newest rejection.
// bookable says whether customers can book them for it now.
func (s *Service) ProPrices(ctx context.Context, uid uuid.UUID) ([]model.ProServicePricing, error) {
	p, err := s.mine(ctx, uid)
	if err != nil {
		return nil, err
	}
	ps, err := s.pricing(ctx)
	if err != nil {
		return nil, err
	}
	f, list, err := ps.ProPricing(ctx, p.ID, s.nowUTC())
	if err != nil {
		return nil, internal(ctx, "pro pricing", err)
	}
	for i := range list {
		v := &list[i]
		hasOption := false
		for _, it := range v.Items {
			if it.ItemKind == "option" && it.Approved != nil {
				hasOption = true
			}
		}
		v.Bookable = f.Status == "approved" && !f.IncidentSuspended && v.SkillStatus == "verified" && hasOption && tax.Supported(v.Family) && (!tax.RegisteredOnly(v.Family) || f.GSTIN != nil)
	}
	return list, nil
}

// ProSubmitPrice submits a price for one option or add-on: pending until an
// admin approves it; the approved price (if any) stays live meanwhile.
func (s *Service) ProSubmitPrice(ctx context.Context, uid uuid.UUID, in model.ProPriceInput) (*model.ProPrice, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	if in.ServiceID == nil || *in.ServiceID == uuid.Nil {
		return nil, apperr.Invalid("service_id", "service_id is required")
	}
	if in.ItemKind == nil || (*in.ItemKind != "option" && *in.ItemKind != "addon") {
		return nil, apperr.Invalid("item_kind", "item_kind must be option or addon")
	}
	if in.ItemID == nil || *in.ItemID == uuid.Nil {
		return nil, apperr.Invalid("item_id", "item_id is required")
	}
	if in.PricePaise == nil || *in.PricePaise < 100 || *in.PricePaise > MaxPricePaise {
		return nil, apperr.Invalid("price_paise", "price_paise must be between 100 and 10000000 (GST-inclusive paise per unit)")
	}
	ps, err := s.pricing(ctx)
	if err != nil {
		return nil, err
	}
	out, err := ps.SubmitProPrice(ctx, store.SubmitPrice{ProID: p.ID, ServiceID: *in.ServiceID, ItemKind: *in.ItemKind,
		ItemID: *in.ItemID, PricePaise: *in.PricePaise, At: s.nowUTC()})
	switch {
	case errors.Is(err, store.ErrSkillRequired):
		return nil, skillRequired("")
	case errors.Is(err, store.ErrNotFound):
		return nil, apperr.New(http.StatusNotFound, apperr.CodeServiceNotFound, "no such active service, option or add-on")
	case errors.Is(err, store.ErrUnchanged):
		return nil, apperr.New(http.StatusConflict, apperr.CodeConflict, "this is already your approved price").
			WithDetails(map[string]any{"reason": "unchanged"})
	case err != nil:
		return nil, internal(ctx, "submit price", err)
	}
	return out, nil
}

// ProWithdrawPrice takes a price back: a pending one is withdrawn, the live
// approved one stops now (the item is no longer bookable with them).
func (s *Service) ProWithdrawPrice(ctx context.Context, uid, priceID uuid.UUID) (*model.ProPrice, error) {
	p, err := s.mine(ctx, uid)
	if err != nil {
		return nil, err
	}
	ps, err := s.pricing(ctx)
	if err != nil {
		return nil, err
	}
	out, err := ps.WithdrawProPrice(ctx, p.ID, priceID, s.nowUTC())
	if errors.Is(err, store.ErrNotFound) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeNotFound, "price not found")
	}
	if st, ok := store.IsTransition(err); ok {
		return nil, apperr.New(http.StatusConflict, apperr.CodeInvalidTransition, "only a pending or live price can be withdrawn").
			WithDetails(map[string]any{"status": st})
	}
	if err != nil {
		return nil, internal(ctx, "withdraw price", err)
	}
	return out, nil
}

// ProSetSameDay records the professional's same-day ("as soon as possible")
// opt-in for one service.
func (s *Service) ProSetSameDay(ctx context.Context, uid, serviceID uuid.UUID, in model.SameDayInput) (*model.SameDaySetting, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	if in.Enabled == nil {
		return nil, apperr.Invalid("enabled", "enabled is required")
	}
	ps, err := s.pricing(ctx)
	if err != nil {
		return nil, err
	}
	out, err := ps.SetSameDay(ctx, p.ID, serviceID, *in.Enabled, s.nowUTC())
	if errors.Is(err, store.ErrSkillRequired) {
		return nil, skillRequired("")
	}
	if err != nil {
		return nil, internal(ctx, "same day", err)
	}
	return out, nil
}

// ---------------------------------------------------------------- admin

// PricePage is the admin review queue page.
type PricePage struct {
	Items      []model.AdminProPrice `json:"items"`
	NextCursor *string               `json:"next_cursor"`
}

var priceStatuses = map[string]bool{"pending": true, "approved": true, "rejected": true, "withdrawn": true}

func encodePriceCursor(c store.PriceCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.SubmittedAt.UnixMicro(), 10) + "|" + c.ID.String()))
}

func decodePriceCursor(raw string) (*store.PriceCursor, *apperr.Error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	bad := apperr.Invalid("cursor", "cursor is not one this service issued")
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, bad
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return nil, bad
	}
	micros, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, bad
	}
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, bad
	}
	return &store.PriceCursor{SubmittedAt: time.UnixMicro(micros).UTC(), ID: uid}, nil
}

// AdminPriceReviews pages professionals' prices by status (default pending:
// the review queue, oldest first), optionally by city or professional.
func (s *Service) AdminPriceReviews(ctx context.Context, status, city, pro, cursor, limitRaw string) (*PricePage, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "pending"
	}
	if !priceStatuses[status] {
		return nil, apperr.Invalid("status", "status must be pending, approved, rejected or withdrawn")
	}
	if city = strings.TrimSpace(city); city != "" {
		c, aerr := normaliseCity(city)
		if aerr != nil {
			return nil, aerr
		}
		city = c
	}
	var proID *uuid.UUID
	if pro = strings.TrimSpace(pro); pro != "" {
		id, err := uuid.Parse(pro)
		if err != nil {
			return nil, apperr.Invalid("pro_id", "pro_id must be a UUID")
		}
		proID = &id
	}
	after, aerr := decodePriceCursor(cursor)
	if aerr != nil {
		return nil, aerr
	}
	limit, aerr := ParseLimit(limitRaw)
	if aerr != nil {
		return nil, aerr
	}
	ps, err := s.pricing(ctx)
	if err != nil {
		return nil, err
	}
	items, err := ps.ListPriceReviews(ctx, status, city, proID, after, limit+1)
	if err != nil {
		return nil, internal(ctx, "price reviews", err)
	}
	out := &PricePage{Items: items}
	if out.Items == nil {
		out.Items = []model.AdminProPrice{}
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[limit-1]
		next := encodePriceCursor(store.PriceCursor{SubmittedAt: last.SubmittedAt, ID: last.ID})
		out.NextCursor = &next
	}
	return out, nil
}

// AdminDecidePrice approves (reason optional) or rejects (reason required)
// a pending price, audited with the change.
func (s *Service) AdminDecidePrice(ctx context.Context, a store.Actor, id uuid.UUID, approve bool, in model.PriceDecisionInput) (*model.AdminProPrice, error) {
	reason, aerr := optionalReason(in.Reason)
	if aerr != nil {
		return nil, aerr
	}
	if !approve && reason == nil {
		return nil, apperr.Invalid("reason", "a reason is required to reject a price")
	}
	ps, err := s.pricing(ctx)
	if err != nil {
		return nil, err
	}
	out, err := ps.DecideProPrice(ctx, a, id, approve, reason, s.nowUTC())
	if errors.Is(err, store.ErrNotFound) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeNotFound, "price not found")
	}
	if st, ok := store.IsTransition(err); ok {
		return nil, apperr.New(http.StatusConflict, apperr.CodeInvalidTransition, "this price was already decided").
			WithDetails(map[string]any{"status": st})
	}
	if err != nil {
		return nil, internal(ctx, "decide price", err)
	}
	return out, nil
}
