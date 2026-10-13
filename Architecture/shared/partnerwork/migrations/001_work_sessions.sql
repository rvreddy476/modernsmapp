CREATE SCHEMA IF NOT EXISTS partner_work;
CREATE TABLE IF NOT EXISTS partner_work.sessions (
 user_id UUID PRIMARY KEY,
 category TEXT CHECK(category IN ('moped','home_services','commerce_delivery','food_delivery')),
 revision BIGINT NOT NULL DEFAULT 0 CHECK(revision>=0),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS partner_work.reservations (
 id UUID PRIMARY KEY,
 user_id UUID NOT NULL REFERENCES partner_work.sessions(user_id),
 category TEXT NOT NULL CHECK(category IN ('moped','home_services','commerce_delivery','food_delivery')),
 job_id UUID NOT NULL,
 starts_at TIMESTAMPTZ NOT NULL,
 ends_at TIMESTAMPTZ NOT NULL CHECK(ends_at>starts_at),
 state TEXT NOT NULL CHECK(state IN ('offered','reserved','active','released','expired')),
 offer_expires_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(category,job_id,user_id)
);
CREATE INDEX IF NOT EXISTS partner_work_pending_user ON partner_work.reservations(user_id,starts_at)
 WHERE state IN ('offered','reserved','active');
-- Every transition is recorded with its aggregate revision. No location,
-- phone number, identity document, or bank information belongs in this log.
CREATE TABLE IF NOT EXISTS partner_work.events (
 id BIGSERIAL PRIMARY KEY,
 user_id UUID NOT NULL REFERENCES partner_work.sessions(user_id),
 revision BIGINT NOT NULL,
 event_type TEXT NOT NULL,
 reservation_id UUID REFERENCES partner_work.reservations(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(user_id,revision)
);
