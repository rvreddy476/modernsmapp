-- 029 — what a seller submitted, what changed since last time, and which
-- live listings a tightened rule has left behind.
--
-- ─── THE PRINCIPLE THIS FILE SERVES ─────────────────────────────────────
--
-- Validation happens at WRITE. Completeness is only asked at SUBMIT.
--
-- 026 and 028 built the write half: every value a seller sends is checked
-- against the definition that asked for it, on create and on patch alike, so
-- a draft may be incomplete but may not be wrong. Nothing has ever asked the
-- other question. `SubmitProductForReview` checks one thing — that the row is
-- a draft — and then puts it in front of a human. So a listing with no price,
-- no stock, no photograph, no GST class and none of the fourteen fields its
-- category asks for lands in the review queue looking exactly like a finished
-- one, and the only person who finds out is the reviewer.
--
-- That is the wrong place for it to be found. A reviewer rejecting an empty
-- listing is a round trip through a human queue for something a query can
-- answer in a millisecond, and the seller learns about it hours later, one
-- missing field at a time.
--
-- ─── WHAT THIS FILE ADDS ────────────────────────────────────────────────
--
--   product_submissions        one row per submit: who, when, the values AS
--                              SUBMITTED, and the schema version they were
--                              checked against. The snapshot is the point —
--                              a reviewer looking at a re-submission needs
--                              to see what CHANGED, and a diff computed
--                              against the live row can only ever compare
--                              "now" with "now".
--
--   product_compliance_gaps    per-product, per-field: this live listing no
--                              longer satisfies its category's current
--                              schema. Decision 8, taken with the founder:
--                              MAKING A FIELD REQUIRED LATER MUST NEVER TAKE
--                              LIVE LISTINGS DOWN.
--
-- ─── WHY A GAP IS A ROW AND NOT A STATUS ────────────────────────────────
--
-- The obvious implementation of "this listing is no longer compliant" is to
-- move it out of `approval_status='approved'`. It is also the one that
-- destroys the business. An operator ticking "make `pages` required" on the
-- Books category would instantly delist every book in the catalogue that
-- predates the field — sellers lose their sales that afternoon, for a rule
-- they were never shown and a field that was optional when they listed.
--
-- So a gap is a ROW BESIDE the product, in its own table, with no foreign key
-- into the lifecycle columns and no trigger that touches them. Nothing in
-- this file can change whether a product sells. `products.status`,
-- `products.approval_status` and `products.published_at` are not mentioned
-- below, and that is deliberate rather than incidental: the flagged listing
-- keeps selling, the seller is told what to fix, and the fix lands on their
-- next edit.
--
-- ─── EXPAND ONLY ────────────────────────────────────────────────────────
--
-- Two CREATE TABLEs and their indexes. No column is added to `products`, no
-- constraint is narrowed, nothing is backfilled, and no existing row is
-- touched. An image running against this schema that knows nothing about
-- either table behaves exactly as it did before.

-- ─── product_submissions ────────────────────────────────────────────────
--
-- ─── WHY THE SNAPSHOT IS STORED AND NOT DERIVED ─────────────────────────
--
-- "What changed since the last submission" cannot be answered from the
-- product row, because the product row holds one state — the current one. The
-- seller's edits between two submissions are exactly what the reviewer needs
-- to see, and they are exactly what an un-snapshotted design has already
-- overwritten. `product_moderation_log` records the REVIEWER's actions and
-- nothing about the listing's content, so it cannot serve either.
--
-- JSONB rather than a normalised value table: this is a frozen photograph of
-- a listing at one instant, not live data. It is never joined, never filtered
-- on and never updated, and normalising it would tie a historical record to
-- the shape of `attribute_definitions` as it stands TODAY — so renaming an
-- attribute would silently rewrite what a seller submitted last March.
CREATE TABLE IF NOT EXISTS product_submissions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id    UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    -- The shop, denormalised from the product. A reviewer's queue filters by
    -- seller and this saves the join; more importantly a submission is a
    -- record of who claimed the listing was ready, and re-deriving that from
    -- a product row that could later move shops would rewrite history.
    seller_id     UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,

    -- The human. Nullable and WITHOUT a foreign key: users live in another
    -- service's database, and a deleted account must not cascade away the
    -- record that a listing was submitted.
    submitted_by  UUID,

    -- 1 for the first submission, 2 for the first re-submission after a
    -- rejection, and so on. Stored rather than counted at read time so the
    -- number a reviewer is shown cannot change when an old row is archived.
    attempt       INT NOT NULL CHECK (attempt >= 1),

    -- Which published attribute-schema version the completeness gate ran
    -- against. Two submissions can be judged by different rules — that is the
    -- whole point of decision 8 — and a diff that did not say so would show
    -- a field appearing out of nowhere with no explanation.
    schema_version INT NOT NULL DEFAULT 0,

    -- The listing as submitted: a JSON ARRAY of
    --   {"code": …, "label": …, "kind": "builtin"|"attribute", "value": …}
    --
    -- An array, not an object keyed by code, for two reasons. A built-in and
    -- an attribute could in principle share a code (the built-ins use a `.`
    -- that `attribute_definitions.code` forbids, so they cannot today — but
    -- an object would make that a silent overwrite rather than a decision).
    -- And the order is the order the form draws the fields in, which is what
    -- makes the diff readable top to bottom instead of alphabetically.
    snapshot      JSONB NOT NULL DEFAULT '[]'::jsonb,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE product_submissions IS
    'One row per submit-for-review, with the listing values AS SUBMITTED. The reviewer diff is '
    'computed between consecutive rows; it cannot be derived from the product row, which only '
    'ever holds the current state.';
