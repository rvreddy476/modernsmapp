-- Fraud review queue
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
CREATE INDEX IF NOT EXISTS idx_fr_creator ON fraud_reviews(creator_id);
CREATE INDEX IF NOT EXISTS idx_fr_status ON fraud_reviews(status) WHERE status = 'pending';

-- Disputes
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
CREATE INDEX IF NOT EXISTS idx_disp_user ON disputes(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_disp_status ON disputes(status) WHERE status IN ('open','investigating');

-- Refunds
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

-- Revenue source tracking on transactions
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS revenue_source TEXT;
