LOCK TABLE rider_partners,rider_rides,rider_ride_offers IN SHARE ROW EXCLUSIVE MODE;
INSERT INTO partner_work.sessions(user_id) SELECT DISTINCT user_id FROM rider_partners ON CONFLICT DO NOTHING;
-- Existing work is preserved even if an older client incorrectly marked duty off.
INSERT INTO partner_work.reservations(id,user_id,category,job_id,starts_at,ends_at,state,offer_expires_at)
 SELECT gen_random_uuid(),p.user_id,'moped',r.id,COALESCE(r.requested_at,now()),GREATEST(COALESCE(r.requested_at,now())+interval '2 hours',now()+interval '1 minute'),'active',now()
 FROM rider_rides r JOIN rider_partners p ON p.id=r.partner_id
 WHERE r.status IN ('partner_assigned','partner_arriving','arrived','otp_verified','in_progress') ON CONFLICT DO NOTHING;
UPDATE partner_work.sessions s SET category='moped' WHERE category IS NULL AND EXISTS(SELECT 1 FROM partner_work.reservations r WHERE r.user_id=s.user_id AND r.category='moped' AND r.state='active');
UPDATE partner_work.sessions s SET category='moped' WHERE category IS NULL AND EXISTS(SELECT 1 FROM rider_partners p WHERE p.user_id=s.user_id AND p.is_online);

CREATE OR REPLACE FUNCTION partner_work.moped_duty_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND NOT NEW.is_online THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NEW.is_online=OLD.is_online THEN RETURN NEW; END IF;
 PERFORM partner_work.domain_duty(NEW.user_id,'moped',NEW.is_online);RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS partner_work_duty ON rider_partners;
CREATE TRIGGER partner_work_duty BEFORE INSERT OR UPDATE OF is_online ON rider_partners FOR EACH ROW EXECUTE FUNCTION partner_work.moped_duty_guard();

CREATE OR REPLACE FUNCTION partner_work.moped_offer_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u UUID;
BEGIN
 SELECT user_id INTO u FROM rider_partners WHERE id=NEW.partner_id;
 IF NEW.status='sent' AND TG_OP='INSERT' THEN
  NEW.expires_at:=LEAST(NEW.expires_at,clock_timestamp()+interval '20 seconds');
  PERFORM partner_work.domain_work(u,'moped',NEW.ride_id,'offered',clock_timestamp(),clock_timestamp()+interval '2 hours',NEW.expires_at);
 ELSIF NEW.status='accepted' THEN
  PERFORM partner_work.domain_work(u,'moped',NEW.ride_id,'reserved',clock_timestamp(),clock_timestamp()+interval '2 hours');
 ELSIF NEW.status::text IN ('expired','rejected','declined','cancelled','superseded') THEN
  PERFORM partner_work.domain_work(u,'moped',NEW.ride_id,'released',clock_timestamp(),clock_timestamp()+interval '2 hours');
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS partner_work_offer ON rider_ride_offers;
CREATE TRIGGER partner_work_offer BEFORE INSERT OR UPDATE OF status ON rider_ride_offers FOR EACH ROW EXECUTE FUNCTION partner_work.moped_offer_guard();

CREATE OR REPLACE FUNCTION partner_work.moped_ride_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u UUID;
BEGIN
 SELECT user_id INTO u FROM rider_partners WHERE id=NEW.partner_id;
 IF NEW.status IN ('partner_assigned','partner_arriving','arrived','otp_verified','in_progress') THEN
  PERFORM partner_work.domain_work(u,'moped',NEW.id,'active',COALESCE(NEW.requested_at,clock_timestamp()),GREATEST(COALESCE(NEW.requested_at,clock_timestamp())+interval '2 hours',clock_timestamp()+interval '1 minute'));
 ELSIF NEW.partner_id IS NOT NULL AND NEW.status::text IN ('completed','cancelled','cancelled_by_customer','cancelled_by_partner','cancelled_by_admin','cancelled_by_system','failed','expired','no_partner_found') THEN
  PERFORM partner_work.domain_work(u,'moped',NEW.id,'released',clock_timestamp(),clock_timestamp()+interval '1 minute');
 END IF; RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS partner_work_ride ON rider_rides;
CREATE TRIGGER partner_work_ride BEFORE INSERT OR UPDATE OF status,partner_id ON rider_rides FOR EACH ROW EXECUTE FUNCTION partner_work.moped_ride_guard();
