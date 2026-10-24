package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Owner-registered auto-block (founder decision 24 Oct 2026; migration 075):
// the hold rule's choice, the kill switch, the catalogue's validation and
// ApplyOwnerMatch's block / mute / track routing. The transactions
// themselves are store tests (store/postgres/copyright_owner_match_test.go)
// and the integration tests.


// newOwnerRig is the title rig with "Jailer 2" registered by its owner for
// automatic blocking.
func newOwnerRig(t *testing.T) *titleRig {
	t.Helper()
	r := newTitleRig(t)
	for i := range r.store.titles {
		if r.store.titles[i].ID == r.jailer {
			r.store.titles[i].AutoBlock = true
			r.store.titles[i].RightsOwner = strPtr("Sun Pictures")
		}
	}
	return r
}

// An auto_block entry decides the hold BLOCKED at once (the store blocks it
// in the publishing transaction); an ordinary entry still goes to review.
func TestTitleHold_AutoBlockEntryDecidesImmediately(t *testing.T) {
	r := newOwnerRig(t)
	ctx := context.Background()
	hold, err := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Jailer 2 full movie"))
	if err != nil || hold == nil {
		t.Fatalf("hold=%+v err=%v", hold, err)
	}
	if !hold.AutoBlock || hold.RightsOwner == nil || *hold.RightsOwner != "Sun Pictures" || hold.Reason != "" {
		t.Fatalf("auto_block entry: hold = %+v, want an automatic block naming the owner", hold)
	}
	// Leo is an ordinary entry: a reviewer decides.
	hold, err = r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Leo full movie"))
	if err != nil || hold == nil || hold.AutoBlock || hold.TitleID != r.leo {
		t.Fatalf("ordinary entry: hold=%+v err=%v, want a hold for review", hold, err)
	}
	// An auto_block entry that (somehow) names no owner is not blocked by
	// the system.
	r = newOwnerRig(t)
	r.store.titles[0].RightsOwner = nil
	if hold, _ := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Jailer 2")); hold == nil || hold.AutoBlock {
		t.Fatalf("owner-less auto_block: hold=%+v, want a review hold", hold)
	}
	// The same thresholds as any hold: a short video is never touched.
	r = newOwnerRig(t)
	if hold, _ := r.svc.decideTitleHold(ctx, r.subject("long_video", 9*60, "Jailer 2")); hold != nil {
		t.Fatalf("a 9-minute video was held: %+v", hold)
	}
	// A verified owner channel is never held, auto_block or not.
	r.channels.byUser[r.author] = &postgres.Channel{ID: r.ownerCh, UserID: r.author}
	if hold, _ := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Jailer 2")); hold != nil {
		t.Fatalf("the owner's own upload was held: %+v", hold)
	}
}

// A title whose owner asked for blocking wins over an earlier-named one
// that only goes to review.
func TestTitleHold_AutoBlockMatchWins(t *testing.T) {
	r := newOwnerRig(t)
	hold, err := r.svc.decideTitleHold(context.Background(), r.subject("long_video", 3000, "Leo vs Jailer 2"))
	if err != nil || hold == nil || hold.TitleID != r.jailer || !hold.AutoBlock {
		t.Fatalf("hold=%+v err=%v, want the auto_block entry", hold, err)
	}
}

// The kill switch sends auto_block matches to a reviewer, at publication and
// at the duration measurement.
func TestAutoBlockKillSwitch(t *testing.T) {
	r := newOwnerRig(t)
	r.svc.SetCopyrightAutoBlockEnabled(false)
	ctx := context.Background()
	hold, err := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Jailer 2"))
	if err != nil || hold == nil || hold.AutoBlock {
		t.Fatalf("kill switch: hold=%+v err=%v, want a review hold", hold, err)
	}
	if err := r.svc.ResolveTitleReviewMeasurement(ctx, uuid.New(), 3600, "long_video"); err != nil {
		t.Fatal(err)
	}
	if len(r.store.resolved) != 1 || r.store.resolved[0].autoBlock {
		t.Fatalf("resolve = %+v, want autoBlock refused", r.store.resolved)
	}
}

