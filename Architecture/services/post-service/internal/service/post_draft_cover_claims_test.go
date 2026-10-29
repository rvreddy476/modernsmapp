package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Composer draft covers (post_draft_cover_claims.go): every writer of a
// post_drafts payload's cover_media_id claims it under post_draft_cover
// before the row names it, and releases it once the row stops naming it.

// fakeDraftRows is post_drafts in memory. Every write mirrors the row into
// the holder fake, as the real MediaHeldBy would read it.
type fakeDraftRows struct {
	rows      map[uuid.UUID]*postgres.PostDraft
	holders   *fakeHolders
	createErr error
	onWrite   func(cover *uuid.UUID) // called at the write, before it lands
}

func newFakeDraftRows(h *fakeHolders) *fakeDraftRows {
	return &fakeDraftRows{rows: map[uuid.UUID]*postgres.PostDraft{}, holders: h}
}

func (f *fakeDraftRows) mirror(d *postgres.PostDraft) {
	var p PostDraftPayload
	_ = json.Unmarshal(d.Payload, &p)
	live := d.Status == "draft" || d.Status == "publishing" || d.Status == "blocked"
	f.holders.set(postgres.HolderPostDraftCover, d.ID, live, optionalID(payloadDraftCover(&p))...)
}

func (f *fakeDraftRows) seen(payload json.RawMessage) {
	if f.onWrite == nil {
		return
	}
	var p PostDraftPayload
	_ = json.Unmarshal(payload, &p)
	f.onWrite(payloadDraftCover(&p))
}

func (f *fakeDraftRows) CreatePostDraft(_ context.Context, d *postgres.PostDraft, _ []uuid.UUID) error {
	f.seen(d.Payload)
	if f.createErr != nil {
		return f.createErr
	}
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	d.Status = "draft"
	cp := *d
	f.rows[d.ID] = &cp
	f.mirror(&cp)
	return nil
}

func (f *fakeDraftRows) UpdatePostDraft(_ context.Context, id, authorID uuid.UUID, postType string,
	payload json.RawMessage, scheduleAt *time.Time, _ []uuid.UUID) (bool, error) {
	f.seen(payload)
	d := f.rows[id]
	if d == nil || d.AuthorID != authorID || (d.Status != "draft" && d.Status != "blocked") {
		return false, nil
	}
	d.PostType, d.Payload, d.ScheduleAt, d.Status = postType, payload, scheduleAt, "draft"
	f.mirror(d)
	return true, nil
}

func (f *fakeDraftRows) GetPostDraft(_ context.Context, id, authorID uuid.UUID) (*postgres.PostDraft, error) {
	d := f.rows[id]
	if d == nil || d.AuthorID != authorID || d.Status == "deleted" {
		return nil, nil
	}
	cp := *d
	return &cp, nil
}

func (f *fakeDraftRows) DeletePostDraft(_ context.Context, id, authorID uuid.UUID) (bool, error) {
	d := f.rows[id]
	if d == nil || d.AuthorID != authorID || (d.Status != "draft" && d.Status != "blocked") {
		return false, nil
	}
	d.Status = "deleted"
	f.mirror(d)
	return true, nil
}

func (f *fakeDraftRows) MarkPostDraftPublished(_ context.Context, id, postID uuid.UUID, _ *uuid.UUID) error {
	d := f.rows[id]
	if d == nil {
		return postgres.ErrDraftClaimLost
	}
	d.Status, d.PublishedPostID = "published", &postID
	f.mirror(d)
	return nil
}

func (f *fakeDraftRows) setStatus(id uuid.UUID, status string) {
	f.rows[id].Status = status
	f.mirror(f.rows[id])
}

func draftCoverRig() (*Service, *fakeClaims, *fakeDraftRows, fakeOwnership) {
	s, claims, holders := claimRig()
	rows := newFakeDraftRows(holders)
	own := fakeOwnership{}
	s.postDraftRows = rows
	s.ownMedia = own
	return s, claims, rows, own
}

// ownImage is a finished, unrefused image upload of owner's.
func ownImage(own fakeOwnership, owner uuid.UUID) uuid.UUID {
	id := uuid.New()
	own[id] = postgres.MediaOwnership{UploaderID: owner, Kind: "image", ProcessingStatus: "ready", ModerationStatus: "passed"}
	return id
}

