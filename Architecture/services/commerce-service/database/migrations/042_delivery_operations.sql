-- No existing shipment, courier, customer price, area or payout is changed.
CREATE TABLE IF NOT EXISTS commerce_delivery_locations (
 owner_kind TEXT NOT NULL CHECK(owner_kind IN ('order','seller')),
 owner_id UUID NOT NULL,
 latitude DOUBLE PRECISION NOT NULL CHECK(latitude BETWEEN -90 AND 90),
 longitude DOUBLE PRECISION NOT NULL CHECK(longitude BETWEEN -180 AND 180),
 label TEXT NOT NULL CHECK(length(btrim(label)) BETWEEN 2 AND 300),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(owner_kind,owner_id)
);
CREATE TABLE IF NOT EXISTS commerce_delivery_availability (
 user_id UUID PRIMARY KEY REFERENCES commerce_delivery_profiles(user_id),
 area_id UUID NOT NULL REFERENCES commerce_delivery_areas(id),
 latitude DOUBLE PRECISION NOT NULL CHECK(latitude BETWEEN -90 AND 90),
 longitude DOUBLE PRECISION NOT NULL CHECK(longitude BETWEEN -180 AND 180),
 online BOOLEAN NOT NULL DEFAULT false,
 revision BIGINT NOT NULL DEFAULT 1,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS commerce_delivery_jobs (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 order_id UUID NOT NULL, seller_id UUID NOT NULL, customer_user_id UUID NOT NULL,
 shipment_id UUID NOT NULL UNIQUE REFERENCES shipments(id),
 area_id UUID NOT NULL REFERENCES commerce_delivery_areas(id),
 status TEXT NOT NULL DEFAULT 'awaiting_ready' CHECK(status IN ('awaiting_ready','unassigned','assigned','picked_up','delivered','failed','cancelled')),
 partner_user_id UUID REFERENCES commerce_delivery_profiles(user_id),
 reservation_id UUID,
 payout_paise BIGINT NOT NULL CHECK(payout_paise>0),
 pickup_latitude DOUBLE PRECISION NOT NULL, pickup_longitude DOUBLE PRECISION NOT NULL,
 drop_latitude DOUBLE PRECISION NOT NULL, drop_longitude DOUBLE PRECISION NOT NULL,
 pickup_label TEXT NOT NULL, drop_label TEXT NOT NULL,
 pickup_code TEXT NOT NULL, delivery_code TEXT NOT NULL,
 code_failures INTEGER NOT NULL DEFAULT 0,
 revision BIGINT NOT NULL DEFAULT 1,
 release_pending BOOLEAN NOT NULL DEFAULT false,
 dispatch_after TIMESTAMPTZ NOT NULL DEFAULT now(),
 ready_at TIMESTAMPTZ, completed_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(order_id,seller_id)
);
ALTER TABLE commerce_delivery_jobs ADD COLUMN IF NOT EXISTS dispatch_after TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE UNIQUE INDEX IF NOT EXISTS commerce_delivery_one_active ON commerce_delivery_jobs(partner_user_id) WHERE status IN ('assigned','picked_up');
CREATE TABLE IF NOT EXISTS commerce_delivery_offers (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 job_id UUID NOT NULL REFERENCES commerce_delivery_jobs(id),
 partner_user_id UUID NOT NULL REFERENCES commerce_delivery_profiles(user_id),
 reservation_id UUID NOT NULL,
 status TEXT NOT NULL DEFAULT 'offered' CHECK(status IN ('offered','accepted','declined','expired','cancelled')),
 expires_at TIMESTAMPTZ NOT NULL,
 release_pending BOOLEAN NOT NULL DEFAULT false,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(job_id,partner_user_id)
);
ALTER TABLE commerce_delivery_offers ADD COLUMN IF NOT EXISTS release_pending BOOLEAN NOT NULL DEFAULT false;
CREATE UNIQUE INDEX IF NOT EXISTS commerce_delivery_one_offer_job ON commerce_delivery_offers(job_id) WHERE status='offered';
CREATE UNIQUE INDEX IF NOT EXISTS commerce_delivery_one_offer_partner ON commerce_delivery_offers(partner_user_id) WHERE status='offered';
CREATE TABLE IF NOT EXISTS commerce_delivery_earnings (
 job_id UUID PRIMARY KEY REFERENCES commerce_delivery_jobs(id),
 partner_user_id UUID NOT NULL REFERENCES commerce_delivery_profiles(user_id),
 amount_paise BIGINT NOT NULL CHECK(amount_paise>0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS commerce_delivery_support (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), job_id UUID NOT NULL REFERENCES commerce_delivery_jobs(id),
 actor_user_id UUID NOT NULL, reason TEXT NOT NULL CHECK(length(btrim(reason)) BETWEEN 3 AND 2000),
 status TEXT NOT NULL DEFAULT 'open', created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Persist courier choice under the order lock before calling an external
-- provider. A coverage change cannot turn an in-flight courier booking local.
CREATE TABLE IF NOT EXISTS commerce_delivery_route_choices (
 order_id UUID NOT NULL, seller_id UUID NOT NULL,
 route TEXT NOT NULL CHECK(route IN ('courier','local')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(order_id,seller_id)
);
