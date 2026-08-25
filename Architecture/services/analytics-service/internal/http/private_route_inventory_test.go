package http

import (
	"os"
	"strings"
	"testing"
)

func TestPrivateAnalyticsRouteInventory(t *testing.T) {
	source, err := os.ReadFile("dashboard.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, handler := range []string{
		"GetContentDetail", "GetContentTrend", "GetContentRetention", "GetContentDemographics",
	} {
		start := strings.Index(text, "func (h *DashboardHandler) "+handler)
		if start < 0 {
			t.Fatalf("missing private handler %s", handler)
		}
		body := text[start:]
		if next := strings.Index(body[1:], "\nfunc "); next >= 0 {
			body = body[:next+1]
		}
		if !strings.Contains(body, "requireOwnedContent(c)") {
			t.Fatalf("%s bypasses owner policy", handler)
		}
	}
	handlerSource, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(handlerSource), `v1.GET("/creator/:userId"`) {
		t.Fatal("public creator analytics route was reintroduced")
	}
}
