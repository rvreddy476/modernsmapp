-- Existing approved locations do not need to be resubmitted when an admin
-- opens coverage. Explicit selections restrict reach; otherwise infer enabled
-- zones intersecting the saved radius. Customer matching additionally checks
-- the actual job point against both the zone and the partner's radius.
CREATE OR REPLACE VIEW doorstep.effective_pro_zones AS
 SELECT pz.pro_id,z.id AS zone_id
 FROM doorstep.pro_zones pz
 JOIN doorstep.professionals p ON p.id=pz.pro_id
 JOIN doorstep.zones z ON z.id=pz.zone_id AND z.city_code=p.city_code
 JOIN doorstep.cities c ON c.code=z.city_code
 WHERE z.active AND c.active
 UNION ALL
 SELECT p.id AS pro_id,z.id AS zone_id
 FROM doorstep.professionals p
 JOIN doorstep.zones z ON z.city_code=p.city_code
 JOIN doorstep.cities c ON c.code=z.city_code
 WHERE z.active AND c.active AND p.home_point IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM doorstep.pro_zones explicit WHERE explicit.pro_id=p.id)
 AND ST_DWithin(p.home_point,z.boundary,p.service_radius_m);

-- City is a derived coverage reference for older applications that predate
-- city setup. Never replace an existing city or guess between multiple cities.
CREATE OR REPLACE FUNCTION doorstep.coverage_city(point geography) RETURNS text
LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN count(DISTINCT z.city_code)=1 THEN min(z.city_code) END
 FROM doorstep.zones z JOIN doorstep.cities c ON c.code=z.city_code
 WHERE z.active AND c.active AND ST_Covers(z.boundary,point)
$$;

CREATE OR REPLACE FUNCTION doorstep.bind_pro_coverage_city() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.city_code IS NULL AND NEW.home_point IS NOT NULL THEN
  NEW.city_code := doorstep.coverage_city(NEW.home_point);
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS bind_pro_coverage_city ON doorstep.professionals;
CREATE TRIGGER bind_pro_coverage_city
 BEFORE INSERT OR UPDATE OF home_point,city_code ON doorstep.professionals
 FOR EACH ROW EXECUTE FUNCTION doorstep.bind_pro_coverage_city();

CREATE OR REPLACE FUNCTION doorstep.bind_existing_coverage_cities() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 UPDATE doorstep.professionals p
 SET city_code=doorstep.coverage_city(p.home_point),updated_at=now()
 WHERE p.city_code IS NULL AND p.home_point IS NOT NULL
 AND doorstep.coverage_city(p.home_point) IS NOT NULL;
 RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS bind_existing_zone_cities ON doorstep.zones;
CREATE TRIGGER bind_existing_zone_cities
 AFTER INSERT OR UPDATE OF active,boundary ON doorstep.zones
 FOR EACH STATEMENT EXECUTE FUNCTION doorstep.bind_existing_coverage_cities();
DROP TRIGGER IF EXISTS bind_existing_active_cities ON doorstep.cities;
CREATE TRIGGER bind_existing_active_cities
 AFTER UPDATE OF active ON doorstep.cities
 FOR EACH STATEMENT EXECUTE FUNCTION doorstep.bind_existing_coverage_cities();

UPDATE doorstep.professionals p
 SET city_code=doorstep.coverage_city(p.home_point),updated_at=now()
 WHERE p.city_code IS NULL AND p.home_point IS NOT NULL
 AND doorstep.coverage_city(p.home_point) IS NOT NULL;
