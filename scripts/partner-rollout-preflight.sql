BEGIN TRANSACTION READ ONLY;
WITH duties AS (
 SELECT user_id,'moped' category FROM rider_partners WHERE is_online
 UNION ALL SELECT user_id,'food_delivery' FROM food.delivery_partners WHERE is_online
 UNION ALL SELECT user_id,'home_services' FROM doorstep.professionals WHERE on_duty
)
SELECT 'multiple_on_duty_categories' AS check_name,count(*) AS conflicts
FROM (SELECT user_id FROM duties GROUP BY user_id HAVING count(DISTINCT category)>1) x;
WITH reservations AS (
 SELECT p.user_id,'moped' category,r.id job_id,COALESCE(r.requested_at,now()) starts_at,
 GREATEST(COALESCE(r.requested_at,now())+interval '2 hours',now()+interval '1 minute') ends_at
 FROM rider_rides r JOIN rider_partners p ON p.id=r.partner_id
 WHERE r.status IN ('partner_assigned','partner_arriving','arrived','otp_verified','in_progress')
 UNION
 SELECT p.user_id,'food_delivery',COALESCE(a.batch_id,a.order_id),now(),now()+interval '2 hours'
 FROM food.delivery_assignments a JOIN food.delivery_partners p ON p.id=a.delivery_partner_id
 WHERE a.status::text IN ('ASSIGNED','ACCEPTED','ARRIVED_AT_RESTAURANT','PICKED_UP','ARRIVED_AT_CUSTOMER')
 UNION
 SELECT p.user_id,'home_services',b.id,b.slot_start,b.slot_end
 FROM doorstep.bookings b JOIN doorstep.professionals p ON p.id=b.reserved_pro_id
 WHERE b.status IN ('confirmed','assigned','en_route','arrived','in_progress','pro_unavailable')
 UNION
 SELECT p.user_id,'home_services',b.id,b.slot_start,b.slot_end
 FROM doorstep.bookings b JOIN doorstep.booking_assignments a ON a.booking_id=b.id AND a.status='accepted'
 JOIN doorstep.professionals p ON p.id=a.pro_id
 WHERE b.status IN ('confirmed','assigned','en_route','arrived','in_progress')
)
SELECT 'overlapping_accepted_work' AS check_name,count(*) AS conflicts
FROM reservations a JOIN reservations b ON a.user_id=b.user_id
 AND (a.category,a.job_id)<(b.category,b.job_id)
 AND a.starts_at<b.ends_at AND b.starts_at<a.ends_at;
COMMIT;
