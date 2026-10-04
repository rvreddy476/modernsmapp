package devseed

// B1 (4 Oct 2026): every service group the founder asked for, beside the
// launch categories — appliance repairs (each with an inspection visit plus
// common repairs), car wash, disinfection, home staffing (hourly and
// monthly), packers and movers, photography, makeup (women's beauty rules),
// yoga and construction. The catalogue is the menu: these prices are the
// city's SUGGESTED prices only; every bookable price is a professional's
// own, approved by an admin. Families without a shared/gst category yet
// (CAR_CARE, HOME_STAFFING, RELOCATION, PHOTOGRAPHY, FITNESS_WELLNESS,
// CONSTRUCTION) stay hidden from customers until the tax lane maps them.

func init() {
	skills = append(skills, b1Skills...)
	for k := range b1CertificateFree {
		certificateFree[k] = true
	}
	categories = append(categories, b1Categories...)
}

var b1Skills = [][2]string{
	{"tv_repair", "TV repair"},
	{"refrigerator_repair", "Refrigerator repair"},
	{"washing_machine_repair", "Washing machine repair"},
	{"microwave_repair", "Microwave repair"},
	{"geyser_repair", "Geyser repair"},
	{"chimney_hob_repair", "Chimney and hob repair"},
	{"computer_repair", "Laptop and computer repair"},
	{"mobile_repair", "Mobile phone repair"},
	{"car_wash", "Car wash"},
	{"disinfection", "Disinfection and sanitising"},
	{"cook", "Cook"},
	{"house_help", "House help"},
	{"nanny", "Nanny"},
	{"driver", "Driver"},
	{"packers_movers", "Packers and movers"},
	{"photographer", "Photographer"},
	{"makeup_artist", "Makeup artist"},
	{"yoga_trainer", "Yoga trainer"},
	{"construction", "Construction and masonry"},
}

// b1CertificateFree: skills with no trade certificate in practice. Every
// other B1 skill (electrical appliances, disinfection chemicals, driving)
// needs one approved by an admin. Declaring never verifies a skill either
// way: an admin does (founder rule 4 Oct 2026).
var b1CertificateFree = map[string]bool{
	"computer_repair": true, "mobile_repair": true, "car_wash": true, "cook": true, "house_help": true, "nanny": true,
	"packers_movers": true, "photographer": true, "makeup_artist": true, "yoga_trainer": true, "construction": true,
}

// optionUnits: the options not priced per job (key: category/service/option).
var optionUnits = map[string]string{
	"home-staffing/cook/hourly":         "per_hour",
	"home-staffing/cook/monthly":        "per_month",
	"home-staffing/house-help/hourly":   "per_hour",
	"home-staffing/house-help/monthly":  "per_month",
	"home-staffing/nanny/hourly":        "per_hour",
	"home-staffing/nanny/monthly":       "per_month",
	"home-staffing/driver/hourly":       "per_hour",
	"home-staffing/driver/monthly":      "per_month",
	"photography/event/hourly":          "per_hour",
	"yoga-trainer/session/per-hour":     "per_hour",
	"yoga-trainer/monthly/monthly":      "per_month",
	"construction/masonry-tiling/hours": "per_hour",
}

// applianceRepair is one appliance category: an inspection visit (the
// professional's diagnosis; parts and repairs then come from the rate card)
// plus common repairs.
func applianceRepair(slug, name, desc, skill string, repairs []serviceSeed, rates []rateSeed) categorySeed {
	inspection := serviceSeed{slug: "inspection-visit", name: "Inspection visit", description: "Diagnosis at home; repairs and parts from the rate card",
		duration: 45, skill: skill, reworkDays: 30,
		options: []optionSeed{{"visit", "Inspection visit", 45, 1, 29900, 0, true}}}
	for i := range repairs {
		repairs[i].skill, repairs[i].reworkDays = skill, 30
	}
	return categorySeed{slug: slug, name: name, description: desc, family: "APPLIANCE_REPAIR", genderRule: "any", extrasPolicy: "rate_card",
		services: append([]serviceSeed{inspection}, repairs...), rates: rates}
}

var labour = rateSeed{"labour_hour", "Additional labour", "per_hour", 35000, 4, false}

