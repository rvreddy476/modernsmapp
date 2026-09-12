package main

// The demo dataset, as data.
//
// Everything the seeder writes is declared here as Go values and nothing is
// computed at run time from the database, so a fresh dev stack and a
// colleague's laptop end up with byte-identical rows. That is also what
// makes the dataset unit-testable: the assertions in catalogue_test.go are
// about this file, and they run with no database at all.
//
// ─── WHY EVERY ID IS A FIXED LITERAL ────────────────────────────────────
//
// The same reason migration 023 gives for category ids. A product id ends
// up in a deep link, a cart, a favourites row and an order; regenerating it
// on every run would make the seeder non-idempotent (a second run would
// insert sixteen more products) and would rot every reference a client kept.
// Fixed ids plus ON CONFLICT DO NOTHING is what makes "run it again" safe.
//
// The literals sit in a reserved 00000000-0000-4000-8000-0000000deNNN block,
// the same shape 023 and 024 use, with the fourth-from-last hex digit saying
// which table the id belongs to: 0 seller, 1 product, 2 variant, 3 second
// variant, 5 banner. A collision with a real gen_random_uuid() row is not a
// practical concern; a collision with another seed file would be, and the
// block is easy to grep for.
//
// ─── WHY PRICES ARE DECLARED IN PAISE ───────────────────────────────────
//
// The storefront reads `selling_price_minor` and `mrp_minor` first and only
// falls back to the NUMERIC rupee columns for rows that predate migration
// 007. `discount_pct` is derived from the minor pair. Declaring the minor
// value and DERIVING the rupee float for the legacy column keeps the two
// from disagreeing, which is the only way a badge can say 20% while the
// price says something else.

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// demoSeller is the one shop every demo product belongs to.
//
// `status` is the onboarding vocabulary from migration 001 and must be
// 'approved'; `store_status` is what the storefront's visibility rule checks
// (productSummaryLive), so anything but 'active' hides the whole catalogue.
// `user_id` is a made-up identity id: nothing in commerce joins it, but it is
// UNIQUE, so it is fixed too.
type demoSeller struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	StoreName   string
	Slug        string
	Description string
	Email       string
	Tagline     string
	City        string
	State       string
}

// demoVariant is one purchasable line under a product. The first variant of
// every product is the cheapest one on purpose: the summary projection picks
// the cheapest active variant for the grid price, and declaring the cheapest
// first makes the dataset readable next to what the app shows.
type demoVariant struct {
	ID          uuid.UUID
	SKU         string
	OptionName  string
	OptionValue string
	MRPMinor    int64
	PriceMinor  int64
	Stock       int
	WeightGrams int
}

// demoProduct is one catalogue row plus its offer, variants and stock.
type demoProduct struct {
	ID               uuid.UUID
	Slug             string
	CategorySlug     string
	Title            string
	ShortTitle       string
	Description      string
	ShortDescription string
	Brand            string
	SKURoot          string
	Keywords         []string
	AvgRating        float32
	ReviewCount      int
	// ViewCount seeds the "best sellers" fallback ordering. A fresh stack has
	// no orders, so Home ranks by views; without a spread here every product
	// would tie on zero and the rail would come out in insertion order.
	ViewCount int
	Featured  bool
	Variants  []demoVariant
}

// demoBanner is one home-carousel card. image_media_id stays NULL for the
// reason migration 024 explains at length: a banner image is a media-service
// asset and a seeder cannot mint one; the client draws its gradient.
type demoBanner struct {
	ID         uuid.UUID
	Title      string
	Subtitle   string
	TargetType string
	// TargetID is a UUID string for category and product targets, or the
	// query text for a search target; the table CHECKs that shape.
	TargetID string
	Position int
}

// demoCatalogue is the whole dataset.
type demoCatalogue struct {
	Seller   demoSeller
	Products []demoProduct
	Banners  []demoBanner
}

// demoID builds one of the reserved literals. table is the hex digit that
// says what kind of row it is, n the ordinal within that kind.
func demoID(table byte, n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-0000000de%c%02x", table, n))
}

