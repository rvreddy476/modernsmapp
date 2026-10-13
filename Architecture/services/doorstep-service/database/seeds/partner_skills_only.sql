-- Opt-in development catalogue bootstrap. Run only with deployment approval.
-- Matches internal/devseed/data.go and data_b1.go; does not seed prices,
-- service availability, partners, bookings or approval state.
-- Existing administrator-managed names and qualification rules are preserved.
BEGIN;
INSERT INTO doorstep.skills (code, name, requires_certificate) VALUES
  ('deep_cleaning', 'Deep cleaning', false),
  ('sofa_carpet_cleaning', 'Sofa and carpet cleaning', false),
  ('ac_service', 'AC service and repair', true),
  ('appliance_repair', 'Appliance repair', true),
  ('ro_service', 'RO water purifier service', true),
  ('electrician', 'Electrician', true),
  ('plumber', 'Plumber', false),
  ('carpenter', 'Carpenter', false),
  ('painter', 'Painter', false),
  ('pest_control', 'Pest control', true),
  ('salon_women', 'Salon for women', false),
  ('salon_men', 'Salon for men', false),
  ('tv_repair', 'TV repair', true),
  ('refrigerator_repair', 'Refrigerator repair', true),
  ('washing_machine_repair', 'Washing machine repair', true),
  ('microwave_repair', 'Microwave repair', true),
  ('geyser_repair', 'Geyser repair', true),
  ('chimney_hob_repair', 'Chimney and hob repair', true),
  ('computer_repair', 'Laptop and computer repair', false),
  ('mobile_repair', 'Mobile phone repair', false),
  ('car_wash', 'Car wash', false),
  ('disinfection', 'Disinfection and sanitising', true),
  ('cook', 'Cook', false),
  ('house_help', 'House help', false),
  ('nanny', 'Nanny', false),
  ('driver', 'Driver', true),
  ('packers_movers', 'Packers and movers', false),
  ('photographer', 'Photographer', false),
  ('makeup_artist', 'Makeup artist', false),
  ('yoga_trainer', 'Yoga trainer', false),
  ('construction', 'Construction and masonry', false)
ON CONFLICT (code) DO NOTHING;
COMMIT;