func TestNormalizeTitleWrite_AutoBlockNeedsOwner(t *testing.T) {
	if _, err := normalizeTitleWrite(postgres.CopyrightTitleWrite{Title: "Jailer 2", AutoBlock: true}); !errors.Is(err, ErrInvalidCopyrightTitle) {
		t.Fatalf("auto_block without owner: err=%v", err)
	}
	if _, err := normalizeTitleWrite(postgres.CopyrightTitleWrite{Title: "Jailer 2", AutoBlock: true, RightsOwner: strPtr("  ")}); !errors.Is(err, ErrInvalidCopyrightTitle) {
		t.Fatalf("auto_block with a blank owner: err=%v", err)
	}
	if _, err := normalizeTitleWrite(postgres.CopyrightTitleWrite{Title: "Jailer 2", RightsOwner: strPtr("Sun\x07Pictures")}); !errors.Is(err, ErrInvalidCopyrightTitle) {
		t.Fatalf("control character in owner: err=%v", err)
	}
	w, err := normalizeTitleWrite(postgres.CopyrightTitleWrite{Title: "Jailer 2", AutoBlock: true, RightsOwner: strPtr(" Sun \n Pictures ")})
	if err != nil || w.RightsOwner == nil || *w.RightsOwner != "Sun Pictures" || !w.AutoBlock {
		t.Fatalf("w=%+v err=%v", w, err)
	}
	// A blank owner on an ordinary entry is simply none.
	if w, err := normalizeTitleWrite(postgres.CopyrightTitleWrite{Title: "Leo", RightsOwner: strPtr(" ")}); err != nil || w.RightsOwner != nil {
		t.Fatalf("blank owner: w=%+v err=%v", w, err)
	}
}

// PATCH keeps the owner when only auto_block changes, and refuses to clear
// the owner of an automatic block.
func TestUpdateCopyrightTitle_AutoBlock(t *testing.T) {
	r := newTitleRig(t)
	ctx := context.Background()
	on := true
	if _, err := r.svc.UpdateCopyrightTitle(ctx, r.leo, CopyrightTitleInput{AutoBlock: &on}, uuid.New()); !errors.Is(err, ErrInvalidCopyrightTitle) {
		t.Fatalf("auto_block on an owner-less entry: err=%v", err)
	}
	got, err := r.svc.UpdateCopyrightTitle(ctx, r.leo, CopyrightTitleInput{AutoBlock: &on, RightsOwner: strPtr("Lalit")}, uuid.New())
	if err != nil || !got.AutoBlock || got.RightsOwner == nil || *got.RightsOwner != "Lalit" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	title := "Leo (2023)"
	if got, err := r.svc.UpdateCopyrightTitle(ctx, r.leo, CopyrightTitleInput{Title: &title}, uuid.New()); err != nil || !got.AutoBlock || *got.RightsOwner != "Lalit" {
		t.Fatalf("an unrelated edit dropped the block: got=%+v err=%v", got, err)
	}
	if _, err := r.svc.UpdateCopyrightTitle(ctx, r.leo, CopyrightTitleInput{ClearRightsOwner: true}, uuid.New()); !errors.Is(err, ErrInvalidCopyrightTitle) {
		t.Fatalf("clearing the owner of an automatic block: err=%v", err)
	}
}

// ── ApplyOwnerMatch ─────────────────────────────────────────────────────

func newOwnerMatchRig(t *testing.T) (*titleRig, uuid.UUID) {
	t.Helper()
	r := newOwnerRig(t)
	post := uuid.New()
	r.store.postAuthors = map[uuid.UUID]uuid.UUID{post: r.author}
	return r, post
}

func TestApplyOwnerMatch_Policies(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		policy  OwnerPolicy
		applied postgres.OwnerMatchPolicy
		outcome postgres.OwnerMatchOutcome
	}{
		{OwnerPolicyBlock, postgres.OwnerPolicyBlock, postgres.OwnerMatchBlocked},
		{" MUTE ", postgres.OwnerPolicyMute, postgres.OwnerMatchHeld},
		{OwnerPolicyTrack, postgres.OwnerPolicyTrack, postgres.OwnerMatchTracked},
	} {
		r, post := newOwnerMatchRig(t)
		res, err := r.svc.ApplyOwnerMatch(ctx, post, OwnerMatch{TitleID: r.jailer, Reference: " fp-123 "}, c.policy)
		if err != nil || res.Outcome != c.outcome {
			t.Fatalf("%s: res=%+v err=%v", c.policy, res, err)
		}
		if len(r.store.ownerMatches) != 1 {
			t.Fatalf("%s: store calls = %d", c.policy, len(r.store.ownerMatches))
		}
		in := r.store.ownerMatches[0]
		if in.Policy != c.applied || in.PostID != post || in.TitleID != r.jailer || in.MatchedTitle != "Jailer 2" ||
			in.RightsOwner == nil || *in.RightsOwner != "Sun Pictures" || in.Reference != "fp-123" {
			t.Fatalf("%s: input = %+v", c.policy, in)
		}
	}
}

