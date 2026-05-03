package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/google/uuid"
)

// indianMobileRE matches a 10-digit Indian mobile number, optionally with
// +91 / 0 prefix.
var indianMobileRE = regexp.MustCompile(`^(?:\+?91|0)?[6-9][0-9]{9}$`)

// RechargeMobileRequest is the inbound shape for POST /v1/billpay/recharge/mobile.
type RechargeMobileRequest struct {
	Phone          string
	Operator       string
	Circle         string
	AmountPaise    int64
	PlanID         *uuid.UUID
	PaymentMethod  string
	IdempotencyKey string
}

// RechargeMobile reuses the Pay() saga, with the recharge-specific provider
// resolution. The mobile-prepaid catalog is keyed on the operator name.
func (s *Service) RechargeMobile(ctx context.Context, userID uuid.UUID, req RechargeMobileRequest) (*PayResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id required")
	}
	phone := normalisePhone(req.Phone)
	if phone == "" || !indianMobileRE.MatchString(phone) {
		return nil, fmt.Errorf("invalid: phone must be a 10-digit Indian mobile")
	}
	if req.AmountPaise <= 0 {
		return nil, fmt.Errorf("invalid: amount must be positive")
	}
	if req.IdempotencyKey == "" {
		return nil, fmt.Errorf("invalid: idempotency_key required")
	}
	switch req.PaymentMethod {
	case "wallet", "upi", "card":
	default:
		return nil, fmt.Errorf("invalid: payment_method must be wallet|upi|card")
	}
	op := strings.ToLower(strings.TrimSpace(req.Operator))
	if op == "" {
		// Fall back to Setu's operator detect.
		o, c, err := s.setu.DetectOperatorCircle(ctx, phone)
		if err != nil {
			return nil, fmt.Errorf("detect operator: %w", err)
		}
		op = strings.ToLower(o)
		if req.Circle == "" {
			req.Circle = c
		}
	}
	// Resolve provider for this operator. We expect SetuBillerID to be the
	// operator name in lower-case for prepaid (e.g. "airtel"); the nightly
	// sync seeds these. The provider lookup below accepts either the explicit
	// setu_biller_id or the canonical mobile_prepaid alias.
	prov, err := s.store.GetProviderBySetuID(ctx, op)
	if err != nil {
		return nil, fmt.Errorf("not_found: no recharge provider for operator %q", op)
	}

	return s.Pay(ctx, userID, PayRequest{
		ProviderID:     prov.ID,
		Identifier:     phone,
		AmountPaise:    req.AmountPaise,
		PaymentMethod:  req.PaymentMethod,
		IdempotencyKey: req.IdempotencyKey,
		ExtraParams: map[string]string{
			"operator": op,
			"circle":   strings.ToUpper(strings.TrimSpace(req.Circle)),
		},
	})
}

// DetectOperatorCircle proxies to Setu's detect endpoint.
func (s *Service) DetectOperatorCircle(ctx context.Context, phone string) (string, string, error) {
	phone = normalisePhone(phone)
	if phone == "" || !indianMobileRE.MatchString(phone) {
		return "", "", fmt.Errorf("invalid: phone must be a 10-digit Indian mobile")
	}
	return s.setu.DetectOperatorCircle(ctx, phone)
}

// ListMobilePlans returns the cached plans for an (operator, circle).
// Falls back to a live fetch from Setu if the cache is empty.
func (s *Service) ListMobilePlans(ctx context.Context, operator, circle string) ([]store.MobilePlan, error) {
	op := strings.ToLower(strings.TrimSpace(operator))
	cir := strings.ToUpper(strings.TrimSpace(circle))
	if op == "" || cir == "" {
		return nil, fmt.Errorf("invalid: operator and circle required")
	}
	cached, err := s.store.ListMobilePlans(ctx, op, cir)
	if err != nil {
		return nil, err
	}
	if len(cached) > 0 {
		return cached, nil
	}
	// Live fetch + cache.
	live, err := s.setu.ListMobilePlans(ctx, op, cir)
	if err != nil {
		return nil, fmt.Errorf("setu list plans: %w", err)
	}
	inputs := make([]store.UpsertMobilePlanInput, 0, len(live))
	for _, p := range live {
		validity := p.ValidityDays
		var vp *int
		if validity > 0 {
			vp = &validity
		}
		var dp *float64
		if p.DataGBPerDay > 0 {
			d := p.DataGBPerDay
			dp = &d
		}
		var tp *int64
		if p.TalktimePaise > 0 {
			t := p.TalktimePaise
			tp = &t
		}
		var sp *int
		if p.SMSCountPerDay > 0 {
			n := p.SMSCountPerDay
			sp = &n
		}
		var desc *string
		if p.Description != "" {
			d := p.Description
			desc = &d
		}
		var cat *string
		if p.Category != "" {
			c := p.Category
			cat = &c
		}
		inputs = append(inputs, store.UpsertMobilePlanInput{
			Operator: op, Circle: cir, PlanAmountPaise: p.AmountPaise,
			ValidityDays: vp, DataGBPerDay: dp, TalktimePaise: tp,
			SMSCountPerDay: sp, Description: desc, Category: cat,
		})
	}
	if err := s.store.ReplaceMobilePlans(ctx, op, cir, inputs); err != nil {
		return nil, err
	}
	return s.store.ListMobilePlans(ctx, op, cir)
}

// normalisePhone strips +91/0/spaces; returns "" if input is empty.
func normalisePhone(raw string) string {
	if raw == "" {
		return ""
	}
	s := strings.ReplaceAll(raw, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.TrimPrefix(s, "+91")
	s = strings.TrimPrefix(s, "0091")
	if strings.HasPrefix(s, "0") {
		s = s[1:]
	}
	return s
}
