package service

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// Join notices (joins.go): viewer.joined on a move to present, at most once
// per user per stream per minute, never for the host, an anonymous
// identity, a viewer the room gate refuses, or a stream without open chat.

// joinRig is a rig whose service reads the fake clock and a fake directory.
func joinRig(t *testing.T) (*rig, uuid.UUID, *postgres.LiveStream, *fakeProfiles) {
	t.Helper()
	host := uuid.New()
	r := newRig(host)
	r.svc.now = r.clock.Now
	profiles := &fakeProfiles{m: map[uuid.UUID]Profile{}}
	r.svc.profiles = profiles
	st := r.store.AddStream(host) // live
	return r, host, st, profiles
}

func joinEv(st *postgres.LiveStream, identity string) WebhookEvent {
	return WebhookEvent{ID: uuid.NewString(), Event: "participant_joined", Room: st.LiveKitRoom, ParticipantIdentity: identity}
}

func leaveEv(st *postgres.LiveStream, identity string) WebhookEvent {
	return WebhookEvent{ID: uuid.NewString(), Event: "participant_left", Room: st.LiveKitRoom, ParticipantIdentity: identity}
}

func mustHook(t *testing.T, r *rig, ev WebhookEvent) {
	t.Helper()
	if err := r.svc.HandleWebhook(ctx, ev); err != nil {
		t.Fatal(err)
	}
}

func joinedCount(r *rig) int { return len(r.ev.ofType(EventViewerJoined)) }

// TestViewerJoinedPayloadShape: the frame carries the envelope, user_id, the
// chat author card under `user`, joined_at, and the same fields again under
// payload.
func TestViewerJoinedPayloadShape(t *testing.T) {
	r, _, st, profiles := joinRig(t)
	v := uuid.New()
	profiles.m[v] = Profile{Name: "Asha Rao", Handle: "asha", AvatarURL: "/v1/media/avatar/1"}
	mustHook(t, r, joinEv(st, v.String()))

	evs := r.ev.ofType(EventViewerJoined)
	if len(evs) != 1 {
		t.Fatalf("viewer.joined frames = %d, want 1", len(evs))
	}
	e := evs[0]
	if e["stream_id"] != st.ID.String() || e["user_id"] != v.String() {
		t.Fatalf("envelope = %v", e)
	}
	if _, err := time.Parse(time.RFC3339, e["at"].(string)); err != nil {
		t.Fatalf("at = %v", e["at"])
	}
	if got, want := e["joined_at"], r.clock.Now().UTC().Format(time.RFC3339); got != want {
		t.Fatalf("joined_at = %v, want %v", got, want)
	}
	user, _ := e["user"].(map[string]any)
	if user["user_id"] != v.String() || user["name"] != "Asha Rao" || user["handle"] != "asha" ||
		user["avatar_url"] != "/v1/media/avatar/1" || user["role"] != postgres.ChatRoleViewer {
		t.Fatalf("user = %v", user)
	}
	if b, ok := user["badges"].([]any); !ok || len(b) != 0 {
		t.Fatalf("badges = %#v, want []", user["badges"])
	}
	p, _ := e["payload"].(map[string]any)
	pu, _ := p["user"].(map[string]any)
	if p["user_id"] != v.String() || p["stream_id"] != st.ID.String() || p["joined_at"] != e["joined_at"] || pu["name"] != "Asha Rao" {
		t.Fatalf("payload = %v", p)
	}
	// Exactly these keys: nothing beyond the contract leaks.
	want := map[string]bool{"type": true, "stream_id": true, "at": true, "user_id": true, "user": true, "joined_at": true, "payload": true}
	for k := range e {
		if !want[k] {
			t.Fatalf("unexpected key %q in %v", k, e)
		}
	}
	// The payload matches viewerJoinedPayload exactly.
	raw, _ := json.Marshal(p)
	var typed viewerJoinedPayload
	if err := json.Unmarshal(raw, &typed); err != nil || typed.UserID != v || typed.User == nil || typed.User.UserID != v {
		t.Fatalf("payload does not decode: %v %+v", err, typed)
	}
}

