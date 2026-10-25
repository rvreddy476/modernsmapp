package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/atpost/identity-profile-service/internal/mediaclient"
	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
)

// One journal for the store and the claim client, so a test can assert the
// ORDER of lock, read, claim, write and release.

type claimJournal struct{ lines []string }

func (j *claimJournal) add(format string, args ...any) {
	j.lines = append(j.lines, fmt.Sprintf(format, args...))
}

type fakeMediaStore struct {
	j        *claimJournal
	held     map[string]*uuid.UUID // kind -> id; nil map entry = column empty
	exists   bool
	lockErr  error
	readErr  error
	writeErr error
	// vanish makes the write find no row (the profile was purged after the read).
	vanish bool
}

func (f *fakeMediaStore) LockMediaHolder(_ context.Context, ref uuid.UUID) (func(), error) {
	if f.lockErr != nil {
		return nil, f.lockErr
	}
	f.j.add("lock")
	return func() { f.j.add("unlock") }, nil
}

func (f *fakeMediaStore) GetProfileMediaID(_ context.Context, _ uuid.UUID, kind string) (*uuid.UUID, bool, error) {
	f.j.add("read %s", kind)
	if f.readErr != nil {
		return nil, false, f.readErr
	}
	return f.held[kind], f.exists, nil
}

func (f *fakeMediaStore) SetProfileMediaID(_ context.Context, _ uuid.UUID, kind string, id uuid.UUID) (bool, error) {
	f.j.add("write %s %s", kind, id)
	if f.writeErr != nil {
		return false, f.writeErr
	}
	if f.vanish || !f.exists {
		return false, nil
	}
	f.held[kind] = &id
	return true, nil
}

type fakeClaims struct {
	j        *claimJournal
	claimErr error
	relErr   error
	relCtx   []error // ctx.Err() seen by each Release
}

func (f *fakeClaims) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.j.add("claim %s %s %v", kind, ref, ids)
	return f.claimErr
}

func (f *fakeClaims) Release(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.j.add("release %s %s %v", kind, ref, ids)
	f.relCtx = append(f.relCtx, ctx.Err())
	return f.relErr
}

func mediaRig(prevAvatar *uuid.UUID) (*Service, *fakeMediaStore, *fakeClaims, *claimJournal) {
	j := &claimJournal{}
	st := &fakeMediaStore{j: j, exists: true, held: map[string]*uuid.UUID{mediaclient.KindProfileAvatar: prevAvatar}}
	cl := &fakeClaims{j: j}
	svc := New(nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithProfileMediaStore(st).WithMediaClaims(cl)
	return svc, st, cl, j
}

func wantJournal(t *testing.T, j *claimJournal, want ...string) {
	t.Helper()
	if strings.Join(j.lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("journal:\n  %s\nwant:\n  %s", strings.Join(j.lines, "\n  "), strings.Join(want, "\n  "))
	}
}

func idp(id uuid.UUID) *uuid.UUID { return &id }

// Lock, read, claim the new image, write, release the old one — in that
// order, all under the lock.
func TestAvatarChangeClaimsBeforeTheWriteAndReleasesTheOldAfter(t *testing.T) {
	old, next := uuid.New(), uuid.New()
	svc, st, _, j := mediaRig(idp(old))
	if err := svc.UpdateAvatar(context.Background(), testUserID, next); err != nil {
		t.Fatal(err)
	}
	wantJournal(t, j,
		"lock",
		"read profile_avatar",
		fmt.Sprintf("claim profile_avatar %s [%s]", testUserID, next),
		fmt.Sprintf("write profile_avatar %s", next),
		fmt.Sprintf("release profile_avatar %s [%s]", testUserID, old),
		"unlock")
	if *st.held[mediaclient.KindProfileAvatar] != next {
		t.Fatal("the avatar was not written")
	}
}

func TestFirstCoverClaimsAndReleasesNothing(t *testing.T) {
	next := uuid.New()
	svc, _, _, j := mediaRig(nil)
	if err := svc.UpdateCover(context.Background(), testUserID, next); err != nil {
		t.Fatal(err)
	}
	wantJournal(t, j,
		"lock",
		"read profile_cover",
		fmt.Sprintf("claim profile_cover %s [%s]", testUserID, next),
		fmt.Sprintf("write profile_cover %s", next),
		"unlock")
}

// A refused claim stops the write: the profile never names unclaimed media.
func TestRefusedClaimBlocksTheWrite(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want error
	}{
		"no such media":      {mediaclient.ErrNotFound, ErrMediaNotFound},
		"media-service down": {fmt.Errorf("%w: status 502", mediaclient.ErrUnavailable), ErrMediaUnavailable},
		"anything else":      {errors.New("eof"), ErrMediaUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			old, next := uuid.New(), uuid.New()
			svc, st, cl, j := mediaRig(idp(old))
			cl.claimErr = tc.err
			if err := svc.UpdateAvatar(context.Background(), testUserID, next); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			for _, l := range j.lines {
				if strings.HasPrefix(l, "write") || strings.HasPrefix(l, "release") {
					t.Fatalf("a refused claim went on to %q", l)
				}
			}
			if *st.held[mediaclient.KindProfileAvatar] != old {
				t.Fatal("the avatar changed although its claim was refused")
			}
		})
	}
}

