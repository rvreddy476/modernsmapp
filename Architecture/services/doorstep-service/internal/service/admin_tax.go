package service

import (
	"context"
	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
	"strings"
	"unicode/utf8"
)

type TaxRegistrationInput struct {
	GSTIN    *string `json:"gstin"`
	Verified bool    `json:"verified"`
	Reason   *string `json:"reason"`
}

func validateTaxRegistration(in TaxRegistrationInput) (*string, error) {
	if in.GSTIN == nil || in.Reason == nil || utf8.RuneCountInString(strings.TrimSpace(*in.Reason)) < 10 || utf8.RuneCountInString(*in.Reason) > 1000 {
		return nil, apperr.Invalid("reason", "provide the GSTIN (blank to remove) and a review reason within 10 to 1000 characters")
	}
	if strings.TrimSpace(*in.GSTIN) == "" {
		return nil, nil
	}
	if !in.Verified {
		return nil, apperr.Invalid("verified", "confirm you checked the active registration and its ownership before recording it")
	}
	gst, err := kyc.ValidateGSTIN(*in.GSTIN)
	if err != nil {
		return nil, apperr.Invalid("gstin", "enter a valid GSTIN including its check character")
	}
	return &gst.Normalized, nil
}
func (s *Service) AdminTaxRegistration(ctx context.Context, id uuid.UUID) (*model.TaxRegistration, error) {
	return s.careAdmin.TaxRegistration(ctx, id)
}
func (s *Service) AdminSetTaxRegistration(ctx context.Context, a store.Actor, id uuid.UUID, in TaxRegistrationInput) (*model.TaxRegistration, error) {
	gst, err := validateTaxRegistration(in)
	if err != nil {
		return nil, err
	}
	return s.careAdmin.SetTaxRegistration(ctx, a, id, gst, strings.TrimSpace(*in.Reason), s.nowUTC())
}
