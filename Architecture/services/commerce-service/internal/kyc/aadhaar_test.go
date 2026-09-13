package kyc

import (
	"math/rand"
	"testing"
)

// The expected answers here are computed from FIRST PRINCIPLES, not from a
// second copy of the tables: the Verhoeff position permutation table is, by
// definition, successive powers of its first row (row i = p^i), and p^8 is the
// identity. A memorised table was wrong once — row 4 read 7,6,8 where p^4 is
// 6,8,7 — and a test that copied the same table agreed with it. That table
// still passed the textbook examples; it silently missed about one adjacent
// transposition in eight, the very error Verhoeff exists to catch.
var testP1 = [10]int{1, 5, 7, 6, 2, 8, 3, 0, 9, 4}

func derivedPermutation() [8][10]int {
	var p [8][10]int
	for j := 0; j < 10; j++ {
		p[0][j] = j
	}
	for i := 1; i < 8; i++ {
		for j := 0; j < 10; j++ {
			p[i][j] = testP1[p[i-1][j]]
		}
	}
	return p
}

// D5 multiplication, as published; its group properties are asserted below
// rather than trusted.
var testD = [10][10]int{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
	{1, 2, 3, 4, 0, 6, 7, 8, 9, 5},
	{2, 3, 4, 0, 1, 7, 8, 9, 5, 6},
	{3, 4, 0, 1, 2, 8, 9, 5, 6, 7},
	{4, 0, 1, 2, 3, 9, 5, 6, 7, 8},
	{5, 9, 8, 7, 6, 0, 4, 3, 2, 1},
	{6, 5, 9, 8, 7, 1, 0, 4, 3, 2},
	{7, 6, 5, 9, 8, 2, 1, 0, 4, 3},
	{8, 7, 6, 5, 9, 3, 2, 1, 0, 4},
	{9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
}

var testInv = [10]int{0, 4, 3, 2, 1, 5, 6, 7, 8, 9}

// withCheckDigit appends the Verhoeff check digit to a payload.
func withCheckDigit(payload string) string {
	p := derivedPermutation()
	c := 0
	for i := 0; i < len(payload); i++ {
		d := int(payload[len(payload)-1-i] - '0')
		c = testD[c][p[(i+1)%8][d]]
	}
	return payload + string(rune('0'+testInv[c]))
}

// wrongCheckDigit returns the same payload with a check digit that fails.
func wrongCheckDigit(payload string) string {
	good := withCheckDigit(payload)
	last := good[len(good)-1]
	bad := byte('0' + (int(last-'0')+1)%10)
	return good[:len(good)-1] + string(bad)
}

func TestTheVerhoeffPermutationTableIsPowersOfItsFirstRow(t *testing.T) {
	want := derivedPermutation()
	for i := 0; i < 8; i++ {
		for j := 0; j < 10; j++ {
			if int(verhoeffP[i][j]) != want[i][j] {
				t.Fatalf("verhoeffP row %d = %v, want p^%d = %v", i, verhoeffP[i], i, want[i])
			}
		}
	}
	// p^8 is the identity, or the derivation above is not the Verhoeff p.
	for j := 0; j < 10; j++ {
		if testP1[want[7][j]] != j {
			t.Fatal("p^8 is not the identity; the first row is not the Verhoeff permutation")
		}
	}
}

func TestTheVerhoeffMultiplicationTableIsAGroup(t *testing.T) {
	for a := 0; a < 10; a++ {
		if int(verhoeffD[0][a]) != a || int(verhoeffD[a][0]) != a {
			t.Fatalf("0 is not the identity at %d", a)
		}
		if verhoeffD[a][testInv[a]] != 0 {
			t.Fatalf("inverse of %d is not %d", a, testInv[a])
		}
		for b := 0; b < 10; b++ {
			if int(verhoeffD[a][b]) != testD[a][b] {
				t.Fatalf("verhoeffD[%d][%d] = %d, want %d", a, b, verhoeffD[a][b], testD[a][b])
			}
			for c := 0; c < 10; c++ {
				if verhoeffD[verhoeffD[a][b]][c] != verhoeffD[a][verhoeffD[b][c]] {
					t.Fatalf("not associative at (%d,%d,%d)", a, b, c)
				}
			}
		}
	}
}

// What Verhoeff guarantees: every single-digit substitution and every adjacent
// transposition is detected. A wrong table keeps the first and loses the second.
func TestVerhoeffDetectsEverySubstitutionAndAdjacentTransposition(t *testing.T) {
	rng := rand.New(rand.NewSource(35))
	digits := func(s string) []byte {
		out := make([]byte, len(s))
		for i := range s {
			out[i] = s[i] - '0'
		}
		return out
	}
	for n := 0; n < 5000; n++ {
		payload := make([]byte, 11)
		for i := range payload {
			payload[i] = byte('0' + rng.Intn(10))
		}
		full := digits(withCheckDigit(string(payload)))
		if !verhoeffValid(full) {
			t.Fatalf("a correctly generated number %v was judged invalid", full)
		}
		for k := 0; k+1 < len(full); k++ {
			if full[k] == full[k+1] {
				continue
			}
			swapped := append([]byte{}, full...)
			swapped[k], swapped[k+1] = swapped[k+1], swapped[k]
			if verhoeffValid(swapped) {
				t.Fatalf("transposing positions %d,%d of %v went undetected", k, k+1, full)
			}
		}
		for k := range full {
			sub := append([]byte{}, full...)
			sub[k] = (sub[k] + 1 + byte(rng.Intn(9))) % 10
			if verhoeffValid(sub) {
				t.Fatalf("substituting position %d of %v went undetected", k, full)
			}
		}
	}
}

// aadhaarVectors pins the Go check. The SQL twin in migration 035 is held to
// the Go function by the store integration suite, which compares the two over a
// large generated set.
func aadhaarVectors() map[string]bool {
	valid := withCheckDigit("23456789012")
	return map[string]bool{
		valid:                         true,
		withCheckDigit("98765432109"): true,
		valid[:4] + " " + valid[4:8] + " " + valid[8:]: true, // printed on the card
		valid[:4] + "-" + valid[4:8] + "-" + valid[8:]: true, // typed with hyphens
		wrongCheckDigit("23456789012"):                 false,
		withCheckDigit("13456789012"):                  false, // leading 1: never issued
		withCheckDigit("03456789012"):                  false, // leading 0: never issued
		valid[:11]:                                     false, // 11 digits
		valid + "7":                                    false, // 13 digits
		"ABCDE1234F":                                   false, // a PAN
		valid[:6] + "X" + valid[7:]:                    false, // a letter
		"":                                             false,
	}
}

func TestLooksLikeAadhaar(t *testing.T) {
	for in, want := range aadhaarVectors() {
		if got := LooksLikeAadhaar(in); got != want {
			t.Errorf("LooksLikeAadhaar(%q) = %t, want %t", in, got, want)
		}
	}
}

// The textbook Verhoeff examples.
func TestVerhoeffKnownAnswers(t *testing.T) {
	digits := func(s string) []byte {
		out := make([]byte, len(s))
		for i := range s {
			out[i] = s[i] - '0'
		}
		return out
	}
	for in, want := range map[string]bool{
		"2363":   true,
		"2364":   false,
		"123451": true,
		"123452": false,
	} {
		if got := verhoeffValid(digits(in)); got != want {
			t.Errorf("verhoeffValid(%s) = %t, want %t", in, got, want)
		}
	}
	if got := withCheckDigit("236"); got != "2363" {
		t.Fatalf("the test's own generator is wrong: 236 -> %s, want 2363", got)
	}
}