// No claim client is not a degraded mode: the write is refused.
func TestNoClaimClientRefusesTheWrite(t *testing.T) {
	svc, st, _, j := mediaRig(nil)
	svc.claims = nil
	if err := svc.UpdateAvatar(context.Background(), testUserID, uuid.New()); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("err = %v, want ErrMediaUnavailable", err)
	}
	for _, l := range j.lines {
		if strings.HasPrefix(l, "write") {
			t.Fatal("an avatar was written with no claim client")
		}
	}
	if st.held[mediaclient.KindProfileAvatar] != nil {
		t.Fatal("the avatar changed")
	}
}

// A failed write releases exactly the new claim and leaves the old one: the
// row still names the old image.
func TestFailedWriteReleasesOnlyTheNewClaim(t *testing.T) {
	old, next := uuid.New(), uuid.New()
	svc, st, _, j := mediaRig(idp(old))
	st.writeErr = errors.New("connection reset")
	if err := svc.UpdateAvatar(context.Background(), testUserID, next); err == nil || errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("err = %v, want the write error", err)
	}
	wantJournal(t, j,
		"lock",
		"read profile_avatar",
		fmt.Sprintf("claim profile_avatar %s [%s]", testUserID, next),
		fmt.Sprintf("write profile_avatar %s", next),
		fmt.Sprintf("release profile_avatar %s [%s]", testUserID, next),
		"unlock")
}

// The profile went away between the read and the write: nothing was written,
// so the claim is compensated.
func TestWriteThatFindsNoProfileReleasesTheClaim(t *testing.T) {
	next := uuid.New()
	svc, st, _, j := mediaRig(nil)
	st.vanish = true
	if err := svc.UpdateAvatar(context.Background(), testUserID, next); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("err = %v, want ErrProfileNotFound", err)
	}
	if last := j.lines[len(j.lines)-2]; last != fmt.Sprintf("release profile_avatar %s [%s]", testUserID, next) {
		t.Fatalf("journal %v: the claim was not compensated", j.lines)
	}
}

func TestNoProfileClaimsNothing(t *testing.T) {
	svc, st, _, j := mediaRig(nil)
	st.exists = false
	if err := svc.UpdateAvatar(context.Background(), testUserID, uuid.New()); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("err = %v, want ErrProfileNotFound", err)
	}
	wantJournal(t, j, "lock", "read profile_avatar", "unlock")
}

// Saving the image the profile already shows is not a new reference.
func TestSameImageClaimsAndReleasesNothing(t *testing.T) {
	id := uuid.New()
	svc, _, _, j := mediaRig(idp(id))
	if err := svc.UpdateAvatar(context.Background(), testUserID, id); err != nil {
		t.Fatal(err)
	}
	wantJournal(t, j, "lock", "read profile_avatar", "unlock")
}

func TestLockOrReadFailureClaimsNothing(t *testing.T) {
	svc, st, _, j := mediaRig(nil)
	st.lockErr = errors.New("pool exhausted")
	if err := svc.UpdateAvatar(context.Background(), testUserID, uuid.New()); err == nil {
		t.Fatal("a lock failure was not reported")
	}
	if len(j.lines) != 0 {
		t.Fatalf("journal %v after a lock failure", j.lines)
	}
	st.lockErr, st.readErr = nil, errors.New("read failed")
	if err := svc.UpdateAvatar(context.Background(), testUserID, uuid.New()); err == nil {
		t.Fatal("a read failure was not reported")
	}
	wantJournal(t, j, "lock", "read profile_avatar", "unlock")
}

