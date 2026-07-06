-- FiGo customer marketplace demo catalog.
-- Idempotent: safe to run more than once in local development.

INSERT INTO food.cuisines (name, slug, image_url, sort_order) VALUES
  ('Pizza', 'pizza', 'https://images.unsplash.com/photo-1579751626657-72bc17010498?auto=format&fit=crop&w=300&q=80', 7),
  ('Burgers', 'burgers', 'https://images.unsplash.com/photo-1568901346375-23c9450c58cd?auto=format&fit=crop&w=300&q=80', 8),
  ('Desserts', 'desserts', 'https://images.unsplash.com/photo-1551024506-0bccd828d307?auto=format&fit=crop&w=300&q=80', 9),
  ('Cafe', 'cafe', 'https://images.unsplash.com/photo-1495474472287-4d71bcdd2085?auto=format&fit=crop&w=300&q=80', 10),
  ('Street Food', 'street-food', 'https://images.unsplash.com/photo-1601050690597-df0568f70950?auto=format&fit=crop&w=300&q=80', 11),
  ('Continental', 'continental', 'https://images.unsplash.com/photo-1547592180-85f173990554?auto=format&fit=crop&w=300&q=80', 12)
ON CONFLICT (slug) DO UPDATE SET image_url = EXCLUDED.image_url, sort_order = EXCLUDED.sort_order, is_active = TRUE;

UPDATE food.cuisines SET image_url = CASE slug
  WHEN 'biryani' THEN 'https://images.unsplash.com/photo-1633945274405-b6c8069047b0?auto=format&fit=crop&w=300&q=80'
  WHEN 'south-indian' THEN 'https://images.unsplash.com/photo-1589301760014-d929f3979dbc?auto=format&fit=crop&w=300&q=80'
  WHEN 'north-indian' THEN 'https://images.unsplash.com/photo-1603894584373-5ac82b2ae398?auto=format&fit=crop&w=300&q=80'
  WHEN 'chinese' THEN 'https://images.unsplash.com/photo-1525755662778-989d0524087e?auto=format&fit=crop&w=300&q=80'
  WHEN 'fast-food' THEN 'https://images.unsplash.com/photo-1594212699903-ec8a3eca50f5?auto=format&fit=crop&w=300&q=80'
  WHEN 'healthy' THEN 'https://images.unsplash.com/photo-1543362906-acfc16c67564?auto=format&fit=crop&w=300&q=80'
  ELSE image_url END
WHERE slug IN ('biryani','south-indian','north-indian','chinese','fast-food','healthy');