COMMENT ON COLUMN product_submissions.snapshot IS
    'JSON array of {code,label,kind,value} in form order. Frozen: never updated after insert.';

-- The reviewer's read is always "this product, newest first" — the latest
-- submission and the one before it, which is the diff.
CREATE INDEX IF NOT EXISTS idx_product_submissions_product
    ON product_submissions(product_id, attempt DESC);

CREATE INDEX IF NOT EXISTS idx_product_submissions_seller
    ON product_submissions(seller_id, created_at DESC);

-- One row per attempt, so a double-tapped Submit button cannot mint two
-- attempt 3s and make the diff ambiguous about which one it compared.
CREATE UNIQUE INDEX IF NOT EXISTS uq_product_submissions_attempt
    ON product_submissions(product_id, attempt);

-- ─── product_compliance_gaps ────────────────────────────────────────────
--
-- One row per (product, field) that a LIVE listing currently fails.
--
-- ─── WHY IT IS NOT A COUNT ──────────────────────────────────────────────
--
-- The authoring path already counts these: `Store.AttributeImpact` answers
-- "how many live listings would tightening this field break", and the admin
-- patch refuses a narrowing edit that has not quoted the number back. That
-- number is what an operator needs BEFORE they tick the box.
--
-- It is useless afterwards. "412 listings are in violation" cannot be shown
-- to a seller, cannot be worked through by the founder, and cannot be
-- resolved — nobody can act on a count. The sweeper below writes the same
-- verdict per product, from the same SQL (see attributeViolationCTE in
-- internal/store/postgres/attributes.go, which both the count and the sweep
-- are built from, so there is exactly one definition of "in violation").
--
-- ─── RESOLUTION IS A TIMESTAMP, NOT A DELETE ────────────────────────────
--
-- A fixed gap keeps its row with `resolved_at` set. Deleting it would make
-- "how long did the catalogue sit in violation after we tightened Books"
-- unanswerable, which is the one question worth asking after a rule change
-- that turned out to be a mistake.
CREATE TABLE IF NOT EXISTS product_compliance_gaps (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id    UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    seller_id     UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,

    -- The attribute this product fails. NULL is reserved for a future
    -- built-in gap (a listing that lost its tax class, say); nothing writes
    -- one today, and the column is nullable rather than absent so adding one
    -- is not another migration.
    definition_id UUID REFERENCES attribute_definitions(id) ON DELETE CASCADE,

    -- The code as the seller's form knows it, and the label as a human reads
    -- it. Both denormalised on purpose: this row is shown to a seller, and a
    -- gap list that had to join three tables to render a sentence would be a
    -- join per row on the founder's queue.
    code          TEXT NOT NULL,
    label         TEXT NOT NULL,

    -- 'missing'      the category now requires this field and the listing has
    --                no value for it.
    -- 'out_of_range' there IS a value and the CURRENT rules reject it — a
    --                bound was narrowed, a regex tightened, or the enum
    --                option the seller picked has since been retired.
    reason        TEXT NOT NULL CHECK (reason IN ('missing','out_of_range')),

    -- The schema version the sweep judged against, so a gap raised under
    -- version 7 is distinguishable from one raised under version 9.
    schema_version INT NOT NULL DEFAULT 0,

    detected_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Refreshed by every sweep that still sees the gap, so a stale row is
    -- visibly stale rather than looking like a fresh finding.
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at   TIMESTAMPTZ
);

COMMENT ON TABLE product_compliance_gaps IS
    'Live listings that fail their category''s CURRENT attribute schema. Advisory only: nothing '
    'here touches products.status or products.approval_status, because a rule tightened today '
    'must never delist a listing that was compliant when it was approved.';

-- One OPEN gap per (product, code). Partial, so the history of resolved gaps
-- can hold as many rows as it likes while the live set stays unambiguous —
-- which is what makes the sweeper's upsert idempotent.
CREATE UNIQUE INDEX IF NOT EXISTS uq_product_compliance_gaps_open
    ON product_compliance_gaps(product_id, code)
 WHERE resolved_at IS NULL;

-- The seller's "action needed" list, and the founder's queue, are both reads
-- of the open set.
CREATE INDEX IF NOT EXISTS idx_product_compliance_gaps_seller_open
    ON product_compliance_gaps(seller_id, detected_at DESC)
 WHERE resolved_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_product_compliance_gaps_definition_open
    ON product_compliance_gaps(definition_id, detected_at DESC)
 WHERE resolved_at IS NULL;
