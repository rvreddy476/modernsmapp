CREATE TABLE IF NOT EXISTS group_event_rsvps (
    event_id UUID NOT NULL,
    user_id TEXT NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('going', 'maybe', 'not_going')),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (event_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_event_rsvps_event ON group_event_rsvps(event_id);
