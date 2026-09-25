package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

/*
The guards on the group post write path.

Creating a post checked that you were a member and that the group let your
role post, and then wrote. It did not refuse a banned member, did not apply
the group's own word blocklist, and had no rate limit of any kind — a script
could fill a group as fast as the network allowed.

These are structural guards: this package's tests have no database and no
Redis, and what is worth protecting is that each check is present and that
the ones whose ORDER matters keep it.
*/

func TestPostWritePathRefusesABannedMember(t *testing.T) {
	body := funcSource(t, "group.go", "CreateGroupPostV2")

	if !strings.Contains(body, "CheckBanned") {
		t.Fatal("CreateGroupPostV2 does not call CheckBanned — GetActiveMember filtering on status='active' is an implicit consequence of how that query is written, not a stated intent, and a refactor of it would silently reopen posting to banned members")
	}
	ban := strings.Index(body, "CheckBanned")
	write := strings.Index(body, "s.store.CreateGroupPostV2")
	if write < 0 {
		t.Fatal("CreateGroupPostV2 no longer calls the store — this guard no longer protects anything")
	}
	if ban > write {
		t.Fatal("the ban check runs AFTER the post is written")
	}
}

func TestPostWritePathAppliesTheWordBlocklist(t *testing.T) {
	body := funcSource(t, "group.go", "CreateGroupPostV2")
	if !strings.Contains(body, "containsBlockedWord") {
		t.Fatal("CreateGroupPostV2 never consults the word blocklist — admins could add, list and remove words and none of it ever affected a post")
	}
}

func TestPostWritePathIsRateLimited(t *testing.T) {
	body := funcSource(t, "group.go", "CreateGroupPostV2")
	if !strings.Contains(body, "rl:group_post:") {
		t.Fatal("CreateGroupPostV2 has no rate limit")
	}
	// Before any work: a limiter that only refuses after the permission reads
	// still lets a flood cost a database round trip per attempt.
	limit := strings.Index(body, "rl:group_post:")
	member := strings.Index(body, "GetActiveMember")
	if member >= 0 && limit > member {
		t.Fatal("the rate limit runs after the membership read; it should be the first thing that can refuse")
	}
}

/*
The blocklist matches whole words.

Substring matching is the Scunthorpe problem: a group blocking "ass" would
refuse "assignment", "class" and "passport". A moderation tool that fires on
words nobody typed gets switched off, which is the same as not having one.
*/
func TestBlocklistMatchesWholeWordsNotSubstrings(t *testing.T) {
	body := funcSource(t, "group.go", "containsBlockedWord")
	if !strings.Contains(body, "FieldsFunc") {
		t.Fatal("containsBlockedWord does not split the text into words — substring matching would refuse 'class' for a blocklist containing 'ass'")
	}
	if strings.Contains(body, "strings.Contains(haystack") {
		t.Fatal("containsBlockedWord uses substring matching")
	}
}

/*
Ending a membership must drop that person's cached membership.

CheckMembershipCached caches "gm:<group>:<user>" for five minutes and nothing
deleted it, so a banned member could keep commenting for up to five minutes
after the ban. The moderator sees a success and the person keeps posting.
*/
func TestEndingAMembershipInvalidatesTheMembershipCache(t *testing.T) {
	for _, fn := range []string{"BanMember", "RemoveMember", "LeaveGroup"} {
		body := funcSource(t, "group.go", fn)
		if !strings.Contains(body, "invalidateMembershipCache") {
			t.Errorf("%s does not drop the gm: membership cache — the person keeps their cached membership for up to five minutes, which is a ban-evasion window for commenting", fn)
		}
	}
}

// A pending post is not in the group yet, so it must not be counted as one.
// Before, every pending post inflated post_count and rejection never undid it.
func TestPendingPostsAreNotCountedUntilApproved(t *testing.T) {
	store := funcSourceIn(t, "../store/group.go", "CreateGroupPostV2")
	if !strings.Contains(store, `p.Status == "published"`) {
		t.Fatal("store.CreateGroupPostV2 bumps post_count unconditionally — a post awaiting approval, or rejected, still counts toward the group's total")
	}

	approve := funcSourceIn(t, "../store/group_posts.go", "ApproveGroupPost")
	if !strings.Contains(approve, "post_count = post_count + 1") {
		t.Fatal("ApproveGroupPost does not count the post it just published, so an approved post never reaches the group's total")
	}
	if !strings.Contains(approve, "status='pending_approval'") {
		t.Fatal("ApproveGroupPost is not guarded on the current status, so approving twice counts the post twice")
	}
}

// The moderation queue reads group_posts, not the table nobody writes.
func TestPendingPostsAreReadFromGroupPostsNotTheDeadQueue(t *testing.T) {
	body := funcSourceIn(t, "../store/group.go", "ListPendingGroupPostsV2")
	if strings.Contains(body, "post_approval_queue") {
		t.Fatal("ListPendingGroupPostsV2 reads post_approval_queue — nothing writes that table (AddToApprovalQueue has no callers), so it would always return empty, which is the bug being fixed")
	}
	if !strings.Contains(body, "status = 'pending_approval'") {
		t.Fatal("ListPendingGroupPostsV2 does not filter on the pending status")
	}
}

// A moderator of one group must not act on another group's post by id.
func TestReviewingAPostChecksItBelongsToTheGroup(t *testing.T) {
	for _, fn := range []string{"ApprovePendingPost", "RejectPendingPost"} {
		body := funcSource(t, "group.go", fn)
		if !strings.Contains(body, "post.GroupID != groupID") {
			t.Errorf("%s does not check the post belongs to the group in the path — a moderator anywhere could approve a post in a group they cannot see", fn)
		}
	}
}

// funcSourceIn returns the body text of the named top-level func or method.
func funcSourceIn(t *testing.T, path, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(raw)
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != name || fd.Body == nil {
			continue
		}
		return src[fset.Position(fd.Body.Pos()).Offset:fset.Position(fd.Body.End()).Offset]
	}
	t.Fatalf("%s: no function named %s — it was renamed or removed, and this guard no longer protects anything", path, name)
	return ""
}
