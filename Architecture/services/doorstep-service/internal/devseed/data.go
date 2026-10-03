package devseed

// Hyderabad (HYD) pilot catalogue. Prices are GST-inclusive paise and are
// realistic placeholders for a dev stack, not commercial decisions.

type addonSeed struct {
	key, name string
	extra     int
	price     int64
}

type groupSeed struct {
	key, name string
	min, max  int
	required  bool
	addons    []addonSeed
}

type optionSeed struct {
	key, name string
	duration  int
	maxQty    int
	price     int64
	mrp       int64 // 0 = none
	isDefault bool
}

type serviceSeed struct {
	slug, name, description string
	duration                int
	skill                   string
	reworkDays              int
	inclusions, exclusions  []string
	options                 []optionSeed
	groups                  []groupSeed
}

type rateSeed struct {
	code, name, unit string
	price            int64
	maxQty           int
	isPart           bool
}

type categorySeed struct {
	slug, name, description string
	family, genderRule      string
	extrasPolicy            string
	services                []serviceSeed
	rates                   []rateSeed
}

var skills = [][2]string{
	{"deep_cleaning", "Deep cleaning"},
	{"sofa_carpet_cleaning", "Sofa and carpet cleaning"},
	{"ac_service", "AC service and repair"},
	{"appliance_repair", "Appliance repair"},
	{"ro_service", "RO water purifier service"},
	{"electrician", "Electrician"},
	{"plumber", "Plumber"},
	{"carpenter", "Carpenter"},
	{"painter", "Painter"},
	{"pest_control", "Pest control"},
	{"salon_women", "Salon for women"},
	{"salon_men", "Salon for men"},
}

// Zones: two adjacent rectangles sharing the lng 78.40 edge.
var zones = []struct {
	slug, name string
	polygon    string
}{
	{"west-hitec-gachibowli", "Gachibowli - HITEC City",
		`{"type":"Polygon","coordinates":[[[78.33,17.40],[78.40,17.40],[78.40,17.48],[78.33,17.48],[78.33,17.40]]]}`},
	{"central-banjara-jubilee", "Banjara - Jubilee Hills",
		`{"type":"Polygon","coordinates":[[[78.40,17.40],[78.47,17.40],[78.47,17.45],[78.40,17.45],[78.40,17.40]]]}`},
}

