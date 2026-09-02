package service

import "testing"

func TestTemplates_RepostAndLiveRegistered(t *testing.T) {
	for _, ev := range []string{"post.reposted", "post_reposted", "live.started", "creator_went_live"} {
		tpl, ok := Templates[ev]
		if !ok {
			t.Fatalf("template %s missing", ev)
		}
		if !tpl.PushEligible {
			t.Fatalf("template %s must be push-eligible", ev)
		}
		if tpl.EventType != ev || tpl.TitleTemplate == "" {
			t.Fatalf("template %s malformed: %+v", ev, tpl)
		}
	}
	if Templates["creator_went_live"].CanAggregate {
		t.Fatal("live starts must not aggregate")
	}
	if categoryForEvent("post_reposted") != catReposts || categoryForEvent("creator_went_live") != catLive {
		t.Fatal("repost/live templates not wired to their categories")
	}
}
