package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
)

func allOnPrefs() *postgres.NotificationPreferences {
	p := postgres.DefaultNotificationPreferences("u1")
	p.PushCommunityPosts = true
	return p
}

// TestDefaultPreferences_LikePushOn pins the TikTok-parity default.
func TestDefaultPreferences_LikePushOn(t *testing.T) {
	if p := postgres.DefaultNotificationPreferences("u1"); !p.PushLikes || !p.InappLikes {
		t.Fatalf("likes must default on for both channels: push=%v inapp=%v", p.PushLikes, p.InappLikes)
	}
}

// TestResolveDecision_InappPushMatrix: the TikTok-style split on one
// ordinary category (comments, registry PushEligible=true).
func TestResolveDecision_InappPushMatrix(t *testing.T) {
	cases := []struct {
		name        string
		inapp, push bool
		wantInbox   bool
		wantWS      bool
		wantPush    bool
	}{
		{"both on", true, true, true, true, true},
		{"inapp off / push on -> no inbox, push still goes", false, true, false, false, true},
		{"inapp on / push off -> inbox only", true, false, true, true, false},
		{"both off -> nothing", false, false, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := allOnPrefs()
			p.InappComments = c.inapp
			p.PushComments = c.push
			for _, ev := range []string{"post.commented", "comment"} {
				d := resolveDecision(ev, p, false)
				if d.CreateInbox != c.wantInbox || d.SendWebSocket != c.wantWS || d.SendPush != c.wantPush {
					t.Fatalf("%s: got %+v, want inbox=%v ws=%v push=%v", ev, d, c.wantInbox, c.wantWS, c.wantPush)
				}
			}
		})
	}
}

// TestResolveDecision_CategoryMapping: each parity toggle controls its
// event types on BOTH channels.
func TestResolveDecision_CategoryMapping(t *testing.T) {
	cases := []struct {
		name    string
		events  []string
		offPush func(*postgres.NotificationPreferences)
		offIn   func(*postgres.NotificationPreferences)
	}{
		{"reposts", []string{"post_reposted", "post.reposted", "post.shared"},
			func(p *postgres.NotificationPreferences) { p.PushReposts = false },
			func(p *postgres.NotificationPreferences) { p.InappReposts = false }},
		{"live", []string{"creator_went_live", "live.started", "live.ended"},
			func(p *postgres.NotificationPreferences) { p.PushLive = false },
			func(p *postgres.NotificationPreferences) { p.InappLive = false }},
		{"messages", []string{"dm", "message_request"},
			func(p *postgres.NotificationPreferences) { p.PushMessages = false },
			func(p *postgres.NotificationPreferences) { p.InappMessages = false }},
		{"mentions", []string{"mention", "comment_mention"},
			func(p *postgres.NotificationPreferences) { p.PushMentions = false },
			func(p *postgres.NotificationPreferences) { p.InappMentions = false }},
		{"likes", []string{"reaction", "comment_reaction"},
			func(p *postgres.NotificationPreferences) { p.PushLikes = false },
			func(p *postgres.NotificationPreferences) { p.InappLikes = false }},
		{"follows", []string{"follow", "user.followed"},
			func(p *postgres.NotificationPreferences) { p.PushFollows = false },
			func(p *postgres.NotificationPreferences) { p.InappFollows = false }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, ev := range c.events {
				on := resolveDecision(ev, allOnPrefs(), false)
				if !on.CreateInbox || !on.SendPush {
					t.Fatalf("%s all-on: %+v", ev, on)
				}
				p := allOnPrefs()
				c.offPush(p)
				d := resolveDecision(ev, p, false)
				if d.SendPush || !d.CreateInbox {
					t.Fatalf("%s push off: %+v", ev, d)
				}
				p = allOnPrefs()
				c.offIn(p)
				d = resolveDecision(ev, p, false)
				if d.CreateInbox || d.SendWebSocket || !d.SendPush {
					t.Fatalf("%s inapp off: %+v", ev, d)
				}
			}
		})
	}
}

// TestResolveDecision_MissedCallAlwaysOn: calls ignore every category
// toggle and honour only the master push switch.
func TestResolveDecision_MissedCallAlwaysOn(t *testing.T) {
	p := allOnPrefs()
	p.PushLikes, p.PushComments, p.PushMessages, p.PushSystem = false, false, false, false
	p.InappLikes, p.InappComments, p.InappMessages, p.InappSystem = false, false, false, false
	d := resolveDecision("missed_call", p, false)
	if !d.CreateInbox || !d.SendPush {
		t.Fatalf("missed_call with all categories off: %+v", d)
	}
	p.PushEnabled = false
	d = resolveDecision("missed_call", p, false)
	if d.SendPush || !d.CreateInbox {
		t.Fatalf("missed_call with master push off: %+v", d)
	}
}

// TestResolveDecision_UnknownTypeStaysPushEligible guards the regression the
// resolver wiring could have caused: QA/dating/wallet/... types have no
// template and used to be pushed unconditionally.
func TestResolveDecision_UnknownTypeStaysPushEligible(t *testing.T) {
	d := resolveDecision("qa.answer.created", allOnPrefs(), false)
	if !d.SendPush || !d.CreateInbox {
		t.Fatalf("unknown type: %+v", d)
	}
	// A registry type that declares itself push-ineligible stays so.
	if d := resolveDecision("post.liked", allOnPrefs(), false); d.SendPush {
		t.Fatalf("post.liked should not push: %+v", d)
	}
}

func TestResolveDecision_MasterAndQuietHours(t *testing.T) {
	p := allOnPrefs()
	p.PushEnabled = false
	if d := resolveDecision("dm", p, false); d.SendPush || !d.CreateInbox {
		t.Fatalf("master off: %+v", d)
	}

	now := time.Now().UTC()
	start := now.Add(-time.Hour).Format("15:04")
	end := now.Add(time.Hour).Format("15:04")
	tz := "UTC"
	p = allOnPrefs()
	p.QuietHoursEnabled, p.QuietHoursStart, p.QuietHoursEnd, p.QuietHoursTZ = true, &start, &end, &tz
	d := resolveDecision("dm", p, false)
	if d.SendPush || !d.DeferPush || !d.CreateInbox {
		t.Fatalf("quiet hours: %+v", d)
	}
	if allowed, deferred := masterPushAllowed(p); allowed || !deferred {
		t.Fatalf("masterPushAllowed in quiet hours = %v,%v", allowed, deferred)
	}
	p.QuietHoursEnabled = false
	if allowed, deferred := masterPushAllowed(p); !allowed || deferred {
		t.Fatalf("masterPushAllowed with quiet hours disabled = %v,%v", allowed, deferred)
	}
}

func TestCategoryForEvent_Unmapped(t *testing.T) {
	for _, ev := range []string{"endorsement", "wallet.credit", fmt.Sprintf("rider.%s", "sos")} {
		if categoryForEvent(ev) != catDefault {
			t.Fatalf("%s should be catDefault", ev)
		}
	}
	if categoryForEvent("missed_call") != catAlwaysOn {
		t.Fatal("missed_call should be catAlwaysOn")
	}
}
