package service

import (
	"errors"
	"sort"
	"strings"
)

// Activity inbox tabs (TikTok parity, T-11).
//
// A tab is DERIVED from the preference category of a notification type
// (categoryForEvent in preferences.go) — there is deliberately no second,
// hand-maintained list of types per tab. A type joins a tab by joining the
// matching preference category, so the Likes tab and the Likes toggles can
// never disagree about what a "like" is.
const (
	InboxTabLikes       = "likes"
	InboxTabComments    = "comments"
	InboxTabMentions    = "mentions"
	InboxTabFollowers   = "followers"
	InboxTabQA          = "qa"
	InboxTabGroups      = "groups"
	InboxTabChannels    = "channels"
	InboxTabCommunities = "communities"
	InboxTabSystem      = "system"

	// InboxTabActivity is the web inbox's older "Activity" tab: a union of
	// the social tabs rather than a tab of its own, so it is not part of the
	// one-tab-per-type mapping below.
	InboxTabActivity = "activity"
)

// ErrUnknownInboxCategory is returned for a ?category= value no tab answers to.
var ErrUnknownInboxCategory = errors.New("unknown notification category")

// inboxTabByPrefCategory maps each preference category onto at most one tab.
// Categories absent here (reposts, live, messages, new videos, orders, food,
// doorstep, always-on, default) appear only under "all".
var inboxTabByPrefCategory = map[prefCategory]string{
	catLikes:             InboxTabLikes,
	catSuperLikes:        InboxTabLikes,
	catComments:          InboxTabComments,
	catReplies:           InboxTabComments,
	catMentions:          InboxTabMentions, // post, comment, group and community mentions/tags
	catGroupMentions:     InboxTabMentions,
	catCommunityMentions: InboxTabMentions,
	catFollows:           InboxTabFollowers,
	catFriendRequests:    InboxTabFollowers, // follow/connection requests and their acceptance
	catGroupPosts:        InboxTabGroups,
	catEventReminders:    InboxTabGroups,
	catChannelUpdates:    InboxTabChannels,
	catChannelUrgent:     InboxTabChannels,
	catCommunityPosts:    InboxTabCommunities,
	catSystem:            InboxTabSystem,
}

// inboxCompositeTabs are tabs that are a union of primary tabs.
var inboxCompositeTabs = map[string][]string{
	InboxTabActivity: {InboxTabLikes, InboxTabComments, InboxTabFollowers},
}

// inboxTabForType returns the single tab a notification type belongs to, or
// "" when it belongs to none (it is still listed under "all").
func inboxTabForType(eventType string) string {
	if tab, ok := inboxTabByPrefCategory[categoryForEvent(eventType)]; ok {
		return tab
	}
	// Q&A events have no preference category of their own; the qa tab has
	// always been the "qa." type namespace.
	if strings.HasPrefix(eventType, "qa.") {
		return InboxTabQA
	}
	return ""
}

// InboxCategories lists every accepted ?category= value except "" and "all".
func InboxCategories() []string {
	seen := map[string]bool{InboxTabQA: true}
	for _, tab := range inboxTabByPrefCategory {
		seen[tab] = true
	}
	for tab := range inboxCompositeTabs {
		seen[tab] = true
	}
	out := make([]string, 0, len(seen))
	for tab := range seen {
		out = append(out, tab)
	}
	sort.Strings(out)
	return out
}

// NormalizeInboxCategory canonicalises a ?category= value. "" and "all" mean
// no filter and normalise to "". ok is false for an unknown category.
func NormalizeInboxCategory(category string) (string, bool) {
	c := strings.ToLower(strings.TrimSpace(category))
	if c == "" || c == "all" {
		return "", true
	}
	for _, tab := range InboxCategories() {
		if tab == c {
			return c, true
		}
	}
	return "", false
}

// inboxTypeMatcher returns the predicate for a normalised, non-empty category.
func inboxTypeMatcher(category string) func(eventType string) bool {
	tabs := map[string]bool{category: true}
	if parts, ok := inboxCompositeTabs[category]; ok {
		tabs = make(map[string]bool, len(parts))
		for _, p := range parts {
			tabs[p] = true
		}
	}
	return func(eventType string) bool {
		tab := inboxTabForType(eventType)
		return tab != "" && tabs[tab]
	}
}
