-- Duty/profile changes must not make an old location look recent.
ALTER TABLE commerce_delivery_availability ADD COLUMN IF NOT EXISTS location_recorded_at timestamptz;
