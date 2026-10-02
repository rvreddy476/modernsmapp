package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Members-only videos on the byte gate (2026-10-02).
//
// The watch page showed the join card to a non-member, but the decision
// media-service asks before serving a byte did not look at tier_required_id
// for a signed-in viewer: holding the media id was enough to fetch the video.
// These tests pin the membership clause on that one decision — the single
// route and the page-sized one — over the same in-memory rig as the rest of
// the gate (media_access_gate_test.go).

// membersRig is the gate rig with its one post gated on a tier, a second
// asset that is the post's cover image, and a membership answer the test
// controls.
type membersRig struct {
	*mediaGateRig
	tier     uuid.UUID
	cover    uuid.UUID
	member   uuid.UUID
	entitled map[uuid.UUID]bool
	entErr   error
	lookups  int
}

func newMembersRig(t *testing.T) *membersRig {
	t.Helper()
	r := &membersRig{mediaGateRig: newMediaGateRig(t), tier: uuid.New(), cover: uuid.New(), member: uuid.New(), entitled: map[uuid.UUID]bool{}}
	r.entitled[r.member] = true
	p := r.store.posts[r.post]
	p.TierRequiredID = &r.tier
	p.CoverMediaID = &r.cover
	p.Media = []postgres.PostMedia{{MediaID: r.media, Kind: "video"}}
	// The cover is the author's own image, reached through the post.
	r.store.facts[r.cover] = postgres.MediaAccessFacts{UploaderID: r.author, ProcessingStatus: "ready", ModerationStatus: "passed"}
	r.store.byMedia[r.cover] = []uuid.UUID{r.post}
	r.svc.offlineEntitlement = func(_ context.Context, viewerID uuid.UUID, _ *postgres.Post) (bool, error) {
		r.lookups++
		if r.entErr != nil {
			return false, r.entErr
		}
		return r.entitled[viewerID], nil
	}
	return r
}

func (r *membersRig) singleOf(viewer, media uuid.UUID, purpose string) (MediaAccessResult, error) {
	return r.svc.ViewerMayAccessMediaFor(context.Background(), viewer, media, purpose)
}

func (r *membersRig) batchOf(t *testing.T, viewer uuid.UUID, media ...uuid.UUID) map[uuid.UUID]MediaAccessResult {
	t.Helper()
	res, err := r.svc.ViewerMayAccessMediaBatch(context.Background(), viewer, media)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	return res
}

func TestMembersOnlyVideoIsRefusedToASignedInNonMember(t *testing.T) {
	r := newMembersRig(t)
	res, err := r.singleOf(r.adult, r.media, "")
	if err != nil {
		t.Fatal(err)
	}
	// The same refusal shape as every other denial: allowed false, decision
	// denied, a reason for the log.
	if res.Allowed || res.Decision != DecisionDenied || res.Reason != "members_only" {
		t.Fatalf("a non-member was not refused a members-only video: %+v", res)
	}
	if b := r.batchOf(t, r.adult, r.media)[r.media]; b.Allowed || b.Decision != DecisionDenied || b.Reason != "members_only" {
		t.Fatalf("batch: %+v", b)
	}
	// The download decision rides on the same one.
	if ok, err := r.svc.ViewerMayDownloadMedia(context.Background(), r.adult, r.media); err != nil || ok {
		t.Fatalf("download: ok=%v err=%v", ok, err)
	}
	// And the plain yes/no the other callers read.
	judge, err := r.svc.postMediaJudge(context.Background(), r.adult, []*postgres.Post{r.store.posts[r.post]})
	if err != nil || judge(r.store.posts[r.post]) {
		t.Fatalf("postMediaJudge allowed a non-member (err=%v)", err)
	}
}

func TestMembersOnlyVideoPlaysForAMember(t *testing.T) {
	r := newMembersRig(t)
	res, err := r.singleOf(r.member, r.media, "")
	if err != nil || !res.Allowed || res.Decision != DecisionAllowed || res.Reason != "post_allowed" {
		t.Fatalf("a member was refused: %+v err=%v", res, err)
	}
	if b := r.batchOf(t, r.member, r.media)[r.media]; !b.Allowed || b.Reason != "post_allowed" {
		t.Fatalf("batch: %+v", b)
	}
	// Membership does not replace the audience: a member the author blocked
	// is refused like anyone else, and nobody is asked about their tier.
	r.svc.storyAudience = NewStoryAudience(fixedRelationships{rels: map[string]ViewerRelationship{r.author.String(): {BlockedBy: true}}})
	r.lookups = 0
	if res, err := r.singleOf(r.member, r.media, ""); err != nil || res.Allowed || res.Reason != "no_visible_post_or_story" {
		t.Fatalf("a blocked member: %+v err=%v", res, err)
	}
	if r.lookups != 0 {
		t.Fatalf("membership was looked up %d times for a viewer who may not see the post", r.lookups)
	}
}

