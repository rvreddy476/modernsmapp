package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/google/uuid"
)

// CreateAccountRequest is the inbound shape for POST /v1/billpay/accounts.
type CreateAccountRequest struct {
	ProviderID  uuid.UUID         `json:"provider_id"`
	Identifier  string            `json:"identifier"`
	ExtraParams map[string]string `json:"extra_params,omitempty"`
	Label       string            `json:"label"`
}

// CreateAccount inserts (or restores) a saved bill account for a user.
// Idempotent on (user, provider, identifier) per the schema's UNIQUE.
func (s *Service) CreateAccount(ctx context.Context, userID uuid.UUID, req CreateAccountRequest) (*store.Account, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id required")
	}
	if req.ProviderID == uuid.Nil {
		return nil, fmt.Errorf("invalid: provider_id required")
	}
	if req.Identifier == "" {
		return nil, fmt.Errorf("invalid: identifier required")
	}
	if req.Label == "" {
		return nil, fmt.Errorf("invalid: label required")
	}
	// Validate provider exists & is active. We also enforce the customer_params
	// regex if the biller defines one for the primary identifier.
	prov, err := s.store.GetProvider(ctx, req.ProviderID)
	if err != nil {
		return nil, err
	}
	if !prov.IsActive {
		return nil, fmt.Errorf("invalid: provider is not active")
	}
	extraJSON := []byte("{}")
	if len(req.ExtraParams) > 0 {
		b, err := json.Marshal(req.ExtraParams)
		if err != nil {
			return nil, fmt.Errorf("marshal extra params: %w", err)
		}
		extraJSON = b
	}
	acc, err := s.store.CreateAccount(ctx, store.CreateAccountInput{
		UserID:          userID,
		ProviderID:      req.ProviderID,
		Identifier:      req.Identifier,
		ExtraParamsJSON: extraJSON,
		Label:           req.Label,
	})
	if err != nil {
		return nil, err
	}
	if err := s.producer.PublishAccountAdded(ctx, userID, acc.ID, req.ProviderID); err != nil {
		slog.Warn("billpay: publish account added failed", "account", acc.ID, "error", err)
	}
	return acc, nil
}

// ListAccounts returns the user's saved bill accounts.
func (s *Service) ListAccounts(ctx context.Context, userID uuid.UUID) ([]store.Account, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id required")
	}
	return s.store.ListAccountsByUser(ctx, userID)
}

// UpdateAccountRequest collects the patchable fields. nil means "leave alone".
type UpdateAccountRequest struct {
	Label          *string `json:"label,omitempty"`
	IsDefault      *bool   `json:"is_default,omitempty"`
	AutopayEnabled *bool   `json:"autopay_enabled,omitempty"`
}

// UpdateAccount applies a patch.
func (s *Service) UpdateAccount(ctx context.Context, userID, accountID uuid.UUID, req UpdateAccountRequest) (*store.Account, error) {
	if userID == uuid.Nil || accountID == uuid.Nil {
		return nil, fmt.Errorf("invalid: ids required")
	}
	if req.Label == nil && req.IsDefault == nil && req.AutopayEnabled == nil {
		return s.store.GetAccount(ctx, userID, accountID)
	}
	return s.store.UpdateAccount(ctx, userID, accountID, store.UpdateAccountInput{
		Label:          req.Label,
		IsDefault:      req.IsDefault,
		AutopayEnabled: req.AutopayEnabled,
	})
}

// DeleteAccount soft-deletes the row.
func (s *Service) DeleteAccount(ctx context.Context, userID, accountID uuid.UUID) error {
	if userID == uuid.Nil || accountID == uuid.Nil {
		return fmt.Errorf("invalid: ids required")
	}
	if err := s.store.SoftDeleteAccount(ctx, userID, accountID); err != nil {
		return err
	}
	if err := s.producer.PublishAccountRemoved(ctx, userID, accountID); err != nil {
		slog.Warn("billpay: publish account removed failed", "account", accountID, "error", err)
	}
	return nil
}

// FetchBill triggers a Setu bill fetch and caches the result locally.
// DPDP: identifier is sent to Setu but never logged in plain text — failure
// path uses MaskIdentifier.
func (s *Service) FetchBill(ctx context.Context, userID, accountID uuid.UUID) (*store.Bill, error) {
	if userID == uuid.Nil || accountID == uuid.Nil {
		return nil, fmt.Errorf("invalid: ids required")
	}
	acc, err := s.store.GetAccount(ctx, userID, accountID)
	if err != nil {
		return nil, err
	}
	prov, err := s.store.GetProvider(ctx, acc.ProviderID)
	if err != nil {
		return nil, err
	}
	if !prov.BillFetchSupported {
		return nil, fmt.Errorf("invalid: provider does not support bill fetch")
	}

	extra := map[string]string{}
	if len(acc.ExtraParams) > 0 {
		_ = json.Unmarshal(acc.ExtraParams, &extra)
	}

	setuBill, err := s.setu.FetchBill(ctx, prov.SetuBillerID, acc.Identifier, extra)
	if err != nil {
		slog.Error("billpay: setu fetch bill failed",
			"provider", prov.SetuBillerID,
			"identifier_masked", store.MaskIdentifier(acc.Identifier),
			"error", err,
		)
		return nil, fmt.Errorf("fetch bill from setu: %w", err)
	}
	if setuBill == nil {
		return nil, fmt.Errorf("not_found: no current bill for this account")
	}

	in := store.InsertBillInput{
		AccountID:       acc.ID,
		BillAmountPaise: setuBill.AmountPaise,
		BillPeriodStart: optString(setuBill.BillPeriodStart),
		BillPeriodEnd:   optString(setuBill.BillPeriodEnd),
		BillDueDate:     optString(setuBill.BillDueDate),
		BillNumber:      optString(setuBill.BillNumber),
		CustomerName:    optString(setuBill.CustomerName),
		SetuBillRef:     optString(setuBill.SetuBillRef),
	}
	bill, err := s.store.InsertBill(ctx, in)
	if err != nil {
		return nil, err
	}
	if err := s.producer.PublishBillFetched(ctx, userID, acc.ID, bill.ID, bill.BillAmountPaise, setuBill.BillDueDate); err != nil {
		slog.Warn("billpay: publish bill fetched failed", "bill", bill.ID, "error", err)
	}
	return bill, nil
}

// optString returns nil for empty strings, &s otherwise.
func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
