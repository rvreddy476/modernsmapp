package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
)

// Communities invite-only pilot (2026-09-12). Every guard below is the
// enforcement point for one line of the founder's decision, so each test
// names the line it defends.

func envMap(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

// The pilot IS the current state, so an unset environment must produce the
// pilot — not the open product.
func TestLoadCommunityPolicy_DefaultsToThePilot(t *testing.T) {
	p, warnings := LoadCommunityPolicy(envMap(nil))
	if !p.Enabled {
		t.Fatal("COMMUNITIES_ENABLED must default to true")
	}
	if !p.PilotMode {
		t.Fatal("COMMUNITIES_PILOT must default to true — the pilot is the current state")
	}
	if p.CreatorAllowlistConfigured || len(p.AllowedCreators) != 0 {
		t.Fatalf("no allowlist expected by default: %+v", p)
	}
	// The "no named moderation owner yet" state has to be loud.
	if !containsSubstring(warnings, "COMMUNITIES_ALLOWED_CREATORS is EMPTY") {
		t.Fatalf("empty allowlist must warn loudly; got %v", warnings)
	}
}

func TestLoadCommunityPolicy_ParsesFlags(t *testing.T) {
	owner := uuid.New()
	other := uuid.New()
	p, _ := LoadCommunityPolicy(envMap(map[string]string{
		"COMMUNITIES_ENABLED":          "false",
		"COMMUNITIES_PILOT":            "true",
		"COMMUNITIES_ALLOWED_CREATORS": " " + owner.String() + " , " + other.String() + " ,",
		"COMMUNITIES_INVITE_BASE_URL":  "https://momentum.app/c/",
	}))
	if p.Enabled {
		t.Fatal("COMMUNITIES_ENABLED=false not honoured")
	}
	if !p.CreatorAllowlistConfigured || len(p.AllowedCreators) != 2 {
		t.Fatalf("allowlist parse: %+v", p)
	}
	if !p.CreatorAllowed(owner) || !p.CreatorAllowed(other) {
		t.Fatal("allowlisted creator refused")
	}
	if p.CreatorAllowed(uuid.New()) {
		t.Fatal("a user outside the allowlist may not create a community")
	}
	if p.InviteBaseURL != "https://momentum.app/c/" {
		t.Fatalf("invite base url: %q", p.InviteBaseURL)
	}
}

// An empty allowlist restricts nobody (so dev keeps working) but a
// non-empty allowlist whose entries are all junk must fail CLOSED, not
// silently reopen creation to everyone.
func TestLoadCommunityPolicy_AllowlistFailsClosedOnJunk(t *testing.T) {
	p, warnings := LoadCommunityPolicy(envMap(map[string]string{
		"COMMUNITIES_ALLOWED_CREATORS": "not-a-uuid,also-bad",
	}))
	if !p.CreatorAllowlistConfigured {
		t.Fatal("a non-empty allowlist env must count as configured")
	}
	if p.CreatorAllowed(uuid.New()) {
		t.Fatal("an allowlist that parsed to zero ids must refuse EVERYONE")
	}
	if !containsSubstring(warnings, "closed to EVERYONE") {
		t.Fatalf("fail-closed allowlist must warn; got %v", warnings)
	}
}

func TestLoadCommunityPolicy_PilotOffIsLoud(t *testing.T) {
	p, warnings := LoadCommunityPolicy(envMap(map[string]string{"COMMUNITIES_PILOT": "0"}))
	if p.PilotMode {
		t.Fatal("COMMUNITIES_PILOT=0 not honoured")
	}
	if !containsSubstring(warnings, "NOT in the invite-only pilot") {
		t.Fatalf("leaving the pilot must warn; got %v", warnings)
	}
	// With the pilot off, nothing is restricted — the pre-pilot behaviour.
	if !p.CreatorAllowed(uuid.New()) {
		t.Fatal("pilot off must not restrict creation")
	}
}

// Task 1: an omitted visibility becomes PRIVATE under the pilot (it used to
// become public), and anything publicly visible is refused outright.
func TestResolveCreateChannelType_PilotPrivateByDefault(t *testing.T) {
	pilot := CommunityPolicy{Enabled: true, PilotMode: true}

	got, err := pilot.ResolveCreateChannelType("", "")
	if err != nil || got != "private" {
		t.Fatalf("omitted visibility must default to private under the pilot; got %q %v", got, err)
	}
	got, err = pilot.ResolveCreateChannelType("private", "")
	if err != nil || got != "private" {
		t.Fatalf("visibility=private: %q %v", got, err)
	}
	// paid maps to private visibility, so it is not a public community.
	if got, err = pilot.ResolveCreateChannelType("", "paid"); err != nil || got != "paid" {
		t.Fatalf("channel_type=paid: %q %v", got, err)
	}

	// Every publicly visible variety is refused, by either wire field.
	for _, tc := range []struct{ visibility, channelType string }{
		{"public", ""},
		{"", "public"},
		{"", "creator"},
		{"", "brand"},
		{"", "education"},
		{"", "official"},
		{"", "topic"},
		{"public", "private"}, // visibility wins over channel_type
	} {
		if _, err := pilot.ResolveCreateChannelType(tc.visibility, tc.channelType); !errors.Is(err, ErrPublicCommunityNotAllowed) {
			t.Fatalf("visibility=%q channel_type=%q must be refused with ErrPublicCommunityNotAllowed; got %v",
				tc.visibility, tc.channelType, err)
		}
	}

	// Validation still happens, and outside the pilot the old default holds.
	if _, err := pilot.ResolveCreateChannelType("", "nonsense"); err == nil {
		t.Fatal("an invalid channel_type must still be refused")
	}
	if _, err := pilot.ResolveCreateChannelType("secret", ""); err == nil {
		t.Fatal("an invalid visibility must still be refused")
	}
	open := CommunityPolicy{Enabled: true, PilotMode: false}
	if got, err := open.ResolveCreateChannelType("", ""); err != nil || got != "public" {
		t.Fatalf("outside the pilot the historical default is public; got %q %v", got, err)
	}
	if got, err := open.ResolveCreateChannelType("public", ""); err != nil || got != "public" {
		t.Fatalf("outside the pilot a public community is allowed; got %q %v", got, err)
	}
}

// Task 1, the update asymmetry the audit found: a raw channel_type on PUT
// must not be able to flip a private community public.
func TestGuardVisibilityChange_NoPrivateToPublicFlip(t *testing.T) {
	pilot := CommunityPolicy{Enabled: true, PilotMode: true}

	for _, requested := range []string{"public", "creator", "brand", "education", "official", "topic"} {
		if err := pilot.GuardVisibilityChange("private", requested); !errors.Is(err, ErrPublicCommunityNotAllowed) {
			t.Fatalf("private -> %s must be refused; got %v", requested, err)
		}
	}
	// Staying private, and private <-> paid, are fine.
	if err := pilot.GuardVisibilityChange("private", "private"); err != nil {
		t.Fatalf("private -> private refused: %v", err)
	}
	if err := pilot.GuardVisibilityChange("private", "paid"); err != nil {
		t.Fatalf("private -> paid refused: %v", err)
	}
	// A legacy public channel that stays public is untouched: the pilot
	// stops new exposure, it does not brick existing rows.
	if err := pilot.GuardVisibilityChange("public", "public"); err != nil {
		t.Fatalf("public -> public refused: %v", err)
	}
	if err := pilot.GuardVisibilityChange("public", "private"); err != nil {
		t.Fatalf("public -> private (tightening) refused: %v", err)
	}
	// Outside the pilot nothing is blocked.
	open := CommunityPolicy{Enabled: true, PilotMode: false}
	if err := open.GuardVisibilityChange("private", "public"); err != nil {
		t.Fatalf("pilot off must allow the flip: %v", err)
	}
}

// Task 3: a direct POST /{id}/subscribe on a private community is refused
// with INVITE_REQUIRED. This replaces the literal `_ = ch` hole.
func TestGuardDirectSubscribe(t *testing.T) {
	pilot := CommunityPolicy{Enabled: true, PilotMode: true}
	for _, private := range []string{"private", "paid"} {
		if err := pilot.GuardDirectSubscribe(private); !errors.Is(err, ErrInviteRequired) {
			t.Fatalf("direct subscribe to %s must need an invite; got %v", private, err)
		}
	}
	// Legacy public rows keep working — none can be created during the
	// pilot, but they exist.
	for _, public := range []string{"public", "creator", "brand", "education", "official", "topic"} {
		if err := pilot.GuardDirectSubscribe(public); err != nil {
			t.Fatalf("direct subscribe to legacy %s refused: %v", public, err)
		}
	}
	open := CommunityPolicy{Enabled: true, PilotMode: false}
	if err := open.GuardDirectSubscribe("private"); err != nil {
		t.Fatalf("pilot off must allow a direct private subscribe: %v", err)
	}
}

// Task 6: `suspended` has to mean something. GetChannelByID/GetMyChannels
// only exclude 'deleted' at SQL level, so this is the gate that makes the
// per-channel emergency disable real.
func TestChannelIsServable(t *testing.T) {
	if !channelIsServable("active") {
		t.Fatal("an active channel must be servable")
	}
	for _, status := range []string{"suspended", "archived", "deleted", ""} {
		if channelIsServable(status) {
			t.Fatalf("status %q must not be servable", status)
		}
	}
}

// Task 6: a suspended channel must disappear from GET /{id}. This is the
// whole read decision GetChannel delegates to.
func TestChannelReadable_SuspensionAndPrivacy(t *testing.T) {
	active := func(ct string) *store.BroadcastChannel {
		return &store.BroadcastChannel{Status: "active", ChannelType: ct}
	}

	// A suspended community reads as NOT FOUND for EVERYONE, its owner
	// included — that is what makes the emergency switch an emergency
	// switch.
	for _, status := range []string{"suspended", "archived", "deleted"} {
		ch := &store.BroadcastChannel{Status: status, ChannelType: "public"}
		for _, viewer := range []struct {
			isOwner bool
			role    string
		}{{true, "owner"}, {false, "admin"}, {false, "subscriber"}, {false, ""}} {
			if err := channelReadable(ch, viewer.isOwner, viewer.role); !errors.Is(err, ErrChannelNotFound) {
				t.Fatalf("status=%q owner=%v role=%q must read as not found; got %v",
					status, viewer.isOwner, viewer.role, err)
			}
		}
	}
	if err := channelReadable(nil, true, "owner"); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("a nil channel must read as not found; got %v", err)
	}

	// An active public channel is readable by anyone, including an
	// unauthenticated caller.
	if err := channelReadable(active("public"), false, ""); err != nil {
		t.Fatalf("an active public channel must be readable by anyone: %v", err)
	}

	// An active private channel: owner and members yes, everyone else no
	// (audit CCh3 — an outsider must not learn it exists).
	for _, private := range []string{"private", "paid"} {
		if err := channelReadable(active(private), true, ""); err != nil {
			t.Fatalf("the owner must be able to read their own %s community: %v", private, err)
		}
		for _, role := range []string{"admin", "editor", "moderator", "subscriber"} {
			if err := channelReadable(active(private), false, role); err != nil {
				t.Fatalf("a %s must be able to read a %s community: %v", role, private, err)
			}
		}
		for _, role := range []string{"", "banned"} {
			if err := channelReadable(active(private), false, role); !errors.Is(err, ErrChannelNotFound) {
				t.Fatalf("role %q must not read a %s community; got %v", role, private, err)
			}
		}
	}
}

