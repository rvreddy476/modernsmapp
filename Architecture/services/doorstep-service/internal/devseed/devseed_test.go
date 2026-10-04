package devseed

import (
	"context"
	"errors"
	"regexp"
	"testing"
)

// Run refuses before touching the database (nil pool) anywhere but an
// explicit local/dev/development environment.
func TestRunRefusesOutsideDevelopment(t *testing.T) {
	for _, env := range []map[string]string{
		nil,
		{"ENV": "prod"},
		{"ENV": "staging"},
		{"ENV": "qa"},
		{"ENV": "dev", "APP_ENV": "production"},
	} {
		err := Run(context.Background(), nil, func(k string) string { return env[k] })
		if !errors.Is(err, ErrNotDevelopment) {
			t.Errorf("env %v: err = %v, want ErrNotDevelopment", env, err)
		}
	}
}

func TestSeedDataShape(t *testing.T) {
	slug := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	code := regexp.MustCompile(`^[a-z0-9_]{2,64}$`)
	skillSet := map[string]bool{}
	for _, k := range skills {
		skillSet[k[0]] = true
	}
	want := map[string]string{"salon-women": "female_pros_only", "salon-men": "male_pros_only", "makeup-artist": "female_pros_only"}
	launch := []string{"home-cleaning", "ac-service-repair", "appliance-ro-repair", "electrician", "plumber", "carpenter",
		"painting", "pest-control", "salon-women", "salon-men"}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, c := range categories {
		seen[c.slug] = true
		if !slug.MatchString(c.slug) {
			t.Errorf("category slug %q", c.slug)
		}
		if rule, ok := want[c.slug]; ok {
			if c.genderRule != rule || c.extrasPolicy != "catalogue_addons_only" || len(c.rates) != 0 {
				t.Errorf("%s: gender %s extras %s rates %d", c.slug, c.genderRule, c.extrasPolicy, len(c.rates))
			}
		} else if c.genderRule != "any" || c.extrasPolicy != "rate_card" || len(c.rates) == 0 {
			t.Errorf("%s: want any/rate_card with a rate card", c.slug)
		}
		if n := len(c.services); n < 2 || n > 4 {
			t.Errorf("%s has %d services, want 2-4", c.slug, n)
		}
		for _, r := range c.rates {
			if !code.MatchString(r.code) || r.price <= 0 || r.maxQty < 1 {
				t.Errorf("%s rate %+v", c.slug, r)
			}
		}
		for _, s := range c.services {
			if !slug.MatchString(s.slug) || !skillSet[s.skill] || s.duration < 15 || s.duration > 720 {
				t.Errorf("%s/%s: slug, skill or duration invalid", c.slug, s.slug)
			}
			defaults := 0
			for _, o := range s.options {
				key := c.slug + "/" + s.slug + "/" + o.key
				if ids[key] {
					t.Errorf("duplicate option key %s", key)
				}
				ids[key] = true
				if o.price <= 0 || (o.mrp != 0 && o.mrp < o.price) || o.maxQty < 1 || o.duration < 5 || o.duration > 720 {
					t.Errorf("option %s invalid", key)
				}
				if o.isDefault {
					defaults++
				}
			}
			if len(s.options) == 0 || defaults > 1 {
				t.Errorf("%s/%s: %d options, %d defaults", c.slug, s.slug, len(s.options), defaults)
			}
			for _, g := range s.groups {
				if g.min < 0 || g.max < 1 || g.min > g.max || len(g.addons) == 0 {
					t.Errorf("%s/%s group %s rule", c.slug, s.slug, g.key)
				}
				for _, a := range g.addons {
					if a.price <= 0 {
						t.Errorf("addon %s price", a.key)
					}
				}
			}
		}
	}
	for _, s := range launch {
		if !seen[s] {
			t.Errorf("launch category %s missing", s)
		}
	}
	if ID("service", "a") == ID("service", "b") || ID("service", "a") != ID("service", "a") {
		t.Fatal("ID must be deterministic and distinct")
	}
}

// B1: every service group the founder asked for is seeded, each appliance
// category with an inspection visit, staffing by the hour and the month.
func TestSeedHasEveryB1Group(t *testing.T) {
	bySlug := map[string]categorySeed{}
	for _, c := range categories {
		bySlug[c.slug] = c
	}
	for _, slug := range []string{"tv-repair", "refrigerator-repair", "washing-machine-repair", "microwave-repair", "geyser-repair",
		"chimney-hob-repair", "computer-repair", "mobile-repair"} {
		c, ok := bySlug[slug]
		if !ok || c.family != "APPLIANCE_REPAIR" || c.services[0].slug != "inspection-visit" {
			t.Errorf("%s: %+v", slug, ok)
		}
	}
	for slug, family := range map[string]string{"car-wash": "CAR_CARE", "disinfection": "PEST_CONTROL", "home-staffing": "HOME_STAFFING",
		"packers-movers": "RELOCATION", "photography": "PHOTOGRAPHY", "makeup-artist": "BEAUTY_SALON", "yoga-trainer": "FITNESS_WELLNESS",
		"construction": "CONSTRUCTION"} {
		if c, ok := bySlug[slug]; !ok || c.family != family {
			t.Errorf("%s family %s", slug, c.family)
		}
	}
	for _, s := range []string{"cook", "house-help", "nanny", "driver"} {
		if optionUnits["home-staffing/"+s+"/hourly"] != "per_hour" || optionUnits["home-staffing/"+s+"/monthly"] != "per_month" {
			t.Errorf("%s units", s)
		}
	}
	for key := range optionUnits {
		found := false
		for _, c := range categories {
			for _, s := range c.services {
				for _, o := range s.options {
					found = found || c.slug+"/"+s.slug+"/"+o.key == key
				}
			}
		}
		if !found {
			t.Errorf("unit for an unknown option %s", key)
		}
	}
}
