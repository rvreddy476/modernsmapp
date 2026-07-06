-- A complete, data-backed showcase menu for FiGo Biryani House.
-- Safe to re-run in local development.

WITH restaurant AS (
  SELECT id FROM food.restaurants WHERE slug = 'figo-biryani-house'
), categories(name, description, sort_order) AS (VALUES
  ('Starters', 'Fire-kissed small plates and kitchen favourites.', 1),
  ('Veg Curries', 'Comforting vegetarian curries finished to order.', 2),
  ('Non-Veg Curries', 'Slow-cooked chicken and mutton gravies.', 3),
  ('Main Course', 'Biryani, rice and generous sharing plates.', 4),
  ('Indian Breads', 'Fresh from the tandoor and griddle.', 5),
  ('Desserts', 'A sweet finish from the FiGo kitchen.', 6)
)
INSERT INTO food.menu_categories (id, restaurant_id, name, description, sort_order)
SELECT (substr(md5(r.id::text || c.name),1,8)||'-'||substr(md5(r.id::text || c.name),9,4)||'-4'||substr(md5(r.id::text || c.name),14,3)||'-8'||substr(md5(r.id::text || c.name),18,3)||'-'||substr(md5(r.id::text || c.name),21,12))::uuid,
       r.id, c.name, c.description, c.sort_order
FROM restaurant r CROSS JOIN categories c
WHERE NOT EXISTS (SELECT 1 FROM food.menu_categories existing WHERE existing.restaurant_id=r.id AND existing.name=c.name);

