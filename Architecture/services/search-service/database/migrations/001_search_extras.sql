CREATE TABLE IF NOT EXISTS saved_searches (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    query       TEXT NOT NULL,
    search_type TEXT NOT NULL DEFAULT 'universal',
    saved_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_saved_searches_user ON saved_searches(user_id, saved_at DESC);

CREATE TABLE IF NOT EXISTS search_history (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    query       TEXT NOT NULL,
    searched_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_search_history_user ON search_history(user_id, searched_at DESC);
