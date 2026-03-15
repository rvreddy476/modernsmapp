-- Referral program
CREATE TABLE IF NOT EXISTS referrals (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    referrer_id    UUID NOT NULL REFERENCES users(id),
    referee_id     UUID REFERENCES users(id),
    invite_code    TEXT NOT NULL UNIQUE,
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','signed_up','qualified','rewarded')),
    clicked_at     TIMESTAMPTZ,
    signed_up_at   TIMESTAMPTZ,
    qualified_at   TIMESTAMPTZ,
    reward_issued  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_referrals_referrer ON referrals(referrer_id, status);
CREATE INDEX IF NOT EXISTS idx_referrals_code ON referrals(invite_code);

-- Attribution: track which invite code was used during signup
ALTER TABLE users ADD COLUMN IF NOT EXISTS referred_by_code TEXT;
