package store

import "testing"

func TestMaskIdentifier(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "XXXX"},
		{"abc", "XXXX"},
		{"1234", "XXXX"},
		{"9876543210", "XXXXXX3210"},
		{"1234567890123", "XXXXXXXXX0123"},
	}
	for _, tc := range cases {
		if got := MaskIdentifier(tc.in); got != tc.want {
			t.Errorf("MaskIdentifier(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