// TestViewerJoinedRoleAndFailedLookup: a moderator is announced as one; a
// failed profile lookup still announces user_id and role, never an error.
func TestViewerJoinedRoleAndFailedLookup(t *testing.T) {
	r, host, st, profiles := joinRig(t)
	mod := uuid.New()
	if err := r.store.ReplaceModerators(ctx, st.ID, []uuid.UUID{mod}, host); err != nil {
		t.Fatal(err)
	}
	profiles.err = errors.New("identity-profile is down")
	mustHook(t, r, joinEv(st, mod.String()))
	evs := r.ev.ofType(EventViewerJoined)
	if len(evs) != 1 {
		t.Fatalf("frames = %d", len(evs))
	}
	user, _ := evs[0]["user"].(map[string]any)
	if user["user_id"] != mod.String() || user["role"] != postgres.ChatRoleModerator {
		t.Fatalf("user = %v", user)
	}
	if _, ok := user["name"]; ok {
		t.Fatalf("a failed lookup must leave the name off: %v", user)
	}
}

// TestViewerJoinedThrottledPerUserPerMinute: leave/rejoin inside a minute is
// announced once; after the minute, again. Other viewers and other streams
// have their own budget.
func TestViewerJoinedThrottledPerUserPerMinute(t *testing.T) {
	r, host, st, _ := joinRig(t)
	v := uuid.NewString()
	for i := 0; i < 4; i++ {
		mustHook(t, r, joinEv(st, v))
		r.clock.Advance(10 * time.Second)
		mustHook(t, r, leaveEv(st, v))
		r.clock.Advance(5 * time.Second)
	}
	if n := joinedCount(r); n != 1 {
		t.Fatalf("frames in a minute of flapping = %d, want 1", n)
	}
	r.clock.Advance(time.Minute)
	mustHook(t, r, joinEv(st, v))
	if n := joinedCount(r); n != 2 {
		t.Fatalf("frames after the minute = %d, want 2", n)
	}
	// Another viewer in the same minute is announced.
	mustHook(t, r, joinEv(st, uuid.NewString()))
	if n := joinedCount(r); n != 3 {
		t.Fatalf("a second viewer: frames = %d, want 3", n)
	}
	// The same viewer in another stream is announced.
	other := r.store.AddStream(host)
	mustHook(t, r, leaveEv(st, v))
	mustHook(t, r, joinEv(other, v))
	if n := joinedCount(r); n != 4 {
		t.Fatalf("same viewer, other stream: frames = %d, want 4", n)
	}
	if got := r.ev.allowed[viewerJoinedKey(st.ID, uuid.MustParse(v))]; got.IsZero() {
		t.Fatal("the throttle key is not the per-stream per-user key")
	}
}

// TestViewerJoinedRepeatJoinDoesNotReEmit: a second participant_joined for a
// viewer already present (a reconnect, a refresh, a redelivery) is no
// transition and announces nothing, even with the throttle off.
func TestViewerJoinedRepeatJoinDoesNotReEmit(t *testing.T) {
	r, _, st, _ := joinRig(t)
	r.ev.allowAll = true
	v := uuid.NewString()
	ev := joinEv(st, v)
	mustHook(t, r, ev)
	mustHook(t, r, ev) // the same webhook id redelivered
	for i := 0; i < 3; i++ {
		r.clock.Advance(2 * time.Minute)
		mustHook(t, r, joinEv(st, v)) // a fresh id, still present
	}
	if n := joinedCount(r); n != 1 {
		t.Fatalf("frames = %d, want 1", n)
	}
	// A real leave then join is a new transition.
	mustHook(t, r, leaveEv(st, v))
	mustHook(t, r, joinEv(st, v))
	if n := joinedCount(r); n != 2 {
		t.Fatalf("after leave+join: frames = %d, want 2", n)
	}
}

// TestViewerJoinedNeverForHost: the creator's connection, on a device
// stream or watching their own encoder stream, is never announced.
func TestViewerJoinedNeverForHost(t *testing.T) {
	r, host, st, _ := joinRig(t)
	r.ev.allowAll = true
	mustHook(t, r, joinEv(st, host.String()))
	// And directly, should a host identity ever reach presence.
	r.svc.announceJoin(ctx, r.store.Stream(st.ID), host)
	if n := joinedCount(r); n != 0 {
		t.Fatalf("host announced %d times", n)
	}
}