func TestApplyOwnerMatch_GuardsBeforeTheStore(t *testing.T) {
	ctx := context.Background()

	// The uploader's own channel owns the title: nothing happens.
	r, post := newOwnerMatchRig(t)
	r.channels.byUser[r.author] = &postgres.Channel{ID: r.ownerCh, UserID: r.author}
	if res, err := r.svc.ApplyOwnerMatch(ctx, post, OwnerMatch{TitleID: r.jailer}, OwnerPolicyBlock); err != nil || res.Outcome != postgres.OwnerMatchOwnerUpload || len(r.store.ownerMatches) != 0 {
		t.Fatalf("owner upload: res=%+v err=%v calls=%d", res, err, len(r.store.ownerMatches))
	}

	// A channel lookup that fails writes nothing.
	r, post = newOwnerMatchRig(t)
	r.svc.channels = errChannels{fakeChannelStore: r.channels, err: errors.New("channels down")}
	if _, err := r.svc.ApplyOwnerMatch(ctx, post, OwnerMatch{TitleID: r.jailer}, OwnerPolicyBlock); err == nil || len(r.store.ownerMatches) != 0 {
		t.Fatalf("channel failure: err=%v calls=%d", err, len(r.store.ownerMatches))
	}

	// A switched-off entry matches nothing.
	r, post = newOwnerMatchRig(t)
	off := r.store.titles[2].ID
	if res, err := r.svc.ApplyOwnerMatch(ctx, post, OwnerMatch{TitleID: off}, OwnerPolicyBlock); err != nil || res.Outcome != postgres.OwnerMatchEntryInactive || len(r.store.ownerMatches) != 0 {
		t.Fatalf("inactive entry: res=%+v err=%v", res, err)
	}

	// A block needs an entry that names its owner; mute and track do not.
	r, post = newOwnerMatchRig(t)
	if _, err := r.svc.ApplyOwnerMatch(ctx, post, OwnerMatch{TitleID: r.leo}, OwnerPolicyBlock); !errors.Is(err, postgres.ErrTitleReviewInvalid) {
		t.Fatalf("block for an owner-less entry: err=%v", err)
	}
	if _, err := r.svc.ApplyOwnerMatch(ctx, post, OwnerMatch{TitleID: r.leo}, OwnerPolicyTrack); err != nil {
		t.Fatalf("track for an owner-less entry: %v", err)
	}

	// Unknown policy, missing ids, unknown post.
	r, post = newOwnerMatchRig(t)
	for name, call := range map[string]func() error{
		"policy":  func() error { _, err := r.svc.ApplyOwnerMatch(ctx, post, OwnerMatch{TitleID: r.jailer}, "delete"); return err },
		"no post": func() error { _, err := r.svc.ApplyOwnerMatch(ctx, uuid.Nil, OwnerMatch{TitleID: r.jailer}, OwnerPolicyBlock); return err },
		"no title": func() error {
			_, err := r.svc.ApplyOwnerMatch(ctx, post, OwnerMatch{}, OwnerPolicyBlock)
			return err
		},
	} {
		if err := call(); !errors.Is(err, postgres.ErrTitleReviewInvalid) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
	if _, err := r.svc.ApplyOwnerMatch(ctx, uuid.New(), OwnerMatch{TitleID: r.jailer}, OwnerPolicyBlock); !errors.Is(err, postgres.ErrPostNotFound) {
		t.Fatalf("unknown post: err=%v", err)
	}
	if len(r.store.ownerMatches) != 0 {
		t.Fatalf("a refused match reached the store: %+v", r.store.ownerMatches)
	}
}

// The kill switch turns an owner's block into an ordinary review hold.
func TestApplyOwnerMatch_KillSwitchSendsBlocksToReview(t *testing.T) {
	r, post := newOwnerMatchRig(t)
	r.svc.SetCopyrightAutoBlockEnabled(false)
	if _, err := r.svc.ApplyOwnerMatch(context.Background(), post, OwnerMatch{TitleID: r.jailer}, OwnerPolicyBlock); err != nil {
		t.Fatal(err)
	}
	if len(r.store.ownerMatches) != 1 || r.store.ownerMatches[0].Policy != postgres.OwnerPolicyReview {
		t.Fatalf("store input = %+v, want the review policy", r.store.ownerMatches)
	}
	// Track and mute are untouched by it.
	if _, err := r.svc.ApplyOwnerMatch(context.Background(), post, OwnerMatch{TitleID: r.jailer}, OwnerPolicyTrack); err != nil || r.store.ownerMatches[1].Policy != postgres.OwnerPolicyTrack {
		t.Fatalf("track under the kill switch: %+v err=%v", r.store.ownerMatches, err)
	}
}

// The author's view names the owner and the system's reason.
func TestCopyrightReviewView_OwnerAndReason(t *testing.T) {
	v := copyrightReviewView(&postgres.TitleReview{State: postgres.TitleReviewBlocked, MatchedTitle: "Jailer 2", AuthorNotified: true,
		RightsOwner: strPtr("Sun Pictures"), Reason: strPtr(postgres.TitleReviewReasonOwnerRegistered)})
	if v == nil || v.RightsOwner != "Sun Pictures" || v.Reason != "owner_registered" {
		t.Fatalf("view = %+v", v)
	}
}
