package search

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// Per-group versions on users_v1 (user_profile_state.go, FIELD GROUPS):
// the username (handle_updated_us) and is_private (privacy_updated_us)
// are ordered against the full rewrites by their own event times.

func at(n int) int64 { return SourceVersion(userT0.Add(time.Duration(n) * time.Second)) }

func fullWrite(t *testing.T, doc map[string]any, d UserDoc) (map[string]any, bool) {
	t.Helper()
	return simulateUserUpsertScript(t, doc, userBody(t, d))
}

func handleWrite(t *testing.T, doc map[string]any, username string, v int64) (map[string]any, bool) {
	t.Helper()
	return simulateUserPartialScript(t, doc, userPartialBody(userGroupHandle, map[string]any{"username": username}, v))
}

func privacyWrite(t *testing.T, doc map[string]any, private bool, v int64) (map[string]any, bool) {
	t.Helper()
	return simulateUserPartialScript(t, doc, userPartialBody(userGroupPrivacy, map[string]any{"is_private": private}, v))
}

// THE bug: a UserProfileUpdated newer than the stored document but older
// than a HandleChanged put the old username back.
func TestUserHandle_LateFullRewriteCannotRestoreAnOldUsername(t *testing.T) {
	doc, _ := fullWrite(t, nil, UserDoc{UserID: userA, Username: "old", DisplayName: "d1", SourceVersion: at(1)})
	doc, wrote := handleWrite(t, doc, "new", at(3))
	if !wrote || doc["username"] != "new" || doc["handle_updated_us"] != at(3) {
		t.Fatalf("HandleChanged did not apply at its version: %v", doc)
	}
	// An edit committed at 2 (before the handle change), delivered late.
	doc, wrote = fullWrite(t, doc, UserDoc{UserID: userA, Username: "old", DisplayName: "d2", SourceVersion: at(2)})
	if !wrote || doc["display_name"] != "d2" || doc["source_updated_us"] != at(2) {
		t.Fatalf("the newer profile fields did not apply: %v", doc)
	}
	if doc["username"] != "new" || doc["handle_updated_us"] != at(3) {
		t.Fatalf("a late rewrite restored the old username: %v", doc)
	}
	// A rewrite after the handle change takes the username over.
	doc, _ = fullWrite(t, doc, UserDoc{UserID: userA, Username: "newer", DisplayName: "d4", SourceVersion: at(4)})
	if doc["username"] != "newer" || doc["handle_updated_us"] != at(4) {
		t.Fatalf("a newer rewrite did not write the username at its version: %v", doc)
	}
}

func TestUserHandle_OlderHandleChangedIsNoop(t *testing.T) {
	doc, _ := fullWrite(t, nil, UserDoc{UserID: userA, Username: "a", SourceVersion: at(1)})
	doc, _ = handleWrite(t, doc, "c", at(3))
	for _, v := range []int64{at(2), at(3)} { // older, and a redelivery
		if got, wrote := handleWrite(t, doc, "b", v); wrote || got["username"] != "c" {
			t.Fatalf("HandleChanged at %d over %d rewrote the username: %v", v, at(3), got)
		}
	}
	// Older than a full rewrite that already carried a newer username.
	doc, _ = fullWrite(t, doc, UserDoc{UserID: userA, Username: "e", SourceVersion: at(5)})
	if got, wrote := handleWrite(t, doc, "d", at(4)); wrote || got["username"] != "e" {
		t.Fatalf("a HandleChanged older than the rewrite applied: %v", got)
	}
	// A document from before the groups: the username falls back to
	// source_updated_us.
	legacy := map[string]any{"user_id": userA, "username": "kept", "source_updated_us": at(5)}
	if _, wrote := handleWrite(t, legacy, "stale", at(4)); wrote {
		t.Fatal("a HandleChanged older than a legacy document's version applied")
	}
	if got, wrote := handleWrite(t, legacy, "fresh", at(6)); !wrote || got["username"] != "fresh" || got["handle_updated_us"] != at(6) {
		t.Fatalf("a newer HandleChanged on a legacy document = %v", got)
	}
}

