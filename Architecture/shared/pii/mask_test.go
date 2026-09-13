package pii

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

// assertMaskSafe checks the masking contract for MaskTail(in, n) == out: empty in
// gives "", otherwise out is "****" plus a suffix of in that is no longer than n
// runes and no longer than half of in.
func assertMaskSafe(t *testing.T, in string, n int, out string) {
	t.Helper()
	if in == "" {
		if out != "" {
			t.Fatalf("MaskTail(%q, %d) = %q, want empty", in, n, out)
		}
		return
	}
	if !strings.HasPrefix(out, maskPrefix) {
		t.Fatalf("MaskTail(%q, %d) = %q, missing fixed prefix", in, n, out)
	}
	revealed := out[len(maskPrefix):]
	if !utf8.ValidString(in) && revealed != "" {
		t.Fatalf("MaskTail(invalid UTF-8, %d) revealed %q", n, revealed)
	}
	if !strings.HasSuffix(in, revealed) {
		t.Fatalf("MaskTail(%q, %d) revealed %q, not a suffix of the input", in, n, revealed)
	}
	rc := utf8.RuneCountInString(revealed)
	if rc > 0 && rc > n {
		t.Fatalf("MaskTail(%q, %d) revealed %d runes, more than n", in, n, rc)
	}
	if rc > utf8.RuneCountInString(in)/2 {
		t.Fatalf("MaskTail(%q, %d) revealed %d runes, more than half the input", in, n, rc)
	}
}

func TestMaskTail(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"empty", "", 4, ""},
		{"len1", "1", 4, "****"},
		{"len2", "12", 4, "****"},
		{"len3", "123", 4, "****"},
		{"len4", "1234", 4, "****"},
		{"len5", "12345", 4, "****"},
		{"len6", "123456", 4, "****"},
		{"len7", "1234567", 4, "****"},
		{"len8", "12345678", 4, "****5678"},
		{"account12", "123456789012", 4, "****9012"},
		{"len1_n1", "1", 1, "****"},
		{"len2_n1", "12", 1, "****2"},
		{"n0", "12345678", 0, "****"},
		{"n0_short", "abc", 0, "****"},
		{"n_neg1", "12345678", -1, "****"},
		{"n_minint", "12345678", math.MinInt, "****"},
		{"n100", "123456789012", 100, "****"},
		{"n_maxint", "123456789012", math.MaxInt, "****"},
		{"unicode", "αβγδεζηθ", 4, "****εζηθ"},
		{"unicode_short", "αβγ", 1, "****γ"},
		{"invalid_utf8", "\xff\xfe\xfd\xfc\xfb\xfa\xf9\xf8", 4, "****"},
		{"invalid_utf8_tail", "12345678\xff", 4, "****"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MaskTail(c.in, c.n)
			if got != c.want {
				t.Fatalf("MaskTail(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
			}
			assertMaskSafe(t, c.in, c.n, got)
		})
	}
}

func TestMaskAccountNumber(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "",
		"123":              "****",
		"1234 5678 9012":   "****9012",
		"1234-5678":        "****5678",
		" 0001234567890 ":  "****7890",
		"12345678\xff9012": "****",
	} {
		got := MaskAccountNumber(in)
		if got != want {
			t.Fatalf("MaskAccountNumber(%q) = %q, want %q", in, got, want)
		}
		stripped := strings.NewReplacer(" ", "", "-", "").Replace(in)
		assertMaskSafe(t, stripped, 4, got)
	}
}

func TestMaskPAN(t *testing.T) {
	for in, want := range map[string]string{
		"":             "",
		"ABCDE1234F":   "****234F",
		"abcde 1234 f": "****234F",
		"abcde-1234-f": "****234F",
		"ABC":          "****",
	} {
		got := MaskPAN(in)
		if got != want {
			t.Fatalf("MaskPAN(%q) = %q, want %q", in, got, want)
		}
		stripped, _ := CompactUpper(in)
		assertMaskSafe(t, stripped, 4, got)
	}
}

func TestMaskDoesNotRevealLength(t *testing.T) {
	short := MaskAccountNumber("123456789")
	long := MaskAccountNumber("123456789012345678")
	if len(short) != len(long) || short[:len(maskPrefix)] != long[:len(maskPrefix)] {
		t.Fatalf("mask shape depends on length: %q vs %q", short, long)
	}
	if short != "****6789" || long != "****5678" {
		t.Fatalf("unexpected masks %q, %q", short, long)
	}
	if a, b := MaskTail("12345678", 4), MaskTail(strings.Repeat("9", 40)+"5678", 4); len(a) != len(b) {
		t.Fatalf("MaskTail shape depends on length: %q vs %q", a, b)
	}
}

func FuzzMaskTail(f *testing.F) {
	for _, seed := range []struct {
		s string
		n int
	}{
		{"", 0}, {"", 4}, {"1", 4}, {"1234567", 4}, {"12345678", 4}, {"12345", 4},
		{"12345678", 0}, {"12345678", -1}, {"12345678", math.MinInt},
		{"123456789012", 100}, {"123", math.MaxInt}, {"12", 1},
		{"\xff\xfe", 1}, {"αβγδ", 2}, {"****1234", 4},
	} {
		f.Add(seed.s, seed.n)
	}
	f.Fuzz(func(t *testing.T, s string, n int) {
		assertMaskSafe(t, s, n, MaskTail(s, n))
	})
}
