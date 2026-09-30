package main

// The GST class and HSN code of every demo product.
//
// A quote refuses to invent tax: a product without a tax class answers
// 409 PRODUCT_TAX_UNCONFIGURED, so a demo catalogue without these cannot be
// checked out at all (found on dev, 30 Sep 2026). These are PLACEHOLDERS for
// a demonstration shop, chosen from the seeded classes (GST 0/5/18 %) by the
// usual HSN chapter for the kind of goods. They are not tax advice and must
// never be copied into a real seller's listing.

type demoTaxLine struct {
	Class string // tax_classes.name, seeded by setup.sql
	HSN   string
}

var demoTax = map[string]demoTaxLine{
	"demo-pulse-anc-wireless-headphones":       {"GST 18%", "85183000"},
	"demo-nova-smartwatch-s2":                  {"GST 18%", "85176290"},
	"demo-heritage-linen-kurta":                {"GST 5%", "62114200"},
	"demo-stride-everyday-sneakers":            {"GST 5%", "64041190"},
	"demo-ember-cast-iron-kadai":               {"GST 5%", "73239100"},
	"demo-cloudnine-cotton-bedsheet-set":       {"GST 5%", "63022100"},
	"demo-dewdrop-vitamin-c-serum":             {"GST 18%", "33049990"},
	"demo-oakbeard-grooming-kit":               {"GST 18%", "33071090"},
	"demo-coorg-single-estate-coffee":          {"GST 5%", "09011112"},
	"demo-harvest-dry-fruit-gift-box":          {"GST 5%", "08029900"},
	"demo-flexcore-yoga-mat":                   {"GST 5%", "95069190"},
	"demo-ironpeak-adjustable-dumbbells":       {"GST 5%", "95069110"},
	"demo-scribe-dotted-notebook-a5":           {"GST 5%", "48202000"},
	"demo-the-quiet-river-novel":               {"GST 0%", "49011010"},
	"demo-kalamkari-hand-painted-wall-hanging": {"GST 5%", "63049219"},
	"demo-brass-diya-set-of-4":                 {"GST 5%", "74199990"},
}

// demoTaxClasses are the class names the map may use; the test pins that
// every entry names one of them, so a typo cannot seed a NULL class.
var demoTaxClasses = map[string]bool{"GST 0%": true, "GST 5%": true, "GST 18%": true}