func TestUserPrivacy_OrderedLikeTheHandle(t *testing.T) {
	doc, _ := fullWrite(t, nil, UserDoc{UserID: userA, DisplayName: "d1", IsPrivate: false, SourceVersion: at(1)})
	doc, _ = privacyWrite(t, doc, true, at(3))
	// A rewrite at 2 carrying the old flag keeps the newer private.
	doc, _ = fullWrite(t, doc, UserDoc{UserID: userA, DisplayName: "d2", IsPrivate: false, SourceVersion: at(2)})
	if doc["is_private"] != true || doc["privacy_updated_us"] != at(3) || doc["display_name"] != "d2" {
		t.Fatalf("a late rewrite restored the old privacy: %v", doc)
	}
	// An older or redelivered settings event is a no-op.
	for _, v := range []int64{at(2), at(3)} {
		if got, wrote := privacyWrite(t, doc, false, v); wrote || got["is_private"] != true {
			t.Fatalf("settings at %d rewrote privacy: %v", v, got)
		}
	}
	// A newer rewrite (its lookup is current) takes it over.
	doc, _ = fullWrite(t, doc, UserDoc{UserID: userA, IsPrivate: false, SourceVersion: at(4)})
	if doc["is_private"] != false || doc["privacy_updated_us"] != at(4) {
		t.Fatalf("a newer rewrite did not write privacy: %v", doc)
	}
}

// An older producer without a timestamp still writes its field, as before
// versions, and stamps nothing. A partial never creates a document.
func TestUserPartial_UnversionedWritesAndMissingDocIsNotCreated(t *testing.T) {
	doc, _ := fullWrite(t, nil, UserDoc{UserID: userA, Username: "a", SourceVersion: at(1)})
	got, wrote := privacyWrite(t, doc, true, 0)
	if !wrote || got["is_private"] != true {
		t.Fatalf("an unversioned settings event was dropped: %v", got)
	}
	if _, ok := got["privacy_updated_us"]; ok && got["privacy_updated_us"] != at(1) {
		t.Fatalf("an unversioned partial stamped a version: %v", got)
	}
	if got, wrote := handleWrite(t, nil, "x", at(2)); wrote || got != nil {
		t.Fatalf("a partial created a document: %v", got)
	}
}

// A kept username does not cost the lifecycle hide.
func TestUserGroups_KeepAHideWhenAGroupIsKept(t *testing.T) {
	doc, _ := fullWrite(t, nil, UserDoc{UserID: userA, Username: "a", SourceVersion: at(1)})
	doc, _ = handleWrite(t, doc, "b", at(3))
	doc["is_hidden"] = true
	doc, _ = fullWrite(t, doc, UserDoc{UserID: userA, Username: "a", DisplayName: "d2", SourceVersion: at(2)})
	if doc["is_hidden"] != true || doc["username"] != "b" || doc["display_name"] != "d2" {
		t.Fatalf("stored = %v, want hidden, username b, profile d2", doc)
	}
}

