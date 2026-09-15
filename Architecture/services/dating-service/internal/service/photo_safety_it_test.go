// Lane D6 dating photo safety integration tests (dating_it_test). Skipped
// unless TEST_PG_DSN is set; refuses a database not named *_test.
// media-service is a fake MediaPhotoClient. Ownership is enforced only in its
// PhotoOwnerStatus, so these tests exercise dating's own check;
// media-service's re-check on prepare and delivery is tested there
// (internal/http/dating_photo_handler_test.go).
package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	datingevents "github.com/atpost/dating-service/internal/events"
	"github.com/atpost/dating-service/internal/store"
	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeMediaPhoto struct {
	owner uuid.UUID
	st    MediaPhotoStatus
}

type fakeMediaPhotos struct {
	mu         sync.Mutex
	media      map[uuid.UUID]*fakeMediaPhoto
	statuses   []uuid.UUID
	prepares   []uuid.UUID
	deletes    [][2]uuid.UUID
	deliveries []string
	deleteErr  error
}

func newFakeMediaPhotos() *fakeMediaPhotos {
	return &fakeMediaPhotos{media: map[uuid.UUID]*fakeMediaPhoto{}}
}

// add registers a ready, passed, scanned, clean image owned by owner.
func (f *fakeMediaPhotos) add(owner uuid.UUID, mutate func(*MediaPhotoStatus)) uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	st := MediaPhotoStatus{MediaID: id, OwnerMatches: true, Kind: "image", Status: "ready", ModerationStatus: "passed",
		ContentType: "image/jpeg", ModerationScanned: true, ModerationScanner: "mock", ModerationLabels: []MediaPhotoLabel{}}
	if mutate != nil {
		mutate(&st)
	}
	f.media[id] = &fakeMediaPhoto{owner: owner, st: st}
	return id
}

func (f *fakeMediaPhotos) setModeration(id uuid.UUID, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.media[id].st.ModerationStatus = status
}

func (f *fakeMediaPhotos) PhotoOwnerStatus(_ context.Context, mediaID, requester uuid.UUID) (*MediaPhotoStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = append(f.statuses, mediaID)
	m, ok := f.media[mediaID]
	if !ok || m.owner != requester {
		return nil, ErrPhotoMediaNotFound
	}
	cp := m.st
	return &cp, nil
}

func (f *fakeMediaPhotos) PreparePhoto(_ context.Context, mediaID, _ uuid.UUID, detectFaces bool) (*MediaPhotoStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepares = append(f.prepares, mediaID)
	m, ok := f.media[mediaID]
	if !ok {
		return nil, ErrPhotoMediaNotFound
	}
	if !m.st.Usable() {
		return nil, ErrPhotoMediaNotReady
	}
	m.st.Prepared = true
	cp := m.st
	if !detectFaces {
		cp.FaceCount = nil
	}
	return &cp, nil
}

func (f *fakeMediaPhotos) PhotoDeliveryURL(_ context.Context, mediaID, owner uuid.UUID, variant string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.media[mediaID]
	if !ok || m.owner != owner {
		return "", ErrPhotoMediaNotFound
	}
	f.deliveries = append(f.deliveries, variant)
	return "https://cdn.test/signed/" + variant + "?Signature=sig", nil
}

func (f *fakeMediaPhotos) DeletePhotoMedia(_ context.Context, mediaID, owner uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, [2]uuid.UUID{mediaID, owner})
	return f.deleteErr
}

func (f *fakeMediaPhotos) counts() (prepares, deletes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prepares), len(f.deletes)
}

type photoEnv struct {
	svc    *Service
	st     *store.Store
	pool   *pgxpool.Pool
	media  *fakeMediaPhotos
	writer *recordingWriter
}

func newPhotoEnv(t *testing.T) *photoEnv {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping lane D6 photo safety tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	ensureSchemaForTest(t, pool)
	st := store.New(pool)
	svc := New(st, nil)
	writer := &recordingWriter{}
	svc.SetProducer(datingevents.NewProducerWithWriter(writer))
	svc.SetMessageClient(&syncStubMessageClient{})
	media := newFakeMediaPhotos()
	svc.SetMediaPhotoClient(media)
	return &photoEnv{svc: svc, st: st, pool: pool, media: media, writer: writer}
}

