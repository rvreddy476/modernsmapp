package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/atpost/channel-service/internal/mediaclient"
	"github.com/google/uuid"
)

// recordingClaims logs every claim call into a shared journal, so a test can
// assert the ORDER of claims, the write and releases.
type recordingClaims struct {
	journal  *[]string
	claimErr map[string]error // kind -> error for Claim
	relErr   error
	ctxErrs  []error // ctx.Err() seen by each Release
}

func (r *recordingClaims) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	*r.journal = append(*r.journal, fmt.Sprintf("claim %s %s %v", kind, ref, ids))
	return r.claimErr[kind]
}

func (r *recordingClaims) Release(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	*r.journal = append(*r.journal, fmt.Sprintf("release %s %s %v", kind, ref, ids))
	r.ctxErrs = append(r.ctxErrs, ctx.Err())
	return r.relErr
}

func claimRig() (*Service, *recordingClaims, *[]string) {
	var journal []string
	rc := &recordingClaims{journal: &journal, claimErr: map[string]error{}}
	return (&Service{}).WithMediaClaims(rc), rc, &journal
}

func journalWrite(j *[]string, err error) func() error {
	return func() error {
		*j = append(*j, "write")
		return err
	}
}

func wantJournal(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("journal:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// Claim, then write, then release what the row dropped — in that order.
func TestClaimsComeBeforeTheWriteAndReleasesAfter(t *testing.T) {
	s, _, j := claimRig()
	ref, a, b := uuid.New(), uuid.New(), uuid.New()
	err := s.writeWithClaims(context.Background(), []claimChange{
		{kind: claimKindUpdate, ref: ref, add: []uuid.UUID{a}, drop: []uuid.UUID{b}},
	}, journalWrite(j, nil))
	if err != nil {
		t.Fatal(err)
	}
	wantJournal(t, *j,
		fmt.Sprintf("claim channel_update %s [%s]", ref, a),
		"write",
		fmt.Sprintf("release channel_update %s [%s]", ref, b))
}

// A refused claim stops the write: the row never names unclaimed media.
func TestRefusedClaimBlocksTheWrite(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want error
	}{
		"no such media":      {mediaclient.ErrNotFound, ErrMediaNotFound},
		"media-service down": {fmt.Errorf("%w: status 502", mediaclient.ErrUnavailable), ErrMediaUnavailable},
		"anything else":      {errors.New("eof"), ErrMediaUnavailable},
	} {
		s, rc, j := claimRig()
		rc.claimErr[claimKindUpdate] = tc.err
		ref, a := uuid.New(), uuid.New()
		err := s.writeWithClaims(context.Background(), []claimChange{
			{kind: claimKindUpdate, ref: ref, add: []uuid.UUID{a}},
		}, journalWrite(j, nil))
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
		wantJournal(t, *j, fmt.Sprintf("claim channel_update %s [%s]", ref, a))
	}
}

// A second change's refusal releases the first change's claim.
func TestPartialClaimIsCompensated(t *testing.T) {
	s, rc, j := claimRig()
	rc.claimErr[claimKindBanner] = mediaclient.ErrNotFound
	ref, avatar, banner := uuid.New(), uuid.New(), uuid.New()
	err := s.writeWithClaims(context.Background(), []claimChange{
		{kind: claimKindAvatar, ref: ref, add: []uuid.UUID{avatar}},
		{kind: claimKindBanner, ref: ref, add: []uuid.UUID{banner}},
	}, journalWrite(j, nil))
	if !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("got %v", err)
	}
	wantJournal(t, *j,
		fmt.Sprintf("claim channel_avatar %s [%s]", ref, avatar),
		fmt.Sprintf("claim channel_banner %s [%s]", ref, banner),
		fmt.Sprintf("release channel_avatar %s [%s]", ref, avatar))
}

// A failed write releases exactly what it claimed and nothing it still holds.
func TestFailedWriteReleasesOnlyWhatItClaimed(t *testing.T) {
	s, _, j := claimRig()
	ref, added, dropped := uuid.New(), uuid.New(), uuid.New()
	boom := errors.New("duplicate key")
	err := s.writeWithClaims(context.Background(), []claimChange{
		{kind: claimKindUpdate, ref: ref, add: []uuid.UUID{added}, drop: []uuid.UUID{dropped}},
	}, journalWrite(j, boom))
	if !errors.Is(err, boom) {
		t.Fatalf("the write's own error must come back, got %v", err)
	}
	wantJournal(t, *j,
		fmt.Sprintf("claim channel_update %s [%s]", ref, added),
		"write",
		fmt.Sprintf("release channel_update %s [%s]", ref, added))
}

// The compensation runs even when the request context is already gone.
func TestReleaseOutlivesTheRequest(t *testing.T) {
	s, rc, j := claimRig()
	ctx, cancel := context.WithCancel(context.Background())
	err := s.writeWithClaims(ctx, []claimChange{
		{kind: claimKindUpdate, ref: uuid.New(), add: []uuid.UUID{uuid.New()}},
	}, func() error {
		cancel() // the client hung up while the write ran
		return journalWrite(j, errors.New("canceled"))()
	})
	if err == nil {
		t.Fatal("want the write error")
	}
	if len(rc.ctxErrs) != 1 || rc.ctxErrs[0] != nil {
		t.Fatalf("the compensating release ran on a dead context: %v", rc.ctxErrs)
	}
}

