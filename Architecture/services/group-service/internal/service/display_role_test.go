package service

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestDisplayRoleDerivesOwnerFromTheCreator pins the rule itself.
//
// group_members.role cannot hold "owner" — the CHECK constraint allows only
// admin, moderator and member — so owner is derived from groups.creator_id or
// it does not exist at all.
func TestDisplayRoleDerivesOwnerFromTheCreator(t *testing.T) {
	cases := []struct {
		name       string
		memberRole string
		isCreator  bool
		want       string
	}{
		{"the creator, stored as admin, is the owner", "admin", true, "owner"},
		{"an admin who did not create it stays an admin", "admin", false, "admin"},
		{"a moderator is not promoted by having created the group", "moderator", true, "moderator"},
		{"a plain member is not promoted by having created the group", "member", true, "member"},
		{"an ordinary member is untouched", "member", false, "member"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayRole(tc.memberRole, tc.isCreator); got != tc.want {
				t.Fatalf("displayRole(%q, isCreator=%v) = %q, want %q",
					tc.memberRole, tc.isCreator, got, tc.want)
			}
		})
	}
}

// TestOwnerIsMintedInExactlyOnePlace is the guard that matters.
//
// The rule was never wrong — GetGroup had it right. The bug was that
// ListMembers did not have it, so the creator read "Owner" on the cover and
// "Admin" in the Members tab: one person with two roles on one screen.
// Re-inlining the derivation anywhere starts that divergence again, so
// "owner" may be produced in exactly one function.
//
// Matched against comment-stripped source, so prose discussing the rule
// cannot satisfy the assertion.
func TestOwnerIsMintedInExactlyOnePlace(t *testing.T) {
	raw, err := os.ReadFile("group.go")
	if err != nil {
		t.Fatalf("read group.go: %v", err)
	}
	src := codeOnly(string(raw))

	/*
		Assignments only. A plain substring search for '= "owner"' also matches
		'== "owner"' and '!= "owner"', which are comparisons against a role the
		caller was handed — this file has four of those and they are fine. The
		character class excludes = ! < > so only a real assignment counts, while
		still catching := .
	*/
	mintsOwner := regexp.MustCompile(`[^=!<>]=\s*"owner"`)
	if n := len(mintsOwner.FindAllString(src, -1)); n != 0 {
		t.Errorf(`group.go assigns "owner" directly %d time(s); only displayRole may produce it, `+
			`or the cover and the members list drift apart again`, n)
	}
	if n := strings.Count(src, `return "owner"`); n != 1 {
		t.Errorf(`group.go returns "owner" from %d place(s), want exactly 1 (displayRole)`, n)
	}
	if !strings.Contains(codeOnly(funcSource(t, "group.go", "displayRole")), `return "owner"`) {
		t.Error("the one place that mints \"owner\" is not displayRole")
	}
}

// TestListMembersAppliesTheOwnerRule pins the call the Members tab actually
// makes — the one that was missing the rule.
func TestListMembersAppliesTheOwnerRule(t *testing.T) {
	body := codeOnly(funcSource(t, "group.go", "ListMembers"))
	if !strings.Contains(body, "displayRole(") {
		t.Error("ListMembers does not call displayRole, so the group's creator renders as Admin " +
			"in the Members tab while the cover calls them the owner")
	}
	if !strings.Contains(body, "CreatorID") {
		t.Error("ListMembers never reads CreatorID, so it cannot know which member is the owner")
	}
}

// TestGetGroupUsesTheSharedRule keeps the original two callers on the shared
// helper rather than a copy that can be edited independently.
func TestGetGroupUsesTheSharedRule(t *testing.T) {
	for _, fn := range []string{"GetGroup", "GetGroupByHandle"} {
		body := codeOnly(funcSource(t, "group.go", fn))
		if !strings.Contains(body, "displayRole(") {
			t.Errorf("%s derives the viewer's role without displayRole; "+
				"a second copy of the rule is how this broke the first time", fn)
		}
	}
}
