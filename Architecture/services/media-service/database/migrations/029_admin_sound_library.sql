-- Admin sound library (2026-10-16).
--
-- The curated library (audio_library, migration 006; used by creators since
-- migration 028) gets an admin tool: upload, describe, enable and withdraw
-- a sound (media-service internal/http/admin_sounds_handler.go, reached only
-- through admin-service with a media:sounds.* service token).
--
-- New columns (mood already exists since 006):
--
--   license_type        licensed | royalty_free | original | public_domain.
--                       NULL only on a legacy row whose source said nothing.
--   rights_holder       who owns the recording.
--   license_reference   the contract / invoice / licence id.
--   license_expires_at  when the licence lapses; an expired sound cannot be
--                       enabled.
--   attribution         credit line, when the licence asks for one.
--   created_by          the admin who added the row (NULL on legacy rows).
--   first_activated_at  the first time the row was enabled. NULL = a draft
--                       that was never public.
--   withdrawn_at        set by a withdraw; a withdrawn row is never active
--   withdraw_reason     (CHECK below). withdraw_scope says whether existing
--   withdraw_scope      posts were silenced too (everywhere: the linked
--                       audio_tracks row is status 'deleted').
--   updated_at          last admin change.
--
-- Status as the admin console reads it: withdrawn (withdrawn_at set), else
-- active (is_active), else draft (never activated), else inactive.
--
-- Every row that exists before this migration was published under the old
-- rules (is_active defaulted TRUE), so it is backfilled as activated at its
-- creation and given the licence its source implies. The backfill runs only
-- in the transaction that adds first_activated_at, so a replay never turns
-- an admin's draft into an "inactive" row.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_schema = current_schema()
           AND table_name = 'audio_library'
           AND column_name = 'first_activated_at'
    ) THEN
        ALTER TABLE audio_library ADD COLUMN first_activated_at TIMESTAMPTZ;
        ALTER TABLE audio_library ADD COLUMN IF NOT EXISTS license_type TEXT;
        UPDATE audio_library
           SET first_activated_at = created_at,
               license_type = COALESCE(license_type, CASE
                   WHEN source = 'original' THEN 'original'
                   WHEN is_licensed OR source = 'licensed' THEN 'licensed'
                   ELSE NULL
               END);
    END IF;
END $$;

ALTER TABLE audio_library ADD COLUMN IF NOT EXISTS license_type       TEXT;
ALTER TABLE audio_library ADD COLUMN IF NOT EXISTS rights_holder      TEXT;
ALTER TABLE audio_library ADD COLUMN IF NOT EXISTS license_reference  TEXT;
ALTER TABLE audio_library ADD COLUMN IF NOT EXISTS license_expires_at TIMESTAMPTZ;
ALTER TABLE audio_library ADD COLUMN IF NOT EXISTS attribution        TEXT;
ALTER TABLE audio_library ADD COLUMN IF NOT EXISTS created_by         UUID;
ALTER TABLE audio_library ADD COLUMN IF NOT EXISTS withdrawn_at       TIMESTAMPTZ;
ALTER TABLE audio_library ADD COLUMN IF NOT EXISTS withdraw_reason    TEXT;
ALTER TABLE audio_library ADD COLUMN IF NOT EXISTS withdraw_scope     TEXT;
ALTER TABLE audio_library ADD COLUMN IF NOT EXISTS updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE audio_library DROP CONSTRAINT IF EXISTS audio_library_license_type_check;
ALTER TABLE audio_library ADD CONSTRAINT audio_library_license_type_check CHECK (
    license_type IS NULL OR license_type IN ('licensed','royalty_free','original','public_domain')
);
ALTER TABLE audio_library DROP CONSTRAINT IF EXISTS audio_library_withdraw_scope_check;
ALTER TABLE audio_library ADD CONSTRAINT audio_library_withdraw_scope_check CHECK (
    withdraw_scope IS NULL OR withdraw_scope IN ('new_uses','everywhere')
);
-- A withdrawn sound is never offered to creators again: the public list and
-- /use read is_active only, so the database holds the two together.
ALTER TABLE audio_library DROP CONSTRAINT IF EXISTS audio_library_withdrawn_inactive_check;
ALTER TABLE audio_library ADD CONSTRAINT audio_library_withdrawn_inactive_check CHECK (
    withdrawn_at IS NULL OR (is_active = FALSE AND withdraw_scope IS NOT NULL)
);

-- One library row per uploaded object: an admin upload key is never shared,
-- so withdrawing one row can never silence another.
CREATE UNIQUE INDEX IF NOT EXISTS uq_audio_library_admin_upload
    ON audio_library (audio_url)
    WHERE audio_url LIKE 'audio-library/%';

-- The admin list: newest first, keyset-paged on (created_at, id).
CREATE INDEX IF NOT EXISTS idx_audio_library_admin_recent
    ON audio_library (created_at DESC, id DESC);
