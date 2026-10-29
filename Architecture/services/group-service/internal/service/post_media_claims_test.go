package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/atpost/group-service/internal/mediaclaims"
	"github.com/atpost/group-service/internal/mediaclient"
	"github.com/google/uuid"
)

func TestPostAttachmentChangeClaimsEveryIDOnceUnderThePost(t *testing.T) {
	post, a, b := uuid.New(), uuid.New(), uuid.New()
	got := postAttachmentChange(post, []uuid.UUID{a, b, a, uuid.Nil})
	if len(got) != 1 || got[0].Kind != mediaclient.KindGroupPostAttachment || got[0].Ref != post ||
		!slices.Equal(got[0].Add, []uuid.UUID{a, b}) || len(got[0].Drop) != 0 {
		t.Fatalf("change %+v", got)
	}
}

func TestReleasePostMediaReleasesAllOnlyWhenThePostHeldMedia(t *testing.T) {
	rc := &recordingClaims{}
	s := (&Service{}).WithMedia(mediaclaims.New(nil, rc))
	post := uuid.New()
	s.releasePostMedia(context.Background(), post, json.RawMessage(`[]`))
	if len(rc.released) != 0 {
		t.Fatalf("a post with no attachments called media-service: %v", rc.released)
	}
	s.releasePostMedia(context.Background(), post, json.RawMessage(`["`+uuid.NewString()+`"]`))
	if !slices.Equal(rc.released, []string{mediaclient.KindGroupPostAttachment + "|" + post.String()}) {
		t.Fatalf("released %v", rc.released)
	}
}

// The write path's order: parse, fix the id, check the media is the
// author's own finished upload, (scope it anonymous), and only then claim and
// insert under the protocol.
func TestPostWriteChecksThenClaimsBeforeTheInsert(t *testing.T) {
	body := codeOnly(funcSource(t, "cross_post.go", "createOnePost"))
	id := strings.Index(body, "post.ID = uuid.New()")
	change := strings.Index(body, "postAttachmentChange(post.ID")
	verify := strings.Index(body, "s.media.Verify(ctx, actorID, media)")
	anon := strings.Index(body, "s.anonymizeAttachments(")
	write := strings.Index(body, "s.media.Write(ctx, media, func() error {")
	insert := strings.Index(body, "s.store.CreateGroupPostV2(")
	if id < 0 || change < id {
		t.Fatal("the post id is not fixed before its attachments are claimed against it")
	}
	if verify < change || anon < verify {
		t.Fatal("the attachments are not checked before they are scoped or claimed")
	}
	if write < anon || insert < write {
		t.Fatal("the insert does not run inside the claim protocol")
	}
	if !strings.Contains(body[verify:anon], `return "", nil, err`) {
		t.Fatal("a refused attachment does not refuse the post")
	}
}

// Every path that stops a post being shown releases what it held.
func TestPostDeletesReleaseTheirAttachments(t *testing.T) {
	for _, c := range []struct{ fn, store, release string }{
		{"DeleteGroupPostV2", "s.store.DeleteGroupPostV2(", "s.releasePostMedia(ctx, postID, p.Attachments)"},
		{"RejectPendingPost", "s.store.RejectGroupPost(", "s.releasePostMedia(ctx, postID, post.Attachments)"},
		{"DeleteGroupPost", "s.store.DeleteGroupPost(", "mediaclient.KindGroupPostAttachment"},
	} {
		body := codeOnly(funcSource(t, "group.go", c.fn))
		w, r := strings.Index(body, c.store), strings.Index(body, c.release)
		if w < 0 || r < w {
			t.Errorf("%s does not release its attachments after the write (write=%d release=%d)", c.fn, w, r)
		}
	}
	body := codeOnly(funcSource(t, "media_claims.go", "releaseGroupMedia"))
	if !strings.Contains(body, "mediaclient.KindGroupPostAttachment") || !strings.Contains(body, "s.store.PostIDsWithAttachments(") {
		t.Fatal("a deleted group does not release its posts' attachments")
	}
}