var categories = []categorySeed{
	{slug: "home-cleaning", name: "Home cleaning", description: "Bathroom, kitchen, full home, sofa and carpet",
		family: "HOME_CLEANING", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "bathroom-deep-cleaning", name: "Bathroom deep cleaning", description: "Tiles, fittings, mirrors and floor scrubbed and sanitised",
				duration: 75, skill: "deep_cleaning", reworkDays: 7,
				inclusions: []string{"Tile and grout scrubbing", "Fittings descaled", "Floor sanitised"}, exclusions: []string{"Ceiling cleaning"},
				options: []optionSeed{{"per-bathroom", "Per bathroom", 75, 4, 54900, 69900, true}},
				groups: []groupSeed{{"extras", "Extras", 0, 2, false, []addonSeed{
					{"exhaust-fan", "Exhaust fan cleaning", 10, 9900}, {"balcony", "Balcony cleaning", 20, 19900}}}}},
			{slug: "kitchen-deep-cleaning", name: "Kitchen deep cleaning", description: "Slab, tiles, sink and cabinets outside degreased",
				duration: 150, skill: "deep_cleaning", reworkDays: 7,
				inclusions: []string{"Slab and tiles degreased", "Sink descaled", "Cabinet exteriors"}, exclusions: []string{"Inside appliances unless added"},
				options: []optionSeed{
					{"empty", "Empty kitchen", 150, 1, 149900, 0, false},
					{"occupied", "Occupied kitchen", 180, 1, 179900, 199900, true}},
				groups: []groupSeed{{"appliances", "Appliances", 0, 3, false, []addonSeed{
					{"chimney", "Chimney cleaning", 30, 44900}, {"fridge", "Fridge cleaning", 20, 29900}, {"microwave", "Microwave cleaning", 10, 14900}}}}},
			{slug: "full-home-deep-cleaning", name: "Full home deep cleaning", description: "Every room, bathroom and the kitchen",
				duration: 300, skill: "deep_cleaning", reworkDays: 7,
				inclusions: []string{"All rooms", "Bathrooms", "Kitchen", "Windows inside"}, exclusions: []string{"Wall washing"},
				options: []optionSeed{
					{"1bhk", "1 BHK", 240, 1, 349900, 399900, false},
					{"2bhk", "2 BHK", 300, 1, 449900, 529900, true},
					{"3bhk", "3 BHK", 360, 1, 599900, 699900, false}},
				groups: []groupSeed{{"add-ons", "Add-ons", 0, 2, false, []addonSeed{
					{"balcony", "Balcony", 30, 29900}, {"cabinets", "Inside cabinets", 45, 49900}}}}},
			{slug: "sofa-carpet-cleaning", name: "Sofa and carpet cleaning", description: "Shampoo and vacuum extraction",
				duration: 60, skill: "sofa_carpet_cleaning", reworkDays: 7,
				options: []optionSeed{
					{"sofa-seat", "Sofa (per seat)", 20, 10, 24900, 0, true},
					{"carpet", "Carpet (up to 50 sq ft)", 30, 5, 39900, 0, false}},
				groups: []groupSeed{{"protection", "Fabric protection", 0, 1, false, []addonSeed{{"stain-guard", "Stain guard", 10, 19900}}}}},
		},
		rates: []rateSeed{
			{"extra_bathroom", "Extra bathroom", "per_item", 54900, 3, false},
			{"extra_balcony", "Extra balcony", "per_item", 29900, 3, false},
		}},
	{slug: "ac-service-repair", name: "AC service and repair", description: "Service, gas refill, repair and installation",
		family: "APPLIANCE_REPAIR", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "ac-foam-jet-service", name: "Foam-jet AC service", description: "Indoor unit foam-jet cleaning and drain check",
				duration: 60, skill: "ac_service", reworkDays: 30,
				options: []optionSeed{
					{"split", "Split AC", 60, 4, 59900, 79900, true},
					{"window", "Window AC", 45, 4, 49900, 0, false}},
				groups: []groupSeed{{"extras", "Extras", 0, 1, false, []addonSeed{{"anti-rust", "Anti-rust coating", 15, 19900}}}}},
			{slug: "ac-gas-refill", name: "AC gas refill", description: "Leak check and full gas refill",
				duration: 90, skill: "ac_service", reworkDays: 30,
				options: []optionSeed{
					{"split", "Split AC", 90, 2, 249900, 0, true},
					{"window", "Window AC", 75, 2, 219900, 0, false}}},
			{slug: "ac-repair", name: "AC repair", description: "Diagnosis visit; parts and repairs from the rate card",
				duration: 45, skill: "ac_service", reworkDays: 30,
				options: []optionSeed{{"visit", "Inspection visit", 45, 1, 29900, 0, true}}},
			{slug: "ac-installation", name: "AC installation", description: "Installation with up to 3 m of copper pipe",
				duration: 120, skill: "ac_service", reworkDays: 30,
				options: []optionSeed{
					{"split", "Split AC installation", 120, 2, 149900, 0, true},
					{"window", "Window AC installation", 60, 2, 79900, 0, false}}},
		},
		rates: []rateSeed{
			{"gas_top_up", "Gas top-up", "per_item", 149900, 1, false},
			{"capacitor", "Capacitor replacement", "per_item", 65000, 2, true},
			{"pcb_repair", "PCB repair", "per_item", 180000, 1, false},
			{"copper_pipe", "Copper pipe", "per_metre", 55000, 10, true},
		}},
	{slug: "appliance-ro-repair", name: "Appliance and RO", description: "RO service, washing machine and refrigerator repair",
		family: "APPLIANCE_REPAIR", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "ro-service", name: "RO service", description: "Cleaning, TDS check and sanitisation",
				duration: 45, skill: "ro_service", reworkDays: 30,
				options: []optionSeed{{"service", "RO service", 45, 1, 44900, 0, true}},
				groups:  []groupSeed{{"filters", "Filters", 0, 1, false, []addonSeed{{"filter-change", "Sediment and carbon filter change", 15, 69900}}}}},
			{slug: "washing-machine-repair", name: "Washing machine repair", description: "Diagnosis visit; parts from the rate card",
				duration: 45, skill: "appliance_repair", reworkDays: 30,
				options: []optionSeed{{"visit", "Inspection visit", 45, 1, 29900, 0, true}}},
			{slug: "refrigerator-repair", name: "Refrigerator repair", description: "Diagnosis visit; parts from the rate card",
				duration: 45, skill: "appliance_repair", reworkDays: 30,
				options: []optionSeed{{"visit", "Inspection visit", 45, 1, 29900, 0, true}}},
		},
		rates: []rateSeed{
			{"ro_membrane", "RO membrane", "per_item", 220000, 1, true},
			{"drum_bearing", "Drum bearing set", "per_item", 145000, 1, true},
			{"labour_hour", "Additional labour", "per_hour", 35000, 4, false},
		}},
	{slug: "electrician", name: "Electrician", description: "Switches, fans, lights and wiring",
		family: "INSTALLATION_REPAIR", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "switch-socket", name: "Switch and socket", description: "Replacement or repair",
				duration: 20, skill: "electrician", reworkDays: 30,
				options: []optionSeed{{"per-point", "Per point", 20, 10, 14900, 0, true}}},
			{slug: "fan", name: "Ceiling fan", description: "Installation or repair",
				duration: 30, skill: "electrician", reworkDays: 30,
				options: []optionSeed{
					{"install", "Ceiling fan installation", 30, 5, 24900, 0, true},
					{"repair", "Fan repair", 30, 5, 19900, 0, false}}},
			{slug: "electrical-inspection", name: "Electrical inspection", description: "Diagnosis visit; work from the rate card",
				duration: 30, skill: "electrician", reworkDays: 30,
				options: []optionSeed{{"visit", "Inspection visit", 30, 1, 19900, 0, true}}},
		},
		rates: []rateSeed{
			{"mcb", "MCB replacement", "per_item", 45000, 4, true},
			{"wiring", "Wiring", "per_metre", 6000, 30, false},
			{"labour_hour", "Additional labour", "per_hour", 29900, 4, false},
		}},
	{slug: "plumber", name: "Plumber", description: "Taps, basins, blockages and leaks",
		family: "INSTALLATION_REPAIR", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "tap", name: "Tap repair or replacement", description: "Per tap",
				duration: 20, skill: "plumber", reworkDays: 30,
				options: []optionSeed{{"per-tap", "Per tap", 20, 6, 14900, 0, true}}},
			{slug: "basin-installation", name: "Wash basin installation", description: "Fixing and connection",
				duration: 60, skill: "plumber", reworkDays: 30,
				options: []optionSeed{{"basin", "Wash basin", 60, 2, 49900, 0, true}}},
			{slug: "blockage-removal", name: "Drain blockage removal", description: "Sink, basin or floor drain",
				duration: 45, skill: "plumber", reworkDays: 30,
				options: []optionSeed{{"per-drain", "Per drain", 45, 3, 39900, 0, true}}},
			{slug: "plumbing-inspection", name: "Plumbing inspection", description: "Diagnosis visit; work from the rate card",
				duration: 30, skill: "plumber", reworkDays: 30,
				options: []optionSeed{{"visit", "Inspection visit", 30, 1, 19900, 0, true}}},
		},
		rates: []rateSeed{
			{"tap_cartridge", "Tap cartridge", "per_item", 35000, 4, true},
			{"pipe", "CPVC pipe", "per_metre", 12000, 20, true},
			{"labour_hour", "Additional labour", "per_hour", 29900, 4, false},
		}},
	{slug: "carpenter", name: "Carpenter", description: "Locks, hinges, furniture and fittings",
		family: "INSTALLATION_REPAIR", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "door-lock", name: "Door lock installation", description: "Per lock",
				duration: 40, skill: "carpenter", reworkDays: 30,
				options: []optionSeed{{"per-lock", "Per lock", 40, 4, 29900, 0, true}}},
			{slug: "furniture-repair", name: "Furniture repair", description: "Diagnosis visit; work from the rate card",
				duration: 30, skill: "carpenter", reworkDays: 30,
				options: []optionSeed{{"visit", "Inspection visit", 30, 1, 19900, 0, true}}},
			{slug: "curtain-rod", name: "Curtain rod installation", description: "Per window",
				duration: 30, skill: "carpenter", reworkDays: 30,
				options: []optionSeed{{"per-window", "Per window", 30, 6, 19900, 0, true}}},
		},
		rates: []rateSeed{
			{"hinge", "Hinge", "per_item", 12000, 8, true},
			{"drawer_channel", "Drawer channel (pair)", "per_item", 35000, 4, true},
			{"labour_hour", "Additional labour", "per_hour", 34900, 4, false},
		}},
	{slug: "painting", name: "Painting", description: "Room painting and waterproofing",
		family: "PAINTING", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "room-painting", name: "Room painting", description: "Two coats, premium emulsion, furniture covered",
				duration: 480, skill: "painter", reworkDays: 30,
				options: []optionSeed{
					{"1-room", "1 room (up to 120 sq ft floor)", 480, 1, 599900, 0, true},
					{"2-rooms", "2 rooms", 720, 1, 1099900, 0, false}}},
			{slug: "waterproofing-inspection", name: "Waterproofing inspection", description: "Moisture check and quote",
				duration: 45, skill: "painter", reworkDays: 7,
				options: []optionSeed{{"visit", "Inspection visit", 45, 1, 29900, 0, true}}},
		},
		rates: []rateSeed{
			{"wall_putty_room", "Wall putty (per room)", "per_item", 180000, 3, false},
			{"extra_wall", "Extra wall", "per_item", 150000, 4, false},
		}},
	{slug: "pest-control", name: "Pest control", description: "General, cockroach and termite",
		family: "PEST_CONTROL", genderRule: "any", extrasPolicy: "rate_card",
		services: []serviceSeed{
			{slug: "general-pest-control", name: "General pest control", description: "Odourless spray for crawling insects",
				duration: 75, skill: "pest_control", reworkDays: 30,
				options: []optionSeed{
					{"1bhk", "1 BHK", 60, 1, 99900, 0, false},
					{"2bhk", "2 BHK", 75, 1, 119900, 0, true},
					{"3bhk", "3 BHK", 90, 1, 149900, 0, false}}},
			{slug: "cockroach-control", name: "Cockroach control", description: "Gel treatment for kitchen and bathrooms",
				duration: 45, skill: "pest_control", reworkDays: 30,
				options: []optionSeed{
					{"1bhk", "1 BHK", 45, 1, 89900, 0, true},
					{"2bhk", "2 BHK", 60, 1, 109900, 0, false}}},
			{slug: "termite-control", name: "Termite control", description: "Drill-fill-seal treatment up to 2 BHK",
				duration: 180, skill: "pest_control", reworkDays: 90,
				options: []optionSeed{{"up-to-2bhk", "Up to 2 BHK", 180, 1, 399900, 0, true}}},
		},
		rates: []rateSeed{
			{"extra_room", "Extra room", "per_item", 39900, 3, false},
			{"gel_treatment", "Extra gel treatment", "per_item", 34900, 2, false},
		}},
	{slug: "salon-women", name: "Salon for women", description: "Waxing, facials, threading, mani-pedi at home",
		family: "BEAUTY_SALON", genderRule: "female_pros_only", extrasPolicy: "catalogue_addons_only",
		services: []serviceSeed{
			{slug: "waxing", name: "Waxing", description: "Single-use spatulas and sealed wax",
				duration: 60, skill: "salon_women", reworkDays: 3,
				options: []optionSeed{
					{"honey", "Full arms and legs (honey)", 60, 1, 79900, 99900, true},
					{"rica", "Full arms and legs (Rica)", 60, 1, 119900, 0, false}},
				groups: []groupSeed{{"add-more", "Add more", 0, 3, false, []addonSeed{
					{"underarms", "Underarms", 10, 9900}, {"half-back", "Half back", 15, 24900}, {"bikini-line", "Bikini line", 20, 39900}}}}},
			{slug: "facial", name: "Facial", description: "Sealed single-use kit",
				duration: 60, skill: "salon_women", reworkDays: 3,
				options: []optionSeed{
					{"cleanup", "Fruit cleanup", 45, 1, 69900, 0, false},
					{"gold", "Gold facial", 60, 1, 129900, 149900, true},
					{"o3", "O3+ facial", 75, 1, 219900, 0, false}},
				groups: []groupSeed{
					{"mask", "Choose a mask", 1, 1, true, []addonSeed{
						{"peel-off", "Peel-off mask", 5, 9900}, {"charcoal", "Charcoal mask", 5, 19900}}},
					{"add-ons", "Add-ons", 0, 2, false, []addonSeed{
						{"threading", "Threading (eyebrows and upper lip)", 10, 9900}, {"head-massage", "Head massage", 20, 29900}}}}},
			{slug: "threading", name: "Threading", description: "Eyebrows, upper lip or full face",
				duration: 15, skill: "salon_women", reworkDays: 1,
				options: []optionSeed{
					{"brows-lip", "Eyebrows and upper lip", 15, 1, 9900, 0, true},
					{"full-face", "Full face", 25, 1, 24900, 0, false}}},
			{slug: "manicure-pedicure", name: "Manicure and pedicure", description: "Sterilised tools",
				duration: 75, skill: "salon_women", reworkDays: 3,
				options: []optionSeed{
					{"classic", "Classic manicure and pedicure", 75, 1, 99900, 0, true},
					{"spa", "Spa manicure and pedicure", 100, 1, 159900, 0, false}}},
		}},
	{slug: "salon-men", name: "Salon for men", description: "Haircut, beard, face care and massage at home",
		family: "BEAUTY_SALON", genderRule: "male_pros_only", extrasPolicy: "catalogue_addons_only",
		services: []serviceSeed{
			{slug: "haircut", name: "Haircut", description: "Cape, sanitised tools, clean-up after",
				duration: 30, skill: "salon_men", reworkDays: 3,
				options: []optionSeed{
					{"haircut", "Haircut", 30, 1, 29900, 0, true},
					{"haircut-beard", "Haircut and beard", 45, 1, 44900, 0, false}},
				groups: []groupSeed{{"add-ons", "Add-ons", 0, 2, false, []addonSeed{
					{"head-massage", "Head massage (10 min)", 10, 14900}, {"hair-colour", "Hair colour (black)", 30, 39900}}}}},
			{slug: "face-care", name: "Face care", description: "Detan and cleanup",
				duration: 45, skill: "salon_men", reworkDays: 3,
				options: []optionSeed{{"detan", "Detan facial", 45, 1, 69900, 0, true}}},
			{slug: "massage", name: "Massage", description: "Head and shoulder or full body",
				duration: 30, skill: "salon_men", reworkDays: 1,
				options: []optionSeed{
					{"head-shoulder", "Head and shoulder massage", 30, 1, 39900, 0, true},
					{"full-body", "Full body massage (60 min)", 60, 1, 149900, 0, false}}},
		}},
}

// Placeholder cancellation rules (editable as data; founder item 3).
var cancellationRules = []struct {
	key       string
	stage     string
	ltMinutes int // 0 = NULL
	fee       int64
	allowed   bool
	sort      int
}{
	{"unassigned", "unassigned", 0, 0, true, 0},
	{"assigned-lt-60", "assigned", 60, 15000, true, 0},
	{"assigned-lt-180", "assigned", 180, 7500, true, 1},
	{"assigned-free", "assigned", 0, 0, true, 2},
	{"en-route", "en_route", 0, 15000, true, 0},
	{"arrived", "arrived", 0, 20000, true, 0},
	{"in-progress", "in_progress", 0, 0, false, 0},
}
