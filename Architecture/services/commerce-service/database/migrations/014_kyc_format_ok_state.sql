-- B12 — `format_ok`: the seller KYC state that says "the paperwork parses,
-- and identity is still unproven".
--
-- Production wires kyc.StubValidator, which performs REGEX checks. Its
-- AllValid verdict was written to `sellers.verification_status` as
-- 'verified', so a well-formed but entirely fictitious PAN, GSTIN and bank
-- account produced a seller row that told the admin queue identity had been
-- confirmed. An admin approving on that signal makes the seller
-- payout-eligible. That is a fraud path, and its root cause is that the
-- schema offered no way to say "format only" — the only affirmative state
-- available was 'verified', so the code used it.
--
-- EXPAND-ONLY. This WIDENS the CHECK: every value previously permitted is
-- still permitted, so an old replica writing 'pending' or 'verified' is
-- unaffected and no existing row can violate it. That is why it belongs in
-- the boot set rather than behind the drained-fleet gate — unlike the
-- NARROWING in 012, which moved to gated/998 for precisely the opposite
-- reason.

DO $$
DECLARE r RECORD;
BEGIN
    FOR r IN
        SELECT conname FROM pg_constraint
         WHERE conrelid = 'sellers'::regclass
           AND contype = 'c'
           AND pg_get_constraintdef(oid) LIKE '%verification_status%'
           AND pg_get_constraintdef(oid) NOT LIKE '%format_ok%'
    LOOP -- expand-only: the replacement below is a strict SUPERSET of the constraint dropped here
        EXECUTE format('ALTER TABLE sellers DROP CONSTRAINT %I', r.conname); -- expand-only: replaced below by a strict SUPERSET that adds 'format_ok'
    END LOOP;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'sellers'::regclass AND conname = 'sellers_verification_status_check_v2'
    ) THEN
        ALTER TABLE sellers
            ADD CONSTRAINT sellers_verification_status_check_v2 -- expand-only: strict superset, cannot reject an old writer or an existing row
            CHECK (verification_status IN (
                'pending','format_ok','verified','rejected','suspended','needs_correction'
            ));
    END IF;
END$$;

COMMENT ON CONSTRAINT sellers_verification_status_check_v2 ON sellers IS
    'Commerce P0 B12. format_ok means a FORMAT-ONLY adapter (kyc.StubValidator) accepted the '
    'seller''s documents: the strings parse, and no identity, GST registry or penny-drop check has '
    'been performed. Only a verifying adapter may write ''verified''. Payout eligibility must treat '
    'format_ok as unverified.';
