package http

import (
	"testing"

	"github.com/atpost/identity-auth-service/internal/service"
)

// validRegistrationBody is a registration payload that satisfies EVERY gate
// the Register handler enforces.
//
// It exists so that a test about consent, or sessions, or cookies does not
// quietly start failing on an unrelated required field. Two tests were broken
// exactly that way: they predated first/last name becoming mandatory and were
// rejected at binding before reaching the behaviour they meant to check.
//
// When a new required field is added to registration, add it HERE and those
// tests keep testing what they were written to test.
func validRegistrationBody() string {
	return `{` +
		`"email":"a@b.com",` +
		`"password":"secret",` +
		`"first_name":"Raghu",` +
		`"last_name":"Varan",` +
		`"dob":"1990-01-01",` +
		`"gender":"male",` +
		`"accepted_terms":true,` +
		`"terms_version":"` + service.CurrentTermsVersion + `"}`
}

// TestValidatePersonName pins the name rule, and in particular the Indic
// cases that were broken.
//
// The original implementation allowed unicode.IsLetter only. That is Unicode
// category L, which excludes combining marks (category M) — the vowel signs,
// viramas and nuktas that most Devanagari, Telugu and Tamil names depend on.
// The effect was that a name's consonant stems were accepted and its vowels
// rejected, so "रघुवरन" failed while the mark-free "कमल" passed. With first
// and last name mandatory at registration, that locked a large share of the
// target market out of creating an account under their real name.
func TestValidatePersonName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		why     string
	}{
		// ── Latin ───────────────────────────────────────────────────────
		{"latin", "Raghu", false, ""},
		{"latin with apostrophe", "O'Brien", false, ""},
		{"latin with hyphen", "Anne-Marie", false, ""},
		{"latin with space", "Devi Prasad", false, ""},
		{"accented latin", "Zoë", false, "U+00EB is a letter, not a mark"},
		{"latin with combining acute", "José", false, "decomposed é: e + U+0301 (Mn)"},

		// ── Indic: the regression this test exists for ─────────────────
		{"devanagari with matra", "रघुवरन", false, "U+0941 DEVANAGARI VOWEL SIGN U (Mn)"},
		{"devanagari with e-matra", "रमेश", false, "U+0947 DEVANAGARI VOWEL SIGN E (Mn)"},
		{"devanagari mark-free", "कमल", false, "passed even before the fix"},
		{"telugu with virama", "రఘువరన్", false, "U+0C41 (Mn), U+0C4D VIRAMA (Mn)"},
		{"tamil with pulli", "ரகுவரன்", false, "U+0BC1 (Mc), U+0BCD PULLI (Mn)"},
		{"devanagari with nukta", "ज़ैन", false, "U+093C NUKTA (Mn)"},
		{"bengali", "শুভ", false, "U+09C1 (Mn)"},

		// ── Still rejected: widening to marks must not widen further ───
		{"digits", "user123", true, "handles belong in a separate, unique field"},
		{"digit after indic", "रघु1", true, ""},
		{"symbol", "Raghu#", true, ""},
		{"emoji", "Raghu😀", true, ""},
		{"underscore", "raghu_v", true, ""},
		{"at sign", "raghu@example", true, ""},

		// ── Length ─────────────────────────────────────────────────────
		{"exactly 50 runes", str(50, 'a'), false, "boundary is inclusive"},
		{"51 runes", str(51, 'a'), true, ""},
		{"50 multibyte runes", str(50, 'क'), false, "counts runes, not bytes"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePersonName(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("validatePersonName(%q) = nil, want error. %s", tc.input, tc.why)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validatePersonName(%q) = %v, want nil. %s", tc.input, err, tc.why)
			}
		})
	}
}

// TestValidatePersonNameAcceptsEmpty documents that emptiness is NOT this
// function's job. The Register handler trims and checks for empty separately,
// so it can return FIRST_NAME_REQUIRED / LAST_NAME_REQUIRED and let the client
// mark the offending input, rather than one generic NAME_INVALID for both.
func TestValidatePersonNameAcceptsEmpty(t *testing.T) {
	if err := validatePersonName(""); err != nil {
		t.Fatalf("validatePersonName(%q) = %v, want nil: the required check lives in Register", "", err)
	}
}

// TestIsAllowedGender pins the stored vocabulary.
//
// These are storage tokens, not display labels. Once rows exist with these
// values, changing one silently splits the data — and nothing else in the
// schema would catch it, because the column is plain TEXT with no CHECK.
func TestIsAllowedGender(t *testing.T) {
	for _, v := range []string{"male", "female", "other"} {
		if !isAllowedGender(v) {
			t.Errorf("isAllowedGender(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "Male", "m", "unspecified", "others", "non-binary"} {
		if isAllowedGender(v) {
			t.Errorf("isAllowedGender(%q) = true, want false", v)
		}
	}
	if got := len(genderValues); got != 3 {
		t.Errorf("genderValues has %d entries, want 3", got)
	}
}

// TestAllowedGendersIsACopy guards the error-detail payload.
//
// allowedGenders() is handed to clients in an error body. Returning the
// backing slice would let a caller mutate the package-level vocabulary.
func TestAllowedGendersIsACopy(t *testing.T) {
	got := allowedGenders()
	got[0] = "mutated"
	if genderValues[0] != "male" {
		t.Fatalf("allowedGenders() exposed the backing array: genderValues[0] = %q", genderValues[0])
	}
}

func str(n int, r rune) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = r
	}
	return string(out)
}
