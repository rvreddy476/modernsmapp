package handle

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		// Dots are dropped, not mapped: Gmail ignores them, so john.doe@
		// and johndoe@ are one mailbox and must not become two handles.
		{"john.doe", "johndoe"},
		{"John.Doe", "johndoe"},
		{"john_doe", "john_doe"},
		{"john-doe", "john_doe"},
		{"john  doe", "john_doe"},
		{"__john__", "john"},
		{"j..o..h..n", "john"},
		{"r@ghu!", "rghu"},
		{"1234", "1234"},
		{"", ""},
		{"日本語", ""},
	}
	for _, c := range cases {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBaseFromEmail(t *testing.T) {
	cases := []struct {
		email, first, last, want string
	}{
		{"john.doe@example.com", "John", "Doe", "johndoe"},
		// A +tag is an alias of the same mailbox, not part of the identity.
		{"john+shopping@example.com", "John", "Doe", "john"},
		{"rvreddy476@gmail.com", "Raghu", "Reddy", "rvreddy476"},
		// Too short after slugifying → falls through to the name.
		{"a@example.com", "Aoi", "Tanaka", "aoi_tanaka"},
		// Reserved local part must not be handed out.
		{"admin@example.com", "Real", "Person", "real_person"},
		// A banned substring is refused even inside a longer word.
		{"supportdesk@example.com", "Clara", "Chen", "clara_chen"},
		// Nothing usable anywhere → the neutral base.
		{"日本@example.com", "", "", "member"},
		{"", "", "", "member"},
		// Phone-only signup: no email, no name.
		{"", "", "", "member"},
	}
	for _, c := range cases {
		got := Base(c.email, c.first, c.last)
		if got != c.want {
			t.Errorf("Base(%q, %q, %q) = %q, want %q", c.email, c.first, c.last, got, c.want)
		}
		if !IsAcceptable(got) {
			t.Errorf("Base(%q, ...) returned %q, which is not acceptable", c.email, got)
		}
	}
}

func TestBaseIsAlwaysAcceptable(t *testing.T) {
	// Whatever goes in, the caller gets something it can legally claim.
	for _, email := range []string{
		"", "@", "a@b", "!!!@example.com", "admin@example.com",
		"averyveryverylongemailaddresslocalpart@example.com",
		"nazi@example.com", "___@example.com",
	} {
		got := Base(email, "", "")
		if !IsAcceptable(got) {
			t.Errorf("Base(%q) = %q, which is not acceptable", email, got)
		}
	}
}

func TestCandidatesAreDistinctAndInBounds(t *testing.T) {
	base := Base("john.doe@example.com", "John", "Doe")
	cands := Candidates(base)
	if len(cands) < 2 {
		t.Fatalf("want several candidates, got %d", len(cands))
	}
	if cands[0] != base {
		t.Errorf("first candidate = %q, want the base %q", cands[0], base)
	}
	// The numbered forms come before the random ones: johndoe2 is a handle
	// someone recognises as theirs, johndoe_7fk2m1 is one they will change.
	if cands[1] != "johndoe2" {
		t.Errorf("second candidate = %q, want %q", cands[1], "johndoe2")
	}
	seen := map[string]bool{}
	for _, c := range cands {
		if seen[c] {
			t.Errorf("duplicate candidate %q", c)
		}
		seen[c] = true
		if len(c) < MinLength || len(c) > MaxLength {
			t.Errorf("candidate %q has length %d, outside [%d,%d]", c, len(c), MinLength, MaxLength)
		}
	}
}

func TestLongLocalPartIsTruncatedWithRoomForASuffix(t *testing.T) {
	base := Base("averyveryverylongemailaddress@example.com", "", "")
	if len(base) > maxBase {
		t.Errorf("base %q is %d chars, want at most %d", base, len(base), maxBase)
	}
	for _, c := range Candidates(base) {
		if len(c) > MaxLength {
			t.Errorf("candidate %q is %d chars, want at most %d", c, len(c), MaxLength)
		}
	}
}

func TestFallbackIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		f := Fallback()
		if seen[f] {
			t.Fatalf("Fallback() repeated %q within 500 draws", f)
		}
		seen[f] = true
		if !strings.HasPrefix(f, "member_") || len(f) > MaxLength {
			t.Errorf("Fallback() = %q, want a member_ handle within %d chars", f, MaxLength)
		}
	}
}

func TestReservedAndBannedAreRefused(t *testing.T) {
	for _, s := range []string{"admin", "settings", "me", "support", "www", "user"} {
		if IsAcceptable(s) {
			t.Errorf("IsAcceptable(%q) = true, want false", s)
		}
	}
	for _, s := range []string{"johndoe", "aoi_tanaka", "member", "rvreddy476"} {
		if !IsAcceptable(s) {
			t.Errorf("IsAcceptable(%q) = false, want true", s)
		}
	}
}
