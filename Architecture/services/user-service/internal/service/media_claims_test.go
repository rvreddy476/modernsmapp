package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/atpost/user-service/internal/mediaclient"
	"github.com/atpost/user-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── fakes ────────────────────────────────────────────────────────────────

// fakeMedia is media-service: Get answers from assets (missing = ErrNotFound)
// and claims are kept the way media_claims.go keeps them: all or nothing,
// refused when an id is not an asset.
type fakeMedia struct {
	mu         sync.Mutex
	assets     map[uuid.UUID]*mediaclient.Asset
	held       map[string]map[uuid.UUID]bool
	gets       []uuid.UUID
	getErr     error
	claimErr   error
	releaseErr error
	releases   int
}

func newFakeMedia(assets ...*mediaclient.Asset) *fakeMedia {
	f := &fakeMedia{assets: map[uuid.UUID]*mediaclient.Asset{}, held: map[string]map[uuid.UUID]bool{}}
	for _, a := range assets {
		f.assets[a.ID] = a
	}
	return f
}

func key(kind string, ref uuid.UUID) string { return kind + "/" + ref.String() }

func (f *fakeMedia) Get(_ context.Context, id uuid.UUID) (*mediaclient.Asset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets = append(f.gets, id)
	if f.getErr != nil {
		return nil, f.getErr
	}
	a, ok := f.assets[id]
	if !ok {
		return nil, mediaclient.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (f *fakeMedia) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return f.claimErr
	}
	for _, id := range ids {
		if _, ok := f.assets[id]; !ok {
			return mediaclient.ErrNotFound
		}
	}
	k := key(kind, ref)
	if f.held[k] == nil {
		f.held[k] = map[uuid.UUID]bool{}
	}
	for _, id := range ids {
		f.held[k][id] = true
	}
	return nil
}

func (f *fakeMedia) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases++
	if f.releaseErr != nil {
		return f.releaseErr
	}
	k := key(kind, ref)
	if len(ids) == 0 {
		delete(f.held, k)
		return nil
	}
	for _, id := range ids {
		delete(f.held[k], id)
	}
	return nil
}

func (f *fakeMedia) ReleaseExcept(_ context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.releaseErr != nil {
		return f.releaseErr
	}
	k := key(kind, ref)
	ks := map[uuid.UUID]bool{}
	for _, id := range keep {
		ks[id] = true
	}
	for id := range f.held[k] {
		if !ks[id] {
			delete(f.held[k], id)
		}
	}
	return nil
}

func (f *fakeMedia) holds(kind string, ref, id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[key(kind, ref)][id]
}

func (f *fakeMedia) holdsAny(kind string, ref uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.held[key(kind, ref)]) > 0
}

func (f *fakeMedia) getCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.gets)
}

// fakeStore keeps the holder rows in memory. Every write records whether the
// media it names was claimed at that moment, which is the protocol's point.
type fakeStore struct {
	mu        sync.Mutex
	media     *fakeMedia
	channels  map[uuid.UUID]*store.Channel
	items     map[uuid.UUID]*store.PortfolioItem
	pages     map[uuid.UUID]*store.BusinessPage
	pending   map[string]store.PendingMediaClaimHolder
	locks     map[uuid.UUID]*sync.Mutex
	writeErr  error
	unclaimed []string // writes that named media with no claim held
	writes    int
	clock     time.Time
}

func newFakeStore(m *fakeMedia) *fakeStore {
	return &fakeStore{
		media: m, channels: map[uuid.UUID]*store.Channel{}, items: map[uuid.UUID]*store.PortfolioItem{},
		pages: map[uuid.UUID]*store.BusinessPage{}, pending: map[string]store.PendingMediaClaimHolder{},
		locks: map[uuid.UUID]*sync.Mutex{}, clock: time.Unix(1_700_000_000, 0),
	}
}

func (s *fakeStore) LockMediaHolder(_ context.Context, ref uuid.UUID) (func(), error) {
	s.mu.Lock()
	l := s.locks[ref]
	if l == nil {
		l = &sync.Mutex{}
		s.locks[ref] = l
	}
	s.mu.Unlock()
	l.Lock()
	var once sync.Once
	return func() { once.Do(l.Unlock) }, nil
}

func (s *fakeStore) checkClaimed(kind string, ref uuid.UUID, id *uuid.UUID) {
	if id != nil && !s.media.holds(kind, ref, *id) {
		s.unclaimed = append(s.unclaimed, fmt.Sprintf("%s %s", key(kind, ref), id))
	}
}

