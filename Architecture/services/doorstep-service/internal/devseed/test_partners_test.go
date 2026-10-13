package devseed

import "testing"

func TestPartnerSandboxGuard(t *testing.T) {
	dev := func(k string) string {
		if k == "ENV" {
			return "development"
		}
		if k == "DOORSTEP_TEST_FIXTURES" {
			return "true"
		}
		return ""
	}
	for _, dsn := range []string{"postgres://u:p@db/app?sslmode=disable", "postgres://u:p@127.0.0.1/app?sslmode=disable", "postgres://u:p@public.example/doorstep_partner_demo_test?sslmode=disable"} {
		if RequirePartnerSandbox(dsn, dev) == nil {
			t.Fatalf("accepted unsafe sandbox %s", dsn)
		}
	}
	dsn := "postgres://u:p@127.0.0.1/doorstep_partner_demo_test?sslmode=disable"
	if err := RequirePartnerSandbox(dsn, dev); err != nil {
		t.Fatal(err)
	}
	if RequirePartnerSandbox(dsn, func(string) string { return "" }) == nil {
		t.Fatal("implicit mode accepted")
	}
	if RequirePartnerSandbox(dsn, func(k string) string {
		if k == "APP_ENV" {
			return "production"
		}
		return dev(k)
	}) == nil {
		t.Fatal("production override accepted")
	}
}
