-- 015: give wallet transactions the description column the code has
-- always tried to write to.
--
-- Store.CreditCreatorFundEarning has written
--   INSERT INTO transactions (..., description, created_at)
-- since the creator fund was added, but no migration ever created that
-- column and setup.sql does not define it either. The insert therefore
-- failed with 42703 on every settlement, which means the creator fund
-- could not credit a single wallet on any environment where the table
-- already existed. It is caught now because the settlement path is
-- exercised end to end for the first time.
--
-- Nullable with no default: old rows keep NULL, and nothing reads the
-- column defensively enough to care. It is where the creator-facing
-- "why were you paid this" derivation is recorded, so it is worth
-- having rather than dropping from the INSERT.

ALTER TABLE transactions
    ADD COLUMN IF NOT EXISTS description TEXT;
