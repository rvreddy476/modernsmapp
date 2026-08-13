-- Module 7 launch integrity: make active report/appeal deduplication a
-- database invariant. Application-level check-then-insert is racy across
-- replicas and cannot be the safety boundary.

-- Deterministically close duplicate active reports before adding the index.
WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY reporter_id, entity_type, entity_id
               ORDER BY created_at ASC, id ASC
           ) AS rn
    FROM trust.reports
    WHERE status IN ('open', 'reviewing')
)
UPDATE trust.reports r
SET status = 'dismissed',
    resolved_at = COALESCE(r.resolved_at, NOW()),
    resolution_notes = CASE
        WHEN btrim(COALESCE(r.resolution_notes, '')) = ''
            THEN 'Closed during launch migration: duplicate active report.'
        ELSE r.resolution_notes || E'\nClosed during launch migration: duplicate active report.'
    END,
    updated_at = NOW()
FROM ranked d
WHERE r.id = d.id AND d.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS uq_reports_one_active_per_reporter_entity
    ON trust.reports (reporter_id, entity_type, entity_id)
    WHERE status IN ('open', 'reviewing');

-- Appeals are constrained in the same migration because the existing table
-- already permits the same multi-replica race.
WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY user_id, content_type, content_id
               ORDER BY submitted_at ASC, id ASC
           ) AS rn
    FROM trust.content_appeals
    WHERE status IN ('open', 'under_review')
)
UPDATE trust.content_appeals a
SET status = 'expired',
    resolution_note = CASE
        WHEN btrim(COALESCE(a.resolution_note, '')) = ''
            THEN 'Closed during launch migration: duplicate active appeal.'
        ELSE a.resolution_note || E'\nClosed during launch migration: duplicate active appeal.'
    END,
    resolved_at = COALESCE(a.resolved_at, NOW())
FROM ranked d
WHERE a.id = d.id AND d.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS uq_appeals_one_active_per_user_content
    ON trust.content_appeals (user_id, content_type, content_id)
    WHERE status IN ('open', 'under_review');