WITH dishes(category_name, name, description, food_type, price, discount, image_url, prep, recommended, rating, reviews) AS (VALUES
  ('Starters','Paneer 65','Crisp paneer tossed with curry leaf, chilli and roasted garlic.','VEG',249::numeric,219::numeric,'https://images.unsplash.com/photo-1567188040759-fb8a883dc6d8?auto=format&fit=crop&w=1000&q=84',18,true,4.8,186),
  ('Starters','Chicken 65','Hyderabadi-style fried chicken with curry leaf and lime.','NON_VEG',289,259,'https://images.unsplash.com/photo-1601050690117-94f5f6fa8bd7?auto=format&fit=crop&w=1000&q=84',20,true,4.9,342),
  ('Starters','Tandoori Mushroom','Charred button mushrooms, hung curd and smoked paprika.','VEG',269,NULL,'https://images.unsplash.com/photo-1603894584373-5ac82b2ae398?auto=format&fit=crop&w=1000&q=84',22,false,4.6,98),
  ('Veg Curries','Paneer Butter Masala','Tandoori paneer in a silky tomato, cashew and fenugreek gravy.','VEG',329,299,'https://images.unsplash.com/photo-1631452180519-c014fe946bc7?auto=format&fit=crop&w=1000&q=84',24,true,4.8,427),
  ('Veg Curries','Kadai Vegetable','Seasonal vegetables, kadai masala, peppers and coriander.','VEG',279,NULL,'https://images.unsplash.com/photo-1547592180-85f173990554?auto=format&fit=crop&w=1000&q=84',22,false,4.5,116),
  ('Veg Curries','Yellow Dal Tadka','Slow-simmered lentils tempered with cumin, garlic and chilli.','VEG',229,209,'https://images.unsplash.com/photo-1546833999-b9f581a1996d?auto=format&fit=crop&w=1000&q=84',20,true,4.7,303),
  ('Non-Veg Curries','Old Delhi Butter Chicken','Charred chicken tikka in a rich tomato and cultured-butter gravy.','NON_VEG',389,349,'https://images.unsplash.com/photo-1603894584373-5ac82b2ae398?auto=format&fit=crop&w=1000&q=84',28,true,4.9,612),
  ('Non-Veg Curries','Andhra Chicken Curry','Country-style chicken curry with red chilli, coconut and curry leaf.','NON_VEG',359,NULL,'https://images.unsplash.com/photo-1601050690597-df0568f70950?auto=format&fit=crop&w=1000&q=84',30,false,4.7,284),
  ('Non-Veg Curries','Mutton Rogan Josh','Tender mutton slow-braised with Kashmiri chilli and whole spices.','NON_VEG',469,439,'https://images.unsplash.com/photo-1545247181-516773cae754?auto=format&fit=crop&w=1000&q=84',36,true,4.8,355),
  ('Main Course','FiGo Family Chicken Biryani','Dum-cooked basmati and chicken for two, with salan and raita.','NON_VEG',649,579,'https://images.unsplash.com/photo-1633945274405-b6c8069047b0?auto=format&fit=crop&w=1000&q=84',34,true,4.9,821),
  ('Main Course','Subz Dum Biryani','Garden vegetables and paneer layered with saffron basmati.','VEG',349,319,'https://images.unsplash.com/photo-1630409351241-e90e7f5e434d?auto=format&fit=crop&w=1000&q=84',28,true,4.7,267),
  ('Main Course','Jeera Rice','Steamed basmati finished with cumin, ghee and coriander.','VEG',179,NULL,'https://images.unsplash.com/photo-1516684732162-798a0062be99?auto=format&fit=crop&w=1000&q=84',16,false,4.5,92),
  ('Indian Breads','Garlic Butter Naan','Tandoor-baked naan brushed with garlic butter and herbs.','VEG',89,79,'https://images.unsplash.com/photo-1601050690597-df0568f70950?auto=format&fit=crop&w=1000&q=84',12,true,4.8,504),
  ('Indian Breads','Whole Wheat Roti','Soft whole-wheat roti cooked fresh on the tandoor wall.','VEG',49,NULL,'https://images.unsplash.com/photo-1626777552726-4a6b54c97e46?auto=format&fit=crop&w=1000&q=84',10,false,4.6,218),
  ('Indian Breads','Laccha Paratha','Flaky layered paratha finished with cultured butter.','VEG',79,NULL,'https://images.unsplash.com/photo-1565557623262-b51c2513a641?auto=format&fit=crop&w=1000&q=84',12,false,4.7,173),
  ('Desserts','Double Ka Meetha','Hyderabadi bread pudding with saffron milk, nuts and khoya.','VEG',159,139,'https://images.unsplash.com/photo-1551024506-0bccd828d307?auto=format&fit=crop&w=1000&q=84',12,true,4.8,244),
  ('Desserts','Warm Gulab Jamun','Two soft khoya dumplings in cardamom and rose syrup.','VEG',119,NULL,'https://images.unsplash.com/photo-1666190094769-4a8f9ab27582?auto=format&fit=crop&w=1000&q=84',8,false,4.7,396),
  ('Desserts','Saffron Matka Kulfi','Dense saffron-pistachio kulfi served in a clay pot.','VEG',179,159,'https://images.unsplash.com/photo-1488900128323-21503983a07e?auto=format&fit=crop&w=1000&q=84',5,true,4.9,188)
)
INSERT INTO food.menu_items (restaurant_id, category_id, name, description, food_type, base_price, discount_price, image_url, preparation_minutes, is_available, is_recommended, tax_percentage, metadata)
SELECT r.id, c.id, d.name, d.description, d.food_type::food.food_type, d.price, d.discount, d.image_url, d.prep, TRUE, d.recommended, 5,
       jsonb_build_object('rating',d.rating,'review_count',d.reviews,'seed','signature-menu')
FROM dishes d
JOIN food.restaurants r ON r.slug='figo-biryani-house'
JOIN food.menu_categories c ON c.restaurant_id=r.id AND c.name=d.category_name
WHERE NOT EXISTS (SELECT 1 FROM food.menu_items existing WHERE existing.restaurant_id=r.id AND existing.name=d.name);

UPDATE food.menu_items
SET metadata = metadata || jsonb_build_object('rating', 4.8, 'review_count', 128)
WHERE restaurant_id=(SELECT id FROM food.restaurants WHERE slug='figo-biryani-house')
  AND NOT (metadata ? 'rating');
