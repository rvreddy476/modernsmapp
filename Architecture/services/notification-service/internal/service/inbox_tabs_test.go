package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// legacyInboxTypes are the short type names the Kafka consumers write to the
// inbox today (events/consumer.go and friends). The template registry holds
// only the dotted names, so the tab invariants are checked over both.
var legacyInboxTypes = []string{
	"reaction", "comment_reaction", "comment", "mention", "comment_mention",
	"follow", "friend_request", "friend_accepted", "post_reposted",
	"endorsement", "business_review", "new_subscriber", "dm", "message_request",
	"missed_call", "group.mention", "qa.answer.created", "qa.question.voted",
}

func primaryInboxTabs() []string {
	var out []string
	for _, tab := range InboxCategories() {
		if _, composite := inboxCompositeTabs[tab]; !composite {
			out = append(out, tab)
		}
	}
	return out
}

func knownInboxTypes() []string {
	types := append([]string(nil), legacyInboxTypes...)
	for t := range Templates {
		types = append(types, t)
	}
	return types
}

// Every type lands in at most one primary tab, and each TikTok tab has members.
func TestInboxTabs_EachTypeInAtMostOneTab(t *testing.T) {
	members := map[string][]string{}
	for _, typ := range knownInboxTypes() {
		var claimed []string
		for _, tab := range primaryInboxTabs() {
			if inboxTypeMatcher(tab)(typ) {
				claimed = append(claimed, tab)
				members[tab] = append(members[tab], typ)
			}
		}
		if len(claimed) > 1 {
			t.Errorf("type %q is claimed by %d tabs: %v", typ, len(claimed), claimed)
		}
	}
	for _, tab := range []string{InboxTabLikes, InboxTabComments, InboxTabMentions, InboxTabFollowers} {
		if len(members[tab]) == 0 {
			t.Errorf("tab %q has no notification types", tab)
		}
	}
	// The template registry alone must already populate the TikTok tabs.
	for _, tab := range []string{InboxTabLikes, InboxTabComments, InboxTabMentions, InboxTabFollowers} {
		found := false
		for typ := range Templates {
			if inboxTypeMatcher(tab)(typ) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no registered template maps to tab %q", tab)
		}
	}
}

// A tab is derived from the preference category, so the tab and the toggles
// agree: every type in a tab shares the preference toggles of that tab.
func TestInboxTabs_DerivedFromPreferenceCategory(t *testing.T) {
	wantCats := map[string][]prefCategory{
		InboxTabLikes:     {catLikes, catSuperLikes},
		InboxTabComments:  {catComments, catReplies},
		InboxTabMentions:  {catMentions, catGroupMentions, catCommunityMentions, catCredits},
		InboxTabFollowers: {catFollows, catFriendRequests},
	}
	for tab, cats := range wantCats {
		for _, typ := range knownInboxTypes() {
			if !inboxTypeMatcher(tab)(typ) {
				continue
			}
			got := categoryForEvent(typ)
			ok := false
			for _, c := range cats {
				ok = ok || c == got
			}
			if !ok {
				t.Errorf("tab %q holds %q whose preference category is %v", tab, typ, got)
			}
		}
	}
}

func TestInboxTabs_TypeTable(t *testing.T) {
	want := map[string]string{
		"post.liked": InboxTabLikes, "reaction": InboxTabLikes, "comment_reaction": InboxTabLikes,
		"post.super_liked": InboxTabLikes,
		"post.commented":   InboxTabComments, "comment": InboxTabComments, "comment.replied": InboxTabComments,
		"mention.created": InboxTabMentions, "mention": InboxTabMentions, "comment_mention": InboxTabMentions,
		"group.mention": InboxTabMentions, "community.mention": InboxTabMentions,
		// Video credits are tags (2026-10-23).
		CreditTypeRequest: InboxTabMentions, CreditTypeAccepted: InboxTabMentions,
		"user.followed": InboxTabFollowers, "follow": InboxTabFollowers,
		"friend_request": InboxTabFollowers, "friend_accepted": InboxTabFollowers,
		"user.friend_request": InboxTabFollowers, "user.friend_accepted": InboxTabFollowers,
		"qa.answer.created":    InboxTabQA,
		"group.post.published": InboxTabGroups, "group.event.reminder": InboxTabGroups,
		"channel.urgent.critical":  InboxTabChannels,
		"community.post.published": InboxTabCommunities,
		"system.login_alert":       InboxTabSystem,
		// Only under "all".
		"post.shared": "", "post_reposted": "", "dm": "", "missed_call": "",
		"creator_went_live": "", "new_subscriber": "", "endorsement": "",
	}
	for typ, tab := range want {
		if got := inboxTabForType(typ); got != tab {
			t.Errorf("inboxTabForType(%q) = %q, want %q", typ, got, tab)
		}
	}
	activity := inboxTypeMatcher(InboxTabActivity)
	for _, typ := range []string{"reaction", "comment", "follow"} {
		if !activity(typ) {
			t.Errorf("activity tab should include %q", typ)
		}
	}
	for _, typ := range []string{"mention", "qa.answer.created", "dm"} {
		if activity(typ) {
			t.Errorf("activity tab should not include %q", typ)
		}
	}
}

