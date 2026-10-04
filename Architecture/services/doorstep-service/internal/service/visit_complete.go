package service

import (
	"context"
	"encoding/json"
	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/google/uuid"
)

func registeredOnly(family string) bool {
	switch family {
	case tax.FamilyBeautySalon, tax.FamilyCarCare, tax.FamilyHomeStaffing, tax.FamilyRelocation, tax.FamilyPhotography, tax.FamilyFitnessWellness:
		return true
	}
	return false
}

func (s *Service) visitInvoice(ctx context.Context, f *store.VisitFacts) (*model.Invoice, error) {
	rec, err := s.bk.Store.BookingRecord(ctx, f.BookingID, nil)
	if err != nil {
		return nil, err
	}
	extras, err := s.bk.Store.BookingExtras(ctx, f.BookingID)
	if err != nil {
		return nil, err
	}
	inv := &model.Invoice{ComputedAt: s.nowUTC(), Lines: []model.InvoiceLine{}, Provisional: true, Note: tax.Note}
	var gstin string
	if f.Pro != nil && f.Pro.GSTIN != nil {
		gstin = *f.Pro.GSTIN
		inv.ProRegistered = gstin != ""
	}
	if registeredOnly(f.Family) && !inv.ProRegistered {
		return nil, apperr.New(422, "DOORSTEP_GST_REGISTRATION_REQUIRED", "this service requires a GST-registered professional")
	}
	lines := []tax.Line{}
	for _, l := range rec.Booking.Items {
		lines = append(lines, tax.Line{Ref: l.RefID.String(), GrossPaise: l.LineTotalPaise})
		inv.Lines = append(inv.Lines, model.InvoiceLine{Ref: l.RefID.String(), Kind: l.Kind, Name: l.Name})
	}
	for _, e := range extras {
		if e.Status == "billed" {
			lines = append(lines, tax.Line{Ref: e.ID.String(), GrossPaise: e.TotalPaise})
			inv.Lines = append(inv.Lines, model.InvoiceLine{Ref: e.ID.String(), Kind: "extra", Name: e.Name})
		}
	}
	result, err := s.tax.SplitInclusive(tax.Input{Family: f.Family, ProfessionalGSTIN: gstin, PlaceOfSupplyState: f.StateCode, At: s.nowUTC(), Lines: lines})
	if err != nil {
		return nil, err
	}
	for i, l := range result.Lines {
		inv.Lines[i].GSTCategory = l.Category
		inv.Lines[i].SAC = l.SAC
		inv.Lines[i].RateBPS = l.RateBPS
		inv.Lines[i].GrossPaise = l.GrossPaise
		inv.Lines[i].TaxablePaise = l.TaxablePaise
		inv.Lines[i].TaxPaise = l.TaxPaise
		inv.TotalPaise += l.GrossPaise
		inv.TaxablePaise += l.TaxablePaise
		inv.TaxPaise += l.TaxPaise
	}
	inv.Provisional = result.Provisional
	inv.Note = result.Note
	return inv, nil
}

func (s *Service) ProVisitComplete(ctx context.Context, user, id uuid.UUID, in model.OTPInput) (*model.ProJob, error) {
	check, err := otpCheck(in, "end")
	if err != nil {
		return nil, err
	}
	f, err := s.visitAccess(ctx, user, id, false)
	if err != nil {
		return nil, err
	}
	if err = visitCompleteGuard(f, user); err != nil {
		return nil, err
	}
	inv, err := s.visitInvoice(ctx, f)
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	raw, err := json.Marshal(inv)
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	commission := inv.TaxablePaise * int64(f.CommissionBPS) / 10000
	_, err = s.visit.CompleteVisit(ctx, store.OTPStep{BookingID: id, ProUser: user, Check: check, At: s.nowUTC()}, store.CompleteSpec{ProID: f.Pro.ProID, Version: f.Version, Invoice: raw, ExtrasTotal: f.ExtrasTotal, Lines: []store.EarningIn{{Kind: "job", AmountPaise: inv.TaxablePaise}, {Kind: "commission", AmountPaise: -commission}}}, func(locked *store.VisitFacts) error { return visitCompleteGuard(locked, user) })
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	s.publishBookingNow(ctx, id)
	return s.ProJob(ctx, user, id)
}
