-- Existing APIs may still read their local online flag. Keep those mirrors
-- false when another category owns duty; the session remains authoritative.
CREATE OR REPLACE FUNCTION partner_work.sync_duty_mirrors() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND NEW.category IS NOT DISTINCT FROM OLD.category THEN RETURN NEW;END IF;
 IF NEW.category IS DISTINCT FROM 'moped' AND to_regclass('public.rider_partners') IS NOT NULL THEN
  EXECUTE 'UPDATE rider_partners SET is_online=false WHERE user_id=$1 AND is_online' USING NEW.user_id;
  IF to_regclass('public.rider_ride_offers') IS NOT NULL THEN
   EXECUTE 'UPDATE rider_ride_offers SET status=''superseded'' WHERE partner_id IN (SELECT id FROM rider_partners WHERE user_id=$1) AND status=''sent''' USING NEW.user_id;
  END IF;
  IF to_regclass('public.rider_partner_locations') IS NOT NULL THEN
   EXECUTE 'UPDATE rider_partner_locations SET is_online=false WHERE partner_id IN (SELECT id FROM rider_partners WHERE user_id=$1) AND is_online' USING NEW.user_id;
  END IF;
 END IF;
 IF NEW.category IS DISTINCT FROM 'food_delivery' AND to_regclass('food.delivery_partners') IS NOT NULL THEN
  EXECUTE 'UPDATE food.delivery_partners SET is_online=false WHERE user_id=$1 AND is_online' USING NEW.user_id;
  IF to_regclass('food.delivery_offers') IS NOT NULL THEN
   EXECUTE 'UPDATE food.delivery_offers SET status=''superseded'' WHERE delivery_partner_id IN (SELECT id FROM food.delivery_partners WHERE user_id=$1) AND status=''pending''' USING NEW.user_id;
  END IF;
 END IF;
 IF NEW.category IS DISTINCT FROM 'home_services' AND to_regclass('doorstep.professionals') IS NOT NULL THEN
  EXECUTE 'UPDATE doorstep.professionals SET on_duty=false WHERE user_id=$1 AND on_duty' USING NEW.user_id;
  IF to_regclass('doorstep.booking_assignments') IS NOT NULL THEN
   EXECUTE 'UPDATE doorstep.booking_assignments SET status=''expired'' WHERE pro_id IN (SELECT id FROM doorstep.professionals WHERE user_id=$1) AND status=''offered''' USING NEW.user_id;
  END IF;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS partner_work_duty_mirrors ON partner_work.sessions;
CREATE TRIGGER partner_work_duty_mirrors AFTER UPDATE OF category ON partner_work.sessions FOR EACH ROW EXECUTE FUNCTION partner_work.sync_duty_mirrors();
