-- rider-service migration 003: the Mopedu pricing engine.
--
-- Every statement is re-runnable (IF NOT EXISTS / ON CONFLICT / guarded
-- UPDATE) so a partially applied run can be retried. Money is BIGINT paise,
-- rates are basis points; the NUMERIC float columns on rider_fare_rules stay
-- for the legacy admin API but no longer price anything.

-- 1. City timezone: fare windows are local time -----------------------------
ALTER TABLE rider_cities ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT 'Asia/Kolkata';

-- 2. Fare windows: peak / night time windows replace the permanent
--    night_multiplier / peak_multiplier. multiplier_bps 10000 = no change.
--    days_of_week is a bitmask, Mon=1 .. Sun=64. start_minute / end_minute are
--    local minutes since midnight; start > end wraps midnight.
CREATE TABLE IF NOT EXISTS rider_fare_windows (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id        UUID NOT NULL REFERENCES rider_cities(id) ON DELETE CASCADE,
    vehicle_type   rider_vehicle_type,
    name           TEXT NOT NULL,
    days_of_week   SMALLINT NOT NULL CHECK (days_of_week BETWEEN 1 AND 127),
    start_minute   INT NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),
    end_minute     INT NOT NULL CHECK (end_minute BETWEEN 0 AND 1440),
    multiplier_bps BIGINT NOT NULL DEFAULT 10000 CHECK (multiplier_bps BETWEEN 10000 AND 30000),
    priority       INT NOT NULL DEFAULT 0,
    is_active      BOOLEAN NOT NULL DEFAULT TRUE,
    effective_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    effective_to   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rider_fare_windows_city
    ON rider_fare_windows(city_id, is_active);

-- Seed: Morning peak Mon-Fri 08:00-10:30 x1.25, Evening peak Mon-Fri
-- 17:30-20:30 x1.25, Night every day 23:00-05:00 x1.20 (bike and auto).
-- Guarded per (city, name, vehicle type) so a re-run never duplicates.
DO $$
DECLARE
    c RECORD;
    w RECORD;
BEGIN
    FOR c IN SELECT id FROM rider_cities WHERE country = 'India' LOOP
        FOR w IN SELECT * FROM (VALUES
                (NULL::rider_vehicle_type, 'Morning peak', 31,  480,  630, 12500, 10),
                (NULL::rider_vehicle_type, 'Evening peak', 31, 1050, 1230, 12500, 10),
                ('auto'::rider_vehicle_type, 'Night',     127, 1380,  300, 12000,  5),
                ('bike'::rider_vehicle_type, 'Night',     127, 1380,  300, 12000,  5)
            ) AS v(vehicle_type, name, days_of_week, start_minute, end_minute, multiplier_bps, priority)
        LOOP
            IF NOT EXISTS (
                SELECT 1 FROM rider_fare_windows f
                WHERE f.city_id = c.id AND f.name = w.name
                  AND f.vehicle_type IS NOT DISTINCT FROM w.vehicle_type
            ) THEN
                INSERT INTO rider_fare_windows (city_id, vehicle_type, name, days_of_week, start_minute, end_minute, multiplier_bps, priority)
                VALUES (c.id, w.vehicle_type, w.name, w.days_of_week, w.start_minute, w.end_minute, w.multiplier_bps, w.priority);
            END IF;
        END LOOP;
    END LOOP;
END $$;

-- 3. Waiting and cancellation charge columns on fare rules -------------------
ALTER TABLE rider_fare_rules ADD COLUMN IF NOT EXISTS waiting_free_minutes     INT    NOT NULL DEFAULT 3;
ALTER TABLE rider_fare_rules ADD COLUMN IF NOT EXISTS waiting_per_minute_paise BIGINT NOT NULL DEFAULT 100;
ALTER TABLE rider_fare_rules ADD COLUMN IF NOT EXISTS cancel_free_seconds      INT    NOT NULL DEFAULT 120;

-- Seeded rules: auto waits at Rs 1.50/min, everything else at the Rs 1 default.
UPDATE rider_fare_rules SET waiting_per_minute_paise = 150
WHERE vehicle_type = 'auto' AND waiting_per_minute_paise = 100;

-- Paise backfill for rows created by the legacy float admin API after 002.
UPDATE rider_fare_rules SET
    base_fare_paise        = ROUND(base_fare * 100),
    per_km_fare_paise      = ROUND(per_km_fare * 100),
    per_minute_fare_paise  = ROUND(per_minute_fare * 100),
    minimum_fare_paise     = ROUND(minimum_fare * 100),
    platform_fee_paise     = ROUND(platform_fee * 100),
    cancellation_fee_paise = ROUND(cancellation_fee * 100)
