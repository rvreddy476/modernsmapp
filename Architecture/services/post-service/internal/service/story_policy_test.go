package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Module 4 M4-P0-1 / M4-P0-2 — the story policy matrix.
//
// EvaluateStoryVisibility is pure, so the whole matrix of moderation state x
// visibility x relationship runs without a database or a graph service. That
// matters because the bugs this closes were combinations nobody enumerated:
// approved+blocked, expired+highlight, close_friends+wrong-direction.

const (
	viewer = "11111111-1111-4111-8111-111111111111"
	author = "22222222-2222-4222-8222-222222222222"
)

func approved(vis string) StoryFacts {
	return StoryFacts{Exists: true, Visibility: vis, ModerationState: StoryModerationApproved}
}

func TestApprovedPublicStoryIsVisibleToAStranger(t *testing.T) {
	if d := EvaluateStoryVisibility(viewer, author, approved(StoryVisibilityPublic), ViewerRelationship{}); d != DenyNone {
		t.Fatalf("stranger denied an approved public story: %s", d)
	}
}

// A block hides the story in BOTH directions. The batch graph contract used to
// report only viewer->target, so the second case here was invisible to it.
func TestBlockDeniesInBothDirections(t *testing.T) {
	for _, tc := range []struct {
		name string
		rel  ViewerRelationship
	}{
		{"viewer blocked the author", ViewerRelationship{Blocked: true}},
		{"author blocked the viewer", ViewerRelationship{BlockedBy: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if d := EvaluateStoryVisibility(viewer, author, approved(StoryVisibilityPublic), tc.rel); d != DenyBlocked {
				t.Fatalf("blocked viewer saw the story: %s", d)
			}
		})
	}
}

// Moderation outranks audience: an unapproved story is invisible to everyone
// but its author, whatever the relationship says.
func TestOnlyApprovedStoriesReachAViewer(t *testing.T) {
	for _, state := range []string{StoryModerationPending, StoryModerationRejected, StoryModerationManualReview} {
		t.Run(state, func(t *testing.T) {
			f := StoryFacts{Exists: true, Visibility: StoryVisibilityPublic, ModerationState: state}
			if d := EvaluateStoryVisibility(viewer, author, f, ViewerRelationship{Follows: true}); d != DenyNotApproved {
				t.Fatalf("state %q was visible to a viewer: %s", state, d)
			}
			// The author still sees their own, which is what makes an honest
			// pending/rejected surface possible.
			if d := EvaluateStoryVisibility(author, author, f, ViewerRelationship{}); d != DenyNone {
				t.Fatalf("author could not see their own %q story: %s", state, d)
			}
		})
	}
}

func TestFollowersOnlyRequiresAFollow(t *testing.T) {
	if d := EvaluateStoryVisibility(viewer, author, approved(StoryVisibilityFollowers), ViewerRelationship{}); d != DenyNotInAudience {
		t.Fatalf("non-follower saw a followers-only story: %s", d)
	}
	if d := EvaluateStoryVisibility(viewer, author, approved(StoryVisibilityFollowers), ViewerRelationship{Follows: true}); d != DenyNone {
		t.Fatalf("follower denied a followers-only story: %s", d)
	}
}

// THE DIRECTION TEST. A close-friends story belongs to its author, so the only
// fact that grants access is the AUTHOR having listed the VIEWER. Using the
// viewer's own list would let anyone into someone else's close-friends
// audience by adding that person to their list.
func TestCloseFriendsUsesTargetOwnedMembershipOnly(t *testing.T) {
	f := approved(StoryVisibilityCloseFriends)

	if d := EvaluateStoryVisibility(viewer, author, f, ViewerRelationship{Follows: true}); d != DenyNotInAudience {
		t.Fatalf("a mere follower entered a close-friends audience: %s", d)
	}
	// The viewer having the AUTHOR on the viewer's own list must not help.
	if d := EvaluateStoryVisibility(viewer, author, f, ViewerRelationship{Follows: true, ViewerIsCloseFriendOfTarget: false}); d != DenyNotInAudience {
		t.Fatalf("self-asserted close friendship granted access: %s", d)
	}
	if d := EvaluateStoryVisibility(viewer, author, f, ViewerRelationship{ViewerIsCloseFriendOfTarget: true}); d != DenyNone {
		t.Fatalf("author-listed close friend was denied: %s", d)
	}
}

// A non-highlight dies at its expiry; an approved highlight survives it but
// stays subject to every other rule.
func TestExpiryAndHighlights(t *testing.T) {
	expired := approved(StoryVisibilityPublic)
	expired.Expired = true
	if d := EvaluateStoryVisibility(viewer, author, expired, ViewerRelationship{}); d != DenyExpired {
		t.Fatalf("expired story still visible: %s", d)
	}

	highlight := expired
	highlight.IsHighlight = true
	if d := EvaluateStoryVisibility(viewer, author, highlight, ViewerRelationship{}); d != DenyNone {
		t.Fatalf("approved highlight did not survive expiry: %s", d)
	}
	// ...but a highlight is not a bypass.
	if d := EvaluateStoryVisibility(viewer, author, highlight, ViewerRelationship{BlockedBy: true}); d != DenyBlocked {
		t.Fatalf("highlight bypassed a block: %s", d)
	}
	unapprovedHighlight := highlight
	unapprovedHighlight.ModerationState = StoryModerationManualReview
	if d := EvaluateStoryVisibility(viewer, author, unapprovedHighlight, ViewerRelationship{}); d != DenyNotApproved {
		t.Fatalf("highlight bypassed moderation: %s", d)
	}
}

