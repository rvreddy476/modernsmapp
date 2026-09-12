package main

// The dataset's own contract, proved without a database. Each test names a
// storefront surface that would look wrong if the property failed, because
// "the seed is internally consistent" is not a reason anybody cares about;
// "Deals of the day has eight cards" is.

import (
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// seededCategorySlugs is the taxonomy migration 023 writes. Copied rather
// than parsed out of the SQL so the test fails loudly if either side drifts,
// which is the point: a product in a category that does not exist is a
// product the seeder refuses to write.
var seededCategorySlugs = map[string]bool{
	"electronics": true, "fashion": true, "home-and-kitchen": true,
	"beauty-and-personal-care": true, "grocery-and-gourmet": true,
	"health-and-wellness": true, "sports-and-fitness": true,
	"books-and-stationery": true, "toys-and-baby": true,
	"jewellery-and-watches": true, "automotive": true,
	"handicrafts-and-decor": true,
}

func TestCatalogueValidates(t *testing.T) {
	if err := catalogue().validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogueHasEnoughToFillTheHomePage(t *testing.T) {
	cat := catalogue()
	if got := len(cat.Products); got != 16 {
		t.Fatalf("products = %d, want 16", got)
	}
	if got := len(cat.Banners); got != 3 {
		t.Fatalf("banners = %d, want 3", got)
	}
	// A home rail carries twelve; the category strip is the seeded
	// taxonomy. Six categories is the floor the task set, eight is what the
	// list has, and the count is pinned so a refactor cannot quietly
	// collapse the strip to two populated tiles.
	if got := len(cat.categorySlugs()); got < 6 {
		t.Fatalf("distinct categories = %d, want at least 6 so the strip has product counts", got)
	}
}

func TestEveryProductIsInASeededCategory(t *testing.T) {
	for _, p := range catalogue().Products {
		if !seededCategorySlugs[p.CategorySlug] {
			t.Errorf("%s is in %q, which migration 023 does not seed", p.Slug, p.CategorySlug)
		}
	}
}

func TestHalfTheProductsCarryADiscount(t *testing.T) {
	cat := catalogue()
	var discounted, full int
	for _, p := range cat.Products {
		if p.hasDiscount() {
			discounted++
		} else {
			full++
		}
	}
	// Eight and eight: "Deals of the day" needs enough rows to be a rail,
	// and the grid needs undiscounted rows so an absent badge is visibly
	// "no deal" rather than "the badge never renders".
	if discounted != 8 || full != 8 {
		t.Fatalf("discounted = %d, at MRP = %d; want 8 and 8", discounted, full)
	}
}

func TestDiscountPctIsNonZeroOnDiscountedProductsAndNilOnTheRest(t *testing.T) {
	// The same derivation the API uses, so the test proves what the badge
	// will say rather than what the dataset intends.
	for _, p := range catalogue().Products {
		v := p.Variants[0]
		pct := postgres.DiscountPct(&v.PriceMinor, &v.MRPMinor)
		switch {
		case p.hasDiscount() && (pct == nil || *pct <= 0):
			t.Errorf("%s: discounted but discount_pct = %v", p.Slug, pct)
		case !p.hasDiscount() && pct != nil:
			t.Errorf("%s: at MRP but discount_pct = %d", p.Slug, *pct)
		}
	}
}

func TestSlugsSKUsAndIDsAreUnique(t *testing.T) {
	cat := catalogue()
	slugs := map[string]string{}
	skus := map[string]string{}
	ids := map[uuid.UUID]string{cat.Seller.ID: "seller", cat.Seller.UserID: "seller user"}
	claim := func(id uuid.UUID, what string) {
		if prev, dup := ids[id]; dup {
			t.Errorf("id %s used by both %s and %s", id, prev, what)
		}
		ids[id] = what
	}
	for _, p := range cat.Products {
		if prev, dup := slugs[p.Slug]; dup {
			t.Errorf("slug %q used twice (%s)", p.Slug, prev)
		}
		slugs[p.Slug] = p.Title
		claim(p.ID, "product "+p.Slug)
		for _, v := range p.Variants {
			if prev, dup := skus[v.SKU]; dup {
				t.Errorf("sku %q used twice (%s)", v.SKU, prev)
			}
			skus[v.SKU] = p.Slug
			claim(v.ID, "variant "+v.SKU)
		}
	}
	for _, b := range cat.Banners {
		claim(b.ID, "banner "+b.Title)
	}
}

func TestEveryProductHasAVariantWithStockAndAnImage(t *testing.T) {
	for _, p := range catalogue().Products {
		if len(p.Variants) == 0 {
			t.Errorf("%s has no variant, so the grid would show no price", p.Slug)
			continue
		}
		for _, v := range p.Variants {
			if v.Stock <= 0 {
				t.Errorf("%s/%s has no stock, so in_stock would be false", p.Slug, v.SKU)
			}
		}
		// The cheapest variant is first, which is the one the summary
		// projection prices from; the dataset keeps them in that order so
		// the file reads like the grid.
		if len(p.Variants) == 2 && p.Variants[1].PriceMinor < p.Variants[0].PriceMinor {
			t.Errorf("%s lists its dearer variant first", p.Slug)
		}
		if !strings.HasPrefix(demoImageURL(p.Slug), "https://") {
			t.Errorf("%s has no https image", p.Slug)
		}
	}
}

func TestIDsSitInTheReservedBlock(t *testing.T) {
	// Every literal shares the prefix so the demo rows can be found and
	// deleted with one LIKE, which is the documented way to reset.
	const prefix = "00000000-0000-4000-8000-0000000de"
	cat := catalogue()
	check := func(id uuid.UUID, what string) {
		if !strings.HasPrefix(id.String(), prefix) {
			t.Errorf("%s id %s is outside the reserved block", what, id)
		}
	}
	check(cat.Seller.ID, "seller")
	for _, p := range cat.Products {
		check(p.ID, p.Slug)
		for _, v := range p.Variants {
			check(v.ID, v.SKU)
		}
	}
	for _, b := range cat.Banners {
		check(b.ID, b.Title)
	}
}

func TestBannersCoverEveryTargetKind(t *testing.T) {
	kinds := map[string]bool{}
	for _, b := range catalogue().Banners {
		kinds[b.TargetType] = true
		if b.TargetType != "search" {
			if _, err := uuid.Parse(b.TargetID); err != nil {
				t.Errorf("banner %q targets %s %q, which the table CHECK would reject", b.Title, b.TargetType, b.TargetID)
			}
		}
	}
	for _, k := range []string{"product", "category", "search"} {
		if !kinds[k] {
			t.Errorf("no banner with target_type %q", k)
		}
	}
}

func TestValidateCatchesTheMistakesItIsFor(t *testing.T) {
	cases := map[string]func(*demoCatalogue){
		"duplicate slug":        func(c *demoCatalogue) { c.Products[1].Slug = c.Products[0].Slug },
		"duplicate sku":         func(c *demoCatalogue) { c.Products[1].Variants[0].SKU = c.Products[0].Variants[0].SKU },
		"no variants":           func(c *demoCatalogue) { c.Products[0].Variants = nil },
		"price above mrp":       func(c *demoCatalogue) { c.Products[0].Variants[0].PriceMinor = c.Products[0].Variants[0].MRPMinor + 1 },
		"zero stock":            func(c *demoCatalogue) { c.Products[0].Variants[0].Stock = 0 },
		"banner with no target": func(c *demoCatalogue) { c.Banners[0].TargetID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := catalogue()
			mutate(&c)
			if err := c.validate(); err == nil {
				t.Fatalf("validate accepted a catalogue with a %s", name)
			}
		})
	}
}