func TestMembersOnlyVideoPlaysForItsOwner(t *testing.T) {
	r := newMembersRig(t)
	r.entErr = errors.New("monetization down") // the owner needs no lookup
	res, err := r.singleOf(r.author, r.media, "")
	if err != nil || !res.Allowed {
		t.Fatalf("the owner was refused their own members-only video: %+v err=%v", res, err)
	}
	if b := r.batchOf(t, r.author, r.media)[r.media]; !b.Allowed {
		t.Fatalf("batch: %+v", b)
	}
	// Past the uploader's own preview: the per-post rule says the same.
	judge, err := r.svc.postMediaJudge(context.Background(), r.author, []*postgres.Post{r.store.posts[r.post]})
	if err != nil || !judge(r.store.posts[r.post]) {
		t.Fatalf("postMediaJudge refused the owner (err=%v)", err)
	}
	if r.lookups != 0 {
		t.Fatalf("the owner's membership of their own tier was looked up %d times", r.lookups)
	}
}

func TestMembersOnlyVideoIsRefusedWhenMembershipDoesNotAnswer(t *testing.T) {
	r := newMembersRig(t)
	r.entErr = errors.New("monetization down")
	// Even a real member is refused while the lookup fails: fail closed.
	for _, viewer := range []uuid.UUID{r.member, r.adult} {
		res, err := r.singleOf(viewer, r.media, "")
		// The single route: unresolved (503 on the wire), never an allow and
		// never a resolved "not a member".
		if !errors.Is(err, ErrStoryPolicyUnresolved) || res.Allowed {
			t.Fatalf("single during an outage: %+v err=%v", res, err)
		}
		// The page: that asset is refused, and only that asset.
		other, otherPost := uuid.New(), uuid.New()
		r.store.facts[other] = postgres.MediaAccessFacts{UploaderID: r.author, ProcessingStatus: "ready", ModerationStatus: "passed"}
		r.store.posts[otherPost] = &postgres.Post{ID: otherPost, AuthorID: r.author, Visibility: "public", ReviewStatus: "approved"}
		r.store.byMedia[other] = []uuid.UUID{otherPost}
		page := r.batchOf(t, viewer, r.media, other)
		if got := page[r.media]; got.Allowed || got.Decision != DecisionDenied || got.Reason != "membership_unresolved" {
			t.Fatalf("batch during an outage: %+v", got)
		}
		if got := page[other]; !got.Allowed {
			t.Fatalf("an ordinary video on the same page was refused: %+v", got)
		}
	}
	// Nothing was remembered: the moment the lookup answers, a member plays.
	r.entErr = nil
	if res, err := r.singleOf(r.member, r.media, ""); err != nil || !res.Allowed {
		t.Fatalf("after the outage: %+v err=%v", res, err)
	}
}

func TestMembershipIsLookedUpOncePerDecision(t *testing.T) {
	r := newMembersRig(t)
	// Three more gated videos by the same creator, on one page.
	media := []uuid.UUID{r.media}
	for i := 0; i < 3; i++ {
		m, p := uuid.New(), uuid.New()
		r.store.facts[m] = postgres.MediaAccessFacts{UploaderID: r.author, ProcessingStatus: "ready", ModerationStatus: "passed"}
		r.store.posts[p] = &postgres.Post{ID: p, AuthorID: r.author, Visibility: "public", ReviewStatus: "approved", TierRequiredID: &r.tier,
			Media: []postgres.PostMedia{{MediaID: m, Kind: "video"}}}
		r.store.byMedia[m] = []uuid.UUID{p}
		media = append(media, m)
	}
	for _, res := range r.batchOf(t, r.member, media...) {
		if !res.Allowed {
			t.Fatalf("a member was refused: %+v", res)
		}
	}
	if r.lookups != 1 {
		t.Fatalf("membership lookups = %d, want 1 for one (creator, tier) on one page", r.lookups)
	}
}

func TestPublicPostIsUnaffectedByTheMembershipClause(t *testing.T) {
	r := newMembersRig(t)
	r.store.posts[r.post].TierRequiredID = nil
	r.entErr = errors.New("monetization down") // never asked
	for _, viewer := range []uuid.UUID{r.adult, r.member, uuid.Nil} {
		res, err := r.singleOf(viewer, r.media, "")
		if err != nil || !res.Allowed || res.Reason != "post_allowed" {
			t.Fatalf("viewer %v on a public post: %+v err=%v", viewer, res, err)
		}
		if b := r.batchOf(t, viewer, r.media)[r.media]; !b.Allowed {
			t.Fatalf("batch, viewer %v: %+v", viewer, b)
		}
	}
	if r.lookups != 0 {
		t.Fatalf("membership was looked up %d times for a post with no tier", r.lookups)
	}
	// An asset on a gated post AND on an ungated one plays through the
	// ungated one.
	r.store.posts[r.post].TierRequiredID = &r.tier
	open := uuid.New()
	r.store.posts[open] = &postgres.Post{ID: open, AuthorID: r.author, Visibility: "public", ReviewStatus: "approved"}
	r.store.byMedia[r.media] = []uuid.UUID{r.post, open}
	r.entErr = nil
	if res, err := r.singleOf(r.adult, r.media, ""); err != nil || !res.Allowed {
		t.Fatalf("an asset also on an ungated post: %+v err=%v", res, err)
	}
}