// Task 6: a suspended channel must disappear from /my — for its owner too.
func TestServableChannelsFiltersSuspended(t *testing.T) {
	live := uuid.New()
	in := []store.BroadcastChannel{
		{ID: live, Status: "active"},
		{ID: uuid.New(), Status: "suspended"},
		{ID: uuid.New(), Status: "archived"},
		{ID: uuid.New(), Status: "deleted"},
	}
	out := servableChannels(in)
	if len(out) != 1 || out[0].ID != live {
		t.Fatalf("/my must list only active channels; got %+v", out)
	}
	if got := servableChannels(nil); got == nil || len(got) != 0 {
		t.Fatalf("an empty roster must stay an empty slice, not nil: %v", got)
	}
}

// Task 4: the authority rules for removal and banning.
func TestGuardMemberModeration(t *testing.T) {
	owner := uuid.New()
	adminA := uuid.New()
	adminB := uuid.New()
	sub := uuid.New()
	stranger := uuid.New()

	// Owner may ban an admin and a subscriber.
	if err := guardMemberModeration("owner", "admin", owner, adminA, owner); err != nil {
		t.Fatalf("owner banning an admin refused: %v", err)
	}
	if err := guardMemberModeration("owner", "subscriber", owner, sub, owner); err != nil {
		t.Fatalf("owner banning a subscriber refused: %v", err)
	}
	// Admin may ban a subscriber.
	if err := guardMemberModeration("admin", "subscriber", adminA, sub, owner); err != nil {
		t.Fatalf("admin banning a subscriber refused: %v", err)
	}
	// Admin may NOT ban another admin.
	if err := guardMemberModeration("admin", "admin", adminA, adminB, owner); !errors.Is(err, ErrCannotModeratePeerAdmin) {
		t.Fatalf("admin banning a peer admin must be refused; got %v", err)
	}
	// Nobody may ban the owner — including via a stale member row.
	if err := guardMemberModeration("admin", "subscriber", adminA, owner, owner); !errors.Is(err, ErrCannotModerateOwner) {
		t.Fatalf("banning the owner must be refused; got %v", err)
	}
	if err := guardMemberModeration("admin", "owner", adminA, sub, owner); !errors.Is(err, ErrCannotModerateOwner) {
		t.Fatalf("an owner-role target must be refused; got %v", err)
	}
	// Nobody may ban themselves.
	if err := guardMemberModeration("admin", "admin", adminA, adminA, owner); !errors.Is(err, ErrCannotModerateSelf) {
		t.Fatalf("self-ban must be refused; got %v", err)
	}
	if err := guardMemberModeration("owner", "owner", owner, owner, owner); !errors.Is(err, ErrCannotModerateSelf) {
		t.Fatalf("owner self-ban must be refused; got %v", err)
	}
	// A subscriber, moderator, editor or non-member may not moderate.
	for _, role := range []string{"", "subscriber", "moderator", "editor", "banned"} {
		if err := guardMemberModeration(role, "subscriber", stranger, sub, owner); err == nil {
			t.Fatalf("role %q must not be able to remove or ban", role)
		}
	}
	// The owner's authority comes from owner_id, not from a member row
	// (older databases can be missing the owner row entirely).
	if err := guardMemberModeration("", "admin", owner, adminA, owner); err != nil {
		t.Fatalf("owner with no member row refused: %v", err)
	}
}

