package http

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/atpost/channel-service/internal/service"
)

/*
A field the client can send must survive all three layers.

Updating a channel passes through three separate structs and one SQL
statement: UpdateChannelRequest (what the handler binds), UpdateChannelParams
(what the service applies) and the UPDATE in store.UpdateChannel (what is
actually written). `handle` was missing from ALL THREE, so the settings
screen's @username field was dropped before it reached any of them — and the
request still answered 200, because JSON binding ignores what it does not
recognise.

That is the shape of the bug the founder reported as "save changes is not
working": the save worked, some of the fields did not, and nothing said so.
A silently partial update is worse than a failure, because a failure can be
retried and this could not.

These guards make the three layers agree.
*/

// Every JSON field the handler accepts must reach the service.
func TestEveryRequestFieldReachesTheService(t *testing.T) {
	req := reflect.TypeOf(UpdateChannelRequest{})
	params := reflect.TypeOf(service.UpdateChannelParams{})

	paramTags := map[string]bool{}
	for i := 0; i < params.NumField(); i++ {
		if tag := jsonName(params.Field(i).Tag.Get("json")); tag != "" {
			paramTags[tag] = true
		}
	}

	for i := 0; i < req.NumField(); i++ {
		name := jsonName(req.Field(i).Tag.Get("json"))
		if name == "" {
			continue
		}
		if !paramTags[name] {
			t.Errorf("UpdateChannelRequest accepts %q but UpdateChannelParams has no such field — it is bound from the body and then dropped, and the request still answers 200", name)
		}
	}
}

// And the handler must actually copy it across: a field present in both
// structs but never assigned is the same silent drop with extra steps.
func TestHandlerCopiesEveryFieldItAccepts(t *testing.T) {
	body := funcBody(t, "handler.go", "UpdateChannel")
	req := reflect.TypeOf(UpdateChannelRequest{})

	for i := 0; i < req.NumField(); i++ {
		field := req.Field(i)
		if jsonName(field.Tag.Get("json")) == "" {
			continue
		}
		if !strings.Contains(body, "req."+field.Name) {
			t.Errorf("UpdateChannel binds %s but never assigns req.%s into the service params", field.Name, field.Name)
		}
	}
}

// Finally the write itself. A service that sets a column the UPDATE does not
// list changes nothing, which is exactly what happened to handle.
func TestUpdateStatementWritesEveryUpdatableColumn(t *testing.T) {
	src, err := os.ReadFile("../store/channel.go")
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	stmt := string(src)
	start := strings.Index(stmt, "func (s *Store) UpdateChannel(")
	if start < 0 {
		t.Fatal("store.UpdateChannel not found — this guard no longer protects anything")
	}
	stmt = stmt[start:]
	if end := strings.Index(stmt, "\n}"); end > 0 {
		stmt = stmt[:end]
	}

	// Columns the service is allowed to change. Each must appear in the SET
	// clause, or the change is accepted, reported as saved, and discarded.
	for _, col := range []string{
		"name", "handle", "description", "avatar_media_id", "banner_media_id",
		"channel_type", "category", "language", "comment_mode", "reaction_mode",
		"forward_allowed", "paid_access", "subscription_price_cents",
		"post_schedule_enabled", "subscriber_count_visible", "allow_preview_posts",
	} {
		if !strings.Contains(stmt, col+" = $") {
			t.Errorf("store.UpdateChannel does not write %q — the service can set it and the row will not change", col)
		}
	}
}

func jsonName(tag string) string {
	if tag == "" || tag == "-" {
		return ""
	}
	if i := strings.Index(tag, ","); i >= 0 {
		tag = tag[:i]
	}
	return tag
}

func funcBody(t *testing.T, path, name string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	s := string(src)
	start := strings.Index(s, "func (h *Handler) "+name+"(")
	if start < 0 {
		t.Fatalf("%s: no handler named %s", path, name)
	}
	s = s[start:]
	if end := strings.Index(s, "\n}"); end > 0 {
		s = s[:end]
	}
	return s
}
