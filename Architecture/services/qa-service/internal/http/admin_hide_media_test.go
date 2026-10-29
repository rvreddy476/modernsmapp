package http

import (
	"context"
	"net/http"
	"testing"

	"github.com/atpost/qa-service/internal/claimsync"
	"github.com/atpost/qa-service/internal/mediaclaims"
	"github.com/atpost/qa-service/internal/mediaclient"
	"github.com/atpost/qa-service/internal/service"
	"github.com/atpost/qa-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// deadHolders says every removed question or answer is dead and holds one
// media id.
type deadHolders struct{}

func (deadHolders) MediaHoldersAmong(_ context.Context, q, a []uuid.UUID) ([]store.MediaHolder, error) {
	var out []store.MediaHolder
	for _, id := range q {
		out = append(out, store.MediaHolder{Kind: mediaclient.KindQuestionMedia, Ref: id})
	}
	for _, id := range a {
		out = append(out, store.MediaHolder{Kind: mediaclient.KindAnswerMedia, Ref: id})
	}
	return out, nil
}
func (deadHolders) LockMediaHolder(context.Context, uuid.UUID) (func(), error) { return func() {}, nil }
func (deadHolders) MediaClaimRowFor(_ context.Context, kind string, ref uuid.UUID) (claimsync.Row, error) {
	return claimsync.Row{Kind: kind, Ref: ref, MediaIDs: []uuid.UUID{uuid.New()}}, nil
}

type releaseLog struct{ released []string }

func (r *releaseLog) Claim(context.Context, string, uuid.UUID, []uuid.UUID) error { return nil }
func (r *releaseLog) Release(_ context.Context, kind string, ref uuid.UUID, _ []uuid.UUID) error {
	r.released = append(r.released, kind+"|"+ref.String())
	return nil
}

type refusingHideStore struct{ adminFakeStore }

func (refusingHideStore) AdminHideContent(context.Context, uuid.UUID, string, uuid.UUID, *uuid.UUID, string) (*store.ModerationAction, error) {
	return nil, store.ErrAdminConflict
}

// The admin-service hide releases what the hidden question or answer held;
// a refused hide releases nothing.
func TestAdminToken_HideReleasesMedia(t *testing.T) {
	rg := newAdminTokenRig(t)
	route := func(st AdminStore, rel *releaseLog) *gin.Engine {
		svc := service.New(nil, nil).WithMedia(mediaclaims.New(nil, rel)).WithMediaHolders(deadHolders{})
		r := gin.New()
		New(svc).WithInternalKey(adminTestInternalKey).WithServiceAuth(rg.v).WithAdminStore(st).RegisterRoutes(r)
		return r
	}
	for _, tc := range []struct{ target, perm, kind string }{
		{"questions", PermQuestionsModerate, mediaclient.KindQuestionMedia},
		{"answers", PermAnswersModerate, mediaclient.KindAnswerMedia},
	} {
		id := uuid.New()
		path := InternalAdminPrefix + "/" + tc.target + "/" + id.String() + "/hide"
		hdr := bearer(rg.mint(t, rg.admin, AudienceQA, []string{tc.perm}, rg.actor.String()))

		rel := &releaseLog{}
		if w := adminServe(route(&adminFakeStore{}, rel), http.MethodPost, path, `{"reason":"spam"}`, hdr); w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", tc.target, w.Code, w.Body.String())
		}
		if want := tc.kind + "|" + id.String(); len(rel.released) != 1 || rel.released[0] != want {
			t.Fatalf("%s: released %v, want [%s]", tc.target, rel.released, want)
		}

		rel = &releaseLog{}
		if w := adminServe(route(&refusingHideStore{}, rel), http.MethodPost, path, `{"reason":"spam"}`, hdr); w.Code == http.StatusOK {
			t.Fatalf("%s: a refused hide answered 200", tc.target)
		}
		if len(rel.released) != 0 {
			t.Fatalf("%s: a refused hide released %v", tc.target, rel.released)
		}
	}
}
