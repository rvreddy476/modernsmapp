package scylla

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// M3: a read that spent its bucket budget on empty months returns a
// bucket-only resume point while the conversation's start is still ahead,
// and nothing once the scan passed it.
func TestNextHistoryCursor(t *testing.T) {
	msg := Message{Bucket: "202601", Ts: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), MsgID: uuid.New()}
	last := &MessageCursor{Bucket: msg.Bucket, Ts: msg.Ts, MsgID: msg.MsgID}
	cases := map[string]struct {
		msgs      []Message
		remaining int
		bucket    string // first bucket not read
		oldest    string
		want      *MessageCursor
	}{
		"page filled":                     {[]Message{msg}, 0, "202512", "202001", last},
		"budget spent, older months left": {nil, 10, "202412", "202001", &MessageCursor{Bucket: "202412"}},
		"budget spent at the start month": {nil, 10, "202001", "202001", &MessageCursor{Bucket: "202001"}},
		"short page, older months left":   {[]Message{msg}, 9, "202412", "202001", &MessageCursor{Bucket: "202412"}},
		"past the conversation's start":   {[]Message{msg}, 9, "201912", "202001", nil},
		"empty and past the start":        {nil, 10, "201912", "202001", nil},
		"unknown start, after a message":  {[]Message{msg}, 9, "202412", "", last},
		"unknown start, empty":            {nil, 10, "202412", "", nil},
	}
	for name, tc := range cases {
		got := nextHistoryCursor(tc.msgs, tc.remaining, tc.bucket, tc.oldest)
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: cursor = %+v, want %+v", name, got, tc.want)
		}
	}
}
