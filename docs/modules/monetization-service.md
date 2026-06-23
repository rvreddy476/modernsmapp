# Module: monetization-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /payout-methods/:id
DELETE /subscribe/:creatorId
GET /admin/fraud-reviews
GET /affiliate/conversions
GET /affiliate/:linkCode
GET /affiliate/links
GET /affiliate/links/:linkId
GET /creator-ledger
GET /creators/:creatorId/tiers
GET /dashboard
GET /disputes
GET /disputes/:id
GET /earnings
GET /entitlements
GET /fundraisers
GET /fundraisers/:fundraiserId
GET /fundraisers/:fundraiserId/donations
GET /fundraisers/mine
GET /invoices
GET /payout-methods
GET /payouts
GET /payout-statements
GET /payout-statements/:id
GET /rates
GET /status
GET /subscriptions/:id/events
GET /tax-profile
GET /tds-summary/:year
GET /tiers
GET /tips/post/:postId
GET /tips/received
GET /tips/sent
GET /transactions
GET /wallet
PATCH /admin/fraud-reviews/:id
PATCH /disputes/:id
PATCH /fundraisers/:fundraiserId/pause
PATCH /tiers/:id
POST /admin/wallet/:userId/freeze
POST /admin/wallet/:userId/rebuild
POST /admin/wallet/:userId/unfreeze
POST /affiliate/links
POST /apply
POST /disputes
POST /entitlements/check
POST /fundraisers
POST /fundraisers/:fundraiserId/donate
POST /internal/charge-and-credit
POST /payout-methods
POST /payouts
POST /refunds
POST /settle
POST /subscribe/:creatorId
POST /subscriptions/:id/cancel
POST /subscriptions/:id/pause
POST /subscriptions/:id/resume
POST /subscriptions/:id/upgrade
POST /tax-info
POST /tax-profile
POST /tiers
POST /tips
POST /:userId/suspend
POST /:userId/unsuspend
POST /webhooks/payout
PUT /rates
GROUP /admin/creator-fund
GROUP /creator-fund
GROUP /v1/monetization
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS creator_ledger (
    user_id       UUID PRIMARY KEY,
    balance       DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    lifetime_earnings DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    pending_payout DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    currency      TEXT NOT NULL DEFAULT 'INR',
    is_frozen     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS creator_tiers (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id       UUID NOT NULL,
    name             TEXT NOT NULL,
    price            DECIMAL(8,2) NOT NULL,
    currency         TEXT NOT NULL DEFAULT 'INR',
    perks            JSONB NOT NULL DEFAULT '[]',
    subscriber_count INTEGER NOT NULL DEFAULT 0,
    is_active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS transactions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id      UUID NOT NULL REFERENCES creator_ledger(user_id),
    type           TEXT NOT NULL CHECK (type IN ('earning', 'payout', 'refund', 'adjustment', 'subscription_payment')),
    amount         DECIMAL(12,2) NOT NULL,
    currency       TEXT NOT NULL DEFAULT 'INR',
    status         TEXT NOT NULL DEFAULT 'completed' CHECK (status IN ('pending', 'completed', 'failed', 'reversed')),
    reference_type TEXT NOT NULL DEFAULT '',
    reference_id   TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS payout_methods (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL,
    method_type      TEXT NOT NULL CHECK (method_type IN ('upi', 'bank_transfer', 'paypal')),
    details_encrypted TEXT NOT NULL,
    is_verified      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscriber_id        UUID NOT NULL,
    creator_id           UUID NOT NULL,
    tier_id              UUID NOT NULL REFERENCES creator_tiers(id),
    tier_name            TEXT NOT NULL,
    price                DECIMAL(8,2) NOT NULL,
    currency             TEXT NOT NULL DEFAULT 'INR',
    status               TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled', 'expired', 'paused')),
    current_period_start TIMESTAMPTZ NOT NULL DEFAULT now(),
    current_period_end   TIMESTAMPTZ NOT NULL DEFAULT now() + INTERVAL '30 days',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tax_info (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL UNIQUE,
    country             TEXT NOT NULL,
    tax_data_encrypted  TEXT NOT NULL,
    verification_status TEXT NOT NULL DEFAULT 'pending' CHECK (verification_status IN ('pending', 'verified', 'rejected')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS monetization_audit_log (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    table_name   TEXT NOT NULL,
    operation    TEXT NOT NULL,
    old_data     JSONB,
    new_data     JSONB,
    performer_id UUID,
    ip_address   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS creator_fund_eligibility (
    creator_id              UUID PRIMARY KEY,
    status                  TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','eligible','ineligible','suspended')),
    view_score_90d          DOUBLE PRECISION NOT NULL DEFAULT 0,
    watch_time_ms_90d       BIGINT NOT NULL DEFAULT 0,
    qualifying_content_count INTEGER NOT NULL DEFAULT 0,
    eligible_since          TIMESTAMPTZ,
    suspended_at            TIMESTAMPTZ,
    suspension_reason       TEXT,
    last_evaluated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS monetization_rpm_rates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_type    TEXT NOT NULL CHECK (content_type IN ('long_video','flick')),
    region_code     TEXT NOT NULL DEFAULT 'IN',
    rpm_paise       BIGINT NOT NULL CHECK (rpm_paise >= 0),
    effective_from  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    effective_to    TIMESTAMPTZ,
    notes           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      UUID
);

CREATE TABLE IF NOT EXISTS creator_fund_earnings (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id          UUID NOT NULL,
    day_bucket          DATE NOT NULL,
    content_type        TEXT NOT NULL CHECK (content_type IN ('long_video','flick')),
    region_code         TEXT NOT NULL DEFAULT 'IN',
    view_count          BIGINT NOT NULL DEFAULT 0,
    watch_time_ms       BIGINT NOT NULL DEFAULT 0,
    rpm_paise           BIGINT NOT NULL DEFAULT 0,
    gross_paise         BIGINT NOT NULL DEFAULT 0,
    platform_fee_paise  BIGINT NOT NULL DEFAULT 0,
    net_paise           BIGINT NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'settled'
        CHECK (status IN ('settled','reversed')),
    settled_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (creator_id, day_bucket, content_type, region_code)
);

CREATE TABLE IF NOT EXISTS tips (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_id       UUID NOT NULL,
    recipient_id    UUID NOT NULL,
    amount_paise    BIGINT NOT NULL CHECK (amount_paise > 0),
    currency        TEXT NOT NULL DEFAULT 'INR',
    message         TEXT,
    post_id         UUID,
    stream_id       UUID,
    status          TEXT NOT NULL DEFAULT 'completed'
        CHECK (status IN ('pending','completed','failed','reversed')),
    failure_reason  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT tips_no_self_tip CHECK (sender_id <> recipient_id)
);

CREATE TABLE IF NOT EXISTS affiliate_links (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- listing_id points at marketplace.listings in commerce_db — a different
    -- database, so no FK here (same pattern as creator_tiers migration 012).
    listing_id       UUID NOT NULL,
    commission_pct   REAL NOT NULL DEFAULT 5.0,
    commission_flat  NUMERIC(8,2),
    link_code        TEXT NOT NULL UNIQUE,
    click_count      BIGINT NOT NULL DEFAULT 0,
    conversion_count BIGINT NOT NULL DEFAULT 0,
    total_earned     NUMERIC(12,2) NOT NULL DEFAULT 0,
    is_active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS affiliate_conversions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    affiliate_id   UUID NOT NULL REFERENCES affiliate_links(id),
    order_id       UUID NOT NULL,
    buyer_id       UUID NOT NULL,
    commission_amt NUMERIC(8,2) NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','confirmed','paid','reversed')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS fundraisers (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id     UUID NOT NULL REFERENCES users(id),
    type           TEXT NOT NULL CHECK (type IN ('personal','community','ngo','emergency')),
    title          TEXT NOT NULL,
    description    TEXT NOT NULL,
    cover_media_id UUID,
    goal_amount    NUMERIC(12,2) NOT NULL,
    raised_amount  NUMERIC(12,2) NOT NULL DEFAULT 0,
    donor_count    INT NOT NULL DEFAULT 0,
    currency       VARCHAR(3) NOT NULL DEFAULT 'INR',
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','completed','cancelled')),
    ngo_id         UUID,
    gst_exempt     BOOLEAN NOT NULL DEFAULT FALSE,
    ends_at        TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS donations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fundraiser_id     UUID NOT NULL REFERENCES fundraisers(id),
    donor_id          UUID NOT NULL REFERENCES users(id),
    amount            NUMERIC(12,2) NOT NULL,
    currency          VARCHAR(3) NOT NULL DEFAULT 'INR',
    payment_intent_id UUID NOT NULL,
    is_anonymous      BOOLEAN NOT NULL DEFAULT FALSE,
    message           TEXT,
    receipt_url       TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payout_requests (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL,
    transaction_id   UUID NOT NULL REFERENCES transactions(id),
    amount           DECIMAL(12,2) NOT NULL,
    currency         TEXT NOT NULL DEFAULT 'INR',
    status           TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'processing', 'paid', 'failed', 'held')),
    payout_method_id UUID,
    requested_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at     TIMESTAMPTZ,
    notes            TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id UUID NOT NULL,
    account_type TEXT NOT NULL CHECK (account_type IN ('user_wallet','platform_revenue','platform_gst','platform_tds','escrow','payout_hold')),
    balance_paise BIGINT NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'INR',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(owner_id, account_type)
);

CREATE TABLE IF NOT EXISTS ledger_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    debit_account_id UUID NOT NULL REFERENCES accounts(id),
    credit_account_id UUID NOT NULL REFERENCES accounts(id),
    amount_paise BIGINT NOT NULL CHECK (amount_paise > 0),
    currency TEXT NOT NULL DEFAULT 'INR',
    reference_type TEXT NOT NULL,
    reference_id UUID,
    idempotency_key TEXT UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS balance_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id),
    balance_paise BIGINT NOT NULL,
    snapshot_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS creator_tax_profiles (
    user_id UUID PRIMARY KEY,
    pan_encrypted TEXT,
    gstin TEXT,
    tax_residency TEXT NOT NULL DEFAULT 'IN',
    tds_exempt BOOLEAN NOT NULL DEFAULT false,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tds_ledger (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id UUID NOT NULL,
    financial_year TEXT NOT NULL,
    gross_amount_paise BIGINT NOT NULL,
    tds_amount_paise BIGINT NOT NULL,
    section TEXT NOT NULL DEFAULT '194-O',
    reference_id UUID,
    deducted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS gst_ledger (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID,
    taxable_amount_paise BIGINT NOT NULL,
    gst_rate_bps INT NOT NULL DEFAULT 1800,
    cgst_paise BIGINT NOT NULL DEFAULT 0,
    sgst_paise BIGINT NOT NULL DEFAULT 0,
    igst_paise BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS invoices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    invoice_number SERIAL,
    invoice_type TEXT NOT NULL CHECK (invoice_type IN ('subscription','payout','donation','service_fee')),
    amount_paise BIGINT NOT NULL,
    tax_paise BIGINT NOT NULL DEFAULT 0,
    total_paise BIGINT NOT NULL,
    pdf_media_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS subscription_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    old_status TEXT,
    new_status TEXT,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payout_batches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','settled','failed')),
    total_paise BIGINT NOT NULL DEFAULT 0,
    payout_count INT NOT NULL DEFAULT 0,
    provider_batch_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS payout_statements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    total_earnings_paise BIGINT NOT NULL DEFAULT 0,
    total_deductions_paise BIGINT NOT NULL DEFAULT 0,
    total_payout_paise BIGINT NOT NULL DEFAULT 0,
    pdf_media_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS fraud_reviews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id UUID NOT NULL,
    review_type TEXT NOT NULL CHECK (review_type IN ('self_subscription','velocity','new_creator_hold','manual')),
    risk_score INT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','investigating','cleared','action_taken')),
    notes TEXT,
    reviewer_id UUID,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS disputes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    transaction_id UUID NOT NULL,
    reason TEXT NOT NULL,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','investigating','resolved_refund','resolved_denied')),
    resolution_notes TEXT,
    resolved_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID NOT NULL,
    dispute_id UUID REFERENCES disputes(id),
    amount_paise BIGINT NOT NULL,
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processed','failed')),
    processed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS creator_fund_eligibility (
    creator_id              UUID PRIMARY KEY,
    status                  TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','eligible','ineligible','suspended')),
    view_score_90d          DOUBLE PRECISION NOT NULL DEFAULT 0,
    watch_time_ms_90d       BIGINT NOT NULL DEFAULT 0,
    qualifying_content_count INTEGER NOT NULL DEFAULT 0,
    eligible_since          TIMESTAMPTZ,
    suspended_at            TIMESTAMPTZ,
    suspension_reason       TEXT,
    last_evaluated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS monetization_rpm_rates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_type    TEXT NOT NULL CHECK (content_type IN ('long_video','flick')),
    region_code     TEXT NOT NULL DEFAULT 'IN',
    rpm_paise       BIGINT NOT NULL CHECK (rpm_paise >= 0),
    effective_from  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    effective_to    TIMESTAMPTZ,
    notes           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      UUID
);

CREATE TABLE IF NOT EXISTS creator_fund_earnings (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id          UUID NOT NULL,
    day_bucket          DATE NOT NULL,
    content_type        TEXT NOT NULL CHECK (content_type IN ('long_video','flick')),
    region_code         TEXT NOT NULL DEFAULT 'IN',
    view_count          BIGINT NOT NULL DEFAULT 0,
    watch_time_ms       BIGINT NOT NULL DEFAULT 0,
    rpm_paise           BIGINT NOT NULL DEFAULT 0,
    gross_paise         BIGINT NOT NULL DEFAULT 0,
    platform_fee_paise  BIGINT NOT NULL DEFAULT 0,
    net_paise           BIGINT NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'settled'
        CHECK (status IN ('settled','reversed')),
    settled_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (creator_id, day_bucket, content_type, region_code)
);

CREATE TABLE IF NOT EXISTS tips (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_id       UUID NOT NULL,
    recipient_id    UUID NOT NULL,
    amount_paise    BIGINT NOT NULL CHECK (amount_paise > 0),
    currency        TEXT NOT NULL DEFAULT 'INR',
    message         TEXT,
    post_id         UUID,
    stream_id       UUID,
    status          TEXT NOT NULL DEFAULT 'completed'
        CHECK (status IN ('pending','completed','failed','reversed')),
    failure_reason  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT tips_no_self_tip CHECK (sender_id <> recipient_id)
);

```

## API types (request/response Go structs with JSON tags)
```go
type CreateAffiliateLinkRequest struct {
	ListingID      string   `json:"listing_id" binding:"required"`
	CommissionPct  float32  `json:"commission_pct"`
	CommissionFlat *float64 `json:"commission_flat"`
}

type CreateDisputeRequest struct {
	TransactionID string `json:"transaction_id" binding:"required"`
	Reason        string `json:"reason" binding:"required"`
	Description   string `json:"description"`
}

type ResolveDisputeRequest struct {
	Status          string `json:"status" binding:"required"`
	ResolutionNotes string `json:"resolution_notes"`
}

type ProcessRefundRequest struct {
	TransactionID string  `json:"transaction_id" binding:"required"`
	AmountPaise   int64   `json:"amount_paise" binding:"required"`
	Reason        string  `json:"reason" binding:"required"`
	DisputeID     *string `json:"dispute_id"`
}

type ResolveFraudReviewRequest struct {
	Status string `json:"status" binding:"required"`
	Notes  string `json:"notes"`
}

type CreateFundraiserRequest struct {
	Type        string   `json:"type" binding:"required"`
	Title       string   `json:"title" binding:"required"`
	Description string   `json:"description" binding:"required"`
	GoalAmount  float64  `json:"goal_amount" binding:"required"`
	EndsAt      *string  `json:"ends_at"`
}

type DonateRequest struct {
	Amount          float64  `json:"amount" binding:"required"`
	PaymentIntentID string   `json:"payment_intent_id" binding:"required"`
	IsAnonymous     bool     `json:"is_anonymous"`
	Message         *string  `json:"message"`
}

type AddPayoutMethodRequest struct {
	MethodType       string `json:"method_type" binding:"required"`
	DetailsEncrypted string `json:"details_encrypted" binding:"required"`
	IsDefault        bool   `json:"is_default"`
}

type RequestPayoutRequest struct {
	AmountPaise    int64  `json:"amount_paise" binding:"required"`
	PayoutMethodID string `json:"payout_method_id" binding:"required"`
}

type SaveTaxInfoRequest struct {
	Country          string `json:"country" binding:"required"`
	TaxDataEncrypted string `json:"tax_data_encrypted" binding:"required"`
}

type CreateTierRequest struct {
	Name       string          `json:"name" binding:"required"`
	PricePaise int64           `json:"price_paise" binding:"required"`
	Currency   string          `json:"currency"`
	Perks      json.RawMessage `json:"perks"`
}

type UpdateTierRequest struct {
	Name       string          `json:"name"`
	PricePaise int64           `json:"price_paise"`
	Currency   string          `json:"currency"`
	Perks      json.RawMessage `json:"perks"`
	IsActive   *bool           `json:"is_active"`
}

type SubscribeRequest struct {
	TierID string `json:"tier_id" binding:"required"`
}

type PayoutWebhookRequest struct {
	ProviderReference string `json:"provider_reference" binding:"required"`
	Status            string `json:"status" binding:"required"`
	FailureReason     string `json:"failure_reason"`
}

type PauseSubscriptionRequest struct {
	PauseUntil string `json:"pause_until" binding:"required"` // RFC3339
}

type CancelSubscriptionRequest struct {
	Reason    string `json:"reason"`
	Immediate bool   `json:"immediate"`
}

type UpgradeSubscriptionRequest struct {
	NewTierID string `json:"new_tier_id" binding:"required"`
}

type SaveTaxProfileRequest struct {
	PANEncrypted *string `json:"pan_encrypted"`
	GSTIN        *string `json:"gstin"`
	TaxResidency string  `json:"tax_residency"`
}
```