func TestNormalizeInboxCategory(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "all": "", "ALL": "", " Likes ": "likes", "comments": "comments",
		"mentions": "mentions", "followers": "followers", "qa": "qa", "activity": "activity",
		"groups": "groups", "channels": "channels", "communities": "communities", "system": "system",
	} {
		got, ok := NormalizeInboxCategory(in)
		if !ok || got != want {
			t.Errorf("NormalizeInboxCategory(%q) = %q,%v want %q,true", in, got, ok, want)
		}
	}
	for _, in := range []string{"like", "follows", "bogus", "qa.", "all,likes"} {
		if _, ok := NormalizeInboxCategory(in); ok {
			t.Errorf("NormalizeInboxCategory(%q) accepted an unknown category", in)
		}
	}
}

// fakeInbox mimics GetNotificationsWithCursor over newest-first rows.
type fakeInbox struct {
	rows  []scylla.Notification
	calls int
}

func (f *fakeInbox) GetNotificationsWithCursor(_ context.Context, _ uuid.UUID, cursorBucket int, cursorTS *gocql.UUID, limit int) ([]scylla.Notification, error) {
	f.calls++
	start := 0
	if cursorBucket != 0 && cursorTS != nil {
		start = len(f.rows)
		for i, r := range f.rows {
			if r.Bucket == cursorBucket && r.TS == *cursorTS {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(f.rows) {
		end = len(f.rows)
	}
	return append([]scylla.Notification(nil), f.rows[start:end]...), nil
}

// newFakeInbox builds newest-first rows spanning two monthly buckets.
func newFakeInbox(types []string) *fakeInbox {
	base := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	f := &fakeInbox{}
	for i, typ := range types {
		at := base.Add(-time.Duration(i) * time.Hour * 6)
		f.rows = append(f.rows, scylla.Notification{
			Bucket: at.Year()*100 + int(at.Month()),
			TS:     gocql.UUIDFromTime(at),
			Type:   typ,
		})
	}
	return f
}

func pageAll(t *testing.T, svc *Service, category string, limit int) []string {
	t.Helper()
	var got []string
	cursor := ""
	for i := 0; i < 1000; i++ {
		page, err := svc.GetNotificationsPage(context.Background(), uuid.New(), limit, cursor, category)
		if err != nil {
			t.Fatalf("category %q: %v", category, err)
		}
		if len(page.Items) > limit {
			t.Fatalf("category %q: page of %d exceeds limit %d", category, len(page.Items), limit)
		}
		for _, n := range page.Items {
			got = append(got, fmt.Sprintf("%s@%s", n.Type, n.TS))
		}
		if page.NextCursor == "" {
			return got
		}
		cursor = page.NextCursor
	}
	t.Fatalf("category %q: paging did not terminate", category)
	return nil
}

func TestGetNotificationsPage_FiltersEachCategoryAcrossPages(t *testing.T) {
	var types []string
	mix := []string{"reaction", "comment", "mention", "follow", "dm", "qa.answer.created",
		"post.liked", "comment.replied", "comment_mention", "friend_request", "post_reposted",
		"group.post.published", "channel.update.published", "community.post.published", "system.verification"}
	for i := 0; i < 160; i++ { // ~40 days: crosses a monthly bucket
		types = append(types, mix[i%len(mix)])
	}
	inbox := newFakeInbox(types)
	svc := New(nil, nil)
	svc.SetInboxPageReader(inbox)

	for _, category := range append([]string{"", "all"}, InboxCategories()...) {
		tab, _ := NormalizeInboxCategory(category)
		var want []string
		for _, r := range inbox.rows {
			if tab == "" || inboxTypeMatcher(tab)(r.Type) {
				want = append(want, fmt.Sprintf("%s@%s", r.Type, r.TS))
			}
		}
		for _, limit := range []int{1, 3, 20} {
			got := pageAll(t, svc, category, limit)
			if len(got) != len(want) {
				t.Fatalf("category %q limit %d: got %d rows, want %d", category, limit, len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("category %q limit %d: row %d = %s, want %s", category, limit, i, got[i], want[i])
				}
			}
		}
	}
}

// Old behaviour: one limit*4 over-fetch, and the cursor dropped whenever the
// matches did not overflow it, so older matches were unreachable.
func TestGetNotificationsPage_SparseMatchesKeepCursor(t *testing.T) {
	types := make([]string, 0, 1300)
	types = append(types, "reaction")
	for i := 0; i < 1200; i++ {
		types = append(types, "dm")
	}
	types = append(types, "post.liked")
	inbox := newFakeInbox(types)
	svc := New(nil, nil)
	svc.SetInboxPageReader(inbox)

	page, err := svc.GetNotificationsPage(context.Background(), uuid.New(), 20, "", "likes")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Type != "reaction" {
		t.Fatalf("first page = %+v", page.Items)
	}
	if page.NextCursor == "" {
		t.Fatal("scan budget spent without a cursor: older likes are unreachable")
	}
	if inbox.calls*filteredInboxBatch > filteredInboxScanBudget {
		t.Fatalf("scanned beyond the budget: %d calls", inbox.calls)
	}

	got := pageAll(t, svc, "likes", 20)
	if len(got) != 2 || got[1][:len("post.liked")] != "post.liked" {
		t.Fatalf("all likes = %v", got)
	}
}

func TestGetNotificationsPage_UnknownCategory(t *testing.T) {
	inbox := newFakeInbox([]string{"reaction"})
	svc := New(nil, nil)
	svc.SetInboxPageReader(inbox)
	_, err := svc.GetNotificationsPage(context.Background(), uuid.New(), 20, "", "bogus")
	if !errors.Is(err, ErrUnknownInboxCategory) {
		t.Fatalf("err = %v, want ErrUnknownInboxCategory", err)
	}
	if inbox.calls != 0 {
		t.Fatalf("unknown category still read the inbox (%d calls)", inbox.calls)
	}
}
