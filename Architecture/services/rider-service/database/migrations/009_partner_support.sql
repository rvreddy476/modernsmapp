-- Reuse the existing complaints/admin resolution workflow without pretending
-- a partner's private support request was submitted by the customer.
CREATE TABLE IF NOT EXISTS rider_complaint_partner_reporters (
 complaint_id UUID PRIMARY KEY REFERENCES rider_complaints(id),
 partner_user_id UUID NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS rider_partner_support_reporter ON rider_complaint_partner_reporters(partner_user_id,created_at DESC);
