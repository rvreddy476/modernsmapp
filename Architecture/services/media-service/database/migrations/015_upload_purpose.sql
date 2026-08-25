-- The composer upload lease — Slice C, C-P0-4.
--
-- WHY THIS COLUMN EXISTS
--
-- Reclaiming CONFIRMED media needs a way to say "this product uploaded it and
-- the user walked away". Without one, the only available test is "no reference
-- I know about points at it", and in a shared database that is not the same
-- claim at all: most services reference media by a plain UUID column with no
-- foreign key, so an avatar, a channel banner or a portfolio image can look
-- unreferenced to a sweep that has not been told where to look.
--
-- The previous sweep had exactly that shape. It was described as a collector
-- for abandoned composer uploads and was in fact a global collector for every
-- old, apparently-unreferenced ready asset. The cost of being wrong is not
-- symmetric: retaining a blob costs storage, deleting a live avatar is
-- irreversible.
--
-- So confirmed-media reclamation is scoped to assets that carry this lease.
--
-- NULLABLE, and deliberately so. Every asset that already exists, and every
-- asset uploaded by any other surface, has NULL here and is therefore NEVER a
-- candidate for confirmed reclamation. New behaviour applies only to uploads
-- that opt in by naming their purpose.
--
-- `pending_upload` assets remain reclaimable regardless of purpose: the bytes
-- never arrived, so there is nothing to lose and nothing can reference them.
ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS upload_purpose TEXT;

-- Supports the sweep's candidate scan, which filters on purpose and age.
-- Partial: only leased rows are ever selected by that branch, and they are a
-- small minority of the table.
CREATE INDEX IF NOT EXISTS idx_media_assets_upload_purpose
    ON media_assets (upload_purpose, created_at)
    WHERE upload_purpose IS NOT NULL;
