LOCK TABLE food.delivery_partners,food.delivery_offers,food.delivery_assignments IN SHARE ROW EXCLUSIVE MODE;
INSERT INTO partner_work.sessions(user_id) SELECT DISTINCT user_id FROM food.delivery_partners ON CONFLICT DO NOTHING;
INSERT INTO partner_work.reservations(id,user_id,category,job_id,starts_at,ends_at,state,offer_expires_at)
 SELECT gen_random_uuid(),p.user_id,'food_delivery',COALESCE(a.batch_id,a.order_id),now(),now()+interval '2 hours','active',now()
 FROM food.delivery_assignments a JOIN food.delivery_partners p ON p.id=a.delivery_partner_id
 WHERE a.status::text IN ('ASSIGNED','ACCEPTED','ARRIVED_AT_RESTAURANT','PICKED_UP','ARRIVED_AT_CUSTOMER') ON CONFLICT DO NOTHING;
UPDATE partner_work.sessions s SET category='food_delivery' WHERE category IS NULL AND EXISTS(SELECT 1 FROM partner_work.reservations r WHERE r.user_id=s.user_id AND r.category='food_delivery' AND r.state='active');
UPDATE partner_work.sessions s SET category='food_delivery' WHERE category IS NULL AND EXISTS(SELECT 1 FROM food.delivery_partners p WHERE p.user_id=s.user_id AND p.is_online);

CREATE OR REPLACE FUNCTION partner_work.food_duty_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND NOT NEW.is_online THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NEW.is_online=OLD.is_online THEN RETURN NEW; END IF;
 PERFORM partner_work.domain_duty(NEW.user_id,'food_delivery',NEW.is_online);RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS partner_work_duty ON food.delivery_partners;
CREATE TRIGGER partner_work_duty BEFORE INSERT OR UPDATE OF is_online ON food.delivery_partners FOR EACH ROW EXECUTE FUNCTION partner_work.food_duty_guard();

CREATE OR REPLACE FUNCTION partner_work.food_offer_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u UUID; j UUID;
BEGIN
 SELECT user_id INTO u FROM food.delivery_partners WHERE id=NEW.delivery_partner_id;
 j:=COALESCE(NEW.batch_id,NEW.order_id);
 IF NEW.status::text='pending' AND TG_OP='INSERT' THEN
  NEW.expires_at:=LEAST(NEW.expires_at,clock_timestamp()+interval '20 seconds');
  PERFORM partner_work.domain_work(u,'food_delivery',j,'offered',clock_timestamp(),clock_timestamp()+interval '2 hours',NEW.expires_at);
 ELSIF NEW.status::text='accepted' THEN
  PERFORM partner_work.domain_work(u,'food_delivery',j,'reserved',clock_timestamp(),clock_timestamp()+interval '2 hours');
 ELSIF NEW.status::text IN ('expired','rejected','superseded') THEN
  PERFORM partner_work.domain_work(u,'food_delivery',j,'released',clock_timestamp(),clock_timestamp()+interval '2 hours');
 END IF;RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS partner_work_offer ON food.delivery_offers;
CREATE TRIGGER partner_work_offer BEFORE INSERT OR UPDATE OF status ON food.delivery_offers FOR EACH ROW EXECUTE FUNCTION partner_work.food_offer_guard();

CREATE OR REPLACE FUNCTION partner_work.food_job_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u UUID; j UUID;
BEGIN
 SELECT user_id INTO u FROM food.delivery_partners WHERE id=NEW.delivery_partner_id;
 j:=COALESCE(NEW.batch_id,NEW.order_id);
 IF NEW.status::text IN ('ASSIGNED','ACCEPTED','ARRIVED_AT_RESTAURANT','PICKED_UP','ARRIVED_AT_CUSTOMER') THEN
  PERFORM partner_work.domain_work(u,'food_delivery',j,'active',clock_timestamp(),clock_timestamp()+interval '2 hours');
 ELSIF NEW.delivery_partner_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM food.delivery_assignments a WHERE a.id<>NEW.id AND a.delivery_partner_id=NEW.delivery_partner_id AND COALESCE(a.batch_id,a.order_id)=j AND a.status::text IN ('ASSIGNED','ACCEPTED','ARRIVED_AT_RESTAURANT','PICKED_UP','ARRIVED_AT_CUSTOMER')) THEN
  PERFORM partner_work.domain_work(u,'food_delivery',j,'released',clock_timestamp(),clock_timestamp()+interval '1 minute');
 END IF;RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS partner_work_job ON food.delivery_assignments;
CREATE TRIGGER partner_work_job BEFORE INSERT OR UPDATE OF status,delivery_partner_id ON food.delivery_assignments FOR EACH ROW EXECUTE FUNCTION partner_work.food_job_guard();
