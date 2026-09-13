package kyc

import (
	"errors"
	"strings"
	"testing"
)

// refD5Mul multiplies two elements of the dihedral group D5 from the group
// rules (0-4 rotations, 5-9 reflections) rather than from a lookup table, so
// it is independent of the production Verhoeff table.
func refD5Mul(a, b int) int {
	switch {
	case a < 5 && b < 5:
		return (a + b) % 5
	case a < 5:
		return 5 + (a+(b-5))%5
	case b < 5:
		return 5 + ((a-5)-b+5)%5
	default:
		return ((a - 5) - (b - 5) + 5) % 5
	}
}

// refPerm applies the Verhoeff position permutation k times, derived from
// its cycle structure (0 1 5 8 9 4 2 7)(3 6) rather than a table.
func refPerm(k, x int) int {
	sigma := [10]int{1, 5, 7, 6, 2, 8, 3, 0, 9, 4}
	for ; k > 0; k-- {
		x = sigma[x]
	}
	return x
}

// refVerhoeffCheck returns the check digit for a digit string.
func refVerhoeffCheck(t *testing.T, base string) byte {
	t.Helper()
	c := 0
	for i := 0; i < len(base); i++ {
		d := int(base[len(base)-1-i] - '0')
		if d < 0 || d > 9 {
			t.Fatalf("refVerhoeffCheck: non-digit")
		}
		c = refD5Mul(c, refPerm((i+1)%8, d))
	}
	for inv := 0; inv < 10; inv++ {
		if refD5Mul(c, inv) == 0 {
			return byte('0' + inv)
		}
	}
	t.Fatal("refVerhoeffCheck: no inverse")
	return 0
}

// synthAadhaarShaped builds a checksum-valid 12-digit number from an
// 11-digit synthetic base. The bases are repetitive patterns chosen for tests.
func synthAadhaarShaped(t *testing.T, base11 string) string {
	t.Helper()
	if len(base11) != 11 {
		t.Fatalf("base must be 11 digits")
	}
	return base11 + string(refVerhoeffCheck(t, base11))
}

func group444(s string, sep string) string { return s[0:4] + sep + s[4:8] + sep + s[8:12] }

// The production tables must equal their derivation, entry for entry.
//
// Known-answer vectors cannot catch a wrong permutation row: the memorised
// row 4 (`9 4 5 3 1 2 8 7 6 0`, derived value `9 4 5 3 1 2 6 8 7 0`) still
// produces the right check digit for 236 and 12345. This test compares every
// entry of both tables against the group rules and the powers of p1.
func TestVerhoeffTablesMatchDerivation(t *testing.T) {
	for a := 0; a < 10; a++ {
		for b := 0; b < 10; b++ {
			if got, want := int(verhoeffMul[a][b]), refD5Mul(a, b); got != want {
				t.Errorf("verhoeffMul[%d][%d] = %d, derived %d", a, b, got, want)
			}
		}
	}
	for k := 0; k < 8; k++ {
		for x := 0; x < 10; x++ {
			if got, want := int(verhoeffPerm[k][x]), refPerm(k, x); got != want {
				t.Errorf("verhoeffPerm[%d][%d] = %d, derived %d", k, x, got, want)
			}
		}
	}
	// p1 has order 8, so the position cycle really does repeat every 8.
	for x := 0; x < 10; x++ {
		if refPerm(8, x) != x {
			t.Fatalf("p1^8 is not the identity at %d; the cycle length assumption is wrong", x)
		}
	}
}

// Verhoeff detects every adjacent transposition of two different digits.
// This is the property a wrong permutation row silently loses.
func TestVerhoeffDetectsEveryAdjacentTransposition(t *testing.T) {
	// Deterministic generator: no math/rand seed to drift across Go versions.
	state := uint64(0x9E3779B97F4A7C15)
	next := func() int {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return int(state % 10)
	}
	for n := 0; n < 5000; n++ {
		base := make([]byte, 11)
		base[0] = byte('2' + next()%8) // Aadhaar-shaped: never starts 0 or 1
		for i := 1; i < 11; i++ {
			base[i] = byte('0' + next())
		}
		valid := string(base) + string(refVerhoeffCheck(t, string(base)))
		if !verhoeffValid(valid) {
			t.Fatalf("a reference-valid number fails verhoeffValid: case %d", n)
		}
		for i := 0; i+1 < len(valid); i++ {
			if valid[i] == valid[i+1] {
				continue
			}
			swapped := []byte(valid)
			swapped[i], swapped[i+1] = swapped[i+1], swapped[i]
			if verhoeffValid(string(swapped)) {
				t.Fatalf("adjacent transposition at position %d not detected (case %d)", i, n)
			}
		}
	}
}