var b1Categories = []categorySeed{
	applianceRepair("tv-repair", "TV repair", "LED, LCD and smart TV repair and wall mounting", "tv_repair",
		[]serviceSeed{
			{slug: "no-display-repair", name: "No picture or no sound", description: "Panel, backlight or board repair",
				duration: 90, options: []optionSeed{{"up-to-43", "Up to 43 inch", 90, 1, 89900, 0, true}, {"above-43", "Above 43 inch", 120, 1, 129900, 0, false}}},
			{slug: "wall-mounting", name: "TV wall mounting", description: "Bracket fitted and levelled",
				duration: 45, options: []optionSeed{{"per-tv", "Per TV", 45, 2, 49900, 0, true}}},
		},
		[]rateSeed{{"backlight_strip", "Backlight strip set", "per_item", 180000, 1, true}, {"power_board", "Power board", "per_item", 220000, 1, true}, labour}),
	applianceRepair("refrigerator-repair", "Refrigerator repair", "Single door, double door and side-by-side", "refrigerator_repair",
		[]serviceSeed{
			{slug: "not-cooling-repair", name: "Not cooling", description: "Leak check, gas charge and thermostat",
				duration: 90, options: []optionSeed{{"single-door", "Single door", 90, 1, 149900, 0, true}, {"double-door", "Double door", 120, 1, 199900, 0, false}}},
		},
		[]rateSeed{{"compressor", "Compressor", "per_item", 650000, 1, true}, {"thermostat", "Thermostat", "per_item", 85000, 1, true}, labour}),
	applianceRepair("washing-machine-repair", "Washing machine repair", "Top load, front load and semi-automatic", "washing_machine_repair",
		[]serviceSeed{
			{slug: "not-spinning-repair", name: "Not spinning or draining", description: "Belt, pump and motor check",
				duration: 75, options: []optionSeed{{"top-load", "Top load", 75, 1, 99900, 0, true}, {"front-load", "Front load", 90, 1, 129900, 0, false}}},
			{slug: "installation", name: "Installation or uninstallation", description: "Inlet and outlet fitted",
				duration: 45, options: []optionSeed{{"install", "Installation", 45, 1, 49900, 0, true}, {"uninstall", "Uninstallation", 30, 1, 34900, 0, false}}},
		},
		[]rateSeed{{"drain_pump", "Drain pump", "per_item", 145000, 1, true}, {"drum_bearing", "Drum bearing set", "per_item", 145000, 1, true}, labour}),
	applianceRepair("microwave-repair", "Microwave repair", "Solo, grill and convection", "microwave_repair",
		[]serviceSeed{
			{slug: "not-heating-repair", name: "Not heating", description: "Magnetron, fuse and door switch check",
				duration: 60, options: []optionSeed{{"any", "Any microwave", 60, 1, 69900, 0, true}}},
		},
		[]rateSeed{{"magnetron", "Magnetron", "per_item", 240000, 1, true}, {"door_switch", "Door switch", "per_item", 35000, 2, true}, labour}),
	applianceRepair("geyser-repair", "Geyser repair", "Storage and instant water heaters", "geyser_repair",
		[]serviceSeed{
			{slug: "not-heating-repair", name: "Not heating", description: "Element and thermostat check",
				duration: 60, options: []optionSeed{{"storage", "Storage geyser", 60, 1, 59900, 0, true}, {"instant", "Instant geyser", 45, 1, 49900, 0, false}}},
			{slug: "installation", name: "Geyser installation", description: "Wall fitting and connections",
				duration: 60, options: []optionSeed{{"install", "Installation", 60, 2, 59900, 0, true}}},
		},
		[]rateSeed{{"heating_element", "Heating element", "per_item", 65000, 1, true}, {"thermostat", "Thermostat", "per_item", 55000, 1, true}, labour}),
	applianceRepair("chimney-hob-repair", "Chimney and hob repair", "Kitchen chimney and gas hob repair and service", "chimney_hob_repair",
		[]serviceSeed{
			{slug: "chimney-deep-clean", name: "Chimney deep cleaning", description: "Filters and motor degreased",
				duration: 90, options: []optionSeed{{"chimney", "Per chimney", 90, 1, 119900, 0, true}}},
			{slug: "hob-burner-repair", name: "Hob burner repair", description: "Burner, igniter and knob repair",
				duration: 45, options: []optionSeed{{"per-burner", "Per burner", 30, 4, 29900, 0, true}}},
		},
		[]rateSeed{{"chimney_motor", "Chimney motor", "per_item", 280000, 1, true}, {"igniter", "Igniter", "per_item", 25000, 4, true}, labour}),
	applianceRepair("computer-repair", "Laptop and computer repair", "Laptops and desktops at home", "computer_repair",
		[]serviceSeed{
			{slug: "software-setup", name: "Software and OS setup", description: "Install, update and virus removal",
				duration: 60, options: []optionSeed{{"per-device", "Per device", 60, 3, 59900, 0, true}}},
			{slug: "hardware-repair", name: "Hardware repair", description: "Keyboard, screen, battery and storage",
				duration: 60, options: []optionSeed{{"laptop", "Laptop", 60, 1, 79900, 0, true}, {"desktop", "Desktop", 60, 1, 69900, 0, false}}},
		},
		[]rateSeed{{"ssd_upgrade", "SSD (512 GB)", "per_item", 450000, 1, true}, {"laptop_battery", "Laptop battery", "per_item", 380000, 1, true}, labour}),
	applianceRepair("mobile-repair", "Mobile phone repair", "Screen, battery and charging port at home", "mobile_repair",
		[]serviceSeed{
			{slug: "screen-replacement", name: "Screen replacement", description: "Fitting only; the screen from the rate card",
				duration: 60, options: []optionSeed{{"per-phone", "Per phone", 60, 1, 49900, 0, true}}},
			{slug: "battery-replacement", name: "Battery or charging port", description: "Fitting only; the part from the rate card",
				duration: 45, options: []optionSeed{{"per-phone", "Per phone", 45, 1, 39900, 0, true}}},
		},
		[]rateSeed{{"screen", "Screen (mid-range)", "per_item", 350000, 1, true}, {"battery", "Battery", "per_item", 180000, 1, true}, labour}),
	{slug: "car-wash", name: "Car wash", description: "Waterless and foam wash at your parking",
		family: "CAR_CARE", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "exterior-wash", name: "Exterior wash", description: "Foam wash and wipe-down",
				duration: 45, skill: "car_wash", reworkDays: 1,
				options: []optionSeed{{"hatchback", "Hatchback", 40, 2, 39900, 0, true}, {"sedan-suv", "Sedan or SUV", 50, 2, 49900, 0, false}}},
			{slug: "interior-exterior", name: "Interior and exterior", description: "Vacuum, dashboard polish and foam wash",
				duration: 90, skill: "car_wash", reworkDays: 1,
				options: []optionSeed{{"hatchback", "Hatchback", 80, 2, 79900, 0, true}, {"sedan-suv", "Sedan or SUV", 100, 2, 99900, 0, false}}},
		},
		rates: []rateSeed{{"seat_shampoo", "Seat shampoo (per seat)", "per_item", 19900, 7, false}, {"wax_polish", "Wax polish", "per_item", 49900, 1, false}}},
	{slug: "disinfection", name: "Disinfection and sanitising", description: "Homes and offices sanitised with approved chemicals",
		family: "PEST_CONTROL", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "home-sanitisation", name: "Home sanitisation", description: "Every room fogged and high-touch surfaces wiped",
				duration: 60, skill: "disinfection", reworkDays: 7,
				options: []optionSeed{{"1bhk", "1 BHK", 45, 1, 69900, 0, false}, {"2bhk", "2 BHK", 60, 1, 89900, 0, true}, {"3bhk", "3 BHK", 75, 1, 109900, 0, false}}},
			{slug: "office-sanitisation", name: "Office sanitisation", description: "Per 500 sq ft",
				duration: 45, skill: "disinfection", reworkDays: 7,
				options: []optionSeed{{"per-500-sqft", "Per 500 sq ft", 30, 10, 49900, 0, true}}},
		},
		rates: []rateSeed{{"extra_room", "Extra room", "per_item", 19900, 4, false}}},
	{slug: "home-staffing", name: "Home staffing", description: "Cook, house help, nanny and driver by the hour or the month",
		family: "HOME_STAFFING", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "cook", name: "Cook", description: "Home-style meals in your kitchen; monthly is a daily visit, the first booked here",
				duration: 60, skill: "cook", reworkDays: 0,
				options: []optionSeed{{"hourly", "By the hour", 60, 12, 29900, 0, true}, {"monthly", "Monthly (one visit a day)", 60, 1, 899900, 0, false}}},
			{slug: "house-help", name: "House help", description: "Sweeping, mopping, dishes and laundry",
				duration: 60, skill: "house_help", reworkDays: 0,
				options: []optionSeed{{"hourly", "By the hour", 60, 12, 19900, 0, true}, {"monthly", "Monthly (one visit a day)", 60, 1, 599900, 0, false}}},
			{slug: "nanny", name: "Nanny", description: "Child care at home",
				duration: 60, skill: "nanny", reworkDays: 0,
				options: []optionSeed{{"hourly", "By the hour", 60, 12, 24900, 0, true}, {"monthly", "Monthly (weekdays)", 60, 1, 1499900, 0, false}}},
			{slug: "driver", name: "Driver", description: "Your car, our driver",
				duration: 60, skill: "driver", reworkDays: 0,
				options: []optionSeed{{"hourly", "By the hour", 60, 12, 24900, 0, true}, {"monthly", "Monthly (weekdays)", 60, 1, 1799900, 0, false}}},
		},
		rates: []rateSeed{{"extra_hour", "Extra hour", "per_hour", 24900, 4, false}}},
	{slug: "packers-movers", name: "Packers and movers", description: "Packing, loading and moving within the city",
		family: "RELOCATION", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "survey-visit", name: "Survey visit", description: "Inventory and an estimate at home",
				duration: 30, skill: "packers_movers", reworkDays: 0,
				options: []optionSeed{{"visit", "Survey visit", 30, 1, 19900, 0, true}}},
			{slug: "home-shifting", name: "Home shifting within the city", description: "Packing, loading, transport and unloading",
				duration: 480, skill: "packers_movers", reworkDays: 7,
				options: []optionSeed{{"1bhk", "1 BHK", 360, 1, 999900, 0, false}, {"2bhk", "2 BHK", 480, 1, 1499900, 0, true}, {"3bhk", "3 BHK", 600, 1, 1999900, 0, false}}},
		},
		rates: []rateSeed{{"extra_carton", "Extra carton", "per_item", 9900, 50, true}, {"labour_hour", "Additional labour", "per_hour", 29900, 8, false}}},
	{slug: "photography", name: "Photography", description: "Events, portraits and products at home",
		family: "PHOTOGRAPHY", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "event", name: "Event photography", description: "Birthdays, pujas and small functions",
				duration: 60, skill: "photographer", reworkDays: 0,
				options: []optionSeed{{"hourly", "By the hour", 60, 8, 149900, 0, true}}},
			{slug: "portrait", name: "Portrait session", description: "Family or individual portraits, 20 edited photos",
				duration: 90, skill: "photographer", reworkDays: 0,
				options: []optionSeed{{"session", "Session", 90, 1, 299900, 0, true}}},
		},
		rates: []rateSeed{{"edited_photo", "Extra edited photo", "per_item", 9900, 50, false}}},
	{slug: "makeup-artist", name: "Makeup artist", description: "Party and bridal makeup at home",
		family: "BEAUTY_SALON", genderRule: "female_pros_only", extrasPolicy: "catalogue_addons_only",
		services: []serviceSeed{
			{slug: "party-makeup", name: "Party makeup", description: "Sealed single-use kit",
				duration: 75, skill: "makeup_artist", reworkDays: 0,
				options: []optionSeed{{"party", "Party makeup", 75, 1, 249900, 0, true}},
				groups: []groupSeed{{"add-ons", "Add-ons", 0, 2, false, []addonSeed{
					{"hairstyling", "Hairstyling", 30, 99900}, {"saree-draping", "Saree draping", 20, 49900}}}}},
			{slug: "bridal-makeup", name: "Bridal makeup", description: "HD bridal makeup with trial consultation",
				duration: 180, skill: "makeup_artist", reworkDays: 0,
				options: []optionSeed{{"hd", "HD bridal", 180, 1, 1499900, 0, true}}},
		}},
	{slug: "yoga-trainer", name: "Yoga trainer", description: "Personal yoga sessions at home",
		family: "FITNESS_WELLNESS", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "session", name: "Yoga session", description: "One trainer, up to two people",
				duration: 60, skill: "yoga_trainer", reworkDays: 0,
				options: []optionSeed{{"per-hour", "Per hour", 60, 2, 69900, 0, true}}},
			{slug: "monthly", name: "Monthly yoga", description: "12 sessions a month; the first booked here",
				duration: 60, skill: "yoga_trainer", reworkDays: 0,
				options: []optionSeed{{"monthly", "Monthly (12 sessions)", 60, 1, 699900, 0, true}}},
		},
		rates: []rateSeed{{"extra_person", "Extra participant", "per_item", 19900, 3, false}}},
	{slug: "construction", name: "Construction and masonry", description: "Masonry, tiling and small civil work",
		family: "CONSTRUCTION", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "site-inspection", name: "Site inspection visit", description: "Measurement and an estimate",
				duration: 45, skill: "construction", reworkDays: 0,
				options: []optionSeed{{"visit", "Inspection visit", 45, 1, 29900, 0, true}}},
			{slug: "masonry-tiling", name: "Masonry and tiling", description: "By the hour; materials from the rate card",
				duration: 60, skill: "construction", reworkDays: 30,
				options: []optionSeed{{"hours", "By the hour", 60, 8, 39900, 0, true}}},
		},
		rates: []rateSeed{{"cement_bag", "Cement bag", "per_item", 45000, 20, true}, {"tile_sqft", "Tiling (per sq ft)", "per_item", 6000, 100, false}}},
}
