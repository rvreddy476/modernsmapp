-- Tier 3d: Tips and Super Chat.
--
-- A tip is a one-shot transfer from a fan to a creator. Optionally
-- anchored to a post (Super Thanks-style) or a live stream (Super
-- Chat-style). Wallet flow reuses the existing ChargeAndCredit helper:
-- atomic debit-credit-transactions in one DB transaction. The row
-- lives in `tips` so we can show "your supporters" / "tips on this
-- post" without grinding through the generic transactions table.
--
-- A future v2 can add gift_catalog_id (virtual gifts), pinned_until
-- (Super Chat highlight duration), and currency conversion. v1 is
-- INR-only and untrimmed.

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

CREATE INDEX IF NOT EXISTS idx_tips_sender
    ON tips (sender_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tips_recipient
    ON tips (recipient_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tips_post
    ON tips (post_id, created_at DESC)
    WHERE post_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tips_stream
    ON tips (stream_id, created_at DESC)
    WHERE stream_id IS NOT NULL;

-- Extend the transactions type CHECK to recognise the two new tip-
-- related transaction kinds. Migration 010 already widened this for
-- creator_fund_earning + view_earnings; we re-state the full set here
-- so this migration is independent of 010's apply order.
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_type_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_type_check
    CHECK (type IN (
        'earning','payout','refund','adjustment','subscription_payment',
        'view_earnings','creator_fund_earning',
        'tip_sent','tip_received'
    ));
