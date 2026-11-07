package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

type fakePrivacy struct {
	private map[string]bool
	err     error
}

func (f fakePrivacy) IsPrivate(_ context.Context, id string) (bool, error) {
	return f.private[id], f.err
}
func (fakePrivacy) Invalidate(string) {}

// S-M1: the rebuild writes the author's real flags, and refuses (the row
// is skipped) when privacy cannot be read rather than writing "public".
func TestVisibilitySource_Resolve(t *testing.T) {
	v := &visibilitySource{
		hidden:  map[string]bool{"deactivated-user": true},
		privacy: fakePrivacy{private: map[string]bool{"private-user": true}},
	}
	ctx := context.Background()
	if got, err := v.resolve(ctx, "deactivated-user"); err != nil || !got.IsHidden || got.IsPrivate {
		t.Fatalf("deactivated author = %+v, %v; want hidden", got, err)
	}
	if got, err := v.resolve(ctx, "private-user"); err != nil || !got.IsPrivate || got.IsHidden {
		t.Fatalf("private author = %+v, %v; want private", got, err)
	}
	if got, err := v.resolve(ctx, "someone"); err != nil || got.IsPrivate || got.IsHidden {
		t.Fatalf("ordinary author = %+v, %v", got, err)
	}

	broken := &visibilitySource{privacy: fakePrivacy{err: errors.New("identity down")}}
	if got, err := broken.resolve(ctx, "someone"); err == nil || got != nil {
		t.Fatalf("an unreadable privacy setting resolved to %+v; it must skip the row", got)
	}
	var none *visibilitySource
	if _, err := none.resolve(ctx, "someone"); err == nil {
		t.Fatal("a missing visibility source resolved an author")
	}
}

// The hidden states are the ones the lifecycle events hide.
func TestHiddenAccountStatuses(t *testing.T) {
	want := map[string]bool{"deactivated": true, "pending_deletion": true, "purged": true}
	if len(hiddenAccountStatuses) != len(want) {
		t.Fatalf("hiddenAccountStatuses = %v", hiddenAccountStatuses)
	}
	for _, s := range hiddenAccountStatuses {
		if !want[s] {
			t.Fatalf("unexpected hidden status %q", s)
		}
	}
}

// Source guard: both rebuilds resolve the flags and the posts rebuild hands
// them to the projection; the users rebuild stamps them on the document.
func TestBackfill_CarriesAuthorVisibility(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	for _, want := range []string{
		"if vis, err = newVisibilitySource(ctx, identityDSN); err != nil {\n\t\t\treturn 0, fmt.Errorf(\"backfill posts: %w\", err)",
		"author, err := vis.resolve(ctx, authorID)",
		"\t\t\tAuthor:    author,\n",
		"if vis, err = newVisibilitySource(ctx, identityDSN); err != nil {\n\t\t\treturn 0, fmt.Errorf(\"backfill users: %w\", err)",
		"d.IsPrivate, d.IsHidden = v.IsPrivate, v.IsHidden",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("cmd/backfill/main.go no longer carries the author's visibility: missing %q", want)
		}
	}
}

// S-M2: the community and channel rebuilds read the status, so a suspended
// or archived row is deleted by IndexCommunity / IndexChannel, not written.
func TestBackfill_ReadsCommunityAndChannelStatus(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	for _, want := range []string{
		"COALESCE(member_count,0), COALESCE(is_verified,false), created_at, status\n",
		"&d.MemberCount, &d.IsVerified, &d.CreatedAt, &d.Status); err != nil {",
		"COALESCE(is_verified,false), created_at, status\n\t      FROM broadcast_channels",
		"&d.SubscriberCount, &d.IsVerified, &d.CreatedAt, &d.Status); err != nil {",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("the community/channel rebuild no longer reads status: missing %q", want)
		}
	}
}
