-- Add reverse lookup indexes for blocks and mutes
CREATE INDEX IF NOT EXISTS idx_blocks_blocked_id ON blocks(blocked_id);
CREATE INDEX IF NOT EXISTS idx_mutes_muted_id ON graph.mutes(muted_id);
