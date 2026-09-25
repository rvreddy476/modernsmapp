package service

import (
	"strings"
	"testing"
)

/*
Anonymity, at the service layer.

The store masks the wire. These guard the decisions above it: that a group
which has not opted in gets a refusal rather than a downgrade, that the alias
is minted per post, and that the two side channels which would name the
author anyway stay shut.
*/

/*
The single most dangerous line in the feature.

If a group disallows anonymity, the post is REFUSED. The tempting
alternative — post it under the member's name instead — is catastrophic and
irreversible: they pressed a button marked anonymous and their name
appeared. There must be no path that clears the flag and carries on.
*/
func TestAnonymousPostIsRefusedNotDowngraded(t *testing.T) {
	body := funcSource(t, "group.go", "CreateGroupPostV2")

	if !strings.Contains(body, "!g.AllowAnonymousPosts") {
		t.Fatal("CreateGroupPostV2 does not check the group allows anonymous posts — any group would silently acquire the feature")
	}

	gate := strings.Index(body, "!g.AllowAnonymousPosts")
	tail := body[gate:]
	// The branch must end the request, not fix up the flag and continue.
	if !strings.Contains(tail[:min(len(tail), 400)], "return nil, fmt.Errorf") {
		t.Fatal("the anonymity gate does not return an error — if it downgrades to a named post instead, the member's name is published against their explicit choice, and that cannot be undone")
	}

	// No assignment anywhere that turns anonymity off and keeps going.
	for _, bad := range []string{
		"params.IsAnonymous = false",
		"IsAnonymous: false",
		"post.IsAnonymous = false",
	} {
		if strings.Contains(body, bad) {
			t.Fatalf("CreateGroupPostV2 contains %q — that is the downgrade path, and it leaks the author", bad)
		}
	}
}

// Per post, not per author, and not hoisted above any loop.
func TestAliasIsMintedForThePostBeingCreated(t *testing.T) {
	body := funcSource(t, "group.go", "CreateGroupPostV2")
	if !strings.Contains(body, "store.NewAnonAlias()") {
		t.Fatal("CreateGroupPostV2 does not mint an alias — with is_anonymous set and anon_alias nil, migration 013's CHECK constraint refuses the row, so anonymous posting fails outright")
	}
	if !strings.Contains(body, "AnonAlias:") {
		t.Fatal("the alias is minted but never put on the post")
	}
}

/*
The contributor leaderboard must not tick.

GET /:groupId/stats/contributors is readable by any member. A post count
that increments at the same moment an anonymous post appears names the
author in any group small enough to watch.
*/
func TestAnonymousPostDoesNotTouchContributorStats(t *testing.T) {
	body := funcSource(t, "group.go", "CreateGroupPostV2")
	idx := strings.Index(body, "IncrementMemberPostCount")
	if idx < 0 {
		t.Fatal("IncrementMemberPostCount is gone — this guard no longer protects anything")
	}
	before := body[:idx]
	if !strings.Contains(before, "if !params.IsAnonymous {") {
		t.Fatal("IncrementMemberPostCount is called unconditionally — the contributors list would identify the author of every anonymous post by the timing of the increment")
	}
}

/*
And the author stays off the event bus.

PublishGroupPostCreated carries the actor to every consumer;
notification-service renders "X posted in Y" from it, which would push the
author's name to the whole group.
*/
func TestAnonymousPostDoesNotPublishTheAuthor(t *testing.T) {
	body := funcSource(t, "group.go", "CreateGroupPostV2")
	// The CALL, not the mention of it in the comment above the call — the
	// comment explains why the guard exists, so searching for the bare name
	// finds the prose and measures the wrong place.
	idx := strings.Index(body, "s.producer.PublishGroupPostCreated(")
	if idx < 0 {
		t.Fatal("PublishGroupPostCreated is gone — this guard no longer protects anything")
	}
	// The guard must be IMMEDIATELY before the publish, not merely somewhere
	// earlier in the function: the stats guard uses the same words, so a
	// whole-body search passes even after this one is deleted.
	window := body[max(0, idx-220):idx]
	if !strings.Contains(window, "if !params.IsAnonymous {") {
		t.Fatal("PublishGroupPostCreated runs for anonymous posts, handing the real author to notification fanout — a push notification would de-anonymise them to the entire group")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
