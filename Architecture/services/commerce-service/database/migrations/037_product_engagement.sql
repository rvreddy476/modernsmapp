-- Commerce — migration 037: product like / dislike, and the two public
-- counters a product page shows (likes, shares).
--
-- Founder, 1 Oct 2026: "add a share button and likes and dislikes for each
-- product, for better product review". The decisions taken with it:
--
--   * the LIKE count is public; a DISLIKE is private — only the shopper who
--     cast it ever sees it (the rule MTube took on 27 Sep). So there is no
--     dislike counter anywhere, by construction: nothing to leak.
--   * one reaction per shopper per product; liking replaces a dislike and
--     vice versa.
--   * a share is counted, not recorded per user.
--
-- ─── WHY THE LIKE COUNT IS DENORMALISED ─────────────────────────────────
--
-- Every product tile on every grid (home rails, browse, favourites, a
-- seller's shop) carries like_count. A COUNT(*) over product_reactions per
-- tile would put an aggregate subquery on the hottest read in the service
-- for a number that changes far less often than it is read. So the count is
-- a column, maintained by the same transaction that writes the reaction row
-- (store.SetProductReaction / ClearProductReaction lock the product row
-- first, then move the counter by the reaction's delta). The CHECK keeps a
-- bug from ever publishing a negative count.
--
-- review_votes and reviews.helpful_count already exist in setup.sql with the
-- shape the helpful vote needs (review_id, user_id, is_helpful), so the
-- review half of this change has no migration.

CREATE TABLE IF NOT EXISTS product_reactions (
    product_id  UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('like','dislike')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (product_id, user_id)
);

ALTER TABLE products ADD COLUMN IF NOT EXISTS like_count  BIGINT NOT NULL DEFAULT 0;
ALTER TABLE products ADD COLUMN IF NOT EXISTS share_count BIGINT NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'products_like_count_nonneg') THEN
        ALTER TABLE products ADD CONSTRAINT products_like_count_nonneg CHECK (like_count >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'products_share_count_nonneg') THEN
        ALTER TABLE products ADD CONSTRAINT products_share_count_nonneg CHECK (share_count >= 0);
    END IF;
END $$;
