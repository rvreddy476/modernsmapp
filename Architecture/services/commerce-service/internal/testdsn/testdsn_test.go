package testdsn

import "testing"

func TestCheckRefusesLiveAndNonTestNames(t *testing.T) {
	cases := []struct {
		dsn    string
		refuse bool
	}{
		{"", false},
		{"postgres://u:p@127.0.0.1:5432/commerce_it_test?sslmode=disable", false},
		{"postgres://u:p@127.0.0.1:5432/commerce_contract_ab12cd34_test", false},
		{"host=127.0.0.1 dbname=commerce_it_test user=u", false},
		{"postgres://u:p@127.0.0.1:5432/commerce_db?sslmode=disable", true},
		{"postgres://u:p@127.0.0.1:5432/commerce_db", true},
		{"postgres://u:p@127.0.0.1:5432/app", true},
		{"postgres://u:p@127.0.0.1:5432/identity_db", true},
		// The denylist's blind spot: a name nobody listed, and not disposable.
		{"postgres://u:p@127.0.0.1:5432/commerce_p0?sslmode=disable", true},
		{"postgres://u:p@127.0.0.1:5432/commerce_staging", true},
		{"host=127.0.0.1 dbname=commerce_db", true},
		// `_test` in the middle is not a suffix.
		{"postgres://u:p@127.0.0.1:5432/commerce_test_live", true},
	}
	for _, c := range cases {
		if got := Check(c.dsn) != ""; got != c.refuse {
			t.Errorf("Check(%q) refused=%v, want %v", c.dsn, got, c.refuse)
		}
	}
}

func TestSwapDatabaseKeepsQuery(t *testing.T) {
	got := SwapDatabase("postgres://u:p@h:5432/commerce_it_test?sslmode=disable", "x_test")
	if got != "postgres://u:p@h:5432/x_test?sslmode=disable" {
		t.Fatalf("got %q", got)
	}
}
