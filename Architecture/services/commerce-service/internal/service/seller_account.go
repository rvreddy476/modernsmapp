// Seller account: onboarding a seller profile, reading it back, the payout
// configuration and payout calculation, and the seller earnings list. Moved
// out of service.go unchanged.

package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Seller Onboarding ───────────────────────────────────────

type OnboardSellerInput struct {
	UserID      uuid.UUID
	SellerType  string
	StoreName   string
	BrandName   *string
	Slug        string
	Description *string
	Email       string
	Phone       *string
	GSTNumber   *string
	State       *string
	City        *string
	PostalCode  *string
}

func (s *Service) OnboardSeller(ctx context.Context, in OnboardSellerInput) (*postgres.Seller, error) {
	if in.StoreName == "" {
		return nil, fmt.Errorf("store_name is required")
	}
	if in.Slug == "" {
		in.Slug = slugify(in.StoreName)
	}
	sel := &postgres.Seller{
		UserID:             in.UserID,
		SellerType:         in.SellerType,
		StoreName:          in.StoreName,
		BrandName:          in.BrandName,
		Slug:               in.Slug,
		Description:        in.Description,
		Email:              in.Email,
		Phone:              in.Phone,
		GSTNumber:          in.GSTNumber,
		State:              in.State,
		City:               in.City,
		PostalCode:         in.PostalCode,
		VerificationStatus: "pending",
		StoreStatus:        "active",
		// The column default (migration 045); CreateSeller does not write it.
		SellScope: postgres.SellScopeAllCategories,
	}
	if err := s.store.CreateSeller(ctx, sel); err != nil {
		return nil, fmt.Errorf("create seller: %w", err)
	}
	s.publish(ctx, "commerce.seller.registered", map[string]any{
		"seller_id": sel.ID, "user_id": sel.UserID, "store_name": sel.StoreName,
	})
	return sel, nil
}

func (s *Service) GetSellerProfile(ctx context.Context, userID uuid.UUID) (*postgres.Seller, error) {
	return s.store.GetSellerByUserID(ctx, userID)
}

// ─── Payout Calculation ──────────────────────────────────────

// PayoutConfig holds the platform's commission / platform-fee / TDS rates.
// Phase 4.1 — previously these were hard-coded constants, making them
// impossible to change without redeploying. Now they're loaded from env at
// boot and applied as defaults when the per-call values aren't supplied.
//
// All values are percent (5.0 = 5%, not 0.05). Negative or out-of-bound
// values fall back to compiled defaults so a misconfigured env can't push
// a seller payout into negative territory.
type PayoutConfig struct {
	CommissionPct  float64
	PlatformFeePct float64
	TDSPct         float64
}

// fallbackPayoutConfig is the value the service falls back to when env vars
// are missing or out of bounds. Matches the historical hard-coded constants
// (5% commission, 2% platform fee, 1% TDS) so behaviour is unchanged for
// deployments that don't configure overrides.
var fallbackPayoutConfig = PayoutConfig{CommissionPct: 5.0, PlatformFeePct: 2.0, TDSPct: 1.0}

// WithPayoutConfig overrides the default fee schedule. Values <=0 or >100
// are rejected and replaced with the fallback so a misconfigured env can't
// produce nonsense payouts.
func (s *Service) WithPayoutConfig(cfg PayoutConfig) *Service {
	if cfg.CommissionPct <= 0 || cfg.CommissionPct > 100 {
		cfg.CommissionPct = fallbackPayoutConfig.CommissionPct
	}
	if cfg.PlatformFeePct <= 0 || cfg.PlatformFeePct > 100 {
		cfg.PlatformFeePct = fallbackPayoutConfig.PlatformFeePct
	}
	if cfg.TDSPct < 0 || cfg.TDSPct > 100 {
		cfg.TDSPct = fallbackPayoutConfig.TDSPct
	}
	s.payoutCfg = cfg
	return s
}

