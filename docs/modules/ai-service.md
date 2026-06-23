# Module: ai-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
GET /jobs/:jobId
POST /caption/suggest
POST /engagement/predict
POST /hashtag/suggest
POST /jobs
POST /moderation/check
POST /scam/check
POST /smart-reply
POST /summary
POST /translation
GROUP /v1/ai
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS ai.ai_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_type        TEXT NOT NULL CHECK (job_type IN (
        'subtitle_generation','caption_suggestion','hashtag_suggestion',
        'thumbnail_generation','dubbing','moderation_check','translation',
        'content_repurpose','smart_reply','summary','search_answer',
        'engagement_prediction','scam_detection','impersonation_check'
    )),
    input_ref_type  TEXT NOT NULL,
    input_ref_id    UUID NOT NULL,
    requester_id    UUID,
    status          TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','processing','completed','failed')),
    result          JSONB,
    error_message   TEXT,
    model_version   TEXT,
    latency_ms      INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS ai.moderation_ai_results (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_type    TEXT NOT NULL CHECK (content_type IN ('post','comment','story','message','profile')),
    content_id      UUID NOT NULL,
    text_score      REAL,
    image_score     REAL,
    flags           TEXT[],
    action          TEXT NOT NULL DEFAULT 'allow'
        CHECK (action IN ('allow','flag','auto_remove','manual_review')),
    model_version   TEXT,
    checked_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

```