func draftPayloadWithCover(cover *uuid.UUID) json.RawMessage {
	if cover == nil {
		return json.RawMessage(`{"text":"hello"}`)
	}
	return json.RawMessage(fmt.Sprintf(`{"text":"hello","cover_media_id":%q}`, cover.String()))
}

func heldByDraft(claims *fakeClaims, id uuid.UUID) []uuid.UUID {
	return claims.held(postgres.HolderPostDraftCover, id)
}

func TestHolderPostDraftCoverMatchesTheClaimKind(t *testing.T) {
	if postgres.HolderPostDraftCover != "post_draft_cover" {
		t.Fatalf("kind = %q; media-service registers post_draft_cover", postgres.HolderPostDraftCover)
	}
}

// ── save ─────────────────────────────────────────────────────────────────

func TestCreatePostDraftClaimsTheCoverBeforeTheInsert(t *testing.T) {
	s, claims, rows, own := draftCoverRig()
	author := uuid.New()
	cover := ownImage(own, author)
	var claimedAtWrite []uuid.UUID
	var draftID uuid.UUID
	rows.onWrite = func(*uuid.UUID) {
		for k := range claims.claims {
			if k.kind == postgres.HolderPostDraftCover {
				draftID = k.ref
			}
		}
		claimedAtWrite = heldByDraft(claims, draftID)
	}
	d, err := s.CreatePostDraft(context.Background(), author, "", draftPayloadWithCover(&cover), nil)
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "claimed when the row was written", claimedAtWrite, cover)
	if draftID != d.ID {
		t.Fatalf("claim ref = %s, want the draft's id %s", draftID, d.ID)
	}
	sameIDs(t, "after save", heldByDraft(claims, d.ID), cover)
}

func TestCreatePostDraftRefusesAStrangersCoverBeforeClaiming(t *testing.T) {
	s, claims, rows, own := draftCoverRig()
	author := uuid.New()
	theirs := ownImage(own, uuid.New())
	_, err := s.CreatePostDraft(context.Background(), author, "", draftPayloadWithCover(&theirs), nil)
	var coverErr *CoverMediaError
	if !errors.Is(err, ErrMediaNotOwned) || !errors.As(err, &coverErr) {
		t.Fatalf("err = %v, want a CoverMediaError wrapping ErrMediaNotOwned", err)
	}
	if len(*claims.log) != 0 || len(rows.rows) != 0 {
		t.Fatalf("claims=%v rows=%d: a stranger's asset must not be claimed or saved", *claims.log, len(rows.rows))
	}
}

// The post cover rule: finished (confirmed), not refused, an image.
func TestCreatePostDraftRefusesAnUnfinishedRefusedOrVideoCover(t *testing.T) {
	for name, m := range map[string]postgres.MediaOwnership{
		"never uploaded": {Kind: "image", ProcessingStatus: "pending_upload", ModerationStatus: "pending"},
		"failed":         {Kind: "image", ProcessingStatus: "failed", ModerationStatus: "pending"},
		"refused":        {Kind: "image", ProcessingStatus: "ready", ModerationStatus: "rejected"},
		"a video":        {Kind: "video", ProcessingStatus: "ready", ModerationStatus: "passed"},
	} {
		t.Run(name, func(t *testing.T) {
			s, claims, rows, own := draftCoverRig()
			author, cover := uuid.New(), uuid.New()
			m.UploaderID = author
			own[cover] = m
			_, err := s.CreatePostDraft(context.Background(), author, "", draftPayloadWithCover(&cover), nil)
			var coverErr *CoverMediaError
			if !errors.As(err, &coverErr) {
				t.Fatalf("err = %v, want a CoverMediaError", err)
			}
			if len(*claims.log) != 0 || len(rows.rows) != 0 {
				t.Fatalf("claims=%v rows=%d", *claims.log, len(rows.rows))
			}
		})
	}
	// Still processing is confirmed: a draft is saved while the upload works.
	s, _, _, own := draftCoverRig()
	author, cover := uuid.New(), uuid.New()
	own[cover] = postgres.MediaOwnership{UploaderID: author, Kind: "image", ProcessingStatus: "processing", ModerationStatus: "pending"}
	if _, err := s.CreatePostDraft(context.Background(), author, "", draftPayloadWithCover(&cover), nil); err != nil {
		t.Fatalf("a confirmed, still-processing cover: %v", err)
	}
}

