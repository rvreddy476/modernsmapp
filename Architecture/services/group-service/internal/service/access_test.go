package service

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

/*
Guards for the group access gate (access.go, audit 2026-11-07 G-M2..G-M4,
G-L1, G-L2). The decision is pure, so every row of its table is tested here
without a database; the wiring test then holds every group-scoped service
method to going through it.
*/

func gateGroup(privacy string, creator uuid.UUID) *store.Group {
	vis := "public"
	if privacy == "private" {
		vis = "private"
	}
	return &store.Group{ID: uuid.New(), CreatorID: creator, PrivacyLevel: privacy, Visibility: vis, Status: "active"}
}

func row(status, role string) *store.GroupMember {
	return &store.GroupMember{Status: status, Role: role}
}

func TestDecideGroupAccess(t *testing.T) {
	owner, viewer := uuid.New(), uuid.New()
	pub, priv, restricted := gateGroup("public", owner), gateGroup("private", owner), gateGroup("restricted", owner)
	legacyPrivate := gateGroup("public", owner)
	legacyPrivate.Visibility = "private"
	deleted := gateGroup("public", owner)
	deleted.Status = "deleted"

	active, banned, left := row("active", "member"), row("banned", "member"), row("left", "member")

	cases := []struct {
		name   string
		g      *store.Group
		viewer uuid.UUID
		m      *store.GroupMember
		mode   accessMode
		want   error
	}{
		{"missing group", nil, viewer, nil, accessRead, ErrGroupNotFound},
		{"deleted group, even for a member", deleted, viewer, active, accessRead, ErrGroupNotFound},
		{"deleted group, even for the creator", deleted, owner, nil, accessCard, ErrGroupNotFound},

		{"public card, stranger", pub, viewer, nil, accessCard, nil},
		{"public read, stranger", pub, viewer, nil, accessRead, nil},
		{"public read, signed out", pub, uuid.Nil, nil, accessRead, nil},
		{"public read, left member", pub, viewer, left, accessRead, nil},
		{"restricted read, stranger", restricted, viewer, nil, accessRead, nil},

		{"public write, stranger", pub, viewer, nil, accessWrite, ErrNotGroupMember},
		{"public write, signed out", pub, uuid.Nil, nil, accessWrite, ErrNotGroupMember},
		{"public write, left member", pub, viewer, left, accessWrite, ErrNotGroupMember},
		{"public write, member", pub, viewer, active, accessWrite, nil},

		{"banned sees the public card", pub, viewer, banned, accessCard, nil},
		{"banned cannot read public content", pub, viewer, banned, accessRead, ErrGroupBanned},
		{"banned cannot write", pub, viewer, banned, accessWrite, ErrGroupBanned},

		{"private card, stranger: same as missing", priv, viewer, nil, accessCard, ErrGroupNotFound},
		{"private read, stranger: same as missing", priv, viewer, nil, accessRead, ErrGroupNotFound},
		{"private read, signed out", priv, uuid.Nil, nil, accessRead, ErrGroupNotFound},
		{"private read, left member", priv, viewer, left, accessRead, ErrGroupNotFound},
		{"private read, banned: same as missing", priv, viewer, banned, accessRead, ErrGroupNotFound},
		{"private card, banned: same as missing", priv, viewer, banned, accessCard, ErrGroupNotFound},
		{"private read, member", priv, viewer, active, accessRead, nil},
		{"private write, member", priv, viewer, active, accessWrite, nil},
		{"legacy visibility=private is private", legacyPrivate, viewer, nil, accessRead, ErrGroupNotFound},

		{"creator with no row reads a private group", priv, owner, nil, accessRead, nil},
		{"creator with a banned row is not locked out", priv, owner, banned, accessWrite, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideGroupAccess(c.g, c.viewer, c.m, c.mode); !errors.Is(got, c.want) || (c.want == nil && got != nil) {
				t.Fatalf("decideGroupAccess = %v, want %v", got, c.want)
			}
		})
	}
}

// The not-found refusal is one value with one message, so a private group
// cannot be told apart from a missing one by status OR by text.
func TestPrivateGroupRefusalIsTheMissingGroupAnswer(t *testing.T) {
	msg := ErrGroupNotFound.Error()
	if !strings.Contains(msg, "not found") {
		t.Fatalf("ErrGroupNotFound = %q: without \"not found\" it is not a 404", msg)
	}
	for _, leak := range []string{"forbidden", "private", "member", "banned"} {
		if strings.Contains(strings.ToLower(msg), leak) {
			t.Fatalf("ErrGroupNotFound = %q names why (%q)", msg, leak)
		}
	}
	if ErrGroupSearchUnavailable.Error() != msg {
		t.Fatalf("search answers %q and the gate answers %q: two texts for one answer", ErrGroupSearchUnavailable, msg)
	}
}