// Fail closed only for what ADDS media.
func TestNoClaimClient(t *testing.T) {
	var j []string
	s := &Service{}
	err := s.writeWithClaims(context.Background(), []claimChange{
		{kind: claimKindUpdate, ref: uuid.New(), add: []uuid.UUID{uuid.New()}},
	}, journalWrite(&j, nil))
	if !errors.Is(err, ErrMediaUnavailable) || len(j) != 0 {
		t.Fatalf("adding media with no claim client: err=%v journal=%v", err, j)
	}
	err = s.writeWithClaims(context.Background(), []claimChange{
		{kind: claimKindUpdate, ref: uuid.New(), drop: []uuid.UUID{uuid.New()}},
	}, journalWrite(&j, nil))
	if err != nil || len(j) != 1 {
		t.Fatalf("removing media must not need media-service: err=%v journal=%v", err, j)
	}
}

// A failed release after a successful write does not fail the write.
func TestReleaseFailureDoesNotUndoTheWrite(t *testing.T) {
	s, rc, j := claimRig()
	rc.relErr = mediaclient.ErrUnavailable
	err := s.writeWithClaims(context.Background(), []claimChange{
		{kind: claimKindUpdate, ref: uuid.New(), drop: []uuid.UUID{uuid.New()}},
	}, journalWrite(j, nil))
	if err != nil {
		t.Fatalf("a committed write must succeed even if its release fails (stale claim, not a dangling one): %v", err)
	}
}

func TestMediaDiff(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	added, removed := mediaDiff([]uuid.UUID{a, b, b}, []uuid.UUID{b, c, c, uuid.Nil})
	if len(added) != 1 || added[0] != c {
		t.Errorf("added %v, want [%s]", added, c)
	}
	if len(removed) != 1 || removed[0] != a {
		t.Errorf("removed %v, want [%s]", removed, a)
	}
	if ad, rm := mediaDiff([]uuid.UUID{a}, []uuid.UUID{a}); len(ad)+len(rm) != 0 {
		t.Errorf("an unchanged list changes nothing: %v %v", ad, rm)
	}
	ad, rm := optionalMediaDiff(&a, &b)
	if len(ad) != 1 || ad[0] != b || len(rm) != 1 || rm[0] != a {
		t.Errorf("replaced avatar: %v %v", ad, rm)
	}
	if ad, rm := optionalMediaDiff(nil, &a); len(ad) != 1 || len(rm) != 0 {
		t.Errorf("new avatar: %v %v", ad, rm)
	}
	nilID := uuid.Nil
	if ad, rm := optionalMediaDiff(&a, &nilID); len(ad) != 0 || len(rm) != 1 {
		t.Errorf("cleared avatar: %v %v", ad, rm)
	}
}

// Every channel writer that stores media runs its store write INSIDE
// writeWithClaims (claimed before, compensated on failure), and the deleters
// release after the row is gone. Pinned on the source: these writers need a
// database (media_claims_integration_test.go proves them in CI).
func TestChannelMediaWritersUseTheClaimProtocol(t *testing.T) {
	raw, err := os.ReadFile("channel.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, tc := range []struct{ sig, before, after string }{
		{"func (s *Service) CreateChannel(", "{kind: claimKindAvatar, ref: ch.ID, add: avatarAdd}", "s.store.CreateChannel("},
		{"func (s *Service) CreateChannel(", "{kind: claimKindBanner, ref: ch.ID, add: bannerAdd}", "s.store.CreateChannel("},
		{"func (s *Service) UpdateChannel(", "{kind: claimKindAvatar, ref: ch.ID, add: avatarAdd, drop: avatarDrop}", "s.store.UpdateChannel("},
		{"func (s *Service) UpdateChannel(", "{kind: claimKindBanner, ref: ch.ID, add: bannerAdd, drop: bannerDrop}", "s.store.UpdateChannel("},
		{"func (s *Service) UpdateChannel(", "optionalMediaDiff(prevAvatar, ch.AvatarMediaID)", "s.writeWithClaims("},
		{"func (s *Service) CreateUpdate(", "{kind: claimKindUpdate, ref: u.ID, add: uniqueMedia(mediaIDs)}", "s.store.CreateUpdate("},
		{"func (s *Service) EditUpdate(", "{kind: claimKindUpdate, ref: u.ID, add: added, drop: removed}", "s.store.UpdateUpdate("},
		{"func (s *Service) EditUpdate(", "mediaDiff(prevMedia, u.MediaIDs)", "s.writeWithClaims("},
		{"func (s *Service) DeleteUpdate(", "s.store.DeleteUpdate(", "s.release(ctx, claimKindUpdate, updateID, nil)"},
		// Read-modify-write of a holder row happens under its media lock.
		{"func (s *Service) EditUpdate(", "s.lockMediaHolder(ctx, updateID)", "s.store.GetUpdate("},
		{"func (s *Service) DeleteUpdate(", "s.lockMediaHolder(ctx, updateID)", "s.store.GetUpdate("},
		{"func (s *Service) UpdateChannel(", "s.lockMediaHolder(ctx, channelID)", "s.store.GetChannelByID("},
		{"func (s *Service) DeleteChannel(", "s.store.DeleteChannel(", "s.releaseChannelClaims(ctx, channelID)"},
	} {
		body, ok := funcBody(src, tc.sig)
		if !ok {
			t.Fatalf("%s not found", tc.sig)
		}
		b, a := strings.Index(body, tc.before), strings.Index(body, tc.after)
		if b < 0 || a < 0 || b > a {
			t.Errorf("%s: want %q before %q (at %d, %d)", tc.sig, tc.before, tc.after, b, a)
		}
	}
}
