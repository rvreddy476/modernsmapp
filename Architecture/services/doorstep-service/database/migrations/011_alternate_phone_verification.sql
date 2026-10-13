-- Codes are hashed, short-lived and scoped to one application. No login tokens.
ALTER TABLE doorstep.pro_applications
 ADD COLUMN IF NOT EXISTS alternate_otp_hash text,
 ADD COLUMN IF NOT EXISTS alternate_otp_expires_at timestamptz,
 ADD COLUMN IF NOT EXISTS alternate_otp_attempts integer NOT NULL DEFAULT 0,
 ADD COLUMN IF NOT EXISTS alternate_otp_sent_at timestamptz,
 ADD COLUMN IF NOT EXISTS alternate_otp_window_at timestamptz,
 ADD COLUMN IF NOT EXISTS alternate_otp_sends integer NOT NULL DEFAULT 0;
