package processing

import "testing"

func TestVideoOriginalCapFromEnvironment(t *testing.T) {
	cases := map[string]int64{
		"":           MaxVideoOriginalBytes,
		"6442450944": 6 << 30, // QA / staging (20 Gi scratch)
		" 1024 ":     1024,
		"0":          0, // no cap: the worker's existing meaning
		"-1":         MaxVideoOriginalBytes,
		"nonsense":   MaxVideoOriginalBytes,
	}
	for v, want := range cases {
		got := VideoOriginalCap(func(k string) string {
			if k == VideoOriginalCapEnv {
				return v
			}
			return ""
		})
		if got != want {
			t.Fatalf("%s=%q: cap %d, want %d", VideoOriginalCapEnv, v, got, want)
		}
	}
}