func (s *fakeStore) MediaClaimRowFor(_ context.Context, kind string, ref uuid.UUID) (store.MediaClaimRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := store.MediaClaimRow{Kind: kind, Ref: ref}
	add := func(id *uuid.UUID) {
		if id != nil {
			row.MediaIDs = []uuid.UUID{*id}
		}
	}
	switch kind {
	case mediaclient.KindChannelAvatar, mediaclient.KindChannelBanner:
		if ch, ok := s.channels[ref]; ok {
			row.Live = true
			if kind == mediaclient.KindChannelAvatar {
				add(ch.AvatarMediaID)
			} else {
				add(ch.BannerMediaID)
			}
		}
	case mediaclient.KindPortfolioItem:
		if it, ok := s.items[ref]; ok {
			row.Live = true
			add(it.MediaID)
		}
	case mediaclient.KindBusinessPageAvatar, mediaclient.KindBusinessPageCover:
		if p, ok := s.pages[ref]; ok {
			row.Live = p.Status != "disabled"
			v := p.AvatarMediaID
			if kind == mediaclient.KindBusinessPageCover {
				v = p.CoverMediaID
			}
			add(pageMediaFor(v))
		}
	}
	return row, nil
}

func (s *fakeStore) enqueue(kind string, ref uuid.UUID) {
	s.clock = s.clock.Add(time.Second)
	s.pending[key(kind, ref)] = store.PendingMediaClaimHolder{Kind: kind, Ref: ref, EnqueuedAt: s.clock}
}

func (s *fakeStore) MarkMediaClaimHolderPending(_ context.Context, kind string, ref uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueue(kind, ref)
	return nil
}