func TestRefVerhoeffAnchor(t *testing.T) {
	// The published worked example: 236 carries check digit 3.
	if got := refVerhoeffCheck(t, "236"); got != '3' {
		t.Fatalf("reference Verhoeff gives %c for 236, want 3", got)
	}
}

func TestLooksLikeAadhaar_Detects(t *testing.T) {
	for _, base := range []string{"99999999999", "22222222222", "50000000000", "80808080808"} {
		a := synthAadhaarShaped(t, base)
		for _, text := range []string{
			a,
			group444(a, " "),
			group444(a, "-"),
			a[0:4] + " " + a[4:8] + "-" + a[8:12],
			"my number is " + a + ", thanks",
			"id:" + group444(a, " ") + ".",
			"(" + group444(a, "-") + ")",
		} {
			if !LooksLikeAadhaar(text) {
				t.Errorf("not detected: base %s form %d", base, len(text))
			}
			if err := RefuseAadhaar(text); !errors.Is(err, ErrAadhaarNotAllowed) {
				t.Errorf("RefuseAadhaar err = %v", err)
			} else if strings.ContainsAny(err.Error(), "0123456789") {
				t.Errorf("error text contains digits: %q", err.Error())
			}
		}
	}
}

func TestLooksLikeAadhaar_ChecksumOffIsNotDetected(t *testing.T) {
	a := synthAadhaarShaped(t, "99999999999")
	for d := byte('0'); d <= '9'; d++ {
		if d == a[11] {
			continue
		}
		bad := a[:11] + string(d)
		for _, text := range []string{bad, group444(bad, " "), "x " + bad + " y"} {
			if LooksLikeAadhaar(text) {
				t.Errorf("checksum-invalid number detected (last digit %c)", d)
			}
		}
	}
}

func TestLooksLikeAadhaar_FirstDigitZeroOrOneIsNotDetected(t *testing.T) {
	for _, base := range []string{"09999999999", "19999999999", "00000000001", "12121212121"} {
		a := synthAadhaarShaped(t, base)
		for _, text := range []string{a, group444(a, " "), group444(a, "-")} {
			if LooksLikeAadhaar(text) {
				t.Errorf("number starting %c detected", a[0])
			}
		}
	}
}

func TestLooksLikeAadhaar_DigitBounded(t *testing.T) {
	a := synthAadhaarShaped(t, "99999999999")
	b := synthAadhaarShaped(t, "22222222222")
	for name, text := range map[string]string{
		"11 digits":                a[:11],
		"13 digits, leading":       "7" + a,
		"13 digits, trailing":      a + "5",
		"14 digits (FSSAI shape)":  "7" + a + "5",
		"14 digits b":              "3" + b + "8",
		"grouped, digit before":    "7" + group444(a, " "),
		"grouped, digit after":     group444(a, "-") + "5",
		"16 digits grouped after":  group444(a, " ") + " 4000",
		"16 digits grouped before": "4000 " + group444(b, " "),
		"16 digits hyphen":         "4000-" + group444(b, "-"),
		"double space":             a[0:4] + "  " + a[4:8] + " " + a[8:12],
		"dot separated":            a[0:4] + "." + a[4:8] + "." + a[8:12],
	} {
		if LooksLikeAadhaar(text) {
			t.Errorf("%s: detected", name)
		}
		if err := RefuseAadhaar(text); err != nil {
			t.Errorf("%s: RefuseAadhaar = %v", name, err)
		}
	}
	// A valid FSSAI licence is 14 digits and must never be refused as Aadhaar.
	fssai := "7" + a + "5"
	if _, err := ValidateFSSAILicence(fssai); err != nil {
		t.Fatalf("setup: %v", err)
	}
}

func TestRefuseAadhaar_CleanText(t *testing.T) {
	for _, text := range []string{"", "no numbers here", "call 9999999999", "order 12345"} {
		if err := RefuseAadhaar(text); err != nil {
			t.Errorf("RefuseAadhaar(%q) = %v", text, err)
		}
	}
}