// payoutConfig returns the configured schedule, falling back to defaults if
// WithPayoutConfig was never called.
func (s *Service) payoutConfig() PayoutConfig {
	if s.payoutCfg.CommissionPct == 0 && s.payoutCfg.PlatformFeePct == 0 && s.payoutCfg.TDSPct == 0 {
		return fallbackPayoutConfig
	}
	return s.payoutCfg
}

// CalculateSellerPayout breaks a gross amount into commission, platform
// fee, TDS, and the seller's net payout. Per-call overrides (e.g. for a
// seller with a negotiated rate) win; if 0 is passed for a value the
// service's configured default is used.
func (s *Service) CalculateSellerPayout(grossAmount float64, commissionPct, platformFeePct float64) (net float64, commission float64, fee float64, tds float64) { // money-exempt: payout preview, fenced at /v1/commerce/payout (B5)
	cfg := s.payoutConfig()
	if commissionPct == 0 {
		commissionPct = cfg.CommissionPct
	}
	if platformFeePct == 0 {
		platformFeePct = cfg.PlatformFeePct
	}
	commission = round2(grossAmount * commissionPct / 100)
	fee = round2(grossAmount * platformFeePct / 100)
	tds = round2((grossAmount - commission - fee) * cfg.TDSPct / 100)
	net = round2(grossAmount - commission - fee - tds)
	return
}

// SellerEarning is one delivered prepaid order item with the gross/commission
// /fee/tds/net breakdown computed via CalculateSellerPayout. Phase 4.4 — the
// prepaid analogue of CODRemittance. We don't have a persisted ledger table
// yet (deferred to Phase 6's outbox/saga work); this is a read-time derivation
// from order_items so the seller UI can show earnings without a write path.
type SellerEarning struct {
	OrderItemID      uuid.UUID  `json:"order_item_id"`
	OrderID          uuid.UUID  `json:"order_id"`
	OrderNumber      string     `json:"order_number"`
	ProductTitle     string     `json:"product_title"`
	SKU              string     `json:"sku"`
	Quantity         int        `json:"quantity"`
	GrossAmount      float64    `json:"gross_amount"`
	CommissionAmount float64    `json:"commission_amount"`
	PlatformFee      float64    `json:"platform_fee"`
	TDSAmount        float64    `json:"tds_amount"`
	NetAmount        float64    `json:"net_amount"`
	PaymentMethod    *string    `json:"payment_method,omitempty"`
	Status           string     `json:"status"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
}

// ListSellerEarnings returns delivered prepaid (non-COD) order items for a
// seller. COD earnings live in their own remittance ledger so they're not
// duplicated here. Phase 4.4.
// HP3: pagination clamped (default 20, max 200).
func (s *Service) ListSellerEarnings(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*SellerEarning, error) {
	limit, offset = clampListPagination(limit, offset)
	items, err := s.store.ListDeliveredItemsForSeller(ctx, sellerID, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]*SellerEarning, 0, len(items))
	for _, row := range items {
		// Skip COD items — they go through the COD remittance ledger.
		if row.PaymentMethod != nil && *row.PaymentMethod == "cod" {
			continue
		}
		net, commission, fee, tds := s.CalculateSellerPayout(row.Item.FinalPrice, 0, 0)
		out = append(out, &SellerEarning{
			OrderItemID:      row.Item.ID,
			OrderID:          row.Item.OrderID,
			OrderNumber:      row.OrderNumber,
			ProductTitle:     row.Item.ProductTitle,
			SKU:              row.Item.SKU,
			Quantity:         row.Item.Quantity,
			GrossAmount:      round2(row.Item.FinalPrice),
			CommissionAmount: commission,
			PlatformFee:      fee,
			TDSAmount:        tds,
			NetAmount:        net,
			PaymentMethod:    row.PaymentMethod,
			Status:           row.Item.Status,
			DeliveredAt:      row.Item.DeliveredAt,
		})
	}
	return out, nil
}
