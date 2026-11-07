package service

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/community-service/internal/store"
	"github.com/google/uuid"
)

/*
Guards for the community access gate (access.go, audit 2026-11-07 K-M1, K-M2,
K-L1, K-L2). The decision is pure, so every row is tested without a database;
the wiring test holds every community-scoped service method to the gate.
*/

func community(t string, owner uuid.UUID) *store.Community {
	return &store.Community{ID: uuid.New(), OwnerID: owner, CommunityType: t, Status: "active"}
}

func TestDecideCommunityAccess(t *testing.T) {
	owner, viewer := uuid.New(), uuid.New()
	pub, priv, fan := community("public", owner), community("private", owner), community("fan", owner)
	archived := community("public", owner)
	archived.Status = "archived"
	suspended := community("public", owner)
	suspended.Status = "suspended"
	v := &viewer
	o := &owner

	cases := []struct {
		name   string
		c      *store.Community
		viewer *uuid.UUID
		role   string
		mode   accessMode
		want   error
	}{
		{"missing", nil, v, "", accessRead, ErrCommunityNotFound},
		{"archived, even for a member", archived, v, "member", accessRead, ErrCommunityNotFound},
		{"archived, even for the owner", archived, o, "owner", accessCard, ErrCommunityNotFound},
		{"suspended", suspended, v, "member", accessCard, ErrCommunityNotFound},

		{"public read, stranger", pub, v, "", accessRead, nil},
		{"public read, signed out", pub, nil, "", accessRead, nil},
		{"public card, stranger", pub, v, "", accessCard, nil},
		{"public write, stranger", pub, v, "", accessWrite, ErrNotCommunityMember},
		{"public write, signed out", pub, nil, "", accessWrite, ErrNotCommunityMember},
		{"public write, pending", pub, v, "pending", accessWrite, ErrNotCommunityMember},
		{"public write, member", pub, v, "member", accessWrite, nil},
		{"public write, expert", pub, v, "expert", accessWrite, nil},

		{"banned sees the public card", pub, v, "banned", accessCard, nil},
		{"banned cannot read public content", pub, v, "banned", accessRead, ErrMemberBanned},
		{"suspended cannot write", pub, v, "suspended", accessWrite, ErrMemberBanned},

		{"private card, stranger: same as missing", priv, v, "", accessCard, ErrCommunityNotFound},
		{"private read, signed out", priv, nil, "", accessRead, ErrCommunityNotFound},
		{"private read, pending", priv, v, "pending", accessRead, ErrCommunityNotFound},
		{"private read, banned: same as missing", priv, v, "banned", accessRead, ErrCommunityNotFound},
		{"private read, member", priv, v, "member", accessRead, nil},
		{"fan is not public", fan, v, "", accessRead, ErrCommunityNotFound},

		{"owner with no row", priv, o, "", accessWrite, nil},
		{"owner whose row says banned", priv, o, "banned", accessWrite, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decideCommunityAccess(c.c, c.viewer, c.role, c.mode)
			if !errors.Is(got, c.want) || (c.want == nil && got != nil) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCommunityNotFoundIsTheMissingAnswer(t *testing.T) {
	if ErrCommunityNotFound.Error() != "community not found" {
		t.Fatalf("ErrCommunityNotFound = %q: it must read exactly as a missing community always has", ErrCommunityNotFound)
	}
	if !errors.Is(ErrCommunityNotFound, store.ErrCommunityNotFound) {
		t.Fatal("the gate's not-found and the store's not-found are two values")
	}
}

// K-L2: one public set, and the gate reads it.
func TestOnePublicSet(t *testing.T) {
	want := map[string]bool{"public": true, "education": true, "professional": true}
	for _, ct := range []string{"public", "education", "professional", "private", "invite", "local", "fan", "brand"} {
		if got := communityIsPublic(&store.Community{CommunityType: ct}); got != want[ct] {
			t.Errorf("communityIsPublic(%q) = %v, want %v", ct, got, want[ct])
		}
	}
	if len(store.PublicCommunityTypes) != len(want) {
		t.Fatalf("PublicCommunityTypes = %v", store.PublicCommunityTypes)
	}
}

func TestEffectiveRoleOwnerComesFromOwnerID(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	c := community("public", owner)
	if r := effectiveRole(c, &owner, "member"); r != "owner" {
		t.Fatalf("owner with a member row ranks %q", r)
	}
	if r := effectiveRole(c, &other, "moderator"); r != "moderator" {
		t.Fatalf("a moderator ranks %q", r)
	}
	if r := effectiveRole(c, nil, ""); r != "" {
		t.Fatalf("signed out ranks %q", r)
	}
}

func TestSpaceInCommunity(t *testing.T) {
	cid := uuid.New()
	sp := &store.CommunitySpace{ID: uuid.New(), CommunityID: cid}
	if err := spaceInCommunity(sp, cid, "member", true); err != nil {
		t.Fatalf("own space refused: %v", err)
	}
	if err := spaceInCommunity(sp, uuid.New(), "admin", false); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("another community's space accepted: %v", err)
	}
	if err := spaceInCommunity(nil, cid, "member", false); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("a nil space accepted: %v", err)
	}
	sp.IsQuarantined = true
	if err := spaceInCommunity(sp, cid, "member", true); !errors.Is(err, ErrSpaceQuarantined) {
		t.Fatalf("a member posted into a quarantined space: %v", err)
	}
	if err := spaceInCommunity(sp, cid, "moderator", true); err != nil {
		t.Fatalf("a moderator refused in a quarantined space: %v", err)
	}
	if err := spaceInCommunity(sp, cid, "member", false); err != nil {
		t.Fatalf("reading a quarantined space refused: %v", err)
	}
}

func TestPostVisible(t *testing.T) {
	cid := uuid.New()
	p := &store.CommunityPost{CommunityID: cid, Status: "published"}
	if err := postVisible(p, cid); err != nil {
		t.Fatal(err)
	}
	if err := postVisible(p, uuid.New()); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("a post of another community served under this one: %v", err)
	}
	p.Status = "deleted"
	if err := postVisible(p, cid); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("a deleted post served: %v", err)
	}
	if err := postVisible(nil, cid); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("a nil post served: %v", err)
	}
}

