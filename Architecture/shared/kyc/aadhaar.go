package kyc

import "errors"

// ErrAadhaarNotAllowed is returned when text appears to contain an Aadhaar
// number. The platform must not collect or store Aadhaar numbers, so a
// free-text field that carries one is refused rather than masked.
//
// The error text contains no digits, and nothing in this file returns,
// logs or wraps the number it found.
var ErrAadhaarNotAllowed = errors.New("AADHAAR_NOT_ALLOWED")

const (
	aadhaarDigits     = 12
	aadhaarGroupedLen = 14 // 4 digits, separator, 4 digits, separator, 4 digits
)

// LooksLikeAadhaar reports whether text contains a token shaped like an
// Aadhaar number: twelve ASCII digits, either contiguous or grouped 4-4-4
// with a single space or hyphen between groups, whose first digit is 2-9 and
// whose Verhoeff checksum holds.
//
// The token must be bounded: no digit immediately before or after it, so a
// 14-digit FSSAI licence or a 13-digit run is not read as containing one. A
// grouped token must also not be one group of a longer grouped run, so a
// 16-digit card number written 4-4-4-4 is not flagged.
//
// A random 12-digit number passes the checksum one time in ten, so a match
// is "looks like", not "is".
func LooksLikeAadhaar(text string) bool {
	for i := 0; i+aadhaarDigits <= len(text); i++ {
		tok := text[i : i+aadhaarDigits]
		if !allDigits(tok) {
			continue
		}
		if !digitBounded(text, i, i+aadhaarDigits) {
			continue
		}
		if aadhaarCandidate(tok) {
			return true
		}
	}
	for i := 0; i+aadhaarGroupedLen <= len(text); i++ {
		t := text[i : i+aadhaarGroupedLen]
		if !allDigits(t[0:4]) || !isGroupSeparator(t[4]) || !allDigits(t[5:9]) ||
			!isGroupSeparator(t[9]) || !allDigits(t[10:14]) {
			continue
		}
		if !digitBounded(text, i, i+aadhaarGroupedLen) {
			continue
		}
		if !notInLongerGroupedRun(text, i, i+aadhaarGroupedLen) {
			continue
		}
		if aadhaarCandidate(t[0:4] + t[5:9] + t[10:14]) {
			return true
		}
	}
	return false
}

// RefuseAadhaar returns ErrAadhaarNotAllowed when text looks like it
// contains an Aadhaar number, and nil otherwise.
func RefuseAadhaar(text string) error {
	if LooksLikeAadhaar(text) {
		return ErrAadhaarNotAllowed
	}
	return nil
}

func aadhaarCandidate(digits string) bool {
	if digits[0] == '0' || digits[0] == '1' {
		return false
	}
	return verhoeffValid(digits)
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func isGroupSeparator(c byte) bool { return c == ' ' || c == '-' }

// digitBounded reports whether s[start:end] has no digit directly on either
// side.
func digitBounded(s string, start, end int) bool {
	if start > 0 && isDigit(s[start-1]) {
		return false
	}
	if end < len(s) && isDigit(s[end]) {
		return false
	}
	return true
}

// notInLongerGroupedRun reports whether s[start:end] is not preceded by
// "digit separator" or followed by "separator digit".
func notInLongerGroupedRun(s string, start, end int) bool {
	if start >= 2 && isGroupSeparator(s[start-1]) && isDigit(s[start-2]) {
		return false
	}
	if end+1 < len(s) && isGroupSeparator(s[end]) && isDigit(s[end+1]) {
		return false
	}
	return true
}

// Verhoeff tables: the dihedral group D5 multiplication table, the position
// permutation table, and the inverse table.
//
// Row i of verhoeffPerm is p1 applied i times, where p1 is row 1. Do NOT
// retype these rows from memory: the commonly memorised row 4 is wrong. An
// earlier version of this file had row 4 as `9 4 5 3 1 2 8 7 6 0`; the
// derived row is `9 4 5 3 1 2 6 8 7 0`. The wrong row still passes every
// textbook example (236 -> 3, 12345 -> 1) and every single-digit
// substitution, but misses some adjacent transpositions — so a raw Aadhaar
// number typed with two neighbouring digits swapped could be judged "not
// Aadhaar" and stored. aadhaar_test.go derives both tables from first
// principles and asserts every entry, and checks that every adjacent
// transposition is detected; change a row and those tests fail.
var verhoeffMul = [10][10]uint8{
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

var verhoeffPerm = [8][10]uint8{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
	{1, 5, 7, 6, 2, 8, 3, 0, 9, 4},
	{5, 8, 0, 3, 7, 9, 6, 1, 4, 2},
	{8, 9, 1, 6, 0, 4, 3, 5, 2, 7},
	{9, 4, 5, 3, 1, 2, 6, 8, 7, 0},
	{4, 2, 8, 6, 5, 7, 3, 9, 0, 1},
	{2, 7, 9, 3, 8, 0, 6, 4, 1, 5},
	{7, 0, 4, 6, 9, 1, 3, 2, 5, 8},
}

// verhoeffValid reports whether an all-digit string, check digit last,
// satisfies the Verhoeff checksum.
func verhoeffValid(digits string) bool {
	var c uint8
	for i := 0; i < len(digits); i++ {
		d := digits[len(digits)-1-i] - '0'
		c = verhoeffMul[c][verhoeffPerm[i%8][d]]
	}
	return c == 0
}