// seedAtPendingPhoto writes the basics and walks the profile to pending_photo.
func seedAtPendingPhoto(t *testing.T, st *store.Store, id uuid.UUID) {
	t.Helper()
	seedBasicsProfile(t, st, id)
	if p := driveProfile(t, st, id, store.ProfileEventBasicsComplete, store.ProfileActorSystem); p.ProfileStatus != store.ProfileStatusPendingPhoto {
		t.Fatalf("seeded status = %s, want pending_photo", p.ProfileStatus)
	}
}

// seedActiveWithPrimary attaches a clean primary photo through the service
// and passes the selfie, leaving the profile active.
func (e *photoEnv) seedActiveWithPrimary(t *testing.T, id uuid.UUID) (*store.Photo, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	seedAtPendingPhoto(t, e.st, id)
	media := e.media.add(id, nil)
	photo, err := e.svc.CreatePhoto(ctx, id, store.CreatePhotoParams{MediaID: media, IsPrimary: true, Visibility: "public"})
	if err != nil {
		t.Fatalf("attach primary: %v", err)
	}
	if err := e.st.RecordSelfieAttempt(ctx, id, 0.99, "passed"); err != nil {
		t.Fatalf("seed selfie: %v", err)
	}
	if p, err := e.svc.advanceOnboarding(ctx, id); err != nil || p.ProfileStatus != store.ProfileStatusActive {
		t.Fatalf("activate = %+v, %v", p, err)
	}
	return photo, media
}

