package service

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/atpost/community-service/internal/store"
	"github.com/google/uuid"
)

/*
A private community is invite-only (founder, 2026-09-12): nobody joins one
directly by holding its id. A private community whose join_mode was left at
the old default "open" could be joined straight away. The default, the save
path and the join path each hold the rule, so a row from before the fix is
covered too.
*/

func TestJoinModeForNewCommunities(t *testing.T) {
	cases := []struct {
		ctype, requested, want string
		err                    error
	}{
		{"public", "", "open", nil},
		{"education", "", "open", nil},
		{"professional", "open", "open", nil},
		{"public", "request", "request", nil},
		{"private", "", privateJoinMode, nil},
		{"invite", "", privateJoinMode, nil},
		{"fan", "", privateJoinMode, nil},
		{"local", "", privateJoinMode, nil},
		{"private", "request", "request", nil},
		{"private", "email_domain", "email_domain", nil},
		{"private", "open", "", ErrPrivateCommunityOpen},
		{"brand", "open", "", ErrPrivateCommunityOpen},
	}
	for _, c := range cases {
		got, err := joinModeFor(c.ctype, c.requested)
		if !errors.Is(err, c.err) || got != c.want {
			t.Errorf("joinModeFor(%q, %q) = %q, %v; want %q, %v", c.ctype, c.requested, got, err, c.want, c.err)
		}
	}
	if privateJoinMode == "open" || !validJoinModes[privateJoinMode] {
		t.Fatalf("privateJoinMode %q must be a valid, non-open join mode", privateJoinMode)
	}
}

func TestEffectiveJoinModeNeverOpensAPrivateCommunity(t *testing.T) {
	owner := uuid.New()
	for _, ctype := range []string{"private", "invite", "fan", "local", "brand", ""} {
		c := community(ctype, owner)
		c.JoinMode = "open"
		if got := effectiveJoinMode(c); got == "open" {
			t.Errorf("a %q community stored as open is joined directly", ctype)
		}
	}
	for _, ctype := range []string{"public", "education", "professional"} {
		c := community(ctype, owner)
		c.JoinMode = "open"
		if got := effectiveJoinMode(c); got != "open" {
			t.Errorf("a public %q community stored as open became %q", ctype, got)
		}
	}
	c := community("private", owner)
	c.JoinMode = "request"
	if effectiveJoinMode(c) != "request" {
		t.Error("a private community's own non-open join mode is not kept")
	}
}

func TestSettleJoinModeOnSave(t *testing.T) {
	owner := uuid.New()
	priv := func(mode string) *store.Community {
		c := community("private", owner)
		c.JoinMode = mode
		return c
	}

	// A save that names "open" on a private community is refused.
	if err := settleJoinMode(priv("open"), true); !errors.Is(err, ErrPrivateCommunityOpen) {
		t.Fatalf("explicit open on a private community: %v", err)
	}
	// A stored "open" (type just made private, or an old row) is closed.
	c := priv("open")
	if err := settleJoinMode(c, false); err != nil || c.JoinMode != privateJoinMode {
		t.Fatalf("stored open on a private community: mode %q, err %v", c.JoinMode, err)
	}
	// Anything else is left alone.
	c = priv("request")
	if err := settleJoinMode(c, true); err != nil || c.JoinMode != "request" {
		t.Fatalf("request on a private community: mode %q, err %v", c.JoinMode, err)
	}
	pub := community("public", owner)
	pub.JoinMode = "open"
	if err := settleJoinMode(pub, true); err != nil || pub.JoinMode != "open" {
		t.Fatalf("open on a public community: mode %q, err %v", pub.JoinMode, err)
	}
}

// The three halves are wired where they act.
func TestJoinModeRuleIsWired(t *testing.T) {
	bodies, _ := serviceFuncs(t)
	if !strings.Contains(bodies["CreateCommunity"], "joinModeFor(ct, params.JoinMode)") {
		t.Error("CreateCommunity does not take its join mode from joinModeFor")
	}
	update := bodies["UpdateCommunity"]
	settle := "if err := settleJoinMode(c, params.JoinMode != nil); err != nil {\n\t\treturn nil, err\n\t}"
	if i, j := strings.Index(update, settle), strings.Index(update, "s.store.UpdateCommunity("); i < 0 || j < 0 || i > j {
		t.Error("UpdateCommunity saves without settling the join mode, or ignores its refusal")
	}
	join := bodies["JoinCommunity"]
	if !strings.Contains(join, "switch effectiveJoinMode(c) {") || strings.Contains(join, "switch c.JoinMode") {
		t.Error("JoinCommunity reads the stored join mode: a private community stored as open is joined directly")
	}
}

/*
member_directory=false keeps the member list to moderators and above, as
group-service's member_list_visible does. It was stored and never read.
*/
func TestMemberDirectoryAllowed(t *testing.T) {
	owner := uuid.New()
	hidden := community("public", owner)
	hidden.MemberDirectory = false
	shown := community("public", owner)
	shown.MemberDirectory = true
	for role, want := range map[string]bool{
		"": false, "member": false, "expert": false, "space_manager": false,
		"moderator": true, "admin": true, "owner": true,
	} {
		if got := memberDirectoryAllowed(hidden, role); got != want {
			t.Errorf("directory off, role %q: allowed = %v, want %v", role, got, want)
		}
		if !memberDirectoryAllowed(shown, role) {
			t.Errorf("directory on, role %q: refused", role)
		}
	}
}

func TestListMembersHonoursTheDirectorySetting(t *testing.T) {
	bodies, _ := serviceFuncs(t)
	list := bodies["ListMembers"]
	gate := strings.Index(list, "s.authorize(ctx, communityID, viewerID, accessRead)")
	check := strings.Index(list, "if !memberDirectoryAllowed(acc.c, acc.role) {")
	refuse := strings.Index(list, "return nil, ErrMemberDirectoryHidden")
	read := strings.Index(list, "s.store.ListMembers(")
	if gate < 0 || check < 0 || refuse < 0 || read < 0 || !(gate < check && check < refuse && refuse < read) {
		t.Fatal("ListMembers reads the list without applying member_directory after the gate")
	}
	if !strings.Contains(ErrMemberDirectoryHidden.Error(), "forbidden") {
		t.Fatal("ErrMemberDirectoryHidden must map to 403")
	}
}

// Migration 004 closes stored rows by the same public set the code uses.
func TestMigration004UsesThePublicSet(t *testing.T) {
	raw, err := os.ReadFile("../../database/migrations/004_private_communities_not_open.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.Join(strings.Fields(codeOnlySQL(string(raw))), " ")
	quoted := make([]string, len(store.PublicCommunityTypes))
	for i, ty := range store.PublicCommunityTypes {
		quoted[i] = "'" + ty + "'"
	}
	for _, must := range []string{
		"SET join_mode = '" + privateJoinMode + "'",
		"WHERE join_mode = 'open'",
		"AND community_type NOT IN (" + strings.Join(quoted, ", ") + ")",
	} {
		if !strings.Contains(sql, must) {
			t.Errorf("migration 004 lost %q:\n%s", must, sql)
		}
	}
}

// codeOnlySQL drops -- comments.
func codeOnlySQL(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