func TestCreatePostDraftFailsClosedWithoutAClaimClient(t *testing.T) {
	s, _, rows, own := draftCoverRig()
	s.claims = nil
	author := uuid.New()
	cover := ownImage(own, author)
	if _, err := s.CreatePostDraft(context.Background(), author, "", draftPayloadWithCover(&cover), nil); !errors.Is(err, ErrMediaClaimsUnavailable) {
		t.Fatalf("err = %v, want ErrMediaClaimsUnavailable", err)
	}
	if len(rows.rows) != 0 {
		t.Fatal("a draft was saved naming an unclaimed cover")
	}
	// No cover: no claim needed.
	if _, err := s.CreatePostDraft(context.Background(), author, "", draftPayloadWithCover(nil), nil); err != nil {
		t.Fatalf("a draft with no cover needs no claim client: %v", err)
	}
}

func TestCreatePostDraftCompensatesAFailedInsert(t *testing.T) {
	s, claims, rows, own := draftCoverRig()
	author := uuid.New()
	cover := ownImage(own, author)
	rows.createErr = postgres.ErrDraftQuota
	if _, err := s.CreatePostDraft(context.Background(), author, "", draftPayloadWithCover(&cover), nil); !errors.Is(err, postgres.ErrDraftQuota) {
		t.Fatalf("err = %v", err)
	}
	for k, ids := range claims.claims {
		if len(ids) > 0 {
			t.Fatalf("a refused insert left a claim on %s %s", k.kind, k.ref)
		}
	}
}

func TestCreatePostDraftCoverGoneAtTheClaimIsACoverRefusal(t *testing.T) {
	s, claims, rows, own := draftCoverRig()
	author := uuid.New()
	cover := ownImage(own, author)
	claims.missing[cover] = true
	_, err := s.CreatePostDraft(context.Background(), author, "", draftPayloadWithCover(&cover), nil)
	var coverErr *CoverMediaError
	if !errors.As(err, &coverErr) || !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("err = %v, want a CoverMediaError wrapping ErrMediaNotFound", err)
	}
	if len(rows.rows) != 0 {
		t.Fatal("a draft was saved naming a deleted asset")
	}
}

// ── update / autosave / schedule ─────────────────────────────────────────

func savedDraft(t *testing.T, s *Service, author uuid.UUID, cover *uuid.UUID) *postgres.PostDraft {
	t.Helper()
	d, err := s.CreatePostDraft(context.Background(), author, "", draftPayloadWithCover(cover), nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestUpdatePostDraftMovesTheClaimToTheNewCover(t *testing.T) {
	ctx := context.Background()
	s, claims, _, own := draftCoverRig()
	author := uuid.New()
	c1, c2 := ownImage(own, author), ownImage(own, author)
	d := savedDraft(t, s, author, &c1)

	if _, err := s.UpdatePostDraft(ctx, d.ID, author, "", draftPayloadWithCover(&c2), nil); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "after the cover changed", heldByDraft(claims, d.ID), c2)

	if _, err := s.UpdatePostDraft(ctx, d.ID, author, "", draftPayloadWithCover(nil), nil); err != nil {
		t.Fatal(err)
	}
	if got := heldByDraft(claims, d.ID); len(got) != 0 {
		t.Fatalf("a cover the draft no longer names is still claimed: %v", got)
	}
}

func TestUpdatePostDraftRefusesAStrangersCoverAndKeepsTheOld(t *testing.T) {
	ctx := context.Background()
	s, claims, rows, own := draftCoverRig()
	author := uuid.New()
	mine, theirs := ownImage(own, author), ownImage(own, uuid.New())
	d := savedDraft(t, s, author, &mine)
	before := len(*claims.log)

	_, err := s.UpdatePostDraft(ctx, d.ID, author, "", draftPayloadWithCover(&theirs), nil)
	if !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("err = %v, want ErrMediaNotOwned", err)
	}
	for _, call := range (*claims.log)[before:] {
		if call == "claim" || call == "release" {
			t.Fatalf("a refused update reached media-service: %v", (*claims.log)[before:])
		}
	}
	if got := payloadDraftCover(mustPayload(t, rows.rows[d.ID].Payload)); got == nil || *got != mine {
		t.Fatal("the refused update was written")
	}
	sameIDs(t, "claims", heldByDraft(claims, d.ID), mine)
}

