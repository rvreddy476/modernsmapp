-- 016: let the ledger hold the platform's share of a creator-fund
-- settlement.
--
-- SettleCreatorFundDay books two double-entry rows that sum to gross:
-- the net to the creator's wallet, and the platform fee into a fee
-- sub-account. It named that sub-account "platform_revenue:fees", which
-- is not in the accounts.account_type CHECK from migration 005, so every
-- settlement failed with 23514 the moment it reached the fee entry —
-- after the wallet had already been credited. The creator fund has
-- therefore never completed a settlement on any environment.
--
-- The account type is renamed to platform_revenue_fees (no colon: the
-- column is an enum-by-CHECK, and the punctuation bought nothing) and
-- added to the allowed set. Nothing is migrated, because no row of this
-- type has ever been written.

ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_account_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_account_type_check
    CHECK (account_type IN (
        'user_wallet',
        'platform_revenue',
        'platform_revenue_fees',
        'platform_gst',
        'platform_tds',
        'escrow',
        'payout_hold'
    ));
