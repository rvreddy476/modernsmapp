package contentcrypt

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/google/uuid"
)

func cipherForTest(t *testing.T) *fieldcrypt.Cipher {
	t.Helper()
	ring, err := fieldcrypt.ParseKeyList("v1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := fieldcrypt.New(ring)
	return c
}

func TestTruncateRunesNeverSplitsACharacter(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 3, "hel"},
		{"నమస్తే", 4, "న"}, // న is 3 bytes; the 4th byte starts మ
		{"నమస్తే", 6, "నమ"},
		{"👋🏽hi", 5, "👋"}, // 4-byte emoji, then a 4-byte modifier
		{"abc", 0, ""},
	}
	for _, c := range cases {
		got := TruncateRunes(c.in, c.max)
		if got != c.want || !utf8.ValidString(got) || len(got) > c.max && len(c.in) > c.max {
			t.Fatalf("TruncateRunes(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
	long := strings.Repeat("ఆ", 100)
	for max := 0; max <= len(long); max++ {
		if got := TruncateRunes(long, max); !utf8.ValidString(got) || len(got) > max {
			t.Fatalf("max %d: %d bytes, valid=%v", max, len(got), utf8.ValidString(got))
		}
	}
}

func TestBindingsDistinguishRows(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	ts := time.Date(2026, 10, 22, 9, 0, 0, 999_999_999, time.UTC)
	if bytes.Equal(MessageRow(a, b), MessageRow(b, a)) {
		t.Fatal("message binding is symmetric")
	}
	if !bytes.Equal(InboxRow(a, b, ts), InboxRow(a, b, ts.Truncate(time.Millisecond))) {
		t.Fatal("inbox binding must survive Scylla's millisecond truncation")
	}
	if bytes.Equal(InboxRow(a, b, ts), InboxRow(a, b, ts.Add(time.Millisecond))) {
		t.Fatal("inbox binding ignores the row's timestamp")
	}
	if bytes.Equal(OutboxRow("A", "k"), OutboxRow("A", "k2")) || bytes.Equal(NoteRow(a, b), NoteRow(b, a)) {
		t.Fatal("bindings collide")
	}
	seen := map[fieldcrypt.Scope]bool{}
	for _, s := range AllScopes {
		if err := s.Validate(); err != nil || seen[s] {
			t.Fatalf("scope %q invalid or duplicated", s)
		}
		seen[s] = true
	}
}

func TestResealIsIdempotent(t *testing.T) {
	ctx := context.Background()
	c := cipherForTest(t)
	row := MessageRow(uuid.New(), uuid.New())
	str := func(s string) *string { return &s }
	fields := []Field{
		{Scope: ScopeIntentText, Row: row, Value: str("legacy text")},
		{Scope: ScopeIntentReplyPreview, Row: row, Value: str("")},
		{Scope: ScopeIntentExtra, Row: row, Value: str(`{"k":1}`), JSON: true},
		{Scope: ScopeStarredPreview, Row: row, Value: nil},
		{Scope: ScopeIdempotencyResponse, Row: row, Value: str("null"), JSON: true},
	}
	out, changed, err := Reseal(ctx, c, fields)
	if err != nil || !changed {
		t.Fatalf("first pass: changed=%v err=%v", changed, err)
	}
	if !fieldcrypt.IsSealed(*out[0]) || !fieldcrypt.IsSealedJSON([]byte(*out[2])) {
		t.Fatal("legacy values were not sealed")
	}
	if *out[1] != "" || out[3] != nil || *out[4] != "null" {
		t.Fatal("values without content must stay as they are")
	}
	for i := range fields {
		fields[i].Value = out[i]
	}
	again, changed, err := Reseal(ctx, c, fields)
	if err != nil || changed {
		t.Fatalf("second pass changed=%v err=%v; must be a no-op", changed, err)
	}
	for i := range again {
		if (again[i] == nil) != (out[i] == nil) || (again[i] != nil && *again[i] != *out[i]) {
			t.Fatalf("field %d rewritten on the second pass", i)
		}
	}
	if got, _ := c.OpenString(ctx, ScopeIntentText, row, *out[0]); got != "legacy text" {
		t.Fatalf("resealed text opens as %q", got)
	}
	if _, _, err := Reseal(ctx, fieldcrypt.Plaintext(), fields); !errors.Is(err, fieldcrypt.ErrNoKeyRing) {
		t.Fatalf("Reseal without a ring: %v", err)
	}
}