// TestViewerJoinedNotForAnonymousOrNonUser: a non-user identity (egress, a
// signed-out viewer's nil id) never reaches presence or the room.
func TestViewerJoinedNotForAnonymousOrNonUser(t *testing.T) {
	r, _, st, _ := joinRig(t)
	r.ev.allowAll = true
	mustHook(t, r, joinEv(st, uuid.Nil.String()))
	mustHook(t, r, joinEv(st, "EG_egress"))
	mustHook(t, r, joinEv(st, ""))
	if n := joinedCount(r); n != 0 {
		t.Fatalf("anonymous/non-user announced %d times", n)
	}
}

// TestViewerJoinedNotWhenChatClosed: an ended, failed, scheduled or
// still-starting stream announces nothing; reconnecting does.
func TestViewerJoinedNotWhenChatClosed(t *testing.T) {
	for _, status := range []string{stEnded, stFailed, stScheduled, stStarting} {
		r, host, _, _ := joinRig(t)
		r.ev.allowAll = true
		st := r.store.AddStreamStatus(host, status)
		mustHook(t, r, joinEv(st, uuid.NewString()))
		if n := joinedCount(r); n != 0 {
			t.Fatalf("%s: announced %d times", status, n)
		}
	}
	r, host, _, _ := joinRig(t)
	st := r.store.AddStreamStatus(host, stReconnecting)
	mustHook(t, r, joinEv(st, uuid.NewString()))
	if n := joinedCount(r); n != 1 {
		t.Fatalf("reconnecting: announced %d times, want 1", n)
	}
}

// TestViewerJoinedNotAfterEnd: a join that lands after the host ended the
// stream announces nothing.
func TestViewerJoinedNotAfterEnd(t *testing.T) {
	r, host, st, _ := joinRig(t)
	r.ev.allowAll = true
	if _, err := r.svc.EndStream(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	mustHook(t, r, joinEv(st, uuid.NewString()))
	if n := joinedCount(r); n != 0 {
		t.Fatalf("announced %d times after the end", n)
	}
}

// TestViewerJoinedRespectsRoomGate: a viewer blocked either way with the
// host, banned from the stream or platform live-banned is not announced,
// and neither is anyone while the gate cannot be decided.
func TestViewerJoinedRespectsRoomGate(t *testing.T) {
	cases := map[string]func(r *rig, host, v uuid.UUID, st *postgres.LiveStream){
		"blocked": func(r *rig, host, v uuid.UUID, _ *postgres.LiveStream) {
			r.graph.blocked[v.String()+":"+host.String()] = true
		},
		"stream ban": func(r *rig, host, v uuid.UUID, st *postgres.LiveStream) {
			if err := r.store.BanFromStream(ctx, st.ID, v, host, ""); err != nil {
				t.Fatal(err)
			}
		},
		"platform ban": func(r *rig, _, v uuid.UUID, _ *postgres.LiveStream) {
			r.store.PlatformBans[v] = postgres.PlatformBan{UserID: v}
		},
		"graph down": func(r *rig, _, _ uuid.UUID, _ *postgres.LiveStream) {
			r.graph.err = errors.New("graph-service is down")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			r, host, st, _ := joinRig(t)
			r.ev.allowAll = true
			v := uuid.New()
			setup(r, host, v, st)
			mustHook(t, r, joinEv(st, v.String()))
			if n := joinedCount(r); n != 0 {
				t.Fatalf("announced %d times", n)
			}
			// The presence itself is still recorded (the count is truthful).
			if got := r.store.Stream(st.ID); got.ViewerCount != 1 {
				t.Fatalf("viewer_count = %d, want 1", got.ViewerCount)
			}
		})
	}
}

// TestViewerJoinedWithoutRealtime: no room events configured, no panic.
func TestViewerJoinedWithoutRealtime(t *testing.T) {
	r, _, st, _ := joinRig(t)
	r.svc.rt = nil
	mustHook(t, r, joinEv(st, uuid.NewString()))
}
