package service

import "testing"

func TestNormalisePhone(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"9876543210", "9876543210"},
		{"+919876543210", "9876543210"},
		{"09876543210", "9876543210"},
		{"00919876543210", "9876543210"},
		{"+91 98765 43210", "9876543210"},
		{"+91-98765-43210", "9876543210"},
	}
	for _, tc := range cases {
		if got := normalisePhone(tc.in); got != tc.want {
			t.Errorf("normalisePhone(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIndianPhoneRE(t *testing.T) {
	good := []string{
		"9876543210", "+919876543210", "09876543210", "8123456789",
	}
	bad := []string{
		"", "12345", "5876543210", "98765432101", "abcdefghij",
	}
	for _, g := range good {
		if !indianPhoneRE.MatchString(g) {
			t.Errorf("expected %q to match", g)
		}
	}
	for _, b := range bad {
		if indianPhoneRE.MatchString(b) {
			t.Errorf("expected %q to NOT match", b)
		}
	}
}

func TestTitleCase(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"pulse":    "Pulse",
		"FOOD":     "FOOD",
		"commerce": "Commerce",
		"x":        "X",
	}
	for in, want := range cases {
		if got := titleCase(in); got != want {
			t.Errorf("titleCase(%q) = %q, want %q", in, got, want)
		}
	}
}
