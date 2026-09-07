package aggregation

import "testing"

// The cap is read once, at construction, from the environment. These
// pin the three answers that matter: the default, an explicit override,
// and the "no cap" escape hatch that has to reproduce today's numbers.
func TestViewCapFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  bool
		raw  string
		want int
	}{
		{name: "unset defaults to five", want: defaultViewCapPerViewerPerDay},
		{name: "empty defaults to five", set: true, raw: "  ", want: defaultViewCapPerViewerPerDay},
		{name: "explicit value is honoured", set: true, raw: "3", want: 3},
		{name: "zero disables the cap", set: true, raw: "0", want: 0},
		{name: "negative disables the cap", set: true, raw: "-1", want: -1},
		{name: "garbage falls back to the default", set: true, raw: "many", want: defaultViewCapPerViewerPerDay},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(viewCapEnv, tc.raw)
			}
			if got := viewCapFromEnv(); got != tc.want {
				t.Fatalf("viewCapFromEnv() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestWithViewCapOverridesTheEnvironment(t *testing.T) {
	t.Setenv(viewCapEnv, "9")
	rollup := (&DailyRollup{viewCapPerViewerPerDay: viewCapFromEnv()}).WithViewCap(2)
	if rollup.ViewCap() != 2 {
		t.Fatalf("ViewCap() = %d, want 2", rollup.ViewCap())
	}
}