func mustPayload(t *testing.T, raw json.RawMessage) *PostDraftPayload {
	t.Helper()
	p, err := parseDraftPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// An autosave re-sending the cover the draft holds is not refused because
// the asset's state changed since (it was verified when it was claimed).
func TestAutosaveKeepingTheHeldCoverIsNotReverified(t *testing.T) {
	ctx := context.Background()
	s, claims, _, own := draftCoverRig()
	author := uuid.New()
	cover := ownImage(own, author)
	d := savedDraft(t, s, author, &cover)
	own[cover] = postgres.MediaOwnership{UploaderID: author, Kind: "image", ProcessingStatus: "ready", ModerationStatus: "rejected"}
	if _, err := s.UpdatePostDraft(ctx, d.ID, author, "", json.RawMessage(fmt.Sprintf(`{"text":"edited","cover_media_id":%q}`, cover)), nil); err != nil {
		t.Fatalf("autosave: %v", err)
	}
	sameIDs(t, "claims", heldByDraft(claims, d.ID), cover)
}

// Someone else's update is a no-op in the store; it must not move the
// owner's claim either way.
func TestStrangersUpdateChangesNoClaim(t *testing.T) {
	ctx := context.Background()
	s, claims, _, own := draftCoverRig()
	author, stranger := uuid.New(), uuid.New()
	cover := ownImage(own, author)
	strangerCover := ownImage(own, stranger)
	d := savedDraft(t, s, author, &cover)

	if _, err := s.UpdatePostDraft(ctx, d.ID, stranger, "", draftPayloadWithCover(&strangerCover), nil); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("err = %v, want ErrDraftNotFound", err)
	}
	sameIDs(t, "the owner's claim", heldByDraft(claims, d.ID), cover)
	if _, err := s.UpdatePostDraft(ctx, d.ID, stranger, "", draftPayloadWithCover(nil), nil); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("err = %v, want ErrDraftNotFound", err)
	}
	sameIDs(t, "the owner's claim after a stranger's clear", heldByDraft(claims, d.ID), cover)
}

// A draft mid-publish refuses edits; its claim stays as it is.
func TestUpdateOfAPublishingDraftKeepsItsClaim(t *testing.T) {
	ctx := context.Background()
	s, claims, rows, own := draftCoverRig()
	author := uuid.New()
	c1, c2 := ownImage(own, author), ownImage(own, author)
	d := savedDraft(t, s, author, &c1)
	rows.setStatus(d.ID, "publishing")
	if _, err := s.UpdatePostDraft(ctx, d.ID, author, "", draftPayloadWithCover(&c2), nil); !errors.Is(err, ErrDraftNotEditable) {
		t.Fatalf("err = %v, want ErrDraftNotEditable", err)
	}
	sameIDs(t, "claims", heldByDraft(claims, d.ID), c1)
}

// Scheduling re-sends the payload: the cover the draft names stays claimed
// and media-service is not asked anything.
func TestScheduleKeepsTheDraftsCoverClaim(t *testing.T) {
	ctx := context.Background()
	s, claims, _, own := draftCoverRig()
	author := uuid.New()
	cover := ownImage(own, author)
	d := savedDraft(t, s, author, &cover)
	before := len(*claims.log)
	at := time.Now().Add(time.Hour)
	if _, _, err := s.PublishPostDraft(ctx, d.ID, author, &at); err != nil {
		t.Fatal(err)
	}
	for _, call := range (*claims.log)[before:] {
		if call == "claim" || call == "release" {
			t.Fatalf("scheduling changed claims: %v", (*claims.log)[before:])
		}
	}
	sameIDs(t, "claims after scheduling", heldByDraft(claims, d.ID), cover)
}

// ── delete ───────────────────────────────────────────────────────────────