func (s *fakeStore) PendingMediaClaimHolders(_ context.Context, limit int) ([]store.PendingMediaClaimHolder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.PendingMediaClaimHolder
	for _, p := range s.pending {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EnqueuedAt.Before(out[j].EnqueuedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *fakeStore) ClearMediaClaimHolderPending(_ context.Context, p store.PendingMediaClaimHolder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.pending[key(p.Kind, p.Ref)]; ok && cur.EnqueuedAt.Equal(p.EnqueuedAt) {
		delete(s.pending, key(p.Kind, p.Ref))
	}
	return nil
}

func (s *fakeStore) isPending(kind string, ref uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pending[key(kind, ref)]
	return ok
}

func (s *fakeStore) CreateChannel(_ context.Context, ch *store.Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if s.writeErr != nil {
		return s.writeErr
	}
	if ch.ID == uuid.Nil {
		ch.ID = uuid.New()
	}
	s.checkClaimed(mediaclient.KindChannelAvatar, ch.ID, ch.AvatarMediaID)
	s.checkClaimed(mediaclient.KindChannelBanner, ch.ID, ch.BannerMediaID)
	cp := *ch
	s.channels[ch.ID] = &cp
	return nil
}

func (s *fakeStore) GetChannelByID(_ context.Context, id uuid.UUID) (*store.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channels[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *ch
	return &cp, nil
}

func (s *fakeStore) UpdateChannel(_ context.Context, u *store.ChannelUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if s.writeErr != nil {
		return s.writeErr
	}
	ch, ok := s.channels[u.ID]
	if !ok || ch.UserID != u.UserID {
		return fmt.Errorf("CHANNEL_NOT_FOUND")
	}
	if u.AvatarMediaID != nil {
		ch.AvatarMediaID = u.AvatarMediaID
	}
	if u.BannerMediaID != nil {
		ch.BannerMediaID = u.BannerMediaID
	}
	if u.Name != nil {
		ch.Name = *u.Name
	}
	s.checkClaimed(mediaclient.KindChannelAvatar, ch.ID, ch.AvatarMediaID)
	s.checkClaimed(mediaclient.KindChannelBanner, ch.ID, ch.BannerMediaID)
	return nil
}

func (s *fakeStore) DeleteChannel(_ context.Context, id, userID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channels[id]
	if !ok || ch.UserID != userID {
		return fmt.Errorf("CHANNEL_NOT_FOUND")
	}
	if ch.AvatarMediaID != nil {
		s.enqueue(mediaclient.KindChannelAvatar, id)
	}
	if ch.BannerMediaID != nil {
		s.enqueue(mediaclient.KindChannelBanner, id)
	}
	delete(s.channels, id)
	return nil
}

func (s *fakeStore) CreatePortfolioItem(_ context.Context, it *store.PortfolioItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if s.writeErr != nil {
		return s.writeErr
	}
	if it.ID == uuid.Nil {
		it.ID = uuid.New()
	}
	s.checkClaimed(mediaclient.KindPortfolioItem, it.ID, it.MediaID)
	cp := *it
	s.items[it.ID] = &cp
	return nil
}

func (s *fakeStore) DeletePortfolioItem(_ context.Context, id, userID uuid.UUID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok || it.UserID != userID {
		return false, pgx.ErrNoRows
	}
	if it.MediaID != nil {
		s.enqueue(mediaclient.KindPortfolioItem, id)
	}
	delete(s.items, id)
	return it.MediaID != nil, nil
}

func (s *fakeStore) checkPageClaimed(p *store.BusinessPage) {
	s.checkClaimed(mediaclient.KindBusinessPageAvatar, p.ID, pageMediaFor(p.AvatarMediaID))
	s.checkClaimed(mediaclient.KindBusinessPageCover, p.ID, pageMediaFor(p.CoverMediaID))
}

func (s *fakeStore) CreateBusinessPage(_ context.Context, p *store.BusinessPage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if s.writeErr != nil {
		return s.writeErr
	}
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	s.checkPageClaimed(p)
	cp := *p
	s.pages[p.ID] = &cp
	return nil
}

func (s *fakeStore) GetBusinessPageByID(_ context.Context, id uuid.UUID, _ *uuid.UUID) (*store.BusinessPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pages[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *p
	return &cp, nil
}

func (s *fakeStore) UpdateBusinessPage(_ context.Context, p *store.BusinessPage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if s.writeErr != nil {
		return s.writeErr
	}
	cur, ok := s.pages[p.ID]
	if !ok || cur.UserID != p.UserID {
		return fmt.Errorf("PAGE_NOT_FOUND")
	}
	cp := *p
	cp.Status = cur.Status
	s.pages[p.ID] = &cp
	if cur.Status != "disabled" {
		s.checkPageClaimed(&cp)
	}
	return nil
}

func (s *fakeStore) DeleteBusinessPage(_ context.Context, id, userID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pages[id]
	if !ok || p.UserID != userID || p.Status == "disabled" {
		return fmt.Errorf("PAGE_NOT_FOUND")
	}
	p.Status = "disabled"
	if p.AvatarMediaID != "" {
		s.enqueue(mediaclient.KindBusinessPageAvatar, id)
	}
	if p.CoverMediaID != "" {
		s.enqueue(mediaclient.KindBusinessPageCover, id)
	}
	return nil
}

func asset(owner uuid.UUID, processing, moderation string) *mediaclient.Asset {
	return &mediaclient.Asset{ID: uuid.New(), UploaderID: owner, FileType: "image", ProcessingStatus: processing, ModerationStatus: moderation}
}

func ready(owner uuid.UUID) *mediaclient.Asset { return asset(owner, "ready", "approved") }

func harness(assets ...*mediaclient.Asset) (*Service, *fakeStore, *fakeMedia) {
	m := newFakeMedia(assets...)
	st := newFakeStore(m)
	svc := &Service{ms: st}
	svc.WithMedia(m)
	return svc, st, m
}

func (s *fakeStore) assertAllClaimed(t *testing.T) {
	t.Helper()
	if len(s.unclaimed) > 0 {
		t.Fatalf("rows were written naming media with no claim held: %v", s.unclaimed)
	}
}

func ptr(id uuid.UUID) *uuid.UUID { return &id }

func seedChannel(st *fakeStore, owner uuid.UUID, avatar *uuid.UUID) uuid.UUID {
	id := uuid.New()
	st.channels[id] = &store.Channel{ID: id, UserID: owner, Handle: "h" + id.String()[:6], AvatarMediaID: avatar}
	return id
}

// ── the per-asset rule ───────────────────────────────────────────────────

func TestCheckAttachAsset(t *testing.T) {
	owner := uuid.New()
	for name, tc := range map[string]struct {
		a    *mediaclient.Asset
		want error
	}{
		"own ready":            {asset(owner, "ready", "approved"), nil},
		"own uploaded":         {asset(owner, "uploaded", "pending"), nil},
		"own processing":       {asset(owner, "processing", "approved"), nil},
		"someone else's":       {asset(uuid.New(), "ready", "approved"), ErrMediaNotOwned},
		"pending upload":       {asset(owner, "pending_upload", "pending"), ErrMediaNotReady},
		"failed":               {asset(owner, "failed", "approved"), ErrMediaNotReady},
		"deleted":              {asset(owner, "deleted", "approved"), ErrMediaNotReady},
		"unknown status":       {asset(owner, "", "approved"), ErrMediaNotReady},
		"moderation rejected":  {asset(owner, "ready", "rejected"), ErrMediaNotReady},
		"moderation not known": {asset(owner, "ready", ""), ErrMediaNotReady},
	} {
		if err := checkAttachAsset(tc.a, owner); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}

// ── channels ─────────────────────────────────────────────────────────────

func TestUpdateChannelClaimsTheNewPictureBeforeTheWriteAndReleasesTheOldAfter(t *testing.T) {
	owner := uuid.New()
	old, next := ready(owner), ready(owner)
	svc, st, m := harness(old, next)
	chID := seedChannel(st, owner, ptr(old.ID))
	_ = m.Claim(context.Background(), mediaclient.KindChannelAvatar, chID, []uuid.UUID{old.ID})

	if err := svc.UpdateChannel(context.Background(), &store.ChannelUpdate{ID: chID, UserID: owner, AvatarMediaID: ptr(next.ID)}); err != nil {
		t.Fatalf("UpdateChannel: %v", err)
	}
	st.assertAllClaimed(t)
	if !m.holds(mediaclient.KindChannelAvatar, chID, next.ID) {
		t.Fatal("the new picture is not claimed")
	}
	if m.holds(mediaclient.KindChannelAvatar, chID, old.ID) {
		t.Fatal("the replaced picture is still claimed; its owner could never delete it")
	}
}

func TestUpdateChannelRefusesMediaThatIsNotTheActorsFinishedUpload(t *testing.T) {
	owner := uuid.New()
	foreign, pending, rejected := ready(uuid.New()), asset(owner, "pending_upload", "pending"), asset(owner, "ready", "rejected")
	for name, tc := range map[string]struct {
		id   uuid.UUID
		want error
	}{
		"someone else's":      {foreign.ID, ErrMediaNotOwned},
		"not uploaded yet":    {pending.ID, ErrMediaNotReady},
		"refused":             {rejected.ID, ErrMediaNotReady},
		"no such asset":       {uuid.New(), ErrMediaNotFound},
		"the nil id":          {uuid.Nil, ErrMediaNotFound},
		"banner someone else": {foreign.ID, ErrMediaNotOwned},
	} {
		t.Run(name, func(t *testing.T) {
			svc, st, m := harness(foreign, pending, rejected)
			chID := seedChannel(st, owner, nil)
			upd := &store.ChannelUpdate{ID: chID, UserID: owner, AvatarMediaID: ptr(tc.id)}
			if name == "banner someone else" {
				upd = &store.ChannelUpdate{ID: chID, UserID: owner, BannerMediaID: ptr(tc.id)}
			}
			if err := svc.UpdateChannel(context.Background(), upd); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if st.writes != 0 || st.channels[chID].AvatarMediaID != nil || st.channels[chID].BannerMediaID != nil {
				t.Fatal("a refused attach was written")
			}
			if m.holdsAny(mediaclient.KindChannelAvatar, chID) || m.holdsAny(mediaclient.KindChannelBanner, chID) {
				t.Fatal("a refused attach left a claim")
			}
		})
	}
}

func TestUpdateChannelFailsClosedWithoutMediaService(t *testing.T) {
	owner := uuid.New()
	a := ready(owner)
	svc, st, _ := harness(a)
	svc.media, svc.claims = nil, nil
	chID := seedChannel(st, owner, nil)
	err := svc.UpdateChannel(context.Background(), &store.ChannelUpdate{ID: chID, UserID: owner, AvatarMediaID: ptr(a.ID)})
	if !errors.Is(err, ErrMediaUnavailable) || st.writes != 0 {
		t.Fatalf("got %v with %d writes, want ErrMediaUnavailable and no write", err, st.writes)
	}

	// A lookup that cannot answer is not a verdict either.
	svc2, st2, m2 := harness(a)
	m2.getErr = fmt.Errorf("%w: status 502", mediaclient.ErrUnavailable)
	chID2 := seedChannel(st2, owner, nil)
	if err := svc2.UpdateChannel(context.Background(), &store.ChannelUpdate{ID: chID2, UserID: owner, AvatarMediaID: ptr(a.ID)}); !errors.Is(err, ErrMediaUnavailable) || st2.writes != 0 {
		t.Fatalf("lookup down: got %v with %d writes", err, st2.writes)
	}
}

// A lookup that passes is not enough: with no claim client the reference
// could not be protected, so the write is refused.
func TestUpdateChannelFailsClosedWithoutTheClaimClient(t *testing.T) {
	owner := uuid.New()
	a := ready(owner)
	svc, st, _ := harness(a)
	svc.claims = nil
	chID := seedChannel(st, owner, nil)
	err := svc.UpdateChannel(context.Background(), &store.ChannelUpdate{ID: chID, UserID: owner, AvatarMediaID: ptr(a.ID)})
	if !errors.Is(err, ErrMediaUnavailable) || st.writes != 0 {
		t.Fatalf("got %v with %d writes, want ErrMediaUnavailable and no write", err, st.writes)
	}
}

// The asset can vanish between the lookup and the claim (an owner delete
// that won the race): media-service refuses the claim, and nothing is written.
func TestUpdateChannelWritesNothingWhenTheClaimIsRefused(t *testing.T) {
	owner := uuid.New()
	a := ready(owner)
	svc, st, m := harness(a)
	chID := seedChannel(st, owner, nil)
	m.claimErr = mediaclient.ErrNotFound
	if err := svc.UpdateChannel(context.Background(), &store.ChannelUpdate{ID: chID, UserID: owner, AvatarMediaID: ptr(a.ID)}); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("got %v, want ErrMediaNotFound", err)
	}
	m.claimErr = fmt.Errorf("%w: status 400", mediaclient.ErrUnavailable) // e.g. claimant not registered yet
	if err := svc.UpdateChannel(context.Background(), &store.ChannelUpdate{ID: chID, UserID: owner, AvatarMediaID: ptr(a.ID)}); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("got %v, want ErrMediaUnavailable", err)
	}
	if st.writes != 0 {
		t.Fatal("a write went ahead without its claim")
	}
}

func TestUpdateChannelReleasesItsClaimWhenTheWriteFails(t *testing.T) {
	owner := uuid.New()
	a := ready(owner)
	svc, st, m := harness(a)
	chID := seedChannel(st, owner, nil)
	st.writeErr = errors.New("db down")
	if err := svc.UpdateChannel(context.Background(), &store.ChannelUpdate{ID: chID, UserID: owner, AvatarMediaID: ptr(a.ID)}); err == nil {
		t.Fatal("the write failure was swallowed")
	}
	if m.holds(mediaclient.KindChannelAvatar, chID, a.ID) {
		t.Fatal("a failed write left its claim; the owner could not delete media nothing shows")
	}
}

func TestUpdateChannelByANonOwnerClaimsNothing(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	a := ready(other)
	svc, st, m := harness(a)
	chID := seedChannel(st, owner, nil)
	err := svc.UpdateChannel(context.Background(), &store.ChannelUpdate{ID: chID, UserID: other, AvatarMediaID: ptr(a.ID)})
	if err == nil || err.Error() != "CHANNEL_NOT_FOUND" {
		t.Fatalf("got %v, want CHANNEL_NOT_FOUND", err)
	}
	if m.holdsAny(mediaclient.KindChannelAvatar, chID) || m.getCount() != 0 {
		t.Fatal("a non-owner's attach reached media-service")
	}
}

// Re-sending the picture the channel already shows attaches nothing new: no
// lookup (it may have been uploaded by whoever set it), no claim, no release.
func TestUpdateChannelDoesNotRecheckMediaItAlreadyHolds(t *testing.T) {
	owner := uuid.New()
	a := ready(owner)
	svc, st, m := harness(a)
	chID := seedChannel(st, owner, ptr(a.ID))
	_ = m.Claim(context.Background(), mediaclient.KindChannelAvatar, chID, []uuid.UUID{a.ID})
	name := "renamed"
	if err := svc.UpdateChannel(context.Background(), &store.ChannelUpdate{ID: chID, UserID: owner, Name: &name, AvatarMediaID: ptr(a.ID)}); err != nil {
		t.Fatal(err)
	}
	if m.getCount() != 0 || !m.holds(mediaclient.KindChannelAvatar, chID, a.ID) {
		t.Fatal("an unchanged picture was re-checked or released")
	}
}

func TestAFailedReleaseIsQueuedForTheDrain(t *testing.T) {
	owner := uuid.New()
	old, next := ready(owner), ready(owner)
	svc, st, m := harness(old, next)
	chID := seedChannel(st, owner, ptr(old.ID))
	_ = m.Claim(context.Background(), mediaclient.KindChannelAvatar, chID, []uuid.UUID{old.ID})
	m.releaseErr = errors.New("media-service down")
	if err := svc.UpdateChannel(context.Background(), &store.ChannelUpdate{ID: chID, UserID: owner, AvatarMediaID: ptr(next.ID)}); err != nil {
		t.Fatalf("a failed release must not fail the edit: %v", err)
	}
	if !st.isPending(mediaclient.KindChannelAvatar, chID) {
		t.Fatal("the failed release was not queued; the old picture would stay pinned forever")
	}

	// The drain settles it once media-service answers: the current picture
	// stays claimed, the replaced one is released, the entry goes.
	m.releaseErr = nil
	rep, err := svc.DrainMediaClaims(context.Background())
	if err != nil || rep.Failed != 0 {
		t.Fatalf("drain: %v %s", err, rep)
	}
	if m.holds(mediaclient.KindChannelAvatar, chID, old.ID) || !m.holds(mediaclient.KindChannelAvatar, chID, next.ID) {
		t.Fatal("the drain did not settle the holder to what the row shows")
	}
	if st.isPending(mediaclient.KindChannelAvatar, chID) {
		t.Fatal("a settled holder was left queued")
	}
}

func TestDeleteChannelReleasesItsMedia(t *testing.T) {
	owner := uuid.New()
	a := ready(owner)
	svc, st, m := harness(a)
	chID := seedChannel(st, owner, ptr(a.ID))
	_ = m.Claim(context.Background(), mediaclient.KindChannelAvatar, chID, []uuid.UUID{a.ID})
	if err := svc.DeleteChannel(context.Background(), chID, owner); err != nil {
		t.Fatal(err)
	}
	if m.holdsAny(mediaclient.KindChannelAvatar, chID) {
		t.Fatal("a deleted channel still pins its picture")
	}
}

func TestDeleteChannelByANonOwnerReleasesNothing(t *testing.T) {
	owner := uuid.New()
	a := ready(owner)
	svc, st, m := harness(a)
	chID := seedChannel(st, owner, ptr(a.ID))
	_ = m.Claim(context.Background(), mediaclient.KindChannelAvatar, chID, []uuid.UUID{a.ID})
	if err := svc.DeleteChannel(context.Background(), chID, uuid.New()); err == nil {
		t.Fatal("a non-owner deleted the channel")
	}
	if !m.holds(mediaclient.KindChannelAvatar, chID, a.ID) {
		t.Fatal("a refused delete released the channel's picture claim")
	}
}

func TestCreateChannelWithMediaClaimsItAgainstTheNewRow(t *testing.T) {
	owner := uuid.New()
	a, foreign := ready(owner), ready(uuid.New())
	svc, st, m := harness(a, foreign)
	ch := &store.Channel{UserID: owner, Handle: "x", Name: "x", BannerMediaID: ptr(a.ID)}
	if err := svc.CreateChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	st.assertAllClaimed(t)
	if !m.holds(mediaclient.KindChannelBanner, ch.ID, a.ID) {
		t.Fatal("the banner is not claimed against the new channel's id")
	}
	if err := svc.CreateChannel(context.Background(), &store.Channel{UserID: owner, AvatarMediaID: ptr(foreign.ID)}); !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("foreign avatar at create: %v", err)
	}
	// No media: no media-service call at all.
	before := m.getCount()
	if err := svc.CreateChannel(context.Background(), &store.Channel{UserID: owner, Handle: "y"}); err != nil || m.getCount() != before {
		t.Fatalf("a channel without media called out: %v", err)
	}
}

// Two edits of one channel at once: whatever the row ends up showing is
// claimed. Without the holder lock one edit's release can drop the claim the
// other's write relies on.
func TestConcurrentChannelEditsLeaveTheFinalPictureClaimed(t *testing.T) {
	owner := uuid.New()
	p, x := ready(owner), ready(owner)
	for round := 0; round < 50; round++ {
		svc, st, m := harness(p, x)
		chID := seedChannel(st, owner, ptr(p.ID))
		_ = m.Claim(context.Background(), mediaclient.KindChannelAvatar, chID, []uuid.UUID{p.ID})
		var wg sync.WaitGroup
		for _, id := range []uuid.UUID{x.ID, p.ID, x.ID, p.ID} {
			wg.Add(1)
			go func(id uuid.UUID) {
				defer wg.Done()
				_ = svc.UpdateChannel(context.Background(), &store.ChannelUpdate{ID: chID, UserID: owner, AvatarMediaID: ptr(id)})
			}(id)
		}
		wg.Wait()
		final := *st.channels[chID].AvatarMediaID
		if !m.holds(mediaclient.KindChannelAvatar, chID, final) {
			t.Fatalf("round %d: the channel shows %s with no claim on it", round, final)
		}
	}
}

// ── portfolio ────────────────────────────────────────────────────────────

func TestAddPortfolioItemVerifiesAndClaimsItsMedia(t *testing.T) {
	owner := uuid.New()
	a, foreign := ready(owner), ready(uuid.New())
	svc, st, m := harness(a, foreign)
	item, err := svc.AddPortfolioItem(context.Background(), owner, "t", "", "project", "", ptr(a.ID), 0)
	if err != nil {
		t.Fatal(err)
	}
	st.assertAllClaimed(t)
	if !m.holds(mediaclient.KindPortfolioItem, item.ID, a.ID) {
		t.Fatal("the item's media is not claimed against its id")
	}
	if _, err := svc.AddPortfolioItem(context.Background(), owner, "t", "", "project", "", ptr(foreign.ID), 0); !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("foreign media: %v", err)
	}
	if _, err := svc.AddPortfolioItem(context.Background(), owner, "t", "", "project", "", ptr(uuid.Nil), 0); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("nil media id: %v", err)
	}
	if len(st.items) != 1 {
		t.Fatalf("refused items were written: %d rows", len(st.items))
	}
	before := m.getCount()
	if _, err := svc.AddPortfolioItem(context.Background(), owner, "t", "", "article", "https://x", nil, 0); err != nil || m.getCount() != before {
		t.Fatalf("an item without media called out: %v", err)
	}
}

