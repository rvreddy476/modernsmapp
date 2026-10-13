-- Explicitly requested launch coverage: Uppal and Peerzadiguda, 5 km each.
-- Apply with psql ON_ERROR_STOP after migrations. No prices, approvals,
-- commissions, cancellation fees or booking schedules are seeded here.
-- The fixed actor identifies deployment automation, NOT a human administrator.
BEGIN;
WITH added AS (
 INSERT INTO doorstep.cities(code,name,state_code,active)
 VALUES('HYD','Hyderabad','36',true)
 ON CONFLICT(code) DO NOTHING RETURNING code
)
INSERT INTO doorstep.admin_audit_log(actor_user_id,permission,action,entity,entity_id,details)
 SELECT 'd7b8393b-d9a4-4a01-a09c-74e3e3b8fe61','deployment:coverage','deployment.city.create','city',code,
 '{"source":"user-authorized coverage deployment","actor_kind":"deployment_automation","request":"Uppal and Peerzadiguda, 5 km each"}'::jsonb FROM added;

WITH requested(name,slug,lat,lng) AS (
 VALUES ('Uppal','uppal',17.4015441,78.5681716),
        ('Peerzadiguda','peerzadiguda',17.3990571,78.6098059)
), added AS (
 INSERT INTO doorstep.zones(city_code,name,slug,boundary,active)
 SELECT 'HYD',name,slug,
 ST_Multi(ST_Buffer(ST_SetSRID(ST_MakePoint(lng,lat),4326)::geography,5000,32)::geometry)::geography,true
 FROM requested
 ON CONFLICT(city_code,slug) DO NOTHING
 RETURNING id,name
)
INSERT INTO doorstep.admin_audit_log(actor_user_id,permission,action,entity,entity_id,details)
 SELECT 'd7b8393b-d9a4-4a01-a09c-74e3e3b8fe61','deployment:coverage','deployment.zone.create','zone',id::text,
 jsonb_build_object('source','user-authorized coverage deployment','actor_kind','deployment_automation','name',name,'radius_m',5000) FROM added;
COMMIT;