func TestDeletePostDraftReleasesItsCover(t *testing.T) {
	ctx := context.Background()
	s, claims, _, own := draftCoverRig()
	author := uuid.New()
	cover := ownImage(own, author)
	d := savedDraft(t, s, author, &cover)

	if err := s.DeletePostDraft(ctx, d.ID, uuid.New()); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("a stranger's delete: %v", err)
	}
	sameIDs(t, "after a stranger's delete", heldByDraft(claims, d.ID), cover)

	if err := s.DeletePostDraft(ctx, d.ID, author); err != nil {
		t.Fatal(err)
	}
	if got := heldByDraft(claims, d.ID); len(got) != 0 {
		t.Fatalf("a deleted draft kept its cover claim: %v", got)
	}
}

// ── publish: the draft id is the post id ─────────────────────────────────

// Publishing releases the DRAFT's claim and leaves the post's cover claim,
// which has the same ref under another kind, alone.
func TestFinishPublishReleasesTheDraftClaimNotThePosts(t *testing.T) {
	ctx := context.Background()
	s, claims, _, own := draftCoverRig()
	author := uuid.New()
	cover := ownImage(own, author)
	d := savedDraft(t, s, author, &cover)
	// CreatePost's claim, under the draft's id (the post id).
	if err := s.setPostCoverClaimed(ctx, d.ID, cover, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.finishPostDraftPublish(ctx, d.ID, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := heldByDraft(claims, d.ID); len(got) != 0 {
		t.Fatalf("a published draft kept its claim: %v", got)
	}
	sameIDs(t, "the post's cover claim", claims.held(postgres.HolderPostCover, d.ID), cover)
}

// A failed publish retry compensates its post_cover claim; the draft's own
// claim, same ref, must survive it.
func TestPostCoverCompensationKeepsTheDraftClaim(t *testing.T) {
	ctx := context.Background()
	s, claims, _, own := draftCoverRig()
	author := uuid.New()
	cover := ownImage(own, author)
	d := savedDraft(t, s, author, &cover)
	_ = s.setPostCoverClaimed(ctx, d.ID, cover, func() error { return errors.New("insert failed") })
	if got := claims.held(postgres.HolderPostCover, d.ID); len(got) != 0 {
		t.Fatalf("the failed post write kept its claim: %v", got)
	}
	sameIDs(t, "the draft's claim", heldByDraft(claims, d.ID), cover)
}

// A blocked or lost-claim publish does not release: the draft is still the
// author's to fix.
func TestFinishPublishThatLostTheClaimReleasesNothing(t *testing.T) {
	ctx := context.Background()
	s, claims, rows, own := draftCoverRig()
	author := uuid.New()
	cover := ownImage(own, author)
	d := savedDraft(t, s, author, &cover)
	rows.setStatus(d.ID, "blocked")
	s.settleMediaHolderBestEffort(ctx, postgres.HolderPostDraftCover, d.ID)
	sameIDs(t, "a blocked draft's claim", heldByDraft(claims, d.ID), cover)
	delete(rows.rows, d.ID) // MarkPostDraftPublished answers ErrDraftClaimLost
	rows.holders.set(postgres.HolderPostDraftCover, d.ID, true, cover)
	if err := s.finishPostDraftPublish(ctx, d.ID, d.ID, nil); !errors.Is(err, postgres.ErrDraftClaimLost) {
		t.Fatalf("err = %v", err)
	}
	sameIDs(t, "claims after a lost finalize", heldByDraft(claims, d.ID), cover)
}

// A deleted draft holds nothing and the update cannot land: no claim.
func TestUpdateOfADeletedDraftClaimsNothing(t *testing.T) {
	ctx := context.Background()
	s, claims, _, own := draftCoverRig()
	author := uuid.New()
	c1, c2 := ownImage(own, author), ownImage(own, author)
	d := savedDraft(t, s, author, &c1)
	if err := s.DeletePostDraft(ctx, d.ID, author); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePostDraft(ctx, d.ID, author, "", draftPayloadWithCover(&c2), nil); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("err = %v, want ErrDraftNotFound", err)
	}
	if got := heldByDraft(claims, d.ID); len(got) != 0 {
		t.Fatalf("an update of a deleted draft claimed %v", got)
	}
}
