package store

import (
	"strings"
	"testing"
)

/*
Writes by id are scoped to the PATH group in SQL (audit 2026-11-07 G-M3), and
every list a stranger sees uses one listable-group rule (G-M1, G-L1).

The service decides privilege on the group in the URL. If the statement then
matches on the row id alone, a moderator of any group acts on every group:
that was the wiki overwrite and the comment delete.
*/

func TestWikiUpdateIsScopedToThePathGroup(t *testing.T) {
	body := codeOnly(funcBody(t, "group_content.go", "UpdateWikiPage"))
	if !strings.Contains(body, "WHERE id=$1 AND group_id=$5") {
		t.Fatal("UpdateWikiPage matches the page by id alone: a member of one group overwrites, and reads back, another group's page")
	}
	if !strings.Contains(body, "groupID,") {
		t.Fatal("UpdateWikiPage does not bind the group id")
	}
	if !strings.Contains(body, "ErrWikiPageNotFound") {
		t.Fatal("a page outside the group must answer not found, not an empty 200")
	}
}

func TestWikiListIsBounded(t *testing.T) {
	body := codeOnly(funcBody(t, "group_content.go", "ListWikiPages"))
	if !strings.Contains(body, "LIMIT $2 OFFSET $3") {
		t.Fatal("ListWikiPages is unbounded: every page's full content in one response")
	}
}

func TestCommentDeleteIsScopedToThePostAndGroup(t *testing.T) {
	body := codeOnly(funcBody(t, "comments.go", "DeleteGroupPostComment"))
	for _, must := range []string{"c.post_id = $2", "p.id = c.post_id", "p.group_id = $3", "AND ($4::boolean OR c.user_id = $5)"} {
		if !strings.Contains(body, must) {
			t.Errorf("DeleteGroupPostComment lost %q: a moderator of one group could delete any group's comment", must)
		}
	}
	if strings.Contains(body, "WHERE id = $1 RETURNING") {
		t.Fatal("DeleteGroupPostComment still has an id-only delete")
	}
}

func TestStrangerListsUseTheListableRule(t *testing.T) {
	for _, c := range []struct{ file, fn string }{
		{"group.go", "SearchGroups"},
		{"group.go", "DiscoverPublicGroups"},
		{"group.go", "DiscoverPublicGroupsByType"},
		{"group_posts.go", "DiscoverGroupsForUser"},
	} {
		if !strings.Contains(codeOnly(funcBody(t, c.file, c.fn)), "listableGroupSQL") {
			t.Errorf("%s does not apply listableGroupSQL: it can list a private or hidden group to anyone", c.fn)
		}
	}
	for _, must := range []string{
		"g.status = 'active'",
		"g.privacy_level IN ('public', 'restricted')",
		"g.visibility <> 'private'",
		"g.group_type NOT IN ('private', 'hidden')",
	} {
		if !strings.Contains(listableGroupSQL, must) {
			t.Errorf("listableGroupSQL lost %q", must)
		}
	}
}

func TestOtherAdminCheckIsOneExistsOverEveryMember(t *testing.T) {
	body := codeOnly(funcBody(t, "members.go", "HasOtherActiveAdmin"))
	for _, must := range []string{"EXISTS(", "user_id <> $2", "role = 'admin'", "status = 'active'"} {
		if !strings.Contains(body, must) {
			t.Errorf("HasOtherActiveAdmin lost %q", must)
		}
	}
	if strings.Contains(body, "LIMIT") {
		t.Fatal("HasOtherActiveAdmin is bounded again: an admin beyond the limit is not found")
	}
}