func TestAnonymousViewerOfAMembersOnlyPostIsUnchanged(t *testing.T) {
	r := newMembersRig(t)
	r.entitled[uuid.Nil] = true // must never be consulted
	for name, media := range map[string]uuid.UUID{"video": r.media, "cover": r.cover} {
		for _, purpose := range []string{"", MediaAccessPurposePoster} {
			res, err := r.singleOf(uuid.Nil, media, purpose)
			if err != nil || res.Allowed || res.Decision != DecisionDenied || res.Reason != "no_public_post" {
				t.Fatalf("anonymous %s (purpose %q): %+v err=%v", name, purpose, res, err)
			}
		}
		if b := r.batchOf(t, uuid.Nil, media)[media]; b.Allowed || b.Reason != "no_public_post" {
			t.Fatalf("anonymous %s, batch: %+v", name, b)
		}
	}
	if r.lookups != 0 {
		t.Fatalf("membership was looked up %d times for a signed-out viewer", r.lookups)
	}
	// The lookup itself never entitles nobody, whatever the seam would say.
	if ok, err := r.svc.viewerEntitled(context.Background(), uuid.Nil, r.store.posts[r.post]); ok || err != nil || r.lookups != 0 {
		t.Fatalf("viewerEntitled(signed out) = %v, %v (lookups %d)", ok, err, r.lookups)
	}
}

// The join card's poster. A signed-in viewer who may see the post and is not
// a member is shown its cover image, and a thumbnail still of the video; the
// video itself, in any other form, is the members'.
func TestMembersOnlyPosterStaysVisibleToASignedInNonMember(t *testing.T) {
	r := newMembersRig(t)

	// The cover image: a different asset, reached as the post's cover.
	res, err := r.singleOf(r.adult, r.cover, "")
	if err != nil || !res.Allowed || res.Reason != "post_allowed" {
		t.Fatalf("the cover of a members-only post was hidden from a non-member: %+v err=%v", res, err)
	}
	if b := r.batchOf(t, r.adult, r.cover)[r.cover]; !b.Allowed {
		t.Fatalf("cover, batch: %+v", b)
	}
	// A thumbnail still of the video, asked for as a poster.
	res, err = r.singleOf(r.adult, r.media, MediaAccessPurposePoster)
	if err != nil || !res.Allowed {
		t.Fatalf("a poster still of a members-only video was hidden: %+v err=%v", res, err)
	}
	// Neither needed the membership lookup, so neither fails with it.
	if r.lookups != 0 {
		t.Fatalf("a poster read looked membership up %d times", r.lookups)
	}
	r.entErr = errors.New("monetization down")
	if res, err := r.singleOf(r.adult, r.cover, ""); err != nil || !res.Allowed {
		t.Fatalf("the cover during a monetization outage: %+v err=%v", res, err)
	}
	r.entErr = nil

	// Anything that is not the poster purpose is playback.
	for _, purpose := range []string{"", "playback", "Poster", "thumb_300"} {
		if res, err := r.singleOf(r.adult, r.media, purpose); err != nil || res.Allowed {
			t.Fatalf("purpose %q played a members-only video for a non-member: %+v err=%v", purpose, res, err)
		}
	}
	// A poster is still the post's: the audience applies to it.
	r.store.posts[r.post].Visibility = "private"
	for _, tc := range []struct {
		media   uuid.UUID
		purpose string
	}{{r.cover, ""}, {r.media, MediaAccessPurposePoster}} {
		if res, err := r.singleOf(r.adult, tc.media, tc.purpose); err != nil || res.Allowed {
			t.Fatalf("a poster of a post the viewer may not see was shown: %+v err=%v", res, err)
		}
	}
	r.store.posts[r.post].Visibility = "public"

	// An asset that is the cover AND something the post plays is not "only a
	// cover": naming your video as your own cover must not open it.
	r.store.posts[r.post].CoverMediaID = &r.media
	if res, err := r.singleOf(r.adult, r.media, ""); err != nil || res.Allowed {
		t.Fatalf("a video named as its own post's cover played for a non-member: %+v err=%v", res, err)
	}
}
