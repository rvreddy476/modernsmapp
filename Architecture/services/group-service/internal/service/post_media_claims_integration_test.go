//go:build integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/atpost/group-service/internal/claimsync"
	"github.com/atpost/group-service/internal/mediaclaims"
	"github.com/atpost/group-service/internal/mediaclient"
	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
DB-backed proof that a group post's attachments hold a claim (kind
group_post_attachment, ref = the post id) from before the row names them
until the post is deleted, rejected, or its group deleted. Same harness as
media_claims_integration_test.go (CI only, *_test databases):

	GROUP_POSTGRES_DSN=…/group_it_test go test -tags integration ./internal/service/ -run PostMediaClaimsIT -p 1
*/

func setupPostMediaClaimsIT(t *testing.T) (*Service, *store.Store, *pgxpool.Pool) {
	t.Helper()
	svc, st, pool := setupMediaClaimsIT(t)
	// The post columns migrations 013 and 014 add (setup.sql predates them).
	if _, err := pool.Exec(context.Background(), `
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS is_anonymous BOOLEAN NOT NULL DEFAULT FALSE;
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS anon_alias UUID;
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS cross_post_group_id UUID;
	`); err != nil {
		t.Fatal(err)
	}
	return svc, st, pool
}

func attachmentsOf(ids ...uuid.UUID) json.RawMessage {
	raw, _ := json.Marshal(ids)
	return raw
}