// Task 3: invite code shape and liveness.
func TestInviteCodeGenerationAndValidation(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := GenerateInviteCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != InviteCodeLength {
			t.Fatalf("code %q is not %d characters", code, InviteCodeLength)
		}
		if !ValidInviteCode(code) {
			t.Fatalf("generated code %q fails its own validator", code)
		}
		seen[code] = true
	}
	if len(seen) < 190 {
		t.Fatalf("codes are not random enough: %d distinct out of 200", len(seen))
	}
	for _, bad := range []string{"", "SHORT", "abcdefghij", "ABCDEFGHI1", "ABCDEFGHI0", "ABCDEFGHIJK", "ABCDEFGH-J"} {
		if ValidInviteCode(bad) {
			t.Fatalf("code %q must be rejected", bad)
		}
	}
}

func TestInviteIsLive(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	two := 2
	zero := 0

	if inviteIsLive(nil, now) {
		t.Fatal("a nil invite is not live")
	}
	if !inviteIsLive(&store.ChannelInvite{ExpiresAt: &future}, now) {
		t.Fatal("an unexpired, unrevoked invite must be live")
	}
	if !inviteIsLive(&store.ChannelInvite{}, now) {
		t.Fatal("an invite with no expiry and no cap must be live")
	}
	if inviteIsLive(&store.ChannelInvite{ExpiresAt: &past}, now) {
		t.Fatal("an expired invite must not be live")
	}
	if inviteIsLive(&store.ChannelInvite{RevokedAt: &past, ExpiresAt: &future}, now) {
		t.Fatal("a revoked invite must not be live")
	}
	if inviteIsLive(&store.ChannelInvite{ExpiresAt: &future, MaxUses: &two, Uses: 2}, now) {
		t.Fatal("an exhausted invite must not be live")
	}
	if !inviteIsLive(&store.ChannelInvite{ExpiresAt: &future, MaxUses: &two, Uses: 1}, now) {
		t.Fatal("an invite with a use left must be live")
	}
	if inviteIsLive(&store.ChannelInvite{MaxUses: &zero}, now) {
		t.Fatal("max_uses=0 leaves no uses")
	}
	// The expiry boundary is exclusive: expires_at == now is spent.
	if inviteIsLive(&store.ChannelInvite{ExpiresAt: &now}, now) {
		t.Fatal("expires_at == now must not be live")
	}
}