// demoImageURL is a stable, public placeholder photograph.
//
// picsum.photos serves the same image for the same seed forever, answers
// 302 to a CDN jpeg, and needs no key. It is a placeholder, not the product
// pictured, which is fine for a demo grid whose job is to look populated.
// The seed is the product slug so two products never share a picture.
func demoImageURL(slug string) string {
	return "https://picsum.photos/seed/" + slug + "/800/800"
}

// rupees converts a paise amount to the legacy NUMERIC(12,2) column value.
func rupees(minor int64) float64 {
	return float64(minor) / 100
}

// discounted is a variant whose selling price is below MRP; the home page's
// "Deals of the day" rail is built from exactly this condition.
func (v demoVariant) discounted() bool {
	return v.MRPMinor > 0 && v.PriceMinor > 0 && v.PriceMinor < v.MRPMinor
}

// hasDiscount is true when the product's cheapest variant carries a cut,
// which is the variant the grid prices from.
func (p demoProduct) hasDiscount() bool {
	return len(p.Variants) > 0 && p.Variants[0].discounted()
}

// catalogue returns the dataset. It is a function rather than a package-level
// var so a test that mutates its result cannot leak into the next test.
func catalogue() demoCatalogue {
	seller := demoSeller{
		ID:          demoID('0', 1),
		UserID:      demoID('0', 2),
		StoreName:   "Momentum Demo Store",
		Slug:        "momentum-demo-store",
		Description: "A demonstration shop seeded by cmd/seed-demo. Every product here is a placeholder for what a real seller would list.",
		Email:       "demo-store@momentum.local",
		Tagline:     "Everything a home screen needs",
		City:        "Hyderabad",
		State:       "Telangana",
	}

	// Eight of the sixteen carry a discount, eight sell at MRP. The split is
	// deliberate: "Deals of the day" must have enough rows to fill a rail,
	// and the grid must also show products WITHOUT a badge so a missing
	// badge reads as "no deal" rather than "the badge is broken".
	products := []demoProduct{
		// ── Electronics ──────────────────────────────────────────
		{
			Slug: "demo-pulse-anc-wireless-headphones", CategorySlug: "electronics",
			Title:            "Pulse ANC Wireless Headphones",
			ShortTitle:       "Pulse ANC Headphones",
			Description:      "Over-ear headphones with hybrid active noise cancellation, 40 hours of playback, USB-C fast charging and a foldable frame that fits the included travel case. Multipoint pairing keeps a phone and a laptop connected at once.",
			ShortDescription: "Hybrid ANC, 40-hour battery, multipoint pairing.",
			Brand:            "Pulse", SKURoot: "DEMO-PULSE-ANC",
			Keywords:  []string{"headphones", "wireless", "noise cancelling", "bluetooth"},
			AvgRating: 4.5, ReviewCount: 312, ViewCount: 4200, Featured: true,
			Variants: []demoVariant{
				{SKU: "DEMO-PULSE-ANC-BLK", OptionName: "Colour", OptionValue: "Midnight Black", MRPMinor: 799900, PriceMinor: 549900, Stock: 40, WeightGrams: 320},
				{SKU: "DEMO-PULSE-ANC-SLV", OptionName: "Colour", OptionValue: "Moon Silver", MRPMinor: 799900, PriceMinor: 579900, Stock: 25, WeightGrams: 320},
			},
		},
		{
			Slug: "demo-nova-smartwatch-s2", CategorySlug: "electronics",
			Title:            "Nova S2 Smartwatch",
			ShortTitle:       "Nova S2 Smartwatch",
			Description:      "A 1.43-inch AMOLED smartwatch with Bluetooth calling, SpO2 and heart-rate tracking, 100+ sport modes and a seven-day battery. IP68 rated, with interchangeable 22 mm straps.",
			ShortDescription: "AMOLED display, Bluetooth calling, 7-day battery.",
			Brand:            "Nova", SKURoot: "DEMO-NOVA-S2",
			Keywords:  []string{"smartwatch", "fitness", "bluetooth calling"},
			AvgRating: 4.2, ReviewCount: 188, ViewCount: 3100,
			Variants: []demoVariant{
				{SKU: "DEMO-NOVA-S2-BLK", OptionName: "Strap", OptionValue: "Black Silicone", MRPMinor: 499900, PriceMinor: 499900, Stock: 60, WeightGrams: 45},
			},
		},
		// ── Fashion ──────────────────────────────────────────────
		{
			Slug: "demo-heritage-linen-kurta", CategorySlug: "fashion",
			Title:            "Heritage Linen Kurta",
			ShortTitle:       "Linen Kurta",
			Description:      "A straight-cut kurta in 100% linen with a mandarin collar, mother-of-pearl buttons and side slits. Pre-washed so it softens with wear rather than shrinking. Pairs with the matching churidar or plain trousers.",
			ShortDescription: "Pure linen, mandarin collar, pre-washed.",
			Brand:            "Heritage", SKURoot: "DEMO-HERITAGE-KURTA",
			Keywords:  []string{"kurta", "linen", "ethnic wear", "men"},
			AvgRating: 4.4, ReviewCount: 96, ViewCount: 2600, Featured: true,
			Variants: []demoVariant{
				{SKU: "DEMO-HERITAGE-KURTA-M", OptionName: "Size", OptionValue: "M", MRPMinor: 249900, PriceMinor: 174900, Stock: 30, WeightGrams: 350},
				{SKU: "DEMO-HERITAGE-KURTA-L", OptionName: "Size", OptionValue: "L", MRPMinor: 249900, PriceMinor: 174900, Stock: 30, WeightGrams: 370},
			},
		},
		{
			Slug: "demo-stride-everyday-sneakers", CategorySlug: "fashion",
			Title:            "Stride Everyday Sneakers",
			ShortTitle:       "Stride Sneakers",
			Description:      "Low-top sneakers with a knit upper, cushioned EVA midsole and a rubber outsole that grips wet pavement. Machine-washable insoles. True to size.",
			ShortDescription: "Knit upper, cushioned midsole, washable insole.",
			Brand:            "Stride", SKURoot: "DEMO-STRIDE-SNK",
			Keywords:  []string{"sneakers", "shoes", "casual", "unisex"},
			AvgRating: 4.1, ReviewCount: 141, ViewCount: 1900,
			Variants: []demoVariant{
				{SKU: "DEMO-STRIDE-SNK-8", OptionName: "Size", OptionValue: "UK 8", MRPMinor: 329900, PriceMinor: 329900, Stock: 18, WeightGrams: 700},
				{SKU: "DEMO-STRIDE-SNK-9", OptionName: "Size", OptionValue: "UK 9", MRPMinor: 329900, PriceMinor: 329900, Stock: 22, WeightGrams: 720},
			},
		},
		// ── Home & Kitchen ───────────────────────────────────────
		{
			Slug: "demo-ember-cast-iron-kadai", CategorySlug: "home-and-kitchen",
			Title:            "Ember Pre-Seasoned Cast Iron Kadai, 26 cm",
			ShortTitle:       "Cast Iron Kadai 26 cm",
			Description:      "A 26 cm cast iron kadai seasoned three times with flaxseed oil, ready for the stove on day one. Holds heat for a proper tadka, works on induction, and comes with a glass lid.",
			ShortDescription: "Pre-seasoned, induction-ready, glass lid included.",
			Brand:            "Ember", SKURoot: "DEMO-EMBER-KADAI",
			Keywords:  []string{"kadai", "cast iron", "cookware", "induction"},
			AvgRating: 4.7, ReviewCount: 420, ViewCount: 3800, Featured: true,
			Variants: []demoVariant{
				{SKU: "DEMO-EMBER-KADAI-26", OptionName: "Size", OptionValue: "26 cm", MRPMinor: 299900, PriceMinor: 199900, Stock: 35, WeightGrams: 2600},
			},
		},
		{
			Slug: "demo-cloudnine-cotton-bedsheet-set", CategorySlug: "home-and-kitchen",
			Title:            "CloudNine 300 TC Cotton Bedsheet Set, King",
			ShortTitle:       "Cotton Bedsheet Set",
			Description:      "A king-size fitted sheet with two pillow covers in 300 thread-count long-staple cotton. Deep elastic pockets fit mattresses up to 25 cm. Colourfast and gets softer with every wash.",
			ShortDescription: "300 TC long-staple cotton, king, with two pillow covers.",
			Brand:            "CloudNine", SKURoot: "DEMO-CLOUDNINE-SHEET",
			Keywords:  []string{"bedsheet", "cotton", "king size", "bedding"},
			AvgRating: 4.3, ReviewCount: 210, ViewCount: 1500,
			Variants: []demoVariant{
				{SKU: "DEMO-CLOUDNINE-SHEET-IVY", OptionName: "Colour", OptionValue: "Ivory", MRPMinor: 189900, PriceMinor: 189900, Stock: 50, WeightGrams: 1200},
			},
		},
		// ── Beauty & Personal Care ───────────────────────────────
		{
			Slug: "demo-dewdrop-vitamin-c-serum", CategorySlug: "beauty-and-personal-care",
			Title:            "Dewdrop 10% Vitamin C Face Serum, 30 ml",
			ShortTitle:       "Vitamin C Serum",
			Description:      "A lightweight serum with 10% stabilised vitamin C, hyaluronic acid and ferulic acid for brighter, more even skin. Fragrance-free and dermatologically tested; comes in an airless pump to keep the formula stable.",
			ShortDescription: "10% stabilised vitamin C, fragrance-free, airless pump.",
			Brand:            "Dewdrop", SKURoot: "DEMO-DEWDROP-VITC",
			Keywords:  []string{"serum", "vitamin c", "skincare", "brightening"},
			AvgRating: 4.6, ReviewCount: 530, ViewCount: 5100, Featured: true,
			Variants: []demoVariant{
				{SKU: "DEMO-DEWDROP-VITC-30", OptionName: "Size", OptionValue: "30 ml", MRPMinor: 99900, PriceMinor: 64900, Stock: 120, WeightGrams: 80},
			},
		},
		{
			Slug: "demo-oakbeard-grooming-kit", CategorySlug: "beauty-and-personal-care",
			Title:            "Oakbeard Beard Grooming Kit",
			ShortTitle:       "Beard Grooming Kit",
			Description:      "Beard oil, balm, a boar-bristle brush and a sandalwood comb in a gift tin. The oil is argan and jojoba with a cedarwood scent; the balm holds shape without stiffness.",
			ShortDescription: "Oil, balm, brush and comb in a gift tin.",
			Brand:            "Oakbeard", SKURoot: "DEMO-OAKBEARD-KIT",
			Keywords:  []string{"beard", "grooming", "men", "gift"},
			AvgRating: 4.0, ReviewCount: 77, ViewCount: 900,
			Variants: []demoVariant{
				{SKU: "DEMO-OAKBEARD-KIT-STD", OptionName: "Pack", OptionValue: "Standard", MRPMinor: 129900, PriceMinor: 129900, Stock: 40, WeightGrams: 260},
			},
		},
		// ── Grocery & Gourmet ────────────────────────────────────
		{
			Slug: "demo-coorg-single-estate-coffee", CategorySlug: "grocery-and-gourmet",
			Title:            "Coorg Single-Estate Arabica Coffee, 500 g",
			ShortTitle:       "Coorg Arabica Coffee",
			Description:      "Medium-roast arabica from a single estate in Coorg, roasted within a week of dispatch. Tasting notes of dark chocolate and orange peel. Available as whole beans or ground for a French press.",
			ShortDescription: "Single estate, medium roast, roasted to order.",
			Brand:            "Coorg Estate", SKURoot: "DEMO-COORG-COFFEE",
			Keywords:  []string{"coffee", "arabica", "coorg", "whole bean"},
			AvgRating: 4.8, ReviewCount: 264, ViewCount: 2200,
			Variants: []demoVariant{
				{SKU: "DEMO-COORG-COFFEE-BEAN", OptionName: "Grind", OptionValue: "Whole Bean", MRPMinor: 79900, PriceMinor: 67900, Stock: 80, WeightGrams: 520},
				{SKU: "DEMO-COORG-COFFEE-FP", OptionName: "Grind", OptionValue: "French Press", MRPMinor: 79900, PriceMinor: 67900, Stock: 60, WeightGrams: 520},
			},
		},
		{
			Slug: "demo-harvest-dry-fruit-gift-box", CategorySlug: "grocery-and-gourmet",
			Title:            "Harvest Premium Dry Fruit Gift Box, 1 kg",
			ShortTitle:       "Dry Fruit Gift Box",
			Description:      "Almonds, cashews, pistachios and walnuts in four 250 g compartments inside a reusable wooden box. Sourced this season and vacuum-packed. A festive card slot is included.",
			ShortDescription: "Four nuts, 1 kg, reusable wooden box.",
			Brand:            "Harvest", SKURoot: "DEMO-HARVEST-DRYFRUIT",
			Keywords:  []string{"dry fruits", "gift box", "almonds", "cashews"},
			AvgRating: 4.4, ReviewCount: 155, ViewCount: 1300,
			Variants: []demoVariant{
				{SKU: "DEMO-HARVEST-DRYFRUIT-1KG", OptionName: "Weight", OptionValue: "1 kg", MRPMinor: 149900, PriceMinor: 149900, Stock: 45, WeightGrams: 1300},
			},
		},
		// ── Sports & Fitness ─────────────────────────────────────
		{
			Slug: "demo-flexcore-yoga-mat", CategorySlug: "sports-and-fitness",
			Title:            "FlexCore Non-Slip Yoga Mat, 6 mm",
			ShortTitle:       "Yoga Mat 6 mm",
			Description:      "A 183 x 61 cm TPE yoga mat with a dual-layer grip surface, 6 mm cushioning for joints and alignment lines printed in. Latex-free and light enough to carry with the included strap.",
			ShortDescription: "TPE, 6 mm, alignment lines, carry strap.",
			Brand:            "FlexCore", SKURoot: "DEMO-FLEXCORE-MAT",
			Keywords:  []string{"yoga mat", "fitness", "exercise", "non slip"},
			AvgRating: 4.3, ReviewCount: 198, ViewCount: 2000,
			Variants: []demoVariant{
				{SKU: "DEMO-FLEXCORE-MAT-TEAL", OptionName: "Colour", OptionValue: "Teal", MRPMinor: 199900, PriceMinor: 119900, Stock: 70, WeightGrams: 950},
				{SKU: "DEMO-FLEXCORE-MAT-PLUM", OptionName: "Colour", OptionValue: "Plum", MRPMinor: 199900, PriceMinor: 119900, Stock: 55, WeightGrams: 950},
			},
		},
		{
			Slug: "demo-ironpeak-adjustable-dumbbells", CategorySlug: "sports-and-fitness",
			Title:            "IronPeak Adjustable Dumbbell Pair, 20 kg",
			ShortTitle:       "Adjustable Dumbbells 20 kg",
			Description:      "Two dumbbells with 20 kg of rubber-coated plates and spin-lock collars, so one pair covers everything from curls to goblet squats. Comes in a carry case.",
			ShortDescription: "20 kg total, rubber-coated plates, spin-lock collars.",
			Brand:            "IronPeak", SKURoot: "DEMO-IRONPEAK-DB",
			Keywords:  []string{"dumbbells", "home gym", "weights", "strength"},
			AvgRating: 4.5, ReviewCount: 88, ViewCount: 1100,
			Variants: []demoVariant{
				{SKU: "DEMO-IRONPEAK-DB-20", OptionName: "Weight", OptionValue: "20 kg", MRPMinor: 349900, PriceMinor: 349900, Stock: 15, WeightGrams: 20500},
			},
		},
		// ── Books & Stationery ───────────────────────────────────
		{
			Slug: "demo-scribe-dotted-notebook-a5", CategorySlug: "books-and-stationery",
			Title:            "Scribe Dotted Notebook, A5, 192 pages",
			ShortTitle:       "Dotted Notebook A5",
			Description:      "A hardbound A5 notebook with 192 pages of 100 gsm dotted paper that takes fountain-pen ink without bleed. Lay-flat binding, two ribbon markers, an elastic closure and a back pocket.",
			ShortDescription: "100 gsm dotted paper, lay-flat, two ribbons.",
			Brand:            "Scribe", SKURoot: "DEMO-SCRIBE-NB",
			Keywords:  []string{"notebook", "dotted", "bullet journal", "stationery"},
			AvgRating: 4.6, ReviewCount: 340, ViewCount: 1700,
			Variants: []demoVariant{
				{SKU: "DEMO-SCRIBE-NB-NAVY", OptionName: "Colour", OptionValue: "Navy", MRPMinor: 59900, PriceMinor: 44900, Stock: 150, WeightGrams: 380},
				{SKU: "DEMO-SCRIBE-NB-SAGE", OptionName: "Colour", OptionValue: "Sage", MRPMinor: 59900, PriceMinor: 44900, Stock: 120, WeightGrams: 380},
			},
		},
		{
			Slug: "demo-the-quiet-river-novel", CategorySlug: "books-and-stationery",
			Title:            "The Quiet River (Paperback)",
			ShortTitle:       "The Quiet River",
			Description:      "A novel following three generations of a family of boat-builders on the Godavari, from the last years of the Nizam to the present day. 412 pages. This is a fictional title created for the demo catalogue.",
			ShortDescription: "Literary fiction, 412 pages, paperback.",
			Brand:            "Riverbank Press", SKURoot: "DEMO-QUIET-RIVER",
			Keywords:  []string{"novel", "fiction", "paperback", "indian literature"},
			AvgRating: 4.2, ReviewCount: 64, ViewCount: 600,
			Variants: []demoVariant{
				{SKU: "DEMO-QUIET-RIVER-PB", OptionName: "Format", OptionValue: "Paperback", MRPMinor: 49900, PriceMinor: 49900, Stock: 90, WeightGrams: 420},
			},
		},
		// ── Handicrafts & Decor ──────────────────────────────────
		{
			Slug: "demo-kalamkari-hand-painted-wall-hanging", CategorySlug: "handicrafts-and-decor",
			Title:            "Kalamkari Hand-Painted Cotton Wall Hanging, 90 x 60 cm",
			ShortTitle:       "Kalamkari Wall Hanging",
			Description:      "A hand-painted Kalamkari panel from Srikalahasti on unbleached cotton, using natural dyes fixed with myrobalan. Each piece is drawn freehand, so no two are identical. Comes with a bamboo hanging rod.",
			ShortDescription: "Hand-painted, natural dyes, bamboo rod included.",
			Brand:            "Srikalahasti Artisans", SKURoot: "DEMO-KALAMKARI-WALL",
			Keywords:  []string{"kalamkari", "wall hanging", "handicraft", "home decor"},
			AvgRating: 4.9, ReviewCount: 41, ViewCount: 1400, Featured: true,
			Variants: []demoVariant{
				{SKU: "DEMO-KALAMKARI-WALL-TREE", OptionName: "Motif", OptionValue: "Tree of Life", MRPMinor: 399900, PriceMinor: 299900, Stock: 8, WeightGrams: 400},
			},
		},
		{
			Slug: "demo-brass-diya-set-of-4", CategorySlug: "handicrafts-and-decor",
			Title:            "Hand-Cast Brass Diya, Set of 4",
			ShortTitle:       "Brass Diya Set",
			Description:      "Four solid brass diyas cast in Moradabad with a peacock motif, each 7 cm across, finished by hand and lacquered so they need no polishing. Packed in a jute pouch for gifting.",
			ShortDescription: "Solid brass, peacock motif, lacquered, jute pouch.",
			Brand:            "Moradabad Brassworks", SKURoot: "DEMO-BRASS-DIYA",
			Keywords:  []string{"diya", "brass", "diwali", "puja", "decor"},
			AvgRating: 4.5, ReviewCount: 119, ViewCount: 800,
			Variants: []demoVariant{
				{SKU: "DEMO-BRASS-DIYA-4", OptionName: "Pack", OptionValue: "Set of 4", MRPMinor: 89900, PriceMinor: 89900, Stock: 60, WeightGrams: 480},
			},
		},
	}

	// Ids are assigned from position so the catalogue above stays readable
	// and so a product added in the middle of the list shifts nothing: the
	// literals are derived from the ordinal, which the test for uniqueness
	// pins.
	for i := range products {
		p := &products[i]
		p.ID = demoID('1', i+1)
		for j := range p.Variants {
			// Table digit '2' for the first variant, '3' for the second. A
			// third variant would need a fourth digit and the test says so.
			p.Variants[j].ID = demoID(byte('2'+j), i+1)
		}
	}

	// The three cards are one of each target kind, on purpose: it exercises
	// every branch the client has for opening a banner, which is the branch
	// a merchandiser is most likely to get wrong in the admin call later.
	banners := []demoBanner{
		{
			ID: demoID('5', 1), Title: "Headphones that hush the commute",
			Subtitle: "Pulse ANC, now under 5,500", TargetType: "product",
			TargetID: products[0].ID.String(), Position: 5,
		},
		{
			ID: demoID('5', 2), Title: "Handmade for the festive season",
			Subtitle: "Kalamkari, brass and more from Indian artisans", TargetType: "category",
			TargetID: "", Position: 40, // filled below from the category id
		},
		{
			ID: demoID('5', 3), Title: "Kitchen upgrades from 1,999",
			Subtitle: "Cast iron, cotton and coffee for the home", TargetType: "search",
			TargetID: "kitchen", Position: 50,
		},
	}
	// Handicrafts is the featured category migration 024's banners do not
	// point at, and its id is fixed by migration 023, so it is safe to write
	// here as a literal. The seeder still verifies the slug exists before it
	// writes anything, so a database where 023 has not run fails early.
	banners[1].TargetID = handicraftsCategoryID.String()

	return demoCatalogue{Seller: seller, Products: products, Banners: banners}
}

