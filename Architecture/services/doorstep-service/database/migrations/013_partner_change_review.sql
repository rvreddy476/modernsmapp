ALTER TABLE doorstep.professionals ADD COLUMN IF NOT EXISTS area_label text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS doorstep.pro_change_requests (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 pro_id uuid NOT NULL REFERENCES doorstep.professionals(id),
 kind text NOT NULL CHECK (kind IN ('profile','skills','area','hours','pan')),
 payload jsonb NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
 reason text,
 created_at timestamptz NOT NULL DEFAULT now(),
 reviewed_at timestamptz,
 reviewed_by uuid
);
CREATE UNIQUE INDEX IF NOT EXISTS pro_change_requests_pending ON doorstep.pro_change_requests(pro_id,kind) WHERE status='pending';
CREATE INDEX IF NOT EXISTS pro_change_requests_queue ON doorstep.pro_change_requests(created_at,id) WHERE status='pending';
