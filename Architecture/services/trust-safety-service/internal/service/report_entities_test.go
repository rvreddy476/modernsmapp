package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeReportHashtagMatchesPostServiceTags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"dance", "dance", true},
		{"#Dance", "dance", true},
		{"  #DANCE_2026 ", "dance_2026", true},
		{"नमस्ते", "नमस्ते", true}, // Devanagari with combining marks
		{"x", "x", true},
		{strings.Repeat("a", MaxReportHashtagLength), strings.Repeat("a", MaxReportHashtagLength), true},
		{strings.Repeat("é", MaxReportHashtagLength), strings.Repeat("é", MaxReportHashtagLength), true}, // runes, not bytes
		{strings.Repeat("a", MaxReportHashtagLength+1), "", false},
		{"", "", false},
		{"#", "", false},
		{"##dance", "", false},
		{"two words", "", false},
		{"dance!", "", false},
		{"11111111-1111-1111-1111-111111111111", "", false},
	}
	for _, tc := range cases {
		got, ok := NormalizeReportHashtag(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("NormalizeReportHashtag(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestResolveReportEntity(t *testing.T) {
	t.Parallel()
	id := uuid.New()

	for _, typ := range []string{"user", "post", "comment", "sound"} {
		got, key, err := ResolveReportEntity(typ, " "+id.String()+" ")
		if err != nil || got != id || key != "" {
			t.Errorf("%s: (%s, %q, %v), want the UUID as given and no key", typ, got, key, err)
		}
		for _, bad := range []string{"dance", "", uuid.Nil.String()} {
			if _, _, err := ResolveReportEntity(typ, bad); !errors.Is(err, ErrInvalidReport) {
				t.Errorf("%s %q: err = %v, want ErrInvalidReport", typ, bad, err)
			}
		}
	}

	a, keyA, err := ResolveReportEntity("hashtag", "#Dance")
	if err != nil || keyA != "dance" {
		t.Fatalf("hashtag: (%s, %q, %v)", a, keyA, err)
	}
	b, _, _ := ResolveReportEntity("hashtag", "dance")
	if a != b || a != ReportEntityID("hashtag", "dance") {
		t.Fatalf("one tag must map to one entity_id: %s vs %s", a, b)
	}
	if a.Version() != 5 {
		t.Fatalf("hashtag entity_id is version %d, want a UUIDv5", a.Version())
	}
	if other, _, _ := ResolveReportEntity("hashtag", "dancing"); other == a {
		t.Fatal("two tags share an entity_id")
	}
	if ReportEntityID("hashtag", "dance") == ReportEntityID("sound", "dance") {
		t.Fatal("the entity type is not part of the derived id")
	}
	// Pinned: changing the namespace would split every stored tag's reports.
	if got := ReportEntityID("hashtag", "dance").String(); got != pinnedDanceID {
		t.Fatalf("ReportEntityID(hashtag, dance) = %s, want %s (the namespace must never change)", got, pinnedDanceID)
	}
	if _, _, err := ResolveReportEntity("hashtag", id.String()); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("a UUID is not a tag: %v", err)
	}
	if _, _, err := ResolveReportEntity("story", id.String()); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("unknown type: %v", err)
	}
}

const pinnedDanceID = "ec942683-d3bb-5885-8092-62e0f2a73d97"

// Validation runs before the store: a bad report never reaches the
// database (the nil store would panic if it did) and is ErrInvalidReport,
// which the handler answers with 400.
func TestFileReportRefRejectsBeforeTheStore(t *testing.T) {
	t.Parallel()
	svc := New(nil, nil)
	reporter := uuid.New()
	for _, tc := range []struct{ typ, ref, reason string }{
		{"hashtag", "not a tag", "spam"},
		{"hashtag", strings.Repeat("a", MaxReportHashtagLength+1), "spam"},
		{"sound", "dance", "spam"},
		{"sound", uuid.NewString(), "not-a-reason"},
		{"story", uuid.NewString(), "spam"},
		{"user", "nope", "spam"},
	} {
		if _, err := svc.FileReportRef(context.Background(), reporter, tc.typ, tc.ref, tc.reason, ""); !errors.Is(err, ErrInvalidReport) {
			t.Errorf("%+v: err = %v, want ErrInvalidReport", tc, err)
		}
	}
}
