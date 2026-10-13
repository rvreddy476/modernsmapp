-- Three pending choices, one accepted assignment. Keep the existing session-row
-- lock so two devices cannot accept conflicting work or exceed the offer cap.
CREATE OR REPLACE FUNCTION partner_work.domain_work(u UUID,c TEXT,j UUID,stage TEXT,starts TIMESTAMPTZ,ends TIMESTAMPTZ,expiry TIMESTAMPTZ DEFAULT NULL) RETURNS void LANGUAGE plpgsql AS $$
DECLARE old partner_work.reservations%ROWTYPE; duty TEXT; rid UUID; next_state TEXT; rev BIGINT;
BEGIN
 IF u IS NULL OR j IS NULL THEN RETURN; END IF;
 INSERT INTO partner_work.sessions(user_id) VALUES(u) ON CONFLICT DO NOTHING;
 SELECT category INTO duty FROM partner_work.sessions WHERE user_id=u FOR UPDATE;
 SELECT * INTO old FROM partner_work.reservations WHERE user_id=u AND category=c AND job_id=j;
 IF stage='released' THEN
  IF old.id IS NULL OR old.state IN ('released','expired') THEN RETURN; END IF;
  UPDATE partner_work.reservations SET state='released',updated_at=clock_timestamp() WHERE id=old.id;
  rid:=old.id; next_state:='released';
 ELSE
  IF old.id IS NOT NULL AND old.state=stage AND (stage='active' OR (stage='reserved' AND old.starts_at=starts AND old.ends_at>=ends)) THEN RETURN; END IF;
  IF old.id IS NOT NULL AND old.state='active' AND stage='reserved' THEN RETURN; END IF;
  IF stage='offered' AND old.id IS NOT NULL THEN
   IF old.state='offered' AND old.offer_expires_at>clock_timestamp() THEN RETURN; END IF;
   RAISE EXCEPTION 'partner_work:EXPIRED' USING ERRCODE='P0001';
  END IF;
  IF stage IN ('offered','active') AND duty IS DISTINCT FROM c THEN RAISE EXCEPTION 'partner_work:OFF_DUTY' USING ERRCODE='P0001'; END IF;
  IF stage IN ('reserved','active') AND old.state='offered' AND (old.offer_expires_at<=clock_timestamp() OR duty IS DISTINCT FROM c) THEN RAISE EXCEPTION 'partner_work:EXPIRED' USING ERRCODE='P0001'; END IF;
  IF old.id IS NOT NULL AND old.state IN ('released','expired') THEN RAISE EXCEPTION 'partner_work:CONFLICT' USING ERRCODE='P0001'; END IF;
  IF stage='offered' AND (SELECT count(*) FROM partner_work.reservations WHERE user_id=u AND state='offered' AND offer_expires_at>clock_timestamp())>=3 THEN
   RAISE EXCEPTION 'partner_work:CONFLICT' USING ERRCODE='P0001';
  END IF;
  IF EXISTS(SELECT 1 FROM partner_work.reservations r WHERE r.user_id=u AND r.id IS DISTINCT FROM old.id AND
    (r.state='active' OR (r.state='reserved' AND (r.starts_at<=clock_timestamp() OR tstzrange(r.starts_at,r.ends_at,'[)') && tstzrange(starts,ends,'[)'))))) THEN
   RAISE EXCEPTION 'partner_work:CONFLICT' USING ERRCODE='P0001';
  END IF;
  IF old.id IS NULL THEN
   rid:=gen_random_uuid();
   INSERT INTO partner_work.reservations(id,user_id,category,job_id,starts_at,ends_at,state,offer_expires_at)
    VALUES(rid,u,c,j,starts,ends,stage,COALESCE(LEAST(expiry,clock_timestamp()+interval '20 seconds'),clock_timestamp()+interval '20 seconds'));
  ELSE
   rid:=old.id;
   UPDATE partner_work.reservations SET state=stage,starts_at=starts,ends_at=ends,updated_at=clock_timestamp() WHERE id=rid;
  END IF;
  IF stage IN ('reserved','active') THEN
   UPDATE partner_work.reservations SET state='released',updated_at=clock_timestamp() WHERE user_id=u AND id<>rid AND state='offered';
  END IF;
  next_state:=stage;
 END IF;
 UPDATE partner_work.sessions SET revision=revision+1,updated_at=clock_timestamp() WHERE user_id=u RETURNING revision INTO rev;
 INSERT INTO partner_work.events(user_id,revision,event_type,reservation_id) VALUES(u,rev,'work.'||next_state,rid);
END $$;
