-- rider-service migration 003: Integer Pricing, KMS Data Keys, and Complete Pilot Seeds

-- 1. KMS Data Key Column for Envelope Encryption
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS kms_data_key_encrypted BYTEA;
ALTER TABLE rider_rides ALTER COLUMN otp_code TYPE TEXT;

-- 2. Pure Integer Money & BPS on Fare Rules
ALTER TABLE rider_fare_rules ADD COLUMN IF NOT EXISTS base_fare_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE rider_fare_rules ADD COLUMN IF NOT EXISTS per_km_fare_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE rider_fare_rules ADD COLUMN IF NOT EXISTS per_minute_fare_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE rider_fare_rules ADD COLUMN IF NOT EXISTS minimum_fare_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE rider_fare_rules ADD COLUMN IF NOT EXISTS platform_fee_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE rider_fare_rules ADD COLUMN IF NOT EXISTS night_multiplier_bps BIGINT NOT NULL DEFAULT 0;
ALTER TABLE rider_fare_rules ADD COLUMN IF NOT EXISTS peak_multiplier_bps BIGINT NOT NULL DEFAULT 0;
ALTER TABLE rider_fare_rules ADD COLUMN IF NOT EXISTS cancellation_fee_paise BIGINT NOT NULL DEFAULT 0;

-- 3. Seed Complete Fare Rules across all vehicle types for all cities
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN SELECT id FROM rider_cities WHERE country = 'India' LOOP
        INSERT INTO rider_fare_rules (city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare, platform_fee, cancellation_fee, base_fare_paise, per_km_fare_paise, per_minute_fare_paise, minimum_fare_paise, platform_fee_paise, cancellation_fee_paise)
        VALUES
            (c.id, 'bike', 15, 6, 0, 25, 0, 10, 1500, 600, 0, 2500, 0, 1000),
            (c.id, 'auto', 25, 12, 0, 40, 0, 15, 2500, 1200, 0, 4000, 0, 1500),
            (c.id, 'mini_cab', 50, 14, 0, 70, 0, 20, 5000, 1400, 0, 7000, 0, 2000),
            (c.id, 'sedan', 70, 16, 0, 90, 0, 25, 7000, 1600, 0, 9000, 0, 2500),
            (c.id, 'suv', 100, 20, 0, 130, 0, 35, 10000, 2000, 0, 13000, 0, 3500),
            (c.id, 'premium', 150, 25, 0, 200, 0, 50, 15000, 2500, 0, 20000, 0, 5000)
        ON CONFLICT DO NOTHING;

        UPDATE rider_fare_rules SET
            base_fare = 150, per_km_fare = 25, base_fare_paise = 15000, per_km_fare_paise = 2500
        WHERE city_id = c.id AND vehicle_type = 'premium';
    END LOOP;
END $$;

-- Backfill integer paise and BPS from existing float values
UPDATE rider_fare_rules SET
    base_fare_paise = ROUND(base_fare * 100),
    per_km_fare_paise = ROUND(per_km_fare * 100),
    per_minute_fare_paise = ROUND(per_minute_fare * 100),
    minimum_fare_paise = ROUND(minimum_fare * 100),
    platform_fee_paise = ROUND(platform_fee * 100),
    night_multiplier_bps = CASE WHEN night_multiplier > 1.0 THEN ROUND((night_multiplier - 1.0) * 10000) ELSE 0 END,
    peak_multiplier_bps = CASE WHEN peak_multiplier > 1.0 THEN ROUND((peak_multiplier - 1.0) * 10000) ELSE 0 END,
    cancellation_fee_paise = ROUND(cancellation_fee * 100)
WHERE base_fare_paise = 0 AND base_fare > 0;

-- 4. Seed city polygon boundaries for dual-point geofencing
DO $$
DECLARE
    blr_id UUID;
    mum_id UUID;
    del_id UUID;
    hyd_id UUID;
BEGIN
    SELECT id INTO blr_id FROM rider_cities WHERE name = 'Bengaluru' LIMIT 1;
    IF blr_id IS NOT NULL THEN
        INSERT INTO rider_zones (id, city_id, name, boundary, is_active)
        VALUES (
            'c0000000-0000-0000-0000-000000000001',
            blr_id,
            'Bengaluru Metro Zone',
            ST_GeogFromText('SRID=4326;POLYGON((77.40 12.80, 77.80 12.80, 77.80 13.15, 77.40 13.15, 77.40 12.80))'),
            TRUE
        ) ON CONFLICT DO NOTHING;
    END IF;

    SELECT id INTO mum_id FROM rider_cities WHERE name = 'Mumbai' LIMIT 1;
    IF mum_id IS NOT NULL THEN
        INSERT INTO rider_zones (id, city_id, name, boundary, is_active)
        VALUES (
            'c0000000-0000-0000-0000-000000000002',
            mum_id,
            'Mumbai Metro Zone',
            ST_GeogFromText('SRID=4326;POLYGON((72.70 18.85, 73.10 18.85, 73.10 19.30, 72.70 19.30, 72.70 18.85))'),
            TRUE
        ) ON CONFLICT DO NOTHING;
    END IF;

    SELECT id INTO del_id FROM rider_cities WHERE name = 'Delhi' LIMIT 1;
    IF del_id IS NOT NULL THEN
        INSERT INTO rider_zones (id, city_id, name, boundary, is_active)
        VALUES (
            'c0000000-0000-0000-0000-000000000003',
            del_id,
            'Delhi NCR Zone',
            ST_GeogFromText('SRID=4326;POLYGON((76.80 28.40, 77.40 28.40, 77.40 28.90, 76.80 28.90, 76.80 28.40))'),
            TRUE
        ) ON CONFLICT DO NOTHING;
    END IF;

    SELECT id INTO hyd_id FROM rider_cities WHERE name = 'Hyderabad' LIMIT 1;
    IF hyd_id IS NOT NULL THEN
        INSERT INTO rider_zones (id, city_id, name, boundary, is_active)
        VALUES (
            'c0000000-0000-0000-0000-000000000004',
            hyd_id,
            'Hyderabad Metro Zone',
            ST_GeogFromText('SRID=4326;POLYGON((78.20 17.20, 78.70 17.20, 78.70 17.60, 78.20 17.60, 78.20 17.20))'),
            TRUE
        ) ON CONFLICT DO NOTHING;
    END IF;
END $$;
