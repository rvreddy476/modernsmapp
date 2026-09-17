package database

import "testing"

func TestRequireTestDatabase(t *testing.T) {
	cases := []struct {
		dsn string
		ok  bool
	}{
		{"postgres://u:p@127.0.0.1:5432/rider_it_test?sslmode=disable", true},
		{"host=127.0.0.1 user=u password=p dbname=rider_it_test sslmode=disable", true},
		{"postgres://u:p@127.0.0.1:5432/app?sslmode=disable", false},
		{"postgres://u:p@127.0.0.1:5432/rider_test_old?sslmode=disable", false},
	}
	for _, c := range cases {
		err := RequireTestDatabase(c.dsn)
		if (err == nil) != c.ok {
			t.Errorf("RequireTestDatabase(%q) err=%v, want ok=%v", c.dsn, err, c.ok)
		}
	}
}