func TestMissingDeletedAndAnonymousAreDenied(t *testing.T) {
	if d := EvaluateStoryVisibility(viewer, author, StoryFacts{Exists: false}, ViewerRelationship{}); d != DenyMissing {
		t.Fatalf("missing story: %s", d)
	}
	deleted := approved(StoryVisibilityPublic)
	deleted.Deleted = true
	if d := EvaluateStoryVisibility(viewer, author, deleted, ViewerRelationship{}); d != DenyDeleted {
		t.Fatalf("deleted story: %s", d)
	}
	if d := EvaluateStoryVisibility("", author, approved(StoryVisibilityPublic), ViewerRelationship{}); d != DenyNoViewer {
		t.Fatalf("anonymous viewer: %s", d)
	}
}

// An unrecognised visibility denies. New audience types must be added
// deliberately rather than defaulting to visible.
func TestUnknownVisibilityDenies(t *testing.T) {
	f := approved("some_future_audience")
	if d := EvaluateStoryVisibility(viewer, author, f, ViewerRelationship{Follows: true}); d != DenyNotInAudience {
		t.Fatalf("unknown visibility defaulted to visible: %s", d)
	}
}

// ── Audience resolution: every uncertainty must be unresolved, never empty ──

type storyStubGraph struct {
	following []string
	rels      map[string]ViewerRelationship
	err       error
}

func (s storyStubGraph) Following(context.Context, string) ([]string, error) {
	return s.following, s.err
}

func (s storyStubGraph) RelationshipBatch(_ context.Context, _ string, ids []string) (map[string]ViewerRelationship, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rels, nil
}

func TestGraphFailureIsUnresolvedNotAnEmptyAudience(t *testing.T) {
	a := NewStoryAudience(storyStubGraph{err: errors.New("timeout")})
	if _, err := a.CandidateAuthors(context.Background(), viewer); !errors.Is(err, ErrStoryPolicyUnresolved) {
		t.Fatalf("graph timeout did not produce unresolved state: %v", err)
	}
}

// A nil graph client must fail closed. Returning an empty audience would make
// an unwired deployment look like a user who follows nobody.
func TestNilGraphClientFailsClosed(t *testing.T) {
	a := NewStoryAudience(nil)
	if _, err := a.CandidateAuthors(context.Background(), viewer); !errors.Is(err, ErrStoryPolicyUnresolved) {
		t.Fatalf("nil graph client did not fail closed: %v", err)
	}
}

// THE PARTIAL-ANSWER TEST. If the graph answers for only some authors, the
// missing ones must not default to the zero relationship — that reads as
// "not blocked".
func TestPartialRelationshipAnswerIsUnresolved(t *testing.T) {
	a := NewStoryAudience(storyStubGraph{rels: map[string]ViewerRelationship{author: {}}})
	_, err := a.Relationships(context.Background(), viewer, []string{author, "33333333-3333-4333-8333-333333333333"})
	if !errors.Is(err, ErrStoryPolicyUnresolved) {
		t.Fatalf("a partial relationship answer was accepted: %v", err)
	}
}

func TestCandidateAuthorsAlwaysIncludesTheViewer(t *testing.T) {
	a := NewStoryAudience(storyStubGraph{following: []string{author}})
	got, err := a.CandidateAuthors(context.Background(), viewer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != viewer {
		t.Fatalf("viewer missing from their own candidate set: %v", got)
	}
}

// ── Source guard ────────────────────────────────────────────────────────────

// TestFollowedIdsIsSourceBanned fails if the client-supplied audience parameter
// is reintroduced anywhere in production source.
//
// M4-P0-1 acceptance criterion 1 asks for exactly this. The parameter was not a
// bug in one branch — it was a documented input ("or the client passes them
// explicitly"), so a future change could reasonably re-add it believing it was
// intended. This makes that fail.
func TestFollowedIdsIsSourceBanned(t *testing.T) {
	roots := []string{"..", "../../internal"}
	seen := map[string]bool{}
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			abs, _ := filepath.Abs(path)
			if seen[abs] {
				return nil
			}
			seen[abs] = true
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			// Strip comment lines: this file's own explanation of the ban
			// names the parameter, and a check that cannot tell code from
			// prose fails on its own documentation.
			for i, line := range strings.Split(string(body), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				if strings.Contains(line, "followed_ids") {
					t.Errorf("%s:%d reintroduces the client-supplied story audience "+
						"parameter. The audience is server-derived; a caller must never "+
						"name the authors whose stories it receives.", path, i+1)
				}
			}
			return nil
		})
	}
}