func TestPostMediaClaimsIT_PostLifecycle(t *testing.T) {
	svc, st, pool := setupPostMediaClaimsIT(t)
	ctx := context.Background()
	admin := uuid.New()
	groupID := seedAdminGroup(t, pool, admin)
	a, b := readyAsset(admin), readyAsset(admin)
	f := newFakeMedia(a, b)
	svc.WithMedia(mediaclaims.New(f, f))

	_, post, err := svc.createOnePost(ctx, admin, groupID, CreateGroupPostV2Params{
		Body: "hello", Attachments: attachmentsOf(a.ID, b.ID, a.ID),
	}, nil)
	if err != nil || post == nil {
		t.Fatalf("create: %v", err)
	}
	if !f.holds(mediaclient.KindGroupPostAttachment, post.ID, a.ID) || !f.holds(mediaclient.KindGroupPostAttachment, post.ID, b.ID) {
		t.Fatalf("a post does not hold claims on its attachments under its own id: %v", f.held)
	}
	if got, err := st.GetGroupPostV2(ctx, post.ID); err != nil || got.ID != post.ID {
		t.Fatalf("the post row is not under the claimed id: %v", err)
	}

	if err := svc.DeleteGroupPostV2(ctx, admin, groupID, post.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(f.held[fakeKey(mediaclient.KindGroupPostAttachment, post.ID)]) != 0 {
		t.Fatalf("a deleted post still holds its attachments: %v", f.held)
	}
}

func TestPostMediaClaimsIT_RefusedAttachmentsWriteNothing(t *testing.T) {
	svc, st, pool := setupPostMediaClaimsIT(t)
	ctx := context.Background()
	admin := uuid.New()
	groupID := seedAdminGroup(t, pool, admin)
	mine, theirs, pending := readyAsset(admin), readyAsset(uuid.New()), readyAsset(admin)
	pending.ProcessingStatus = "pending_upload"
	f := newFakeMedia(mine, theirs, pending)
	svc.WithMedia(mediaclaims.New(f, f))

	if _, _, err := svc.createOnePost(ctx, admin, groupID, CreateGroupPostV2Params{
		Body: "x", Attachments: attachmentsOf(mine.ID, theirs.ID),
	}, nil); !errors.Is(err, mediaclaims.ErrMediaNotOwned) {
		t.Fatalf("someone else's attachment = %v", err)
	}
	if _, _, err := svc.createOnePost(ctx, admin, groupID, CreateGroupPostV2Params{
		Body: "x", Attachments: attachmentsOf(pending.ID),
	}, nil); !errors.Is(err, mediaclaims.ErrMediaNotReady) {
		t.Fatalf("an unconfirmed attachment = %v", err)
	}
	if _, _, err := svc.createOnePost(ctx, admin, groupID, CreateGroupPostV2Params{
		Body: "x", Attachments: json.RawMessage(`[{"url":"https://elsewhere"}]`),
	}, nil); err == nil {
		t.Fatal("an attachment that names no media id was stored")
	}
	if posts, _ := st.ListGroupPostsV2(ctx, groupID, nil, "", 10, 0); len(posts) != 0 {
		t.Fatalf("a post with refused attachments was written: %d", len(posts))
	}
	if len(f.held) != 0 {
		t.Fatalf("a refused attach left claims: %v", f.held)
	}

	// Unconfigured: a post with attachments is refused, one without is not.
	svc.WithMedia(nil)
	if _, _, err := svc.createOnePost(ctx, admin, groupID, CreateGroupPostV2Params{
		Body: "x", Attachments: attachmentsOf(mine.ID),
	}, nil); !errors.Is(err, mediaclaims.ErrMediaUnavailable) {
		t.Fatalf("no media-service = %v", err)
	}
	if _, post, err := svc.createOnePost(ctx, admin, groupID, CreateGroupPostV2Params{Body: "text only"}, nil); err != nil || post == nil {
		t.Fatalf("a post without attachments needed media-service: %v", err)
	}
}

// A moderator's rejection and a group delete release what the posts held.
func TestPostMediaClaimsIT_RejectAndGroupDeleteRelease(t *testing.T) {
	svc, _, pool := setupPostMediaClaimsIT(t)
	ctx := context.Background()
	admin := uuid.New()
	groupID := seedAdminGroup(t, pool, admin)
	a, b := readyAsset(admin), readyAsset(admin)
	f := newFakeMedia(a, b)
	svc.WithMedia(mediaclaims.New(f, f))

	_, pending, err := svc.createOnePost(ctx, admin, groupID, CreateGroupPostV2Params{Body: "p", Attachments: attachmentsOf(a.ID)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE group_posts SET status = 'pending_approval' WHERE id = $1`, pending.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.RejectPendingPost(ctx, admin, groupID, pending.ID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if f.holds(mediaclient.KindGroupPostAttachment, pending.ID, a.ID) {
		t.Fatal("a rejected post still holds its attachment")
	}

	_, kept, err := svc.createOnePost(ctx, admin, groupID, CreateGroupPostV2Params{Body: "k", Attachments: attachmentsOf(b.ID)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !f.holds(mediaclient.KindGroupPostAttachment, kept.ID, b.ID) {
		t.Fatal("the second post holds no claim")
	}
	if err := svc.DeleteGroup(ctx, admin, groupID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if f.holds(mediaclient.KindGroupPostAttachment, kept.ID, b.ID) {
		t.Fatalf("a deleted group's post still holds its attachment: %v", f.held)
	}
}

func TestPostMediaClaimsIT_ReconcileBackfillsAndReleasesPosts(t *testing.T) {
	_, st, pool := setupPostMediaClaimsIT(t)
	ctx := context.Background()
	admin := uuid.New()
	groupID := seedAdminGroup(t, pool, admin)
	a, b, gone := readyAsset(admin), readyAsset(admin), uuid.New()
	f := newFakeMedia(a, b)
	// A post written before the protocol, in the object shape, naming an
	// asset media-service no longer has next to one it has.
	var postID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO group_posts (group_id, author_id, body, attachments)
		VALUES ($1, $2, 'legacy', $3) RETURNING id`, groupID, admin.String(),
		`[{"media_id":"`+a.ID.String()+`"},"`+gone.String()+`",{"url":"x"}]`).Scan(&postID); err != nil {
		t.Fatal(err)
	}
	var deadID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO group_posts (group_id, author_id, body, attachments, status)
		VALUES ($1, $2, 'gone', $3, 'deleted') RETURNING id`, groupID, admin.String(),
		string(attachmentsOf(b.ID))).Scan(&deadID); err != nil {
		t.Fatal(err)
	}
	if err := f.Claim(ctx, mediaclient.KindGroupPostAttachment, deadID, []uuid.UUID{b.ID}); err != nil {
		t.Fatal(err) // a release that was lost when the post was deleted
	}
	pagers := []claimsync.Pager{st.GroupPostMediaClaimRows}
	rep, err := claimsync.Run(ctx, st, f, pagers, true)
	if err != nil || rep.Failed != 0 {
		t.Fatalf("apply: %v %s", err, rep)
	}
	if !f.holds(mediaclient.KindGroupPostAttachment, postID, a.ID) {
		t.Fatalf("the backfill did not claim a legacy post's attachment: %v", f.held)
	}
	if len(rep.Dangling) != 1 || rep.Dangling[0].MediaID != gone {
		t.Fatalf("the missing attachment was not reported: %+v", rep.Dangling)
	}
	if f.holds(mediaclient.KindGroupPostAttachment, deadID, b.ID) {
		t.Fatal("the repair did not release a deleted post's attachment")
	}
	if _, err := pool.Exec(ctx, `UPDATE groups SET status = 'deleted' WHERE id = $1`, groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := claimsync.Run(ctx, st, f, pagers, true); err != nil {
		t.Fatal(err)
	}
	if f.holds(mediaclient.KindGroupPostAttachment, postID, a.ID) {
		t.Fatal("a deleted group's post still holds media after the repair")
	}
}