// A failed release after a committed write is not the user's failure: the
// save succeeded, the stale claim is the reconcile tool's.
func TestFailedReleaseAfterTheWriteIsStillASuccess(t *testing.T) {
	svc, _, cl, _ := mediaRig(idp(uuid.New()))
	cl.relErr = mediaclient.ErrUnavailable
	if err := svc.UpdateAvatar(context.Background(), testUserID, uuid.New()); err != nil {
		t.Fatalf("err = %v", err)
	}
}

// Releases run detached from the request: a client that hung up after the
// write committed must not leave the old image pinned.
func TestReleaseOutlivesTheRequest(t *testing.T) {
	svc, st, cl, _ := mediaRig(idp(uuid.New()))
	ctx, cancel := context.WithCancel(context.Background())
	st.j = &claimJournal{}
	cl.j = st.j
	wrapped := &cancelOnWrite{fakeMediaStore: st, cancel: cancel}
	svc.WithProfileMediaStore(wrapped)
	if err := svc.UpdateAvatar(ctx, testUserID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if len(cl.relCtx) != 1 || cl.relCtx[0] != nil {
		t.Fatalf("release saw %v; want a live context", cl.relCtx)
	}
}

type cancelOnWrite struct {
	*fakeMediaStore
	cancel func()
}

func (c *cancelOnWrite) SetProfileMediaID(ctx context.Context, u uuid.UUID, kind string, id uuid.UUID) (bool, error) {
	ok, err := c.fakeMediaStore.SetProfileMediaID(ctx, u, kind, id)
	c.cancel()
	return ok, err
}

// The general update may not name media: only the claiming routes may.
func TestUpdateProfileRefusesMedia(t *testing.T) {
	for name, params := range map[string]store.UpdateProfileParams{
		"avatar": {AvatarMediaID: idp(uuid.New())},
		"cover":  {CoverMediaID: idp(uuid.New())},
	} {
		f := &fakeProfileWrites{profile: storedProfile()}
		svc, _ := identityService(f)
		if _, err := svc.UpdateProfile(context.Background(), testUserID, params); !errors.Is(err, ErrMediaRouteRequired) {
			t.Fatalf("%s: err = %v, want ErrMediaRouteRequired", name, err)
		}
		if len(f.updates) != 0 {
			t.Fatalf("%s: the profile was written", name)
		}
	}
}

type fakeEraser struct {
	j   *claimJournal
	err error
}

func (f *fakeEraser) PurgeUser(_ context.Context, id uuid.UUID) error {
	f.j.add("purge %s", id)
	return f.err
}

// A purged profile holds nothing: release both kinds after the erase, never
// before it (the row would still name them), and never instead of it.
func TestPurgeReleasesTheProfilesClaimsAfterTheErase(t *testing.T) {
	svc, _, cl, j := mediaRig(nil)
	e := svc.ReleasingMediaOnPurge(&fakeEraser{j: j})
	if err := e.PurgeUser(context.Background(), testUserID); err != nil {
		t.Fatal(err)
	}
	wantJournal(t, j,
		"purge "+testUserID.String(),
		fmt.Sprintf("release profile_avatar %s []", testUserID),
		fmt.Sprintf("release profile_cover %s []", testUserID))

	// A failed purge releases nothing and is reported.
	j.lines = nil
	if err := svc.ReleasingMediaOnPurge(&fakeEraser{j: j, err: errors.New("db down")}).PurgeUser(context.Background(), testUserID); err == nil {
		t.Fatal("a failed purge was swallowed")
	}
	wantJournal(t, j, "purge "+testUserID.String())

	// A failed release does not fail the purge (it would be retried for a
	// reason the purge itself does not have).
	j.lines = nil
	cl.relErr = mediaclient.ErrUnavailable
	if err := e.PurgeUser(context.Background(), testUserID); err != nil {
		t.Fatalf("a failed release failed the purge: %v", err)
	}
}
