LOCK TABLE doorstep.professionals,doorstep.bookings,doorstep.booking_assignments IN SHARE ROW EXCLUSIVE MODE;
INSERT INTO partner_work.sessions(user_id) SELECT DISTINCT user_id FROM doorstep.professionals ON CONFLICT DO NOTHING;
-- Preserve future confirmed reservations without assigning their category duty.
INSERT INTO partner_work.reservations(id,user_id,category,job_id,starts_at,ends_at,state,offer_expires_at)
 SELECT gen_random_uuid(),p.user_id,'home_services',b.id,b.slot_start,b.slot_end,CASE WHEN b.status IN ('en_route','arrived','in_progress') THEN 'active' ELSE 'reserved' END,now()
 FROM doorstep.bookings b JOIN doorstep.professionals p ON p.id=b.reserved_pro_id
 WHERE b.status IN ('confirmed','assigned','en_route','arrived','in_progress','pro_unavailable') ON CONFLICT DO NOTHING;
INSERT INTO partner_work.reservations(id,user_id,category,job_id,starts_at,ends_at,state,offer_expires_at)
 SELECT gen_random_uuid(),p.user_id,'home_services',b.id,b.slot_start,b.slot_end,CASE WHEN b.status IN ('en_route','arrived','in_progress') THEN 'active' ELSE 'reserved' END,now()
 FROM doorstep.bookings b JOIN doorstep.booking_assignments a ON a.booking_id=b.id AND a.status='accepted' JOIN doorstep.professionals p ON p.id=a.pro_id
 WHERE b.status IN ('confirmed','assigned','en_route','arrived','in_progress') ON CONFLICT DO NOTHING;
UPDATE partner_work.sessions s SET category='home_services' WHERE category IS NULL AND EXISTS(SELECT 1 FROM partner_work.reservations r WHERE r.user_id=s.user_id AND r.category='home_services' AND r.state='active');
UPDATE partner_work.sessions s SET category='home_services' WHERE category IS NULL AND EXISTS(SELECT 1 FROM doorstep.professionals p WHERE p.user_id=s.user_id AND p.on_duty);

CREATE OR REPLACE FUNCTION partner_work.home_duty_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND NOT NEW.on_duty THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NEW.on_duty=OLD.on_duty THEN RETURN NEW; END IF;
 PERFORM partner_work.domain_duty(NEW.user_id,'home_services',NEW.on_duty);RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS partner_work_duty ON doorstep.professionals;
CREATE TRIGGER partner_work_duty BEFORE INSERT OR UPDATE OF on_duty ON doorstep.professionals FOR EACH ROW EXECUTE FUNCTION partner_work.home_duty_guard();

CREATE OR REPLACE FUNCTION partner_work.home_assignment_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u UUID; st TIMESTAMPTZ; en TIMESTAMPTZ; duty TEXT;
BEGIN
 SELECT user_id INTO u FROM doorstep.professionals WHERE id=NEW.pro_id;
 SELECT slot_start,slot_end INTO st,en FROM doorstep.bookings WHERE id=NEW.booking_id;
 IF NEW.status='offered' AND TG_OP='INSERT' THEN
  SELECT category INTO duty FROM partner_work.sessions WHERE user_id=u FOR UPDATE;
  IF duty IS DISTINCT FROM 'home_services' THEN RAISE EXCEPTION 'partner_work:OFF_DUTY' USING ERRCODE='P0001'; END IF;
  NEW.offer_expires_at:=LEAST(NEW.offer_expires_at,clock_timestamp()+interval '20 seconds');
  -- A previously confirmed scheduled reservation is already protected. The
  -- offer itself retains its own absolute expiry in the assignment table.
  IF NOT EXISTS(SELECT 1 FROM partner_work.reservations WHERE user_id=u AND category='home_services' AND job_id=NEW.booking_id AND state='reserved') THEN
   PERFORM partner_work.domain_work(u,'home_services',NEW.booking_id,'offered',st,en,NEW.offer_expires_at);
  END IF;
 ELSIF NEW.status='accepted' THEN
  IF NEW.offer_expires_at<=clock_timestamp() THEN RAISE EXCEPTION 'partner_work:EXPIRED' USING ERRCODE='P0001'; END IF;
  PERFORM partner_work.domain_work(u,'home_services',NEW.booking_id,'reserved',st,en);
 ELSIF NEW.status IN ('declined','expired','released','completed','no_show','cancelled') THEN
  PERFORM partner_work.domain_work(u,'home_services',NEW.booking_id,'released',st,en);
 END IF;RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS partner_work_assignment ON doorstep.booking_assignments;
CREATE TRIGGER partner_work_assignment BEFORE INSERT OR UPDATE OF status ON doorstep.booking_assignments FOR EACH ROW EXECUTE FUNCTION partner_work.home_assignment_guard();

CREATE OR REPLACE FUNCTION partner_work.home_booking_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u UUID; p UUID;
BEGIN
 IF TG_OP='UPDATE' AND OLD.reserved_pro_id IS DISTINCT FROM NEW.reserved_pro_id AND OLD.reserved_pro_id IS NOT NULL THEN
  SELECT user_id INTO u FROM doorstep.professionals WHERE id=OLD.reserved_pro_id;
  PERFORM partner_work.domain_work(u,'home_services',NEW.id,'released',NEW.slot_start,NEW.slot_end);
 END IF;
 p:=NEW.reserved_pro_id;
 IF p IS NULL THEN SELECT pro_id INTO p FROM doorstep.booking_assignments WHERE booking_id=NEW.id AND status='accepted' LIMIT 1;END IF;
 SELECT user_id INTO u FROM doorstep.professionals WHERE id=p;
 IF NEW.status IN ('confirmed','assigned') THEN
  PERFORM partner_work.domain_work(u,'home_services',NEW.id,'reserved',NEW.slot_start,NEW.slot_end);
 ELSIF NEW.status IN ('en_route','arrived','in_progress') THEN
  PERFORM partner_work.domain_work(u,'home_services',NEW.id,'active',NEW.slot_start,NEW.slot_end);
 ELSIF NEW.status IN ('completed','cancelled','expired','customer_no_show','pro_no_show') THEN
  -- Include released/reassigned partners so no stale calendar reservation
  -- survives the booking's authoritative terminal state.
  FOR u IN SELECT user_id FROM partner_work.reservations WHERE category='home_services' AND job_id=NEW.id AND state IN ('reserved','active','offered') ORDER BY user_id LOOP
   PERFORM partner_work.domain_work(u,'home_services',NEW.id,'released',NEW.slot_start,NEW.slot_end);
  END LOOP;
 END IF;RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS partner_work_booking ON doorstep.bookings;
CREATE TRIGGER partner_work_booking BEFORE INSERT OR UPDATE OF status,reserved_pro_id,slot_start,slot_end ON doorstep.bookings FOR EACH ROW EXECUTE FUNCTION partner_work.home_booking_guard();