// Every arrival order of one history ends at the newest value of each
// group. History: rewrite@1, handle@2, settings@3, rewrite@4 (reads the
// state after 2 and 3), handle@5.
func TestUserGroups_AnyArrivalOrderEndsAtTheNewestPerGroup(t *testing.T) {
	writes := []func(map[string]any) map[string]any{
		func(d map[string]any) map[string]any {
			d, _ = fullWrite(t, d, UserDoc{UserID: userA, Username: "a", DisplayName: "d1", SourceVersion: at(1)})
			return d
		},
		func(d map[string]any) map[string]any { d, _ = handleWrite(t, d, "b", at(2)); return d },
		func(d map[string]any) map[string]any { d, _ = privacyWrite(t, d, true, at(3)); return d },
		func(d map[string]any) map[string]any {
			d, _ = fullWrite(t, d, UserDoc{UserID: userA, Username: "b", DisplayName: "d4", IsPrivate: true, SourceVersion: at(4)})
			return d
		},
		func(d map[string]any) map[string]any { d, _ = handleWrite(t, d, "c", at(5)); return d },
	}
	var perms [][]int
	var permute func([]int, int)
	permute = func(a []int, k int) {
		if k == len(a) {
			perms = append(perms, append([]int(nil), a...))
			return
		}
		for i := k; i < len(a); i++ {
			a[k], a[i] = a[i], a[k]
			permute(a, k+1)
			a[k], a[i] = a[i], a[k]
		}
	}
	permute([]int{0, 1, 2, 3, 4}, 0)
	for _, p := range perms {
		// The partials cannot create the document: the first full rewrite
		// to arrive creates it, and a partial before it is lost (404),
		// unless a later rewrite or partial carries the same state.
		var doc map[string]any
		for _, i := range p {
			doc = writes[i](doc)
		}
		if doc["display_name"] != "d4" || doc["is_private"] != true || doc["source_updated_us"] != at(4) {
			t.Fatalf("order %v: profile/privacy = %v", p, doc)
		}
		// The handle at 5 is lost only when it arrived before any document.
		firstFull := -1
		for j, i := range p {
			if i == 0 || i == 3 {
				firstFull = j
				break
			}
		}
		handle5Pos := -1
		for j, i := range p {
			if i == 4 {
				handle5Pos = j
			}
		}
		want := "c"
		if handle5Pos < firstFull {
			want = "b"
		}
		if doc["username"] != want {
			t.Fatalf("order %v: username = %v, want %s (%v)", p, doc["username"], want, doc)
		}
	}
}

// decideUserRewrite is the Go statement of the script's group decision.
func TestDecideUserRewrite(t *testing.T) {
	src := map[string]any{"source_updated_us": at(2), "handle_updated_us": at(5)}
	got := decideUserRewrite(src, at(3))
	if !got[userGroupProfile] || got[userGroupHandle] || !got[userGroupPrivacy] {
		t.Fatalf("decide = %v, want profile and privacy (fallback to source), not handle", got)
	}
	if got := decideUserRewrite(nil, 0); !got[userGroupProfile] || !got[userGroupHandle] || !got[userGroupPrivacy] {
		t.Fatalf("a first unversioned write must write every group: %v", got)
	}
}

// The partial writers send the ordered script with their version, and the
// versions never reach a grouped result.
func TestUserPartialWriters_CarryTheirVersion(t *testing.T) {
	store, rec := newRecordingStore(t)
	ctx := context.Background()
	if err := store.UpdateUserUsername(ctx, userA, "asha", at(3)); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateUserPrivacy(ctx, userB, true, at(4)); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]struct {
		field string
		v     int64
		key   string
	}{userA: {"handle_updated_us", at(3), "username"}, userB: {"privacy_updated_us", at(4), "is_private"}} {
		reqs := rec.matching(http.MethodPost, "/"+IndexUsers+"/_update/"+id)
		if len(reqs) != 1 {
			t.Fatalf("%s: requests = %d", id, len(reqs))
		}
		var body struct {
			Upsert any `json:"upsert"`
			Script struct {
				Source string `json:"source"`
				Params struct {
					Version      int64           `json:"version"`
					VersionField string          `json:"version_field"`
					Apply        map[string]bool `json:"apply"`
					Doc          map[string]any  `json:"doc"`
				} `json:"params"`
			} `json:"script"`
		}
		if err := json.Unmarshal(reqs[0].raw, &body); err != nil {
			t.Fatal(err)
		}
		p := body.Script.Params
		if body.Script.Source != userPartialScript || p.Version != want.v || p.VersionField != want.field || p.Doc[want.key] == nil {
			t.Fatalf("%s: body = %s", id, reqs[0].raw)
		}
		if body.Upsert != nil || p.Apply[string(orderOlder)] || p.Apply[string(orderSame)] || !p.Apply[string(orderNewer)] {
			t.Fatalf("%s: upsert %v apply %v", id, body.Upsert, p.Apply)
		}
	}

	item := map[string]any{"user_id": userA, "source_updated_us": 1, "handle_updated_us": 2, "privacy_updated_us": 3}
	StripUserVersions(item)
	if len(item) != 1 {
		t.Fatalf("versions left on a result: %v", item)
	}
}
