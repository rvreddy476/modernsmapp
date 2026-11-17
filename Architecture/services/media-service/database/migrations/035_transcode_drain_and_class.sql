-- Transcode drains and transcode classes (re-audit 2026-11-17: REG-2, N-1).
--
-- REG-2. A run that is SIGTERM-drained and then hard-killed at the end of the
-- pod's grace period never returns, so migration 033's arithmetic counted it
-- as a LOST worker; three rollouts during one long transcode failed the
-- video. The worker now records the drain start BEFORE it waits, as a
-- finish of the run plus one drain:
--
--   transcode_delivery_drains   runs of transcode_delivery_event that began
--                               a graceful drain (each also counted in
--                               transcode_delivery_finishes, so a kill
--                               during the drain is not a lost worker).
--
-- N-1. The per-class queue gauges carried one pipeline-wide value. The short
-- consumer now records which class it routed a request into:
--
--   transcode_class        'short' | 'long'
--   transcode_class_event  the request (event id) the class belongs to; a
--                          request that is not the current one (a re-queue
--                          not yet routed) counts as waiting on the short
--                          intake, where it actually is.
--
-- Expand-only: nullable / defaulted columns on media_assets that no other
-- writer names. An older worker ignores them; a rollback leaves them unused
-- (the older worker's lost-run arithmetic never reads drains, and its gauges
-- never read the class). They do not reference media, so reclaim_policy.go
-- has nothing to classify.
ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS transcode_delivery_drains INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS transcode_class           TEXT,
    ADD COLUMN IF NOT EXISTS transcode_class_event     TEXT;
