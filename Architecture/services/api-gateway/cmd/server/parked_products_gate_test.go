package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Parked products (security sweep #13 follow-up, 24 Oct 2026): Q&A, business
// pages, memories and Feast each have their own edge gate, closed unless the
// product's flag says "true". Broadcast channels have none: the web
// messenger's Channels lane and Android chat's Communities use them.

var parkedProductFlags = map[string][]string{
	"QA_PUBLIC_ENABLED":       {"/v1/qa", "/v1/qa/questions", "/v1/qa/questions/abc/answers"},
	"PAGES_PUBLIC_ENABLED":    {"/v1/pages", "/v1/pages/abc", "/v1/pages/abc/reviews"},
	"MEMORIES_PUBLIC_ENABLED": {"/v1/memories", "/v1/memories/today"},
	"FOOD_PUBLIC_ENABLED":     {"/v1/food", "/v1/food/restaurants", "/v1/food/kitchen/orders", "/v1/food/rider/offers"},
}

func TestParkedProductsAreClosedByDefault(t *testing.T) {
	products := productsFrom(t, nil)
	for flag, paths := range parkedProductFlags {
		for _, path := range paths {
			if !gated(t, products, path) {
				t.Errorf("%s is open with %s unset", path, flag)
			}
		}
	}
	// Anything but the literal "true" keeps it closed.
	for _, v := range []string{"", "false", "1", "yes", "on"} {
		products := productsFrom(t, map[string]string{"QA_PUBLIC_ENABLED": v})
		if !gated(t, products, "/v1/qa") {
			t.Errorf("QA_PUBLIC_ENABLED=%q opened /v1/qa", v)
		}
	}
}

func TestParkedProductsOpenOnTheirOwnFlagOnly(t *testing.T) {
	for flag, paths := range parkedProductFlags {
		products := productsFrom(t, map[string]string{flag: "true"})
		for _, path := range paths {
			if gated(t, products, path) {
				t.Errorf("%s=true still gates %s", flag, path)
			}
		}
		for other, otherPaths := range parkedProductFlags {
			if other == flag {
				continue
			}
			for _, path := range otherPaths {
				if !gated(t, products, path) {
					t.Errorf("%s=true opened %s", flag, path)
				}
			}
		}
		// No parked flag opens a dormant product.
		for _, path := range []string{"/v1/groups", "/v1/communities", "/v1/rider", "/v1/dating", "/v1/doorstep"} {
			if !gated(t, products, path) {
				t.Errorf("%s=true opened %s", flag, path)
			}
		}
	}
}

func TestParkedGatesLeaveBroadcastChannelsAndNeighboursAlone(t *testing.T) {
	products := productsFrom(t, nil)
	for _, path := range []string{
		"/v1/broadcast-channels", "/v1/broadcast-channels/my", "/v1/broadcast-channels/abc/updates",
		"/v1/qax", "/v1/pagesx", "/v1/memoriesx", "/v1/foodx", "/v1/posts", "/v1/chat/conversations",
	} {
		if gated(t, products, path) {
			t.Errorf("%s was gated", path)
		}
	}
}

// Dev compose opens the parked products (the services stay testable on the
// devbox); the production compose closes them.
func TestComposeParkedProductFlags(t *testing.T) {
	for file, want := range map[string]string{
		"../../../../docker/docker-compose.yml":            "${%s:-true}",
		"../../../../docker/docker-compose.production.yml": `"false"`,
	} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(raw)
		for flag := range parkedProductFlags {
			value := want
			if strings.Contains(want, "%s") {
				value = fmt.Sprintf(want, flag)
			}
			if !strings.Contains(text, "      "+flag+": "+value+"\n") && !strings.Contains(text, "      "+flag+": "+value+"\r\n") {
				t.Errorf("%s does not set %s: %s", file, flag, value)
			}
		}
	}
}
