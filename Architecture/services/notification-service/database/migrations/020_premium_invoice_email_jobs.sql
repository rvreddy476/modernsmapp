-- Migration 020: the buyer's Momentum Premium tax-invoice and credit-note
-- e-mails (13 Nov 2026).
--
-- monetization-service publishes premium.invoice.issued and
-- premium.credit_note.issued (ids only: user_id, invoice_id, pass_id, kind)
-- on monetization.events. The consumer queues one row per document here and
-- commits the offset only after the row is written; a worker reads the
-- document's facts from monetization (internal, service-token route bound to
-- the event's user), looks up the buyer's VERIFIED address from identity by
-- the event's user_id, and sends — the copyright_email_jobs pattern (019).
--
-- id is the UUIDv5 of (event type, invoice id) — PremiumInvoiceEmailDedupID —
-- which is the same key monetization derives the event id from, so a
-- redelivered or re-published event inserts nothing (ON CONFLICT DO NOTHING).
-- The payload holds ids only, never an address or an amount, and is emptied
-- once the job is done. recipient_id lets an account purge remove the rows.
CREATE TABLE IF NOT EXISTS premium_invoice_email_jobs (
    id           UUID PRIMARY KEY,
    recipient_id UUID NOT NULL,
    payload      JSONB NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'processing', 'sent', 'skipped', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until  TIMESTAMPTZ,
    lease_id     UUID,
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS premium_invoice_email_jobs_due ON premium_invoice_email_jobs (available_at, created_at)
    WHERE status IN ('pending', 'processing');
CREATE INDEX IF NOT EXISTS premium_invoice_email_jobs_recipient ON premium_invoice_email_jobs (recipient_id);
