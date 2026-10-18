package scylla

import "testing"

func TestMissingMessageColumnsIncludesPostID(t *testing.T) {
	// A cluster built before reply/edit/post columns existed.
	original := map[string]bool{
		"conversation_id": true, "bucket": true, "ts": true, "msg_id": true,
		"sender_id": true, "type": true, "text": true, "media_id": true,
		"is_deleted": true, "created_at": true,
	}
	got := map[string]string{}
	for _, c := range missingMessageColumns(original) {
		got[c.name] = c.cqlType
	}
	want := map[string]string{
		"reply_to_id": "uuid", "reply_to_preview": "text", "reply_to_sender_id": "uuid",
		"is_edited": "boolean", "edited_at": "timestamp", "post_id": "uuid",
		"extra": "text",
	}
	if len(got) != len(want) {
		t.Fatalf("missing columns = %v, want %v", got, want)
	}
	for name, typ := range want {
		if got[name] != typ {
			t.Errorf("column %s: got %q, want %q", name, got[name], typ)
		}
	}

	// The dev cluster as of 2026-10-14: everything but post_id.
	for name := range want {
		original[name] = true
	}
	delete(original, "post_id")
	delete(original, "extra")
	if cols := missingMessageColumns(original); len(cols) != 2 || cols[0].name != "post_id" || cols[1].name != "extra" {
		t.Fatalf("dev-shaped cluster: missing = %v, want post_id and extra", cols)
	}
	original["post_id"] = true
	original["extra"] = true
	if cols := missingMessageColumns(original); len(cols) != 0 {
		t.Fatalf("converged table must need nothing, got %v", cols)
	}
}