func TestGuardUnban(t *testing.T) {
	cases := []struct {
		actor, banner string
		ok            bool
	}{
		{"member", "", false},
		{"space_manager", "moderator", false},
		{"moderator", "moderator", true},
		{"moderator", "", true},
		{"moderator", "admin", false},
		{"moderator", "owner", false},
		{"admin", "moderator", true},
		{"admin", "owner", false},
		{"owner", "owner", true},
	}
	for _, c := range cases {
		if got := guardUnban(c.actor, c.banner); c.ok != (got == nil) {
			t.Errorf("guardUnban(%q, %q) = %v, want ok=%v", c.actor, c.banner, got, c.ok)
		}
	}
	if !errors.Is(guardUnban("moderator", "admin"), ErrBanPlacedAbove) {
		t.Fatal("a moderator lifting an admin's ban is not ErrBanPlacedAbove")
	}
}

func TestRoleHelpers(t *testing.T) {
	for _, r := range []string{"owner", "admin", "moderator", "space_manager", "expert", "member"} {
		if !isMemberRole(r) {
			t.Errorf("%q is not a member role", r)
		}
	}
	for _, r := range []string{"pending", "banned", "suspended", ""} {
		if isMemberRole(r) {
			t.Errorf("%q counts as a member", r)
		}
	}
	for _, r := range []string{"banned", "suspended"} {
		if !isBannedRole(r) {
			t.Errorf("%q is not banned", r)
		}
	}
	for _, r := range []string{"owner", "banned", "suspended", "pending", ""} {
		if assignableRoles[r] {
			t.Errorf("UpdateMemberRole may set %q", r)
		}
	}
}

var (
	lineComment  = regexp.MustCompile(`(?m)//.*$`)
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func codeOnly(src string) string {
	return lineComment.ReplaceAllString(blockComment.ReplaceAllString(src, " "), "")
}

// serviceFuncs returns name -> code-only body for every method of *Service
// in the package's non-test files, and which of them take a communityID.
func serviceFuncs(t *testing.T) (map[string]string, map[string]bool) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	bodies, scoped := map[string]string{}, map[string]bool{}
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
			if !ok || fd.Recv == nil || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			bodies[name] = codeOnly(string(src[fset.Position(fd.Body.Pos()).Offset:fset.Position(fd.Body.End()).Offset]))
			for _, field := range fd.Type.Params.List {
				for _, n := range field.Names {
					if n.Name == "communityID" {
						scoped[name] = true
					}
				}
			}
		}
	}
	return bodies, scoped
}