// Task 5: the report queue's keyset cursor must round-trip and must not
// crash on junk.
func TestReportCursorRoundTrip(t *testing.T) {
	ts := time.Date(2026, 9, 12, 8, 30, 15, 123456789, time.UTC)
	id := uuid.New()
	cur := store.EncodeReportCursor(ts, id)
	gotTS, gotID, ok := store.DecodeReportCursor(cur)
	if !ok || !gotTS.Equal(ts) || gotID != id {
		t.Fatalf("cursor round trip: %v %v %v", gotTS, gotID, ok)
	}
	for _, bad := range []string{"", "!!!", "Zm9v", store.EncodeReportCursor(ts, id) + "x"} {
		if _, _, ok := store.DecodeReportCursor(bad); ok {
			t.Fatalf("junk cursor %q accepted", bad)
		}
	}
}

func containsSubstring(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

// Every owner/admin write path must be gated on channel status, not just
// the invite routes.
//
// This is a source-level assertion because the alternative is a database.
// It exists because a live probe found the gap: with the read paths gated
// and requireAdmin ungated, POST /{id}/updates on a SUSPENDED community
// answered 201 and published. The fan-out roster join withheld the
// notifications, which is worse than failing outright — the content lands
// in the community and nobody suspects it was never delivered.
//
// The gate now lives inside requireAdmin and requireOwner, so a write path
// added later is gated by default. If someone moves it back out to the call
// sites, this test says so.
func TestOwnerAndAdminGatesCheckChannelStatus(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("community.go"))
	if err != nil {
		t.Fatalf("cannot read community.go: %v", err)
	}
	for _, fn := range []string{"requireOwner", "requireAdmin"} {
		body, ok := funcBody(string(src), "func (s *Service) "+fn+"(")
		if !ok {
			t.Fatalf("%s not found in community.go; if it was renamed, update this test", fn)
		}
		if !strings.Contains(body, "channelIsServable(ch.Status)") {
			t.Errorf("%s does not gate on channel status. A suspended community could be "+
				"published to or administered, which is the one thing a suspension must stop.", fn)
		}
	}
}

// funcBody returns the text from the start of the named function to the
// next top-level `func ` after it.
func funcBody(src, signature string) (string, bool) {
	i := strings.Index(src, signature)
	if i < 0 {
		return "", false
	}
	rest := src[i+len(signature):]
	if j := strings.Index(rest, "\nfunc "); j >= 0 {
		return rest[:j], true
	}
	return rest, true
}
