package postgres

import "testing"

// TestRequireTestDatabase pins the guard that keeps the integration suite off
// live databases. It parses DSNs offline, so it runs without TEST_PG_DSN.
func TestRequireTestDatabase(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		ok   bool
	}{
		{name: "url form _test", dsn: "postgres://u:p@127.0.0.1:5432/food_it_test?sslmode=disable", ok: true},
		{name: "keyword form _test", dsn: "host=127.0.0.1 user=u password=p dbname=food_it_test sslmode=disable", ok: true},
		{name: "app database refused", dsn: "postgres://u:p@127.0.0.1:5432/app?sslmode=disable", ok: false},
		{name: "test prefix is not enough", dsn: "postgres://u:p@127.0.0.1:5432/test_app?sslmode=disable", ok: false},
		{name: "_test must be the suffix", dsn: "postgres://u:p@127.0.0.1:5432/food_test_live?sslmode=disable", ok: false},
		{name: "no database name refused", dsn: "host=127.0.0.1 user=u password=p sslmode=disable", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := requireTestDatabase(tc.dsn)
			if tc.ok && err != nil {
				t.Fatalf("want accepted, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want refused, got nil")
			}
		})
	}
}
