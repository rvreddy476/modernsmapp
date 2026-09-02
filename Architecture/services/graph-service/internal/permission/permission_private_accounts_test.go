package permission

import "testing"

// Private accounts (TikTok model): the follow, comment and view_posts rows of
// the matrix, driven by account_visibility and allow_comments_from.

func TestResolveFollowPrivateAccount(t *testing.T) {
	// Public target: plain allow, no fallback.
	d := Resolve(ActionFollow, Facts{}, Privacy{AccountVisibility: "public"})
	if !d.Allowed || d.Fallback != "" {
		t.Errorf("public follow = %+v, want Allowed with no fallback", d)
	}
	// Private target: still Allowed (the endpoint accepts the call) but the
	// channel is a follow REQUEST — the endpoint answers "requested".
	d = Resolve(ActionFollow, Facts{}, Privacy{AccountVisibility: "private"})
	if !d.Allowed || d.Fallback != "follow_request" || d.Reason != "private_account" {
		t.Errorf("private follow = %+v, want Allowed with fallback follow_request", d)
	}
	// Already following a private account: nothing to request.
	d = Resolve(ActionFollow, Facts{ActorFollowsTarget: true}, Privacy{AccountVisibility: "private"})
	if !d.Allowed || d.Fallback != "" {
		t.Errorf("follow of an already-followed private account = %+v, want plain allow", d)
	}
	// Unknown visibility (privacy fetch failed) must NOT convert to a request:
	// a transient outage would otherwise strand public follows as pending.
	d = Resolve(ActionFollow, Facts{}, Privacy{})
	if !d.Allowed || d.Fallback != "" {
		t.Errorf("follow under unknown visibility = %+v, want plain allow", d)
	}
	// Blocked either way denies regardless.
	if d := Resolve(ActionFollow, Facts{Blocked: true}, Privacy{AccountVisibility: "private"}); d.Allowed {
		t.Error("blocked follow must be denied")
	}
}

func TestResolveComment(t *testing.T) {
	cases := []struct {
		name    string
		facts   Facts
		privacy Privacy
		want    bool
	}{
		{"everyone: stranger may comment", Facts{}, Privacy{AllowCommentsFrom: "everyone"}, true},
		{"everyone: blocked denies", Facts{Blocked: true}, Privacy{AllowCommentsFrom: "everyone"}, false},
		{"friends: stranger denied", Facts{}, Privacy{AllowCommentsFrom: "friends"}, false},
		{"friends: one-way follower denied", Facts{ActorFollowsTarget: true}, Privacy{AllowCommentsFrom: "friends"}, false},
		{"friends: mutual follow allowed", Facts{ActorFollowsTarget: true, TargetFollowsActor: true}, Privacy{AllowCommentsFrom: "friends"}, true},
		{"friends: connection allowed", Facts{IsConnection: true}, Privacy{AllowCommentsFrom: "friends"}, true},
		{"friends: blocked connection denied", Facts{IsConnection: true, Blocked: true}, Privacy{AllowCommentsFrom: "friends"}, false},
		{"unknown setting fails closed even for a connection", Facts{IsConnection: true}, Privacy{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(ActionComment, tc.facts, tc.privacy); got.Allowed != tc.want {
				t.Errorf("Allowed = %v, want %v (%+v)", got.Allowed, tc.want, got)
			}
		})
	}
}

func TestResolveViewPosts(t *testing.T) {
	cases := []struct {
		name    string
		facts   Facts
		privacy Privacy
		want    bool
	}{
		{"public: stranger may view", Facts{}, Privacy{AccountVisibility: "public"}, true},
		{"public: blocked denies", Facts{Blocked: true}, Privacy{AccountVisibility: "public"}, false},
		{"private: stranger denied", Facts{}, Privacy{AccountVisibility: "private"}, false},
		{"private: follower may view", Facts{ActorFollowsTarget: true}, Privacy{AccountVisibility: "private"}, true},
		{"private: being followed by the target is not enough", Facts{TargetFollowsActor: true}, Privacy{AccountVisibility: "private"}, false},
		{"private: blocked follower denied", Facts{ActorFollowsTarget: true, Blocked: true}, Privacy{AccountVisibility: "private"}, false},
		{"unknown visibility fails closed for a stranger", Facts{}, Privacy{}, false},
		{"unknown visibility still admits a follower", Facts{ActorFollowsTarget: true}, Privacy{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(ActionViewPosts, tc.facts, tc.privacy); got.Allowed != tc.want {
				t.Errorf("Allowed = %v, want %v (%+v)", got.Allowed, tc.want, got)
			}
		})
	}
}

func TestParseActionsKnowsPrivateAccountActions(t *testing.T) {
	got := ParseActions([]string{"view_posts", "comment", "nonsense"})
	if len(got) != 2 || got[0] != ActionViewPosts || got[1] != ActionComment {
		t.Errorf("ParseActions = %v, want [view_posts comment]", got)
	}
}