func (e *photoEnv) sourceOf(t *testing.T, photoID uuid.UUID) (source, reason string) {
	t.Helper()
	var src, rsn *string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT moderation_source, moderation_reason FROM dating_photos WHERE id = $1`, photoID).Scan(&src, &rsn); err != nil {
		t.Fatalf("read photo: %v", err)
	}
	if src != nil {
		source = *src
	}
	if rsn != nil {
		reason = *rsn
	}
	return source, reason
}

func (e *photoEnv) rejectedEvents(t *testing.T) int {
	t.Helper()
	n := 0
	for _, ev := range e.writer.events(t) {
		if ev.EventType == sharedevents.EventDatingPhotoModerationRejected {
			n++
		}
	}
	return n
}

func TestPhotoAttach_AnotherUsersMediaIs404(t *testing.T) {
	e := newPhotoEnv(t)
	ctx := context.Background()
	owner, other := uuid.New(), uuid.New()
	seedAtPendingPhoto(t, e.st, owner)
	foreign := e.media.add(other, nil)

	_, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: foreign, IsPrimary: true})
	if !errors.Is(err, ErrPhotoMediaNotFound) {
		t.Fatalf("attach another user's media: err=%v, want ErrPhotoMediaNotFound", err)
	}
	if photos, _ := e.st.ListPhotos(ctx, owner); len(photos) != 0 {
		t.Fatalf("a photo was stored for foreign media: %+v", photos)
	}
	if prepares, _ := e.media.counts(); prepares != 0 {
		t.Fatalf("foreign media was prepared (%d calls)", prepares)
	}
}

func TestPhotoAttach_NotReadyMediaIs409(t *testing.T) {
	e := newPhotoEnv(t)
	ctx := context.Background()
	owner := uuid.New()
	seedAtPendingPhoto(t, e.st, owner)
	for name, mutate := range map[string]func(*MediaPhotoStatus){
		"processing":       func(st *MediaPhotoStatus) { st.Status = "processing" },
		"moderation held":  func(st *MediaPhotoStatus) { st.ModerationStatus = "manual_review" },
		"media rejected":   func(st *MediaPhotoStatus) { st.Status = "rejected" },
		"video, not image": func(st *MediaPhotoStatus) { st.Kind = "video" },
	} {
		media := e.media.add(owner, mutate)
		if _, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: media}); !errors.Is(err, ErrPhotoMediaNotReady) {
			t.Fatalf("%s: err=%v, want ErrPhotoMediaNotReady", name, err)
		}
	}
	if photos, _ := e.st.ListPhotos(ctx, owner); len(photos) != 0 {
		t.Fatalf("not-ready media was stored: %+v", photos)
	}
	if prepares, _ := e.media.counts(); prepares != 0 {
		t.Fatalf("not-ready media was prepared (%d calls)", prepares)
	}
}

func TestPhotoAttach_SeventhPhotoRefused(t *testing.T) {
	e := newPhotoEnv(t)
	ctx := context.Background()
	owner := uuid.New()
	seedAtPendingPhoto(t, e.st, owner)
	var first uuid.UUID
	for i := 0; i < 6; i++ {
		media := e.media.add(owner, nil)
		if i == 0 {
			first = media
		}
		if _, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: media, SortOrder: i}); err != nil {
			t.Fatalf("photo %d: %v", i+1, err)
		}
	}
	seventh := e.media.add(owner, nil)
	if _, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: seventh}); !errors.Is(err, store.ErrPhotoLimitReached) {
		t.Fatalf("7th photo: err=%v, want ErrPhotoLimitReached", err)
	}
	if prepares, _ := e.media.counts(); prepares != 6 {
		t.Fatalf("prepares = %d; the refused 7th must not be prepared", prepares)
	}
	// The store enforces the limit too, whatever the caller pre-checked.
	if _, err := e.st.CreateModeratedPhoto(ctx, owner, store.CreatePhotoParams{MediaID: seventh}, 6,
		store.PhotoDecision{Status: store.PhotoStatusApproved, Source: store.PhotoSourceAuto}); !errors.Is(err, store.ErrPhotoLimitReached) {
		t.Fatalf("store 7th: err=%v", err)
	}
	if _, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: first}); !errors.Is(err, store.ErrPhotoAlreadyAttached) {
		t.Fatalf("same media again: err=%v, want ErrPhotoAlreadyAttached", err)
	}
	if photos, _ := e.st.ListPhotos(ctx, owner); len(photos) != 6 {
		t.Fatalf("photos = %d, want 6", len(photos))
	}
}

func TestPhotoAttach_CleanApprovesAndAdvancesProfile(t *testing.T) {
	e := newPhotoEnv(t)
	ctx := context.Background()
	owner := uuid.New()
	seedAtPendingPhoto(t, e.st, owner)
	media := e.media.add(owner, func(st *MediaPhotoStatus) { one := 1; st.FaceCount = &one })

	photo, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: media, IsPrimary: true, Visibility: "public"})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if photo.ModerationStatus != store.PhotoStatusApproved || !photo.IsPrimary {
		t.Fatalf("photo = %+v; want approved primary", photo)
	}
	if src, _ := e.sourceOf(t, photo.ID); src != store.PhotoSourceAuto {
		t.Fatalf("moderation_source = %q, want auto", src)
	}
	if got := profileStatusOf(t, e.st, owner); got != store.ProfileStatusPendingSelfie {
		t.Fatalf("profile = %s, want pending_selfie (writer advanced on the approved primary)", got)
	}
}

func TestPhotoAttach_ExplicitResultRejected(t *testing.T) {
	e := newPhotoEnv(t)
	ctx := context.Background()
	owner := uuid.New()
	seedAtPendingPhoto(t, e.st, owner)
	media := e.media.add(owner, func(st *MediaPhotoStatus) {
		st.ModerationLabels = []MediaPhotoLabel{{Name: "Nudity", Parent: "Explicit Nudity", Confidence: 96}}
	})

	photo, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: media, IsPrimary: true})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if photo.ModerationStatus != store.PhotoStatusRejected || photo.IsPrimary ||
		photo.ModerationReason == nil || *photo.ModerationReason != PhotoReasonExplicit {
		t.Fatalf("photo = %+v; want rejected, not primary, reason EXPLICIT_CONTENT", photo)
	}
	if got := profileStatusOf(t, e.st, owner); got != store.ProfileStatusPendingPhoto {
		t.Fatalf("profile = %s, want pending_photo", got)
	}
	if n := e.rejectedEvents(t); n != 1 {
		t.Fatalf("rejected events = %d, want 1", n)
	}
}

func TestPhotoAttach_BorderlineUnscannedAndFacelessGoToReview(t *testing.T) {
	e := newPhotoEnv(t)
	ctx := context.Background()
	owner := uuid.New()
	seedAtPendingPhoto(t, e.st, owner)
	zero := 0
	cases := []struct {
		name    string
		mutate  func(*MediaPhotoStatus)
		primary bool
		reason  string
	}{
		{"swimwear", func(st *MediaPhotoStatus) {
			st.ModerationLabels = []MediaPhotoLabel{{Name: "Swimwear or Underwear", Confidence: 92}}
		}, false, PhotoReasonBorderline},
		{"never scanned", func(st *MediaPhotoStatus) { st.ModerationScanned = false }, false, PhotoReasonNotScanned},
		{"primary without a face", func(st *MediaPhotoStatus) { st.FaceCount = &zero }, true, PhotoReasonNoFace},
	}
	for _, tc := range cases {
		media := e.media.add(owner, tc.mutate)
		photo, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: media, IsPrimary: tc.primary})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if photo.ModerationStatus != store.PhotoStatusPendingReview || photo.ModerationReason == nil || *photo.ModerationReason != tc.reason {
			t.Fatalf("%s: photo = %+v; want pending_review %s", tc.name, photo, tc.reason)
		}
	}
	if got := profileStatusOf(t, e.st, owner); got != store.ProfileStatusPendingPhoto {
		t.Fatalf("profile = %s, want pending_photo (nothing approved)", got)
	}
	queue, err := e.st.ListPendingPhotos(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	inQueue := 0
	for _, p := range queue {
		if p.UserID == owner {
			inQueue++
		}
	}
	if inQueue != len(cases) {
		t.Fatalf("moderator queue holds %d of the owner's photos, want %d", inQueue, len(cases))
	}
	if n := e.rejectedEvents(t); n != 0 {
		t.Fatalf("review is not a rejection: %d rejected events", n)
	}
}

func TestPhotoLaterRejection_ActivePrimaryBackToPendingPhoto(t *testing.T) {
	e := newPhotoEnv(t)
	ctx := context.Background()

	// A moderator rejects the approved primary of an active profile.
	byAdmin := uuid.New()
	photo, _ := e.seedActiveWithPrimary(t, byAdmin)
	if _, err := e.svc.SetPhotoModerationStatus(ctx, uuid.New(), photo.ID, store.PhotoStatusRejected, "not the account holder"); err != nil {
		t.Fatalf("admin reject: %v", err)
	}
	if got := profileStatusOf(t, e.st, byAdmin); got != store.ProfileStatusPendingPhoto {
		t.Fatalf("after admin rejection profile = %s, want pending_photo", got)
	}
	if src, _ := e.sourceOf(t, photo.ID); src != store.PhotoSourceAdmin {
		t.Fatalf("source = %q, want admin", src)
	}
	events := e.rejectedEvents(t)
	// The same rejection again changes nothing and publishes nothing.
	if _, err := e.svc.SetPhotoModerationStatus(ctx, uuid.New(), photo.ID, store.PhotoStatusRejected, "not the account holder"); err != nil {
		t.Fatalf("admin reject again: %v", err)
	}
	if n := e.rejectedEvents(t); n != events {
		t.Fatalf("duplicate rejection published again: %d -> %d", events, n)
	}

	// media-service later withdraws the asset: the recheck rejects it.
	byMedia := uuid.New()
	photo, media := e.seedActiveWithPrimary(t, byMedia)
	// Another approved, non-primary photo does not keep the photo step.
	if _, err := e.svc.CreatePhoto(ctx, byMedia, store.CreatePhotoParams{MediaID: e.media.add(byMedia, nil), SortOrder: 1}); err != nil {
		t.Fatalf("second photo: %v", err)
	}
	if got := profileStatusOf(t, e.st, byMedia); got != store.ProfileStatusActive {
		t.Fatalf("adding a photo moved the profile to %s; the approved primary still stands", got)
	}
	e.media.setModeration(media, "rejected")
	recheck := store.PhotoRecheck{Photo: *photo, ModerationSource: store.PhotoSourceAuto}
	events = e.rejectedEvents(t)
	if err := e.svc.recheckPhoto(ctx, e.svc.PhotoSafety(), recheck); err != nil {
		t.Fatalf("recheck: %v", err)
	}
	after, err := e.st.GetPhotoByID(ctx, photo.ID)
	if err != nil || after.ModerationStatus != store.PhotoStatusRejected || after.ModerationReason == nil ||
		*after.ModerationReason != PhotoReasonMediaUnavailable {
		t.Fatalf("photo after recheck = %+v, %v", after, err)
	}
	if got := profileStatusOf(t, e.st, byMedia); got != store.ProfileStatusPendingPhoto {
		t.Fatalf("after media rejection profile = %s, want pending_photo", got)
	}
	if n := e.rejectedEvents(t); n != events+1 {
		t.Fatalf("rejected events %d -> %d, want one more", events, n)
	}
	// A duplicate result is idempotent.
	recheck.ModerationStatus = store.PhotoStatusRejected
	if err := e.svc.recheckPhoto(ctx, e.svc.PhotoSafety(), recheck); err != nil {
		t.Fatalf("recheck again: %v", err)
	}
	if n := e.rejectedEvents(t); n != events+1 {
		t.Fatalf("duplicate recheck published again: %d", n)
	}
}

func TestPhotoRecheck_ModeratorApprovalStandsAndLegacyPhotosArePrepared(t *testing.T) {
	e := newPhotoEnv(t)
	ctx := context.Background()
	owner := uuid.New()
	seedAtPendingPhoto(t, e.st, owner)
	// A borderline photo a moderator approved: the same labels on recheck do
	// not send it back to review.
	borderline := e.media.add(owner, func(st *MediaPhotoStatus) {
		st.ModerationLabels = []MediaPhotoLabel{{Name: "Swimwear or Underwear", Confidence: 92}}
	})
	photo, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: borderline, IsPrimary: true})
	if err != nil || photo.ModerationStatus != store.PhotoStatusPendingReview {
		t.Fatalf("attach = %+v, %v", photo, err)
	}
	if photo, err = e.svc.SetPhotoModerationStatus(ctx, uuid.New(), photo.ID, store.PhotoStatusApproved, ""); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := e.svc.recheckPhoto(ctx, e.svc.PhotoSafety(), store.PhotoRecheck{Photo: *photo, ModerationSource: store.PhotoSourceAdmin}); err != nil {
		t.Fatalf("recheck: %v", err)
	}
	if cur, _ := e.st.GetPhotoByID(ctx, photo.ID); cur.ModerationStatus != store.PhotoStatusApproved {
		t.Fatalf("moderator approval overturned: %+v", cur)
	}

	// A photo attached before lane D6 (stored straight in the table) is
	// prepared and classified.
	legacyMedia := e.media.add(owner, nil)
	legacy, err := e.st.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: legacyMedia, SortOrder: 2})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := e.media.counts()
	if err := e.svc.recheckPhoto(ctx, e.svc.PhotoSafety(), store.PhotoRecheck{Photo: *legacy}); err != nil {
		t.Fatalf("legacy recheck: %v", err)
	}
	if after, _ := e.media.counts(); after != before+1 {
		t.Fatalf("legacy photo was not prepared (prepares %d -> %d)", before, after)
	}
	if cur, _ := e.st.GetPhotoByID(ctx, legacy.ID); cur.ModerationStatus != store.PhotoStatusApproved {
		t.Fatalf("legacy photo = %+v; want approved by the automated result", cur)
	}
}

func TestPhotoDelete_CallsMediaDelete(t *testing.T) {
	e := newPhotoEnv(t)
	ctx := context.Background()
	owner := uuid.New()
	primary, primaryMedia := e.seedActiveWithPrimary(t, owner)
	extraMedia := e.media.add(owner, nil)
	extra, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: extraMedia, SortOrder: 1})
	if err != nil {
		t.Fatal(err)
	}

	// media-service down: the photo stays.
	e.media.deleteErr = ErrPhotoMediaUnavailable
	if err := e.svc.DeletePhoto(ctx, owner, extra.ID); !errors.Is(err, ErrPhotoMediaUnavailable) {
		t.Fatalf("delete with media down: err=%v", err)
	}
	if _, err := e.st.GetPhotoForUser(ctx, owner, extra.ID); err != nil {
		t.Fatalf("photo removed although the media delete failed: %v", err)
	}
	e.media.deleteErr = nil

	if err := e.svc.DeletePhoto(ctx, owner, extra.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, deletes := e.media.counts()
	if deletes != 2 || e.media.deletes[1] != [2]uuid.UUID{extraMedia, owner} {
		t.Fatalf("media deletes = %v; want (media, owner) for the deleted photo", e.media.deletes)
	}
	if _, err := e.st.GetPhotoForUser(ctx, owner, extra.ID); !errors.Is(err, store.ErrPhotoNotFound) {
		t.Fatalf("photo still present: %v", err)
	}
	if got := profileStatusOf(t, e.st, owner); got != store.ProfileStatusActive {
		t.Fatalf("deleting a non-primary photo moved the profile to %s", got)
	}

	// Deleting the approved primary takes the photo step back.
	if err := e.svc.DeletePhoto(ctx, owner, primary.ID); err != nil {
		t.Fatalf("delete primary: %v", err)
	}
	if e.media.deletes[len(e.media.deletes)-1] != [2]uuid.UUID{primaryMedia, owner} {
		t.Fatalf("primary media not deleted: %v", e.media.deletes)
	}
	if got := profileStatusOf(t, e.st, owner); got != store.ProfileStatusPendingPhoto {
		t.Fatalf("after deleting the primary profile = %s, want pending_photo", got)
	}
	// Someone else's photo id is a not-found and deletes nothing.
	if err := e.svc.DeletePhoto(ctx, uuid.New(), primary.ID); !errors.Is(err, store.ErrPhotoNotFound) {
		t.Fatalf("foreign delete: err=%v", err)
	}
	_ = time.Now
}