// handicraftsCategoryID is migration 023's literal for 'handicrafts-and-decor'.
var handicraftsCategoryID = uuid.MustParse("00000000-0000-4000-8000-00000000c00c")

// categorySlugs is the distinct set of category slugs the dataset needs, in
// first-seen order. The seeder resolves these to ids in one query and refuses
// to run if any is missing.
func (c demoCatalogue) categorySlugs() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range c.Products {
		if !seen[p.CategorySlug] {
			seen[p.CategorySlug] = true
			out = append(out, p.CategorySlug)
		}
	}
	return out
}

// validate is the dataset's own consistency check, run by the tests and by
// the seeder before it opens a transaction. Catching a duplicate slug here
// is a clear message; catching it as a unique-violation halfway through a
// transaction is a rollback and a stack trace.
func (c demoCatalogue) validate() error {
	var problems []string
	slugs := map[string]bool{}
	ids := map[uuid.UUID]bool{c.Seller.ID: true, c.Seller.UserID: true}
	skus := map[string]bool{}
	for _, p := range c.Products {
		if slugs[p.Slug] {
			problems = append(problems, "duplicate product slug "+p.Slug)
		}
		slugs[p.Slug] = true
		if ids[p.ID] {
			problems = append(problems, "duplicate id on product "+p.Slug)
		}
		ids[p.ID] = true
		if len(p.Variants) == 0 {
			problems = append(problems, "product "+p.Slug+" has no variant")
		}
		if len(p.Variants) > 2 {
			problems = append(problems, "product "+p.Slug+" has more than two variants; demoID reserves digits for two")
		}
		for _, v := range p.Variants {
			if skus[v.SKU] {
				problems = append(problems, "duplicate sku "+v.SKU)
			}
			skus[v.SKU] = true
			if ids[v.ID] {
				problems = append(problems, "duplicate id on variant "+v.SKU)
			}
			ids[v.ID] = true
			if v.MRPMinor <= 0 || v.PriceMinor <= 0 || v.PriceMinor > v.MRPMinor {
				problems = append(problems, "variant "+v.SKU+" has a price outside 0 < price <= mrp")
			}
			if v.Stock <= 0 {
				problems = append(problems, "variant "+v.SKU+" has no stock; the grid would show it as unavailable")
			}
		}
	}
	for _, b := range c.Banners {
		if ids[b.ID] {
			problems = append(problems, "duplicate id on banner "+b.Title)
		}
		ids[b.ID] = true
		if b.TargetID == "" {
			problems = append(problems, "banner "+b.Title+" has no target")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("demo catalogue is inconsistent:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}
