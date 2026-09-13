package kyc

import "strings"

// LooksLikeAadhaar reports whether s is shaped like a real Aadhaar number:
// twelve digits (spaces and hyphens ignored), a first digit from 2 to 9, and a
// valid Verhoeff check digit — the three properties UIDAI assigns every number.
//
// Commerce must never STORE an Aadhaar number. A seller KYC document of type
// `aadhaar` is kept as an uploaded media reference only, and any free-text
// document number that passes this check is refused, because a seller typing
// their Aadhaar into the "other document" box is the realistic way one ends
// up in the table.
//
// False positives are expected and acceptable: roughly one random 12-digit
// string in ten carries a valid Verhoeff digit. That is why the one document
// type whose number is legitimately a long digit string — a cancelled cheque's
// bank account — is exempted by the caller rather than here.
//
// This lives in commerce until the shared `shared/kyc` package grows the same
// function; when it does, switch to it and delete this file. The SQL twin in
// migration 035 (`commerce_looks_like_aadhaar`) must stay in step, and the
// integration suite checks the two agree on the same vectors.
func LooksLikeAadhaar(s string) bool {
	digits := make([]byte, 0, 12)
	for _, r := range s {
		switch {
		case r == ' ' || r == '-':
			continue
		case r >= '0' && r <= '9':
			digits = append(digits, byte(r-'0'))
		default:
			return false
		}
	}
	if len(digits) != 12 {
		return false
	}
	if digits[0] < 2 {
		return false
	}
	return verhoeffValid(digits)
}

// NormalizeDocumentNumber trims a document number for the checks above. It
// deliberately does not upper-case or strip anything else: the value is only
// inspected, and whatever is stored is what the seller sent.
func NormalizeDocumentNumber(s string) string {
	return strings.TrimSpace(s)
}

// Verhoeff tables: the dihedral group D5 multiplication table, the position
// permutation, and (in the tests) the inverse used to compute a check digit.
var (
	verhoeffD = [10][10]byte{
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
	verhoeffP = [8][10]byte{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		{1, 5, 7, 6, 2, 8, 3, 0, 9, 4},
		{5, 8, 0, 3, 7, 9, 6, 1, 4, 2},
		{8, 9, 1, 6, 0, 4, 3, 5, 2, 7},
		{9, 4, 5, 3, 1, 2, 6, 8, 7, 0}, // p^4; see TestTheVerhoeffPermutationTableIsPowersOfItsFirstRow
		{4, 2, 8, 6, 5, 7, 3, 9, 0, 1},
		{2, 7, 9, 3, 8, 0, 6, 4, 1, 5},
		{7, 0, 4, 6, 9, 1, 3, 2, 5, 8},
	}
)

// verhoeffValid reports whether digits (check digit last) satisfy Verhoeff.
func verhoeffValid(digits []byte) bool {
	var c byte
	for i := 0; i < len(digits); i++ {
		d := digits[len(digits)-1-i]
		c = verhoeffD[c][verhoeffP[i%8][d]]
	}
	return c == 0
}