WHERE base_fare_paise = 0 AND base_fare > 0;

-- 4. Customer outstanding: a cancellation fee owed by the customer, charged on
--    the next ride's quote (or paid directly through the payments lane).
--    settled_by_ride_id is set when a ride reserves the line at creation; it
--    is cleared when that ride is cancelled and the row goes to 'settled' when
--    the ride completes.
CREATE TABLE IF NOT EXISTS rider_customer_outstanding (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_user_id   UUID NOT NULL,
    ride_id            UUID NOT NULL REFERENCES rider_rides(id),
    amount_paise       BIGINT NOT NULL CHECK (amount_paise > 0),
    reason             TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','settled','waived')),
    settled_by_ride_id UUID REFERENCES rider_rides(id),
    waived_by          UUID,
    waive_reason       TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at         TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_rider_outstanding_customer_pending
    ON rider_customer_outstanding(customer_user_id) WHERE status = 'pending';
CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_outstanding_ride
    ON rider_customer_outstanding(ride_id);

ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS waiting_charge_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS tracked_distance_m   INT;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS actual_duration_s    INT;

-- 5. Coupons ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_coupons (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code                 TEXT NOT NULL,
    description          TEXT NOT NULL DEFAULT '',
    discount_type        TEXT NOT NULL CHECK (discount_type IN ('flat','percent')),
    discount_value_paise BIGINT NOT NULL DEFAULT 0 CHECK (discount_value_paise >= 0),
    percent_bps          INT    NOT NULL DEFAULT 0 CHECK (percent_bps BETWEEN 0 AND 10000),
    max_discount_paise   BIGINT NOT NULL DEFAULT 0 CHECK (max_discount_paise >= 0),
    min_fare_paise       BIGINT NOT NULL DEFAULT 0 CHECK (min_fare_paise >= 0),
    city_id              UUID REFERENCES rider_cities(id),
    vehicle_types        TEXT[],
    first_ride_only      BOOLEAN NOT NULL DEFAULT FALSE,
    per_user_limit       INT NOT NULL DEFAULT 1 CHECK (per_user_limit >= 0),
    total_limit          INT NOT NULL DEFAULT 0 CHECK (total_limit >= 0),
    used_count           INT NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    starts_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ends_at              TIMESTAMPTZ,
    is_active            BOOLEAN NOT NULL DEFAULT TRUE,
    created_by           UUID,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_coupons_code ON rider_coupons(UPPER(code));

CREATE TABLE IF NOT EXISTS rider_coupon_redemptions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    coupon_id        UUID NOT NULL REFERENCES rider_coupons(id),
    customer_user_id UUID NOT NULL,
    ride_id          UUID NOT NULL REFERENCES rider_rides(id),
    quote_id         UUID,
    discount_paise   BIGINT NOT NULL CHECK (discount_paise >= 0),
    status           TEXT NOT NULL DEFAULT 'reserved' CHECK (status IN ('reserved','applied','released')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_coupon_redemptions_ride ON rider_coupon_redemptions(ride_id);
CREATE INDEX IF NOT EXISTS idx_rider_coupon_redemptions_coupon_user
    ON rider_coupon_redemptions(coupon_id, customer_user_id);

-- 6. Server-tracked route points (partner GPS during arrived / in_progress) --
CREATE TABLE IF NOT EXISTS rider_ride_track_points (
    id          BIGSERIAL PRIMARY KEY,
    ride_id     UUID NOT NULL REFERENCES rider_rides(id) ON DELETE CASCADE,
    partner_id  UUID NOT NULL,
    lat         DOUBLE PRECISION NOT NULL,
    lng         DOUBLE PRECISION NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    speed_mps   DOUBLE PRECISION
);
CREATE INDEX IF NOT EXISTS idx_rider_track_points_ride ON rider_ride_track_points(ride_id, recorded_at);

-- 7. Ride payment methods: wallet is no longer accepted for rides (it stays
--    for partner subscriptions); card joins upi for the payments lane. The
--    constraint keeps 'wallet' so historical rows remain valid.
ALTER TABLE rider_ride_payments DROP CONSTRAINT IF EXISTS rider_ride_payments_payment_method_check;
ALTER TABLE rider_ride_payments ADD CONSTRAINT rider_ride_payments_payment_method_check
    CHECK (payment_method IN ('cash','wallet','upi','card'));