INSERT INTO food.restaurant_partners (id, owner_user_id, legal_name, display_name, phone, email, status, approved_by, approved_at, metadata)
VALUES ('22222222-2222-4222-8222-222222222229', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa9', 'FiGo Demo Hospitality Private Limited', 'FiGo Demo Hospitality', '+919900000009', 'catalog@figo.local', 'APPROVED', '99999999-9999-4999-8999-999999999999', NOW(), '{"seed":"customer-marketplace"}')
ON CONFLICT (id) DO UPDATE SET status='APPROVED', approved_at=NOW(), approved_by=EXCLUDED.approved_by;

INSERT INTO food.restaurants (
  id, partner_id, owner_user_id, name, slug, description, phone, email, status, is_open, is_accepting_orders,
  address_line1, city, state, postal_code, latitude, longitude, avg_rating, rating_count, min_order_amount,
  packaging_fee, avg_preparation_minutes, commission_percentage, approved_by, approved_at, metadata
) VALUES
('33333333-3333-4333-8333-333333333341','22222222-2222-4222-8222-222222222229','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa9','Charcoal & Lime','charcoal-and-lime','Flame-grilled kebabs, wraps and rice plates.','+919900000101','charcoal@figo.local','ACTIVE',TRUE,TRUE,'Jubilee Hills Road 36','Hyderabad','Telangana','500033',17.4312,78.4071,4.80,1240,199,18,22,16,'99999999-9999-4999-8999-999999999999',NOW(),'{"badge":"Chef pick"}'),
('33333333-3333-4333-8333-333333333342','22222222-2222-4222-8222-222222222229','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa9','Dough Theory','dough-theory','Hand-stretched sourdough pizza and baked sides.','+919900000102','dough@figo.local','ACTIVE',TRUE,TRUE,'Madhapur Main Road','Hyderabad','Telangana','500081',17.4483,78.3915,4.70,864,249,15,26,16,'99999999-9999-4999-8999-999999999999',NOW(),'{"badge":"Top rated"}'),
('33333333-3333-4333-8333-333333333343','22222222-2222-4222-8222-222222222229','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa9','Little Green Table','little-green-table','Bright salads, grain bowls and cold-pressed drinks.','+919900000103','green@figo.local','ACTIVE',TRUE,TRUE,'Film Nagar','Hyderabad','Telangana','500096',17.4138,78.4153,4.90,532,179,10,16,14,'99999999-9999-4999-8999-999999999999',NOW(),'{"badge":"Healthy choice"}'),
('33333333-3333-4333-8333-333333333344','22222222-2222-4222-8222-222222222229','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa9','The Noodle Room','the-noodle-room','Wok-tossed noodles, dim sum and Asian bowls.','+919900000104','noodles@figo.local','ACTIVE',TRUE,TRUE,'Kondapur Botanical Road','Hyderabad','Telangana','500084',17.4698,78.3639,4.50,731,159,14,19,15,'99999999-9999-4999-8999-999999999999',NOW(),'{"badge":"Fast delivery"}'),
('33333333-3333-4333-8333-333333333345','22222222-2222-4222-8222-222222222229','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa9','Bombay Bun Club','bombay-bun-club','Loaded burgers, crisp fries and thick shakes.','+919900000105','buns@figo.local','ACTIVE',TRUE,TRUE,'Banjara Hills Road 12','Hyderabad','Telangana','500034',17.4156,78.4347,4.60,1088,199,20,21,17,'99999999-9999-4999-8999-999999999999',NOW(),'{"badge":"Most loved"}'),
('33333333-3333-4333-8333-333333333346','22222222-2222-4222-8222-222222222229','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa9','Mitti Tiffin House','mitti-tiffin-house','South Indian breakfasts made fresh through the day.','+919900000106','mitti@figo.local','ACTIVE',TRUE,TRUE,'Begumpet Old Airport Road','Hyderabad','Telangana','500016',17.4440,78.4660,4.80,1422,99,8,14,12,'99999999-9999-4999-8999-999999999999',NOW(),'{"badge":"Local favourite"}'),
('33333333-3333-4333-8333-333333333347','22222222-2222-4222-8222-222222222229','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa9','Melt & Crumb','melt-and-crumb','Small-batch cakes, cookies and plated desserts.','+919900000107','crumb@figo.local','ACTIVE',TRUE,TRUE,'Sainikpuri High Street','Hyderabad','Telangana','500094',17.4875,78.5520,4.70,419,149,12,18,15,'99999999-9999-4999-8999-999999999999',NOW(),'{"badge":"New"}'),
('33333333-3333-4333-8333-333333333348','22222222-2222-4222-8222-222222222229','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa9','Deccan Dastarkhwan','deccan-dastarkhwan','Slow-cooked biryani, haleem and old-city classics.','+919900000108','deccan@figo.local','ACTIVE',TRUE,TRUE,'Tolichowki Main Road','Hyderabad','Telangana','500008',17.3991,78.4152,4.90,2216,249,22,29,18,'99999999-9999-4999-8999-999999999999',NOW(),'{"badge":"Iconic"}')
ON CONFLICT (slug) DO UPDATE SET status='ACTIVE', is_open=TRUE, is_accepting_orders=TRUE, approved_at=NOW(), approved_by=EXCLUDED.approved_by;

INSERT INTO food.restaurant_images (restaurant_id, image_url, image_type, sort_order)
SELECT r.id, v.image_url, 'hero', 0 FROM (VALUES
 ('charcoal-and-lime','https://images.unsplash.com/photo-1599487488170-d11ec9c172f0?auto=format&fit=crop&w=1200&q=84'),
 ('dough-theory','https://images.unsplash.com/photo-1579751626657-72bc17010498?auto=format&fit=crop&w=1200&q=84'),
 ('little-green-table','https://images.unsplash.com/photo-1543362906-acfc16c67564?auto=format&fit=crop&w=1200&q=84'),
 ('the-noodle-room','https://images.unsplash.com/photo-1569718212165-3a8278d5f624?auto=format&fit=crop&w=1200&q=84'),
 ('bombay-bun-club','https://images.unsplash.com/photo-1568901346375-23c9450c58cd?auto=format&fit=crop&w=1200&q=84'),
 ('mitti-tiffin-house','https://images.unsplash.com/photo-1589301760014-d929f3979dbc?auto=format&fit=crop&w=1200&q=84'),
 ('melt-and-crumb','https://images.unsplash.com/photo-1551024506-0bccd828d307?auto=format&fit=crop&w=1200&q=84'),
 ('deccan-dastarkhwan','https://images.unsplash.com/photo-1633945274405-b6c8069047b0?auto=format&fit=crop&w=1200&q=84')
) AS v(slug,image_url) JOIN food.restaurants r ON r.slug=v.slug
WHERE NOT EXISTS (SELECT 1 FROM food.restaurant_images i WHERE i.restaurant_id=r.id AND i.image_type='hero');

INSERT INTO food.restaurant_cuisines (restaurant_id, cuisine_id)
SELECT r.id, c.id FROM (VALUES
 ('charcoal-and-lime','north-indian'),('charcoal-and-lime','fast-food'),('dough-theory','pizza'),
 ('little-green-table','healthy'),('little-green-table','continental'),('the-noodle-room','chinese'),
 ('bombay-bun-club','burgers'),('bombay-bun-club','fast-food'),('mitti-tiffin-house','south-indian'),
 ('melt-and-crumb','desserts'),('melt-and-crumb','cafe'),('deccan-dastarkhwan','biryani'),('deccan-dastarkhwan','north-indian')
) AS v(slug,cuisine_slug) JOIN food.restaurants r ON r.slug=v.slug JOIN food.cuisines c ON c.slug=v.cuisine_slug
ON CONFLICT DO NOTHING;

INSERT INTO food.menu_categories (id, restaurant_id, name, description, sort_order)
SELECT (substr(md5(r.slug),1,8)||'-'||substr(md5(r.slug),9,4)||'-4'||substr(md5(r.slug),14,3)||'-8'||substr(md5(r.slug),18,3)||'-'||substr(md5(r.slug),21,12))::uuid, r.id, 'Popular', 'Most ordered dishes', 1
FROM food.restaurants r WHERE r.slug IN ('charcoal-and-lime','dough-theory','little-green-table','the-noodle-room','bombay-bun-club','mitti-tiffin-house','melt-and-crumb','deccan-dastarkhwan')
ON CONFLICT (id) DO NOTHING;

INSERT INTO food.menu_items (restaurant_id, category_id, name, description, food_type, base_price, discount_price, image_url, preparation_minutes, is_available, is_recommended, tax_percentage, metadata)
SELECT r.id, c.id, v.item_name, v.description, v.food_type::food.food_type, v.price, v.discount, v.image_url, r.avg_preparation_minutes, TRUE, TRUE, 5, '{"seed":"customer-marketplace"}'
FROM (VALUES
 ('charcoal-and-lime','Smoked Chicken Kebab','Charcoal grilled chicken, herb chutney.','NON_VEG',329::numeric,289::numeric,'https://images.unsplash.com/photo-1599487488170-d11ec9c172f0?auto=format&fit=crop&w=800&q=82'),
 ('dough-theory','Garden Burrata Pizza','Sourdough crust, tomato, burrata and basil.','VEG',449,399,'https://images.unsplash.com/photo-1579751626657-72bc17010498?auto=format&fit=crop&w=800&q=82'),
 ('little-green-table','Avocado Grain Bowl','Millets, greens, avocado and citrus dressing.','VEG',299,269,'https://images.unsplash.com/photo-1543362906-acfc16c67564?auto=format&fit=crop&w=800&q=82'),
 ('the-noodle-room','Chilli Garlic Noodles','Wok noodles with vegetables and chilli crisp.','VEG',249,219,'https://images.unsplash.com/photo-1569718212165-3a8278d5f624?auto=format&fit=crop&w=800&q=82'),
 ('bombay-bun-club','Double Smash Burger','Two chicken patties, cheese and house sauce.','NON_VEG',349,299,'https://images.unsplash.com/photo-1568901346375-23c9450c58cd?auto=format&fit=crop&w=800&q=82'),
 ('mitti-tiffin-house','Ghee Karam Dosa','Crisp dosa, karam podi, ghee and chutneys.','VEG',159,139,'https://images.unsplash.com/photo-1589301760014-d929f3979dbc?auto=format&fit=crop&w=800&q=82'),
 ('melt-and-crumb','Dark Chocolate Slice','Fudgy chocolate cake with ganache.','VEG',219,189,'https://images.unsplash.com/photo-1551024506-0bccd828d307?auto=format&fit=crop&w=800&q=82'),
 ('deccan-dastarkhwan','Hyderabadi Chicken Biryani','Dum-cooked basmati, chicken, mirchi salan and raita.','NON_VEG',379,329,'https://images.unsplash.com/photo-1633945274405-b6c8069047b0?auto=format&fit=crop&w=800&q=82')
) AS v(slug,item_name,description,food_type,price,discount,image_url)
JOIN food.restaurants r ON r.slug=v.slug JOIN food.menu_categories c ON c.restaurant_id=r.id AND c.name='Popular'
WHERE NOT EXISTS (SELECT 1 FROM food.menu_items m WHERE m.restaurant_id=r.id AND m.name=v.item_name);

INSERT INTO food.coupons (id, code, title, description, coupon_type, discount_value, max_discount_amount, min_order_amount, total_usage_limit, per_user_usage_limit, starts_at, ends_at, is_active, funded_by, created_by)
VALUES
 ('77777777-7777-4777-8777-777777777772','WELCOME100','Your first FiGo feast','Flat ₹100 off for new customers.','FLAT',100,100,399,10000,1,NOW()-INTERVAL '1 day',NOW()+INTERVAL '180 days',TRUE,'PLATFORM','99999999-9999-4999-8999-999999999999'),
 ('77777777-7777-4777-8777-777777777773','QUICK30','Fast food, lighter bill','30% off up to ₹120.','PERCENTAGE',30,120,299,10000,3,NOW()-INTERVAL '1 day',NOW()+INTERVAL '180 days',TRUE,'PLATFORM','99999999-9999-4999-8999-999999999999'),
 ('77777777-7777-4777-8777-777777777774','WEEKEND75','Weekend table','Flat ₹75 off this weekend.','FLAT',75,75,249,10000,2,NOW()-INTERVAL '1 day',NOW()+INTERVAL '180 days',TRUE,'PLATFORM','99999999-9999-4999-8999-999999999999')
ON CONFLICT (code) DO UPDATE SET is_active=TRUE, starts_at=EXCLUDED.starts_at, ends_at=EXCLUDED.ends_at;

INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, entity_id, new_value, user_agent)
SELECT '99999999-9999-4999-8999-999999999999', 'restaurant.approved', 'restaurant', r.id,
       jsonb_build_object('status','ACTIVE','source','admin-demo-catalog'), 'FiGo Admin seed'
FROM food.restaurants r
WHERE r.slug IN ('charcoal-and-lime','dough-theory','little-green-table','the-noodle-room','bombay-bun-club','mitti-tiffin-house','melt-and-crumb','deccan-dastarkhwan')
  AND NOT EXISTS (SELECT 1 FROM food.admin_audit_logs a WHERE a.entity_id=r.id AND a.action='restaurant.approved');