/*
Every community-scoped method goes through the gate. A new route that reads
the store straight from its path id, which is how K-M1 and K-M2 happened,
fails here. The exceptions say why they are safe.
*/
func TestEveryCommunityScopedMethodGoesThroughTheGate(t *testing.T) {
	entries := []string{"s.authorize(", "s.authorizeRole(", "s.authorizeRoleOr(", "s.engagementGate(", "s.moderatePost(", "s.AuthorizeMembership("}
	exempt := map[string]string{
		// A join request is how someone given a private community's id gets
		// in; it refuses a non-active community and a banned row itself.
		"JoinCommunity": "join is not hidden behind the private rule",
		// Leaving touches only the caller's own row and refuses a ban.
		"LeaveCommunity": "own row only",
		// Cache and media helpers called after the gate.
		"adjustMemberCount":     "counter",
		"invalidateMemberCache": "cache",
		"lockMediaHolder":       "lock",
		"releaseCommunityMedia": "after the owner-only delete",
	}
	bodies, scoped := serviceFuncs(t)
	var missing []string
	checked := 0
	for name := range scoped {
		if _, ok := exempt[name]; ok || !ast.IsExported(name) {
			continue
		}
		checked++
		found := false
		for _, e := range entries {
			if strings.Contains(bodies[name], e) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("community-scoped methods that bypass the access gate:\n  %s", strings.Join(missing, "\n  "))
	}
	if checked < 30 {
		t.Fatalf("only %d community-scoped methods checked; the scan has lost its target", checked)
	}
}

// The ban rules are wired where they act (K-L1).
func TestBansStick(t *testing.T) {
	bodies, _ := serviceFuncs(t)
	leave := bodies["LeaveCommunity"]
	if i, j := strings.Index(leave, "if isBannedRole(member.Role) {"), strings.Index(leave, "s.store.RemoveMember("); i < 0 || j < 0 || i > j {
		t.Fatal("LeaveCommunity deletes the row before refusing a ban: leaving erases the ban")
	}
	ban := bodies["BanMember"]
	if i, j := strings.Index(ban, "ErrAlreadyBanned"), strings.Index(ban, "s.store.BanMember("); i < 0 || j < 0 || i > j {
		t.Fatal("BanMember re-bans a banned row, rewriting banned_by")
	}
	unban := bodies["UnbanMember"]
	if i, j := strings.Index(unban, "guardUnban(acc.role, placedBy)"), strings.Index(unban, "s.store.UnbanMember("); i < 0 || j < 0 || i > j {
		t.Fatal("UnbanMember lifts a ban without checking who placed it")
	}
	role := bodies["UpdateMemberRole"]
	for _, must := range []string{"if !assignableRoles[newRole] {", "if isBannedRole(target.Role) {"} {
		if !strings.Contains(role, must) {
			t.Errorf("UpdateMemberRole lost %q: a role change can be an unban, or set a status", must)
		}
	}
}

// Posts go into a space of the PATH community, and replies under a post of
// the same space (K-M2).
func TestPostWritesAreScopedToThePathCommunity(t *testing.T) {
	bodies, _ := serviceFuncs(t)
	create := bodies["CreateCommunityPost"]
	for _, must := range []string{"s.space(ctx, acc, spaceID, true)", "parent.SpaceID != spaceID", "parent.ParentPostID != nil"} {
		if !strings.Contains(create, must) {
			t.Errorf("CreateCommunityPost lost %q", must)
		}
	}
	list := bodies["ListCommunityPosts"]
	if i, j := strings.Index(list, "s.authorize("), strings.Index(list, "s.store.ListSpacePosts("); i < 0 || j < 0 || i > j {
		t.Fatal("the space branch of ListCommunityPosts runs before the gate")
	}
	if !strings.Contains(list, "s.space(ctx, acc, *spaceID, false)") {
		t.Fatal("ListCommunityPosts does not check the space belongs to the community")
	}
	if !strings.Contains(bodies["engagementGate"], "if viewerID == uuid.Nil {") {
		t.Fatal("engagement accepts a blank user")
	}
}

/*
The gate's own wiring: the member read fails closed (a database error is
returned, never read as "not a member", which on a public community would
admit a banned caller), and the role check sits between the gate and the
caller. These run against a database in production, so they are pinned here.
*/
func TestTheGateFailsClosed(t *testing.T) {
	bodies, _ := serviceFuncs(t)
	auth := bodies["authorize"]
	for _, must := range []string{
		"if m, err = s.store.GetMember(ctx, communityID, *viewerID); err != nil {",
		"decideCommunityAccess(c, viewerID, role, mode)",
	} {
		if !strings.Contains(auth, must) {
			t.Errorf("authorize lost %q", must)
		}
	}
	role := bodies["authorizeRole"]
	for _, must := range []string{"s.authorize(ctx, communityID, &actorID, accessWrite)", "if !isAtLeast(acc.role, minRole) {", "return nil, ErrInsufficientRole"} {
		if !strings.Contains(role, must) {
			t.Errorf("authorizeRole lost %q", must)
		}
	}
	if !strings.Contains(bodies["moderatePost"], `s.authorizeRole(ctx, communityID, actorID, "moderator")`) {
		t.Error("moderatePost no longer needs a moderator")
	}
	if !strings.Contains(bodies["AcceptAnswer"], "if p.AuthorID != actorID.String() {") {
		t.Error("AcceptAnswer no longer needs the question's author")
	}
	if !strings.Contains(bodies["UnbanMember"], `ban.Role != "banned"`) {
		t.Error("UnbanMember lifts a row that is not a ban")
	}
}
