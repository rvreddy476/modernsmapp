-- Configuration only. No seller, area, payout, or dispatch is enabled by default.
CREATE TABLE IF NOT EXISTS commerce_delivery_areas (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 name TEXT NOT NULL CHECK(length(btrim(name)) BETWEEN 2 AND 100),
 latitude DOUBLE PRECISION NOT NULL CHECK(latitude BETWEEN -90 AND 90),
 longitude DOUBLE PRECISION NOT NULL CHECK(longitude BETWEEN -180 AND 180),
 radius_meters INTEGER NOT NULL CHECK(radius_meters BETWEEN 100 AND 100000),
 payout_paise BIGINT CHECK(payout_paise > 0),
 enabled BOOLEAN NOT NULL DEFAULT false,
 revision BIGINT NOT NULL DEFAULT 1,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK(NOT enabled OR payout_paise IS NOT NULL)
);
CREATE TABLE IF NOT EXISTS commerce_delivery_area_sellers (
 area_id UUID NOT NULL REFERENCES commerce_delivery_areas(id),
 seller_id UUID NOT NULL REFERENCES sellers(id),
 enabled BOOLEAN NOT NULL DEFAULT false,
 revision BIGINT NOT NULL DEFAULT 1,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(area_id,seller_id)
);
CREATE TABLE IF NOT EXISTS commerce_delivery_config_audit (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 area_id UUID NOT NULL REFERENCES commerce_delivery_areas(id),
 seller_id UUID,
 actor_id UUID NOT NULL,
 before_state JSONB,
 after_state JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
DROP TRIGGER IF EXISTS commerce_delivery_config_audit_append_only ON commerce_delivery_config_audit;
CREATE TRIGGER commerce_delivery_config_audit_append_only BEFORE UPDATE OR DELETE ON commerce_delivery_config_audit -- expand-only: guards commerce_delivery_config_audit, a table this file creates; no pre-041 image writes it
 FOR EACH ROW EXECUTE FUNCTION commerce_delivery_audit_append_only();
