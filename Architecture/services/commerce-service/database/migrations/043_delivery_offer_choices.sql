-- The job still goes to one partner at a time. A partner can compare three jobs.
DROP INDEX IF EXISTS commerce_delivery_one_offer_partner;
CREATE OR REPLACE FUNCTION commerce_delivery_offer_choice_limit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='offered' AND NEW.expires_at>clock_timestamp() THEN
  PERFORM user_id FROM commerce_delivery_profiles WHERE user_id=NEW.partner_user_id FOR UPDATE;
  IF (SELECT count(*) FROM commerce_delivery_offers WHERE partner_user_id=NEW.partner_user_id
      AND id<>NEW.id AND status='offered' AND expires_at>clock_timestamp())>=3 THEN
   RAISE EXCEPTION 'delivery offer choices full' USING ERRCODE='23505';
  END IF;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS commerce_delivery_offer_choice_limit ON commerce_delivery_offers;
CREATE TRIGGER commerce_delivery_offer_choice_limit BEFORE INSERT OR UPDATE OF status,expires_at ON commerce_delivery_offers
 FOR EACH ROW EXECUTE FUNCTION commerce_delivery_offer_choice_limit();
