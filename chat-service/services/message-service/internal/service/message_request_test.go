package service

import "testing"

func TestContainsLink(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"hi, would love to connect", false},
		{"check https://spam.example for deals", true},
		{"go to http://x.io", true},
		{"visit www.something.net", true},
		{"my site is coolstuff.com really", true},
		{"meeting at 3pm tomorrow", false},
		{"e.g. that is fine", false}, // "e.g." must not trip the domain matcher
	}
	for _, tc := range cases {
		if got := containsLink(tc.text); got != tc.want {
			t.Errorf("containsLink(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
