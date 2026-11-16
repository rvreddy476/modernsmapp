-- 054 — a seller may mark their OWN self-shipped parcel delivered.
--
-- A seller who records their own courier and tracking number at ship time
-- (applyManualBooking: only under a provider that accepts manual booking)
-- has no carrier webhook behind the shipment, so nothing ever moved the
-- order past `shipped`: no delivered notice, no review, no delivered tab.
--
-- Three things, all expand-only:
--
--   shipments.seller_managed     the shipment was recorded by the seller
--                                with their own courier, not booked with a
--                                carrier whose webhooks track it. Written at
--                                booking from now on; backfilled below from
--                                the booking event's own remark, which is how
--                                the timeline has always said so.
--   shipments.delivery_*         who confirmed delivery when it was not the
--                                courier (today only 'seller'), when they
--                                said it was delivered is delivered_at; when
--                                they said so, their note and proof photo.
--                                The buyer's order screen shows the first,
--                                so "marked delivered by the seller" is never
--                                presented as a courier scan.
--   order_status_transitions     shipped|out_for_delivery -> delivered for a
--                                seller. The Go path admits it only for a
--                                seller-managed shipment of the seller's own
--                                order (Store.MarkDeliveredBySeller).
--
-- Expand-only: every column is nullable or carries a constant default, so
-- ADD COLUMN is catalog-only (no rewrite) and an old pod that never names
-- them is unaffected; an old pod's manual booking during the rollout is
-- recorded seller_managed=false (the default) and is simply not markable.
-- Adding a permitted pair to the matrix cannot reject any writer. The ALTER
-- runs only when a column is missing, with lock_timeout 5s, so an ALTER
-- queued behind a long transaction fails the boot (the pod retries, old pods
-- keep serving) instead of stalling every shipment write behind it.
DO $$
DECLARE
    prev_lock_timeout TEXT := current_setting('lock_timeout');
BEGIN
    IF to_regclass('shipments') IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = to_regclass('shipments')
          AND attname = 'delivery_proof_media_id' AND NOT attisdropped
    ) THEN
        PERFORM set_config('lock_timeout', '5s', true);
        ALTER TABLE shipments
            ADD COLUMN IF NOT EXISTS seller_managed             BOOLEAN NOT NULL DEFAULT false,
            ADD COLUMN IF NOT EXISTS delivery_confirmed_by      TEXT,
            ADD COLUMN IF NOT EXISTS delivery_confirmed_by_user UUID,
            ADD COLUMN IF NOT EXISTS delivery_confirmed_at      TIMESTAMPTZ,
            ADD COLUMN IF NOT EXISTS delivery_note              TEXT,
            ADD COLUMN IF NOT EXISTS delivery_proof_media_id    UUID;
        PERFORM set_config('lock_timeout', prev_lock_timeout, true);
    END IF;
END $$;

-- Backfill: a shipment whose booking event says "recorded by seller: …"
-- (service.bookingRemark with manual=true) was recorded by the seller. That
-- remark is written only when applyManualBooking took the seller's values.
UPDATE shipments sh
   SET seller_managed = true
 WHERE sh.seller_managed = false
   AND sh.courier <> 'cleestudio_local'
   AND EXISTS (
       SELECT 1 FROM shipment_events ev
        WHERE ev.shipment_id = sh.id
          AND ev.status = 'booked'
          AND ev.remark LIKE 'recorded by seller:%'
   );

INSERT INTO order_status_transitions (from_status, to_status, actor_type) VALUES
    ('shipped','delivered','seller'),
    ('out_for_delivery','delivered','seller')
ON CONFLICT DO NOTHING;
