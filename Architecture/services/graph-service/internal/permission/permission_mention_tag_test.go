package permission

import "testing"

// who_can_mention / who_can_tag (2026-10-24): everyone | following | no_one,
// where "following" is the TARGET following the actor. Unknown denies.
func TestResolveMentionAndTag(t *testing.T) {
	cases := []struct {
		name    string
		facts   Facts
		setting string
		want    bool
	}{
		{"everyone allows a stranger", Facts{}, "everyone", true},
		{"everyone still loses to a block", Facts{Blocked: true, TargetFollowsActor: true}, "everyone", false},
		{"following allows someone the target follows", Facts{TargetFollowsActor: true}, "following", true},
		{"following denies a stranger", Facts{}, "following", false},
		{"following denies a follower the target does not follow back", Facts{ActorFollowsTarget: true}, "following", false},
		{"following denies a legacy connection without the follow", Facts{IsConnection: true}, "following", false},
		{"following denies a blocked pair even with the follow", Facts{Blocked: true, TargetFollowsActor: true}, "following", false},
		{"no_one denies even a mutual follow", Facts{ActorFollowsTarget: true, TargetFollowsActor: true, IsConnection: true}, "no_one", false},
		{"unknown (strict default) denies", Facts{TargetFollowsActor: true}, "", false},
		{"unrecognised value denies", Facts{TargetFollowsActor: true}, "friends", false},
	}
	for _, tc := range cases {
		for _, action := range []Action{ActionMention, ActionTag} {
			p := Privacy{}
			if action == ActionMention {
				p.WhoCanMention = tc.setting
				p.WhoCanTag = "everyone" // the other setting must not leak in
			} else {
				p.WhoCanTag = tc.setting
				p.WhoCanMention = "everyone"
			}
			got := Resolve(action, tc.facts, p)
			if got.Allowed != tc.want {
				t.Errorf("%s / %s: allowed=%v, want %v (%+v)", action, tc.name, got.Allowed, tc.want, got)
			}
		}
	}
}

// Each action reads its own setting.
func TestMentionAndTagReadTheirOwnSetting(t *testing.T) {
	p := Privacy{WhoCanMention: "no_one", WhoCanTag: "everyone"}
	if Resolve(ActionMention, Facts{}, p).Allowed {
		t.Fatal("mention must read who_can_mention (no_one)")
	}
	if !Resolve(ActionTag, Facts{}, p).Allowed {
		t.Fatal("tag must read who_can_tag (everyone)")
	}
	p = Privacy{WhoCanMention: "everyone", WhoCanTag: "no_one"}
	if !Resolve(ActionMention, Facts{}, p).Allowed || Resolve(ActionTag, Facts{}, p).Allowed {
		t.Fatal("mention/tag settings crossed")
	}
}

// The client-facing check routes parse with ParseActions: they must not
// answer "may I mention X", or the actor reads X's setting off the answer.
// Only the internal gate's parser knows the two names.
func TestMentionAndTagAreInternalOnlyActions(t *testing.T) {
	if got := ParseActions([]string{"mention", "tag"}); len(got) != 0 {
		t.Fatalf("ParseActions (client check API) must not know mention/tag, got %v", got)
	}
	for name, want := range map[string]Action{"mention": ActionMention, "tag": ActionTag, "comment": ActionComment, "view_posts": ActionViewPosts} {
		if got, ok := ParseCanAction(name); !ok || got != want {
			t.Errorf("ParseCanAction(%q) = %q,%v; want %q", name, got, ok, want)
		}
	}
	if _, ok := ParseCanAction("nope"); ok {
		t.Fatal("ParseCanAction accepted an unknown name")
	}
}