func TestAddPortfolioItemReleasesWhenTheWriteFails(t *testing.T) {
	owner := uuid.New()
	a := ready(owner)
	svc, st, m := harness(a)
	st.writeErr = errors.New("db down")
	if _, err := svc.AddPortfolioItem(context.Background(), owner, "t", "", "project", "", ptr(a.ID), 0); err == nil {
		t.Fatal("write failure swallowed")
	}
	m.mu.Lock()
	n := 0
	for _, ids := range m.held {
		n += len(ids)
	}
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("a failed item create left %d claim(s)", n)
	}
}

func TestDeletePortfolioItemReleasesItsMedia(t *testing.T) {
	owner := uuid.New()
	a := ready(owner)
	svc, _, m := harness(a)
	item, err := svc.AddPortfolioItem(context.Background(), owner, "t", "", "project", "", ptr(a.ID), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeletePortfolioItem(context.Background(), item.ID, uuid.New()); err == nil {
		t.Fatal("a non-owner deleted the item")
	}
	if !m.holds(mediaclient.KindPortfolioItem, item.ID, a.ID) {
		t.Fatal("a refused delete released the claim")
	}
	if err := svc.DeletePortfolioItem(context.Background(), item.ID, owner); err != nil {
		t.Fatal(err)
	}
	if m.holdsAny(mediaclient.KindPortfolioItem, item.ID) {
		t.Fatal("a deleted item still pins its media")
	}
}

// ── business pages ───────────────────────────────────────────────────────

func seedPage(st *fakeStore, owner uuid.UUID, avatar, cover, status string) uuid.UUID {
	id := uuid.New()
	st.pages[id] = &store.BusinessPage{ID: id, UserID: owner, PageHandle: "p" + id.String()[:6], AvatarMediaID: avatar, CoverMediaID: cover, Status: status}
	return id
}

func TestCreateBusinessPageVerifiesAndClaimsAvatarAndCover(t *testing.T) {
	owner := uuid.New()
	av, cv, foreign := ready(owner), ready(owner), ready(uuid.New())
	svc, st, m := harness(av, cv, foreign)
	p := &store.BusinessPage{UserID: owner, PageHandle: "shop", AvatarMediaID: av.ID.String(), CoverMediaID: cv.ID.String(), Status: "draft"}
	if err := svc.CreateBusinessPage(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	st.assertAllClaimed(t)
	if !m.holds(mediaclient.KindBusinessPageAvatar, p.ID, av.ID) || !m.holds(mediaclient.KindBusinessPageCover, p.ID, cv.ID) {
		t.Fatal("page media not claimed")
	}
	for name, tc := range map[string]struct {
		avatar string
		want   error
	}{
		"not a media id": {"https://cdn.example/logo.png", ErrMediaInvalid},
		"someone else's": {foreign.ID.String(), ErrMediaNotOwned},
		"no such asset":  {uuid.NewString(), ErrMediaNotFound},
	} {
		if err := svc.CreateBusinessPage(context.Background(), &store.BusinessPage{UserID: owner, PageHandle: name, AvatarMediaID: tc.avatar}); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
	if len(st.pages) != 1 {
		t.Fatalf("refused pages were written: %d", len(st.pages))
	}
}

// The handler reads the page BEFORE the lock. The diff must be against the
// row as it is under the lock, or a concurrent edit's claim is released.
func TestUpdateBusinessPageDiffsAgainstTheRowUnderTheLock(t *testing.T) {
	owner := uuid.New()
	old, current := ready(owner), ready(owner)
	svc, st, m := harness(old, current)
	pageID := seedPage(st, owner, current.ID.String(), "", "approved")
	_ = m.Claim(context.Background(), mediaclient.KindBusinessPageAvatar, pageID, []uuid.UUID{current.ID})

	// A stale copy still naming `old`, as read before a concurrent edit.
	stale := &store.BusinessPage{ID: pageID, UserID: owner, AvatarMediaID: old.ID.String(), Status: "approved"}
	if err := svc.UpdateBusinessPage(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	st.assertAllClaimed(t)
	if !m.holds(mediaclient.KindBusinessPageAvatar, pageID, old.ID) {
		t.Fatal("the avatar the row now shows is not claimed")
	}
	if m.holds(mediaclient.KindBusinessPageAvatar, pageID, current.ID) {
		t.Fatal("the avatar the row no longer shows is still claimed")
	}
}

func TestUpdateBusinessPageRules(t *testing.T) {
	owner := uuid.New()
	a, foreign := ready(owner), ready(uuid.New())

	t.Run("a legacy non-id carried over unchanged is left alone", func(t *testing.T) {
		svc, st, m := harness(a)
		pageID := seedPage(st, owner, "legacy-logo.png", "", "approved")
		p := &store.BusinessPage{ID: pageID, UserID: owner, AvatarMediaID: "legacy-logo.png", PageName: "renamed"}
		if err := svc.UpdateBusinessPage(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		if m.getCount() != 0 {
			t.Fatal("an unchanged value was looked up")
		}
	})
	t.Run("a changed value must be a media id", func(t *testing.T) {
		svc, st, _ := harness(a)
		pageID := seedPage(st, owner, "", "", "approved")
		err := svc.UpdateBusinessPage(context.Background(), &store.BusinessPage{ID: pageID, UserID: owner, CoverMediaID: "javascript:alert(1)"})
		if !errors.Is(err, ErrMediaInvalid) || st.writes != 0 {
			t.Fatalf("got %v with %d writes", err, st.writes)
		}
	})
	t.Run("someone else's media is refused", func(t *testing.T) {
		svc, st, m := harness(foreign)
		pageID := seedPage(st, owner, "", "", "approved")
		err := svc.UpdateBusinessPage(context.Background(), &store.BusinessPage{ID: pageID, UserID: owner, CoverMediaID: foreign.ID.String()})
		if !errors.Is(err, ErrMediaNotOwned) || st.writes != 0 || m.holdsAny(mediaclient.KindBusinessPageCover, pageID) {
			t.Fatalf("got %v with %d writes", err, st.writes)
		}
	})
	t.Run("a disabled page takes no new media", func(t *testing.T) {
		svc, st, m := harness(a)
		pageID := seedPage(st, owner, "", "", "disabled")
		err := svc.UpdateBusinessPage(context.Background(), &store.BusinessPage{ID: pageID, UserID: owner, AvatarMediaID: a.ID.String()})
		if !errors.Is(err, ErrPageDisabled) || st.writes != 0 || m.holdsAny(mediaclient.KindBusinessPageAvatar, pageID) {
			t.Fatalf("got %v with %d writes", err, st.writes)
		}
	})
	t.Run("only the owner", func(t *testing.T) {
		svc, st, m := harness(a)
		pageID := seedPage(st, owner, "", "", "approved")
		err := svc.UpdateBusinessPage(context.Background(), &store.BusinessPage{ID: pageID, UserID: uuid.New(), AvatarMediaID: a.ID.String()})
		if err == nil || err.Error() != "PAGE_NOT_FOUND" || m.getCount() != 0 {
			t.Fatalf("got %v", err)
		}
	})
}

func TestDeleteBusinessPageReleasesAvatarAndCover(t *testing.T) {
	owner := uuid.New()
	av, cv := ready(owner), ready(owner)
	svc, st, m := harness(av, cv)
	p := &store.BusinessPage{UserID: owner, PageHandle: "shop", AvatarMediaID: av.ID.String(), CoverMediaID: cv.ID.String(), Status: "draft"}
	if err := svc.CreateBusinessPage(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteBusinessPage(context.Background(), p.ID, owner); err != nil {
		t.Fatal(err)
	}
	if m.holdsAny(mediaclient.KindBusinessPageAvatar, p.ID) || m.holdsAny(mediaclient.KindBusinessPageCover, p.ID) {
		t.Fatal("a disabled page still pins its media")
	}
	_ = st
}

// A delete whose release failed is settled by the drain from the entry the
// delete queued, though the row (or its media) is gone.
func TestDrainReleasesADeletedHoldersClaims(t *testing.T) {
	owner := uuid.New()
	a := ready(owner)
	svc, st, m := harness(a)
	item, err := svc.AddPortfolioItem(context.Background(), owner, "t", "", "project", "", ptr(a.ID), 0)
	if err != nil {
		t.Fatal(err)
	}
	m.releaseErr = errors.New("down")
	if err := svc.DeletePortfolioItem(context.Background(), item.ID, owner); err != nil {
		t.Fatal(err)
	}
	if !m.holds(mediaclient.KindPortfolioItem, item.ID, a.ID) || !st.isPending(mediaclient.KindPortfolioItem, item.ID) {
		t.Fatal("setup: expected a stale claim with a queued entry")
	}
	if _, err := svc.DrainMediaClaims(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !st.isPending(mediaclient.KindPortfolioItem, item.ID) {
		t.Fatal("an entry was cleared though its release failed")
	}
	m.releaseErr = nil
	if _, err := svc.DrainMediaClaims(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.holdsAny(mediaclient.KindPortfolioItem, item.ID) || st.isPending(mediaclient.KindPortfolioItem, item.ID) {
		t.Fatal("the drain did not release the deleted item's claim")
	}
}

// Deterministic half of the concurrency proof: a media edit waits for the
// holder lock before it reads the row, so a second edit cannot read, claim
// and release in between another's steps.
func TestMediaEditsWaitForTheHolderLock(t *testing.T) {
	owner := uuid.New()
	a := ready(owner)
	for name, run := range map[string]func(svc *Service, ref uuid.UUID) error{
		"channel update": func(svc *Service, ref uuid.UUID) error {
			return svc.UpdateChannel(context.Background(), &store.ChannelUpdate{ID: ref, UserID: owner, AvatarMediaID: ptr(a.ID)})
		},
		"page update": func(svc *Service, ref uuid.UUID) error {
			return svc.UpdateBusinessPage(context.Background(), &store.BusinessPage{ID: ref, UserID: owner, AvatarMediaID: a.ID.String()})
		},
		"channel delete": func(svc *Service, ref uuid.UUID) error {
			return svc.DeleteChannel(context.Background(), ref, owner)
		},
		"portfolio delete": func(svc *Service, ref uuid.UUID) error {
			return svc.DeletePortfolioItem(context.Background(), ref, owner)
		},
		"page delete": func(svc *Service, ref uuid.UUID) error {
			return svc.DeleteBusinessPage(context.Background(), ref, owner)
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc, st, _ := harness(a)
			ref := seedChannel(st, owner, nil)
			st.pages[ref] = &store.BusinessPage{ID: ref, UserID: owner, Status: "approved"}
			st.items[ref] = &store.PortfolioItem{ID: ref, UserID: owner}
			unlock, _ := st.LockMediaHolder(context.Background(), ref)
			done := make(chan error, 1)
			go func() { done <- run(svc, ref) }()
			select {
			case err := <-done:
				t.Fatalf("finished (%v) while another writer held the holder lock", err)
			case <-time.After(100 * time.Millisecond):
			}
			unlock()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("after unlock: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("never took the lock")
			}
		})
	}
}