func TestMemberRankCreatorComesFromCreatorID(t *testing.T) {
	owner, viewer := uuid.New(), uuid.New()
	g := gateGroup("public", owner)
	if r := memberRank(g, owner, row("active", "member")); r != rankOwner {
		t.Fatalf("creator demoted to member in the row ranks %d, want owner", r)
	}
	if r := memberRank(g, owner, nil); r != rankOwner {
		t.Fatalf("creator with no row ranks %d, want owner", r)
	}
	if r := memberRank(g, viewer, row("banned", "admin")); r != rankNone {
		t.Fatalf("a banned admin row ranks %d, want none", r)
	}
	if r := memberRank(g, viewer, row("active", "moderator")); r != rankModerator {
		t.Fatalf("active moderator ranks %d", r)
	}
	if r := memberRank(g, uuid.Nil, nil); r != rankNone {
		t.Fatalf("signed out ranks %d", r)
	}
	if r := roleRank("owner"); r != rankNone {
		t.Fatalf("a stored role %q must not mint the owner rank, got %d", "owner", r)
	}
}

func TestGuardMemberModeration(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	refusal := errors.New("forbidden: refusal")
	cases := []struct {
		name          string
		actor, target int
		self          bool
		want          error
	}{
		{"member cannot moderate", rankMember, rankMember, false, refusal},
		{"none cannot moderate", rankNone, rankMember, false, refusal},
		{"moderator on member", rankModerator, rankMember, false, nil},
		{"moderator on moderator", rankModerator, rankModerator, false, ErrCannotModeratePeer},
		{"moderator on admin", rankModerator, rankAdmin, false, ErrCannotModeratePeer},
		{"admin on moderator", rankAdmin, rankModerator, false, nil},
		{"admin on admin", rankAdmin, rankAdmin, false, ErrCannotModeratePeer},
		{"admin on owner", rankAdmin, rankOwner, false, ErrCannotModerateOwner},
		{"owner on admin", rankOwner, rankAdmin, false, nil},
		{"anyone on themselves", rankOwner, rankOwner, true, ErrCannotModerateSelf},
		{"admin on themselves", rankAdmin, rankAdmin, true, ErrCannotModerateSelf},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target := b
			if c.self {
				target = a
			}
			got := guardMemberModeration(c.actor, c.target, a, target, refusal)
			if !errors.Is(got, c.want) || (c.want == nil && got != nil) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestGuardRoleChange(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	cases := []struct {
		name                   string
		actor, target, newRank int
		self                   bool
		ok                     bool
		want                   error
	}{
		{"moderator cannot change roles", rankModerator, rankMember, rankMember, false, false, nil},
		{"admin makes a moderator", rankAdmin, rankMember, rankModerator, false, true, nil},
		{"admin demotes a moderator", rankAdmin, rankModerator, rankMember, false, true, nil},
		{"admin cannot make an admin", rankAdmin, rankMember, rankAdmin, false, false, ErrOnlyOwnerPromotes},
		{"admin cannot demote an admin", rankAdmin, rankAdmin, rankMember, false, false, ErrCannotModeratePeer},
		{"admin cannot demote the owner", rankAdmin, rankOwner, rankMember, false, false, ErrCannotModerateOwner},
		{"owner makes an admin", rankOwner, rankModerator, rankAdmin, false, true, nil},
		{"owner demotes an admin", rankOwner, rankAdmin, rankMember, false, true, nil},
		{"owner cannot demote themselves", rankOwner, rankOwner, rankMember, true, false, ErrCannotModerateSelf},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target := b
			if c.self {
				target = a
			}
			got := guardRoleChange(c.actor, c.target, c.newRank, a, target)
			if c.ok != (got == nil) {
				t.Fatalf("got %v, want ok=%v", got, c.ok)
			}
			if c.want != nil && !errors.Is(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestGuardUnban(t *testing.T) {
	cases := []struct {
		name          string
		actor, banner int
		ok            bool
	}{
		{"moderator cannot unban", rankModerator, rankModerator, false},
		{"member cannot unban", rankMember, rankNone, false},
		{"admin lifts a moderator's ban", rankAdmin, rankModerator, true},
		{"admin lifts another admin's ban", rankAdmin, rankAdmin, true},
		{"admin lifts a departed banner's ban", rankAdmin, rankNone, true},
		{"admin cannot lift the owner's ban", rankAdmin, rankOwner, false},
		{"owner lifts any ban", rankOwner, rankOwner, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := guardUnban(c.actor, c.banner); c.ok != (got == nil) {
				t.Fatalf("got %v, want ok=%v", got, c.ok)
			}
		})
	}
	if !errors.Is(guardUnban(rankAdmin, rankOwner), ErrBanPlacedAbove) {
		t.Fatal("an admin lifting the owner's ban is not refused as ErrBanPlacedAbove")
	}
}

func TestSettingAllows(t *testing.T) {
	cases := []struct {
		setting string
		rank    int
		want    bool
	}{
		{"all_members", rankMember, true},
		{"all_members", rankNone, false},
		{"admins_mods", rankMember, false},
		{"admins_mods", rankModerator, true},
		{"admins_only", rankModerator, false},
		{"admins_only", rankAdmin, true},
		{"admins_only", rankOwner, true},
		{"", rankMember, true},
	}
	for _, c := range cases {
		if got := settingAllows(c.setting, c.rank); got != c.want {
			t.Errorf("settingAllows(%q, %d) = %v, want %v", c.setting, c.rank, got, c.want)
		}
	}
}

func TestMemberListVisibility(t *testing.T) {
	g := gateGroup("public", uuid.New())
	g.MemberListVisible = false
	if memberListAllowed(g, rankMember) {
		t.Fatal("member_list_visible=false still shows the list to a plain member")
	}
	if !memberListAllowed(g, rankModerator) {
		t.Fatal("member_list_visible=false hides the list from the group's moderators")
	}
	g.MemberListVisible = true
	if !memberListAllowed(g, rankNone) {
		t.Fatal("a visible member list is hidden")
	}
}

func TestGroupTypeMapsOntoPrivacy(t *testing.T) {
	for _, gt := range []string{"private", "hidden"} {
		if got, err := privacyForGroupType(gt, ""); err != nil || got != "private" {
			t.Errorf("group_type %q -> privacy %q, %v; want private", gt, got, err)
		}
		if got, err := privacyForGroupType(gt, "private"); err != nil || got != "private" {
			t.Errorf("group_type %q with privacy private -> %q, %v", gt, got, err)
		}
		if _, err := privacyForGroupType(gt, "public"); err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Errorf("group_type %q with privacy public was accepted: %v", gt, err)
		}
	}
	if got, _ := privacyForGroupType("study", "restricted"); got != "restricted" {
		t.Errorf("an ordinary type changed the requested privacy to %q", got)
	}
	if got, _ := privacyForGroupType("public", ""); got != "" {
		t.Errorf("an ordinary type invented a privacy %q (the caller defaults it)", got)
	}
}

func TestAssignableRolesExcludeStatuses(t *testing.T) {
	for _, r := range []string{"banned", "owner", "removed", ""} {
		if assignableRoles[r] {
			t.Errorf("UpdateMemberRole may set %q", r)
		}
	}
	for _, r := range []string{"admin", "moderator", "member"} {
		if !assignableRoles[r] {
			t.Errorf("UpdateMemberRole may not set %q", r)
		}
	}
}

/*
Every group-scoped method goes through the gate.

A method of *Service that takes a groupID must call one of the gate's entry
points (or a reviewed wrapper around one). A new route that reads the store
straight from its path group, which is how G-M3 and G-M4 happened, fails here.
The few exceptions are listed with the reason they are safe.
*/
func TestEveryGroupScopedMethodGoesThroughTheGate(t *testing.T) {
	entries := []string{
		"s.authorize(", "s.authorizeRank(", "s.gate(", "s.cardAccess(",
		"s.checkGroupAccess(", "s.engagementGate(", "s.requirePostModerator(",
		"s.readableGroupForSearch(", "s.createOnePost(", "s.CreateGroupPostV2(",
		// anonymous_reveal.go: creator, or an active admin/moderator row; it
		// fails closed and has its own guard tests.
		"s.canModerate(",
	}
	exempt := map[string]string{
		// Leaving only touches the caller's own active row; it refuses the
		// creator and re-checks admins with one EXISTS (G-L2).
		"LeaveGroup": "own row only",
		// Internal helpers that receive an already-authorised group.
		"containsBlockedWord":       "called after the gate by createOnePost",
		"lockMediaHolder":           "a lock, not a read",
		"releaseGroupMedia":         "called after the owner-only delete",
		"syncMemberToChat":          "called after the gate",
		"invalidateGroupCache":      "cache only",
		"invalidateMembershipCache": "cache only",
		"ensureGroupChat":           "creation path",
		"createGroupChat":           "creation path",
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || fd.Body == nil || !takesGroupID(fd) {
				continue
			}
			name := fd.Name.Name
			if _, ok := exempt[name]; ok || !ast.IsExported(name) {
				continue
			}
			body := codeOnly(string(src[fset.Position(fd.Body.Pos()).Offset:fset.Position(fd.Body.End()).Offset]))
			checked++
			found := false
			for _, e := range entries {
				if strings.Contains(body, e) {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, path+":"+name)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("group-scoped methods that bypass the access gate:\n  %s", strings.Join(missing, "\n  "))
	}
	if checked < 40 {
		t.Fatalf("only %d group-scoped methods were checked; the scan has lost its target", checked)
	}
}

func takesGroupID(fd *ast.FuncDecl) bool {
	for _, field := range fd.Type.Params.List {
		for _, n := range field.Names {
			if n.Name == "groupID" {
				return true
			}
		}
	}
	return false
}

// inOrder fails unless every want appears in body, each after the one before.
func inOrder(t *testing.T, fn, body string, wants ...string) {
	t.Helper()
	at := 0
	for _, w := range wants {
		i := strings.Index(body[at:], w)
		if i < 0 {
			t.Errorf("%s: %q missing or out of order", fn, w)
			return
		}
		at += i + len(w)
	}
}

/*
The guards are wired where they act, ahead of the write they protect. The
pure guards above are tested by table; this pins each call site, so a route
that stops calling its guard, or calls it with the wrong standing, fails.
*/
func TestGuardsAreWiredWhereTheyAct(t *testing.T) {
	cases := []struct {
		file, fn string
		wants    []string
	}{
		// The member read fails closed: an error is returned, never read as
		// "not a member" (which on a public group would admit a banned caller).
		{"access.go", "gate", []string{"if m, err = s.store.GetMember(ctx, g.ID, actorID); err != nil {", "return nil, err", "decideGroupAccess(g, actorID, m, mode)"}},
		{"access.go", "authorizeRank", []string{"s.authorize(ctx, groupID, actorID, accessWrite)", "if acc.rank < minRank {", "return nil, refusal"}},
		{"membership.go", "JoinGroup", []string{"accessCard", "if isBanned(acc.member) {", "return \"\", ErrGroupBanned"}},
		{"invites.go", "CreateJoinRequest", []string{"accessCard", "if isBanned(acc.member) {", "return nil, ErrGroupBanned"}},
		{"membership.go", "LeaveGroup", []string{"if g.CreatorID == actorID {", "return ErrCreatorCannotLeave", "s.store.HasOtherActiveAdmin(ctx, groupID, actorID)", "if !otherAdmin {", "s.store.RemoveMember("}},
		{"membership.go", "ListMembers", []string{"accessRead", "if !memberListAllowed(g, acc.rank) {", "return nil, ErrMemberListHidden"}},
		{"membership.go", "UpdateMemberRole", []string{"if !assignableRoles[role] {", "guardRoleChange(acc.rank, targetRank(acc.g, target), roleRank(role), actorID, targetID); err != nil {", "s.store.UpdateMemberRole("}},
		{"membership.go", "RemoveMember", []string{"guardMemberModeration(acc.rank, targetRank(acc.g, target), actorID, targetID, refusal); err != nil {", "s.store.KickMember("}},
		{"membership.go", "BanMember", []string{"guardMemberModeration(acc.rank, targetRank(acc.g, target), actorID, targetID, refusal); err != nil {", "s.store.BanMember"}},
		{"membership.go", "UnbanMember", []string{"if !isBanned(ban) {", "s.bannerRank(ctx, acc.g, ban)", "guardUnban(acc.rank, placedBy); err != nil {", "s.store.UnbanMember("}},
		{"comments.go", "AddGroupPostComment", []string{"accessWrite", "if !settingAllows(acc.g.CommentPermission, acc.rank) {", "return nil, ErrCommentsRestricted"}},
		{"comments.go", "DeleteGroupPostComment", []string{"isPrivileged := acc.rank >= rankModerator", "s.store.DeleteGroupPostComment(ctx, groupID, postID, commentID, actorID.String(), isPrivileged)"}},
		{"groups.go", "CreateGroup", []string{"privacyForGroupType(groupType, req.PrivacyLevel)"}},
		{"groups.go", "UpdateGroupSettings", []string{"if groupTypeNeedsPrivacy(groupType) && acc.g.PrivacyLevel != \"private\" {"}},
	}
	for _, c := range cases {
		inOrder(t, c.fn, codeOnly(funcSource(t, c.file, c.fn)), c.wants...)
	}
}
