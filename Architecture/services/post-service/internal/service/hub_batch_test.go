package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Creator Hub batch (2026-09-28): the widened PATCH, the bulk routes, the
// age gate, hide_like_count, the related post and private sharing, each
// guard pinned without a database. The SQL halves are in
// store/postgres/hub_batch_integration_test.go.

var hubNow = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func newHubRig(t *testing.T) (*Service, *fakePostEditStore, uuid.UUID, *postgres.Post) {
	t.Helper()
	svc, store, owner, post := newEditRig(t)
	svc.now = func() time.Time { return hubNow }
	return svc, store, owner, post
}

// ── A. PATCH: one test per new validation code ─────────────────────────────

func TestUpdatePostRefusesEveryBadHubValue(t *testing.T) {
	svc, store, owner, post := newHubRig(t)
	ctx := context.Background()
	foreign := &postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "long_video", Visibility: "public"}
	store.posts[foreign.ID] = foreign
	deletedAt := hubNow.Add(-time.Hour)
	deleted := &postgres.Post{ID: uuid.New(), AuthorID: owner, ContentType: "long_video", DeletedAt: &deletedAt}
	store.posts[deleted.ID] = deleted
	cases := []struct {
		name string
		in   PostEditInput
		want error
		code string
	}{
		{"license", PostEditInput{License: str("cc-by")}, ErrInvalidLicense, "INVALID_LICENSE"},
		{"recording date shape", PostEditInput{RecordingDate: str("20/09/2026")}, ErrInvalidRecordingDate, "INVALID_RECORDING_DATE"},
		{"recording date in the future", PostEditInput{RecordingDate: str("2026-09-30")}, ErrInvalidRecordingDate, "INVALID_RECORDING_DATE"},
		{"recording location over 100 runes", PostEditInput{RecordingLocation: str(strings.Repeat("ఆ", 101))}, ErrInvalidRecordingLocation, "INVALID_RECORDING_LOCATION"},
		{"remix setting", PostEditInput{RemixSetting: str("sometimes")}, ErrInvalidRemixSetting, "INVALID_REMIX_SETTING"},
		{"comment moderation", PostEditInput{CommentModeration: str("loose")}, ErrInvalidCommentModeration, "INVALID_COMMENT_MODERATION"},
		{"comment access", PostEditInput{CommentAccess: str("friends")}, ErrInvalidCommentAccess, "INVALID_COMMENT_ACCESS"},
		{"comment sort", PostEditInput{DefaultCommentSort: str("oldest")}, ErrInvalidCommentSort, "INVALID_COMMENT_SORT"},
		{"related: malformed id", PostEditInput{RelatedPostID: str("nope")}, ErrRelatedNotFound, "RELATED_NOT_FOUND"},
		{"related: missing post", PostEditInput{RelatedPostID: str(uuid.NewString())}, ErrRelatedNotFound, "RELATED_NOT_FOUND"},
		{"related: someone else's post", PostEditInput{RelatedPostID: str(foreign.ID.String())}, ErrRelatedNotFound, "RELATED_NOT_FOUND"},
		{"related: deleted post", PostEditInput{RelatedPostID: str(deleted.ID.String())}, ErrRelatedNotFound, "RELATED_NOT_FOUND"},
		{"related: itself", PostEditInput{RelatedPostID: str(post.ID.String())}, ErrRelatedSelf, "RELATED_SELF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.UpdatePost(ctx, owner, post.ID, tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			if status, code := PostEditErrorStatus(err); status != http.StatusUnprocessableEntity || code != tc.code {
				t.Fatalf("mapped to %d %s want 422 %s", status, code, tc.code)
			}
		})
	}
	if len(store.updates) != 0 {
		t.Fatalf("a refused edit reached the store: %+v", store.updates)
	}
}

func TestUpdatePostAcceptsAndCanonicalisesHubValues(t *testing.T) {
	svc, store, owner, post := newHubRig(t)
	ctx := context.Background()
	mine := &postgres.Post{ID: uuid.New(), AuthorID: owner, ContentType: "long_video", Visibility: "private"}
	store.posts[mine.ID] = mine
	_, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{
		PaidPromotion: boolp(true), AlteredContent: boolp(true), AllowEmbedding: boolp(false),
		License: str(" Creative_Commons "), RemixSetting: str("allow_audio_only"), CommentModeration: str("HOLD_ALL"),
		CommentAccess: str("followers"), DefaultCommentSort: str("Newest"),
		RecordingDate: str("2026-09-28"), RecordingLocation: str("  Hyderabad  "),
		AgeRestricted: boolp(true), HideLikeCount: boolp(true), RelatedPostID: str(mine.ID.String()),
	})
	if err != nil {
		t.Fatalf("valid edit: %v", err)
	}
	p := store.updates[len(store.updates)-1]
	if *p.License != "creative_commons" || *p.CommentModeration != "hold_all" || *p.DefaultCommentSort != "newest" ||
		*p.RecordingLocation != "Hyderabad" || p.RecordingDate.Format("2006-01-02") != "2026-09-28" ||
		!*p.AgeRestricted || !*p.HideLikeCount || *p.RelatedPostID != mine.ID || !*p.PaidPromotion || *p.AllowEmbedding {
		t.Fatalf("patch not canonical: %+v", p)
	}
	if p.AuditAction != "post.edit" {
		t.Fatalf("audit action %q", p.AuditAction)
	}
	// "" clears the two nullable fields.
	if _, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{RecordingDate: str(""), RelatedPostID: str("")}); err != nil {
		t.Fatal(err)
	}
	p = store.updates[len(store.updates)-1]
	if !p.ClearRecordingDate || !p.ClearRelatedPost || p.RecordingDate != nil || p.RelatedPostID != nil {
		t.Fatalf("clear: %+v", p)
	}
}

// notify_subscribers rewrites the policy of a scheduled post only, and a
// NULL (legacy) policy keeps main_feed at its resolved value.
func TestUpdatePostNotifySubscribersOnlyBeforePublish(t *testing.T) {
	svc, store, owner, post := newHubRig(t)
	ctx := context.Background()
	if _, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{NotifySubscribers: boolp(false)}); err != nil {
		t.Fatal(err)
	}
	if p := store.updates[len(store.updates)-1]; p.Distribution != nil {
		t.Fatalf("published post got a policy write: %s", p.Distribution)
	}
	publishAt := hubNow.Add(24 * time.Hour)
	store.posts[post.ID].PublishAt = &publishAt
	if _, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{NotifySubscribers: boolp(false)}); err != nil {
		t.Fatal(err)
	}
	p := store.updates[len(store.updates)-1]
	var policy DistributionPolicy
	if err := json.Unmarshal(p.Distribution, &policy); err != nil || policy.NotifySubscribers == nil || *policy.NotifySubscribers ||
		policy.MainFeed == nil || !*policy.MainFeed || policy.Version != 1 {
		t.Fatalf("policy=%s err=%v", p.Distribution, err)
	}
	if p.DistributionEvent == nil || p.DistributionChange == nil {
		t.Fatal("no event / audit entry for the policy write")
	}
	if et, _ := p.DistributionEvent(7); et != "PostDistributionUpdated" {
		t.Fatalf("event %q", et)
	}
	// Same value again: nothing to write.
	if _, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{NotifySubscribers: boolp(false)}); err != nil {
		t.Fatal(err)
	}
	if p := store.updates[len(store.updates)-1]; p.Distribution != nil {
		t.Fatal("unchanged notify_subscribers rewrote the policy")
	}
}

func TestRecordingDateAllowsTodayInTheEasternmostZone(t *testing.T) {
	late := time.Date(2026, 9, 28, 23, 0, 0, 0, time.UTC) // already the 29th at UTC+14
	if _, err := parseRecordingDate("2026-09-29", late); err != nil {
		t.Fatalf("today somewhere refused: %v", err)
	}
	if _, err := parseRecordingDate("2026-09-30", late); !errors.Is(err, ErrInvalidRecordingDate) {
		t.Fatalf("tomorrow everywhere accepted: %v", err)
	}
}

// ── C. bulk edit ────────────────────────────────────────────────────────────

func TestBulkEditRefusesTheRequestBeforeAnyWrite(t *testing.T) {
	svc, store, owner, post := newHubRig(t)
	ctx := context.Background()
	other := &postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), Visibility: "public"}
	store.posts[other.ID] = other
	cases := []struct {
		name  string
		ids   []uuid.UUID
		patch BulkPatch
		want  error
	}{
		{"empty patch", []uuid.UUID{post.ID}, BulkPatch{}, ErrBulkEmptyPatch},
		{"bad tags mode", []uuid.UUID{post.ID}, BulkPatch{Tags: &[]string{"x"}, TagsMode: "merge"}, ErrBulkTagsMode},
		{"bad visibility", []uuid.UUID{post.ID}, BulkPatch{Visibility: str("staged")}, ErrInvalidVisibility},
		{"bad license", []uuid.UUID{post.ID}, BulkPatch{License: str("gpl")}, ErrInvalidLicense},
		{"bad remix", []uuid.UUID{post.ID}, BulkPatch{RemixSetting: str("x")}, ErrInvalidRemixSetting},
		{"bad moderation", []uuid.UUID{post.ID}, BulkPatch{CommentModeration: str("x")}, ErrInvalidCommentModeration},
		{"bad access", []uuid.UUID{post.ID}, BulkPatch{CommentAccess: str("x")}, ErrInvalidCommentAccess},
		{"bad sort", []uuid.UUID{post.ID}, BulkPatch{DefaultCommentSort: str("x")}, ErrInvalidCommentSort},
		{"future recording date", []uuid.UUID{post.ID}, BulkPatch{RecordingDate: str("2027-01-01")}, ErrInvalidRecordingDate},
		{"bad language", []uuid.UUID{post.ID}, BulkPatch{Language: str("english-language-x")}, ErrInvalidLanguage},
		{"one foreign id", []uuid.UUID{post.ID, other.ID}, BulkPatch{AgeRestricted: boolp(true)}, ErrNotPostAuthor},
		{"one unknown id", []uuid.UUID{post.ID, uuid.New()}, BulkPatch{AgeRestricted: boolp(true)}, ErrNotPostAuthor},
		{"no ids", nil, BulkPatch{AgeRestricted: boolp(true)}, ErrBulkNothing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.BulkEditUploads(ctx, owner, BulkEditInput{PostIDs: tc.ids, Patch: tc.patch}); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
	if len(store.updates) != 0 {
		t.Fatalf("a refused bulk reached the store: %+v", store.updates)
	}
}

// One bad post never rolls back the others: a post whose own validation
// fails (a category its kind does not admit) and a post whose write fails
// each get their own outcome, and every other post is written.
func TestBulkEditIsolatesEachPost(t *testing.T) {
	svc, store, owner, post := newHubRig(t)
	ctx := context.Background()
	flick := &postgres.Post{ID: uuid.New(), AuthorID: owner, ContentType: "flick", Visibility: "public"}
	broken := &postgres.Post{ID: uuid.New(), AuthorID: owner, ContentType: "long_video", Visibility: "public"}
	last := &postgres.Post{ID: uuid.New(), AuthorID: owner, ContentType: "long_video", Visibility: "public"}
	for _, p := range []*postgres.Post{flick, broken, last} {
		store.posts[p.ID] = p
	}
	store.failWrite[broken.ID] = errors.New("connection reset")

	out, err := svc.BulkEditUploads(ctx, owner, BulkEditInput{
		PostIDs: []uuid.UUID{post.ID, flick.ID, broken.ID, last.ID, post.ID},
		Patch:   BulkPatch{Category: str("podcasts"), AgeRestricted: boolp(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Fatalf("outcomes=%+v (want one per distinct id)", out)
	}
	want := []struct {
		id   uuid.UUID
		ok   bool
		code string
	}{{post.ID, true, ""}, {flick.ID, false, "INVALID_CATEGORY"}, {broken.ID, false, "INTERNAL_ERROR"}, {last.ID, true, ""}}
	for i, w := range want {
		if out[i].ID != w.id || out[i].OK != w.ok || out[i].Error != w.code {
			t.Fatalf("outcome %d = %+v want %+v", i, out[i], w)
		}
	}
	if !store.posts[post.ID].AgeRestricted || !store.posts[last.ID].AgeRestricted || store.posts[flick.ID].AgeRestricted {
		t.Fatal("the good posts were not written, or the refused one was")
	}
	for _, p := range store.updates {
		if p.AuditAction != "post.bulk_edit" {
			t.Fatalf("bulk write audited as %q", p.AuditAction)
		}
	}
}

func TestBulkEditTagsModes(t *testing.T) {
	svc, store, owner, post := newHubRig(t)
	ctx := context.Background()
	store.posts[post.ID].Tags = []string{"Go", "kafka"}
	run := func(mode string, tags ...string) []string {
		t.Helper()
		out, err := svc.BulkEditUploads(ctx, owner, BulkEditInput{PostIDs: []uuid.UUID{post.ID}, Patch: BulkPatch{Tags: &tags, TagsMode: mode}})
		if err != nil || !out[0].OK {
			t.Fatalf("%s: %v %+v", mode, err, out)
		}
		return store.posts[post.ID].Tags
	}
	if got := run("", "go", "redis"); strings.Join(got, ",") != "Go,kafka,redis" {
		t.Fatalf("add (default, deduped case-insensitively): %v", got)
	}
	if got := run("remove", "GO", " Redis "); strings.Join(got, ",") != "kafka" {
		t.Fatalf("remove: %v", got)
	}
	if got := run("replace", "one"); strings.Join(got, ",") != "one" {
		t.Fatalf("replace: %v", got)
	}
	// The caps are per post: adding past 20 fails that post only.
	many := make([]string, 20)
	for i := range many {
		many[i] = "t" + string(rune('a'+i))
	}
	out, err := svc.BulkEditUploads(ctx, owner, BulkEditInput{PostIDs: []uuid.UUID{post.ID}, Patch: BulkPatch{Tags: &many}})
	if err != nil || out[0].OK || out[0].Error != "INVALID_TAGS" {
		t.Fatalf("over the cap: %+v %v", out, err)
	}
}

// ── D. bulk delete ──────────────────────────────────────────────────────────

func TestBulkDeleteRunsEachIDThroughTheOwnerDelete(t *testing.T) {
	var seen []uuid.UUID
	owner := uuid.New()
	mine, foreign, gone := uuid.New(), uuid.New(), uuid.New()
	svc := NewForHandlerTests(HandlerTestDeps{BulkDelete: func(_ context.Context, id, caller uuid.UUID) error {
		seen = append(seen, id)
		if caller != owner {
			t.Errorf("delete ran as %s", caller)
		}
		switch id {
		case foreign:
			return ErrPostForbidden
		case gone:
			return ErrPostNotFound
		}
		return nil
	}})
	out, err := svc.BulkDeleteUploads(context.Background(), owner, []uuid.UUID{mine, foreign, gone, mine})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || !out[0].OK || out[1].OK || out[1].Error != "FORBIDDEN" || out[2].OK || out[2].Error != "NOT_FOUND" {
		t.Fatalf("outcomes=%+v", out)
	}
	if len(seen) != 3 {
		t.Fatalf("delete calls=%v (a failure must not stop the rest; duplicates once)", seen)
	}
	if _, err := svc.BulkDeleteUploads(context.Background(), owner, nil); !errors.Is(err, ErrBulkNothing) {
		t.Fatalf("empty: %v", err)
	}
	many := make([]uuid.UUID, MaxBulkPostIDs+1)
	for i := range many {
		many[i] = uuid.New()
	}
	if _, err := svc.BulkDeleteUploads(context.Background(), owner, many); !errors.Is(err, ErrBulkTooMany) {
		t.Fatalf("over the cap: %v", err)
	}
	if _, err := svc.BulkDeleteUploads(context.Background(), uuid.Nil, []uuid.UUID{mine}); !errors.Is(err, ErrNotPostAuthor) {
		t.Fatalf("anonymous: %v", err)
	}
}

// ── B. age gate ─────────────────────────────────────────────────────────────

type fakeBirthDates struct {
	dob   map[uuid.UUID]*time.Time
	err   error
	calls int32
}

func (f *fakeBirthDates) BirthDate(_ context.Context, id uuid.UUID) (*time.Time, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.err != nil {
		return nil, f.err
	}
	return f.dob[id], nil
}

func dayp(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestIsAdultOnCountsTheBirthday(t *testing.T) {
	now := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	if !isAdultOn(*dayp(2008, 9, 28), now) {
		t.Fatal("18th birthday today refused")
	}
	if isAdultOn(*dayp(2008, 9, 29), now) {
		t.Fatal("17 years 364 days admitted")
	}
	// 29 Feb: the birthday in a common year is 1 March.
	if isAdultOn(*dayp(2008, 2, 29), time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("leap-day birth admitted on 28 Feb")
	}
}

func TestAgeGateRefusals(t *testing.T) {
	owner, adult, minor, unknown := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	src := &fakeBirthDates{dob: map[uuid.UUID]*time.Time{adult: dayp(1990, 1, 1), minor: dayp(2012, 1, 1)}}
	s := &Service{birthDates: src, now: func() time.Time { return hubNow }}
	p := &postgres.Post{ID: uuid.New(), AuthorID: owner, AgeRestricted: true}
	ctx := context.Background()
	cases := []struct {
		name   string
		viewer *uuid.UUID
		want   error
		status int
		code   string
	}{
		{"anonymous", nil, ErrAgeSignIn, 401, "AGE_RESTRICTED_SIGN_IN"},
		{"nil uuid", &uuid.Nil, ErrAgeSignIn, 401, "AGE_RESTRICTED_SIGN_IN"},
		{"under 18", &minor, ErrAgeRestricted, 403, "AGE_RESTRICTED"},
		{"no date of birth", &unknown, ErrAgeUnverified, 403, "AGE_UNVERIFIED"},
	}
	for _, tc := range cases {
		err := s.checkAgeGate(ctx, p, tc.viewer)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err=%v want %v", tc.name, err, tc.want)
		}
		if status, code := PostEditErrorStatus(err); status != tc.status || code != tc.code {
			t.Fatalf("%s: mapped %d %s", tc.name, status, code)
		}
	}
	if err := s.checkAgeGate(ctx, p, &adult); err != nil {
		t.Fatalf("adult refused: %v", err)
	}
	calls := atomic.LoadInt32(&src.calls)
	if err := s.checkAgeGate(ctx, p, &owner); err != nil || atomic.LoadInt32(&src.calls) != calls {
		t.Fatalf("owner refused or looked up: %v", err)
	}
	open := &postgres.Post{ID: uuid.New(), AuthorID: owner}
	if err := s.checkAgeGate(ctx, open, nil); err != nil {
		t.Fatalf("unrestricted post refused: %v", err)
	}
	// Fail closed: a lookup error and an unwired source both refuse.
	src.err = errors.New("identity down")
	if err := s.checkAgeGate(ctx, p, &adult); !errors.Is(err, ErrAgeUnverified) {
		t.Fatalf("lookup error: %v", err)
	}
	if err := (&Service{}).checkAgeGate(ctx, p, &adult); !errors.Is(err, ErrAgeUnverified) {
		t.Fatalf("unwired: %v", err)
	}
}

// A page asks for the viewer's age at most once, and only when it holds an
// age-restricted post; list reads drop what the viewer may not see.
func TestAgeAllowanceOneLookupPerPage(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	src := &fakeBirthDates{dob: map[uuid.UUID]*time.Time{viewer: dayp(2011, 5, 5)}}
	s := &Service{birthDates: src, now: func() time.Time { return hubNow }}
	ok := s.ageAllowance(context.Background(), &viewer)
	open := &postgres.Post{AuthorID: author}
	if !ok(open) || atomic.LoadInt32(&src.calls) != 0 {
		t.Fatal("an unrestricted post triggered a lookup or was dropped")
	}
	r1, r2 := &postgres.Post{AuthorID: author, AgeRestricted: true}, &postgres.Post{AuthorID: author, AgeRestricted: true}
	if ok(r1) || ok(r2) {
		t.Fatal("minor admitted to an age-restricted post")
	}
	if n := atomic.LoadInt32(&src.calls); n != 1 {
		t.Fatalf("lookups=%d want 1", n)
	}
	if !ok(&postgres.Post{AuthorID: viewer, AgeRestricted: true}) {
		t.Fatal("owner's own restricted post dropped")
	}
	anon := s.ageAllowance(context.Background(), nil)
	if anon(r1) {
		t.Fatal("anonymous admitted")
	}
}

func TestIdentityBirthDatesClient(t *testing.T) {
	adult, unknown, broken, other := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("X-Internal-Service-Key") != "k" || r.Header.Get("X-Caller-Service") != "post-service" {
			t.Errorf("headers: key=%q caller=%q", r.Header.Get("X-Internal-Service-Key"), r.Header.Get("X-Caller-Service"))
		}
		if r.Header.Get("X-User-Id") != "" {
			t.Error("a user identity header reached the service-only route")
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/internal/v1/profiles/users/"), "/identity")
		switch id {
		case adult.String():
			_, _ = w.Write([]byte(`{"data":{"user_id":"` + id + `","first_name":"A","dob":"1990-02-03","dob_source":"registration"}}`))
		case other.String():
			_, _ = w.Write([]byte(`{"data":{"user_id":"` + adult.String() + `","dob":"1990-02-03","dob_source":"profile"}}`))
		case broken.String():
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := NewIdentityBirthDates(srv.URL, "k")
	ctx := context.Background()
	dob, err := c.BirthDate(ctx, adult)
	if err != nil || dob == nil || dob.Format("2006-01-02") != "1990-02-03" {
		t.Fatalf("adult: %v %v", dob, err)
	}
	if _, err := c.BirthDate(ctx, adult); err != nil || atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("second read was not cached (hits=%d)", hits)
	}
	if dob, err := c.BirthDate(ctx, unknown); err != nil || dob != nil {
		t.Fatalf("404 must read as no date of birth: %v %v", dob, err)
	}
	if _, err := c.BirthDate(ctx, broken); err == nil {
		t.Fatal("502 read as an answer")
	}
	if _, err := c.BirthDate(ctx, other); err == nil {
		t.Fatal("a body for a different user was accepted")
	}
	if _, err := NewIdentityBirthDates("", "k").BirthDate(ctx, adult); err == nil {
		t.Fatal("unconfigured client answered")
	}
	// A date nobody stands behind is not a verified age.
	if dob, err := decodeBirthDate([]byte(`{"data":{"user_id":"`+adult.String()+`","dob":"1990-02-03","dob_source":"none"}}`), adult); err != nil || dob != nil {
		t.Fatalf("dob_source none: %v %v", dob, err)
	}
}

// GET /v1/videos/:id runs the post's read gate before any metadata read.
func TestGetVideoDetailRunsThePostReadGateFirst(t *testing.T) {
	for _, refusal := range []error{ErrPostNotVisible, ErrAgeSignIn, ErrAgeRestricted, ErrAgeUnverified} {
		store := &fakeAuthoringStore{meta: &postgres.VideoMetadata{PlaybackURL: strptr("https://cdn/x.m3u8")}}
		s := newAuthoringService(store)
		s.readGate = func(context.Context, uuid.UUID, *uuid.UUID) error { return refusal }
		vm, err := s.GetVideoDetailForCaller(context.Background(), uuid.New(), nil)
		if !errors.Is(err, refusal) || vm != nil {
			t.Fatalf("%v: vm=%v err=%v", refusal, vm, err)
		}
	}
	// Unwired: fails closed rather than skipping the gate.
	s := &Service{}
	s.authoringOwners = &fakeAuthoringStore{meta: &postgres.VideoMetadata{}}
	if _, err := s.GetVideoDetailForCaller(context.Background(), uuid.New(), nil); !errors.Is(err, ErrAuthoringStoreUnavailable) {
		t.Fatalf("unwired: %v", err)
	}
}

// The comments reads run the same gate before touching the store (a nil
// pgStore would panic if they did not).
func TestCommentReadsRunThePostReadGateFirst(t *testing.T) {
	s := &Service{readGate: func(context.Context, uuid.UUID, *uuid.UUID) error { return ErrPostNotVisible }}
	if _, _, err := s.ListCommentsSortedPG(context.Background(), uuid.New(), nil, "", 20, "top"); !errors.Is(err, ErrPostNotVisible) {
		t.Fatalf("list: %v", err)
	}
	if _, err := s.GetCommentsAroundPG(context.Background(), uuid.New(), uuid.New(), nil, 20); !errors.Is(err, ErrPostNotVisible) {
		t.Fatalf("around: %v", err)
	}
}

// ── B. hide_like_count, related post, owner-only notify_subscribers ─────────

func TestHideLikeCountIsOwnerOnly(t *testing.T) {
	owner, viewer := uuid.New(), uuid.New()
	mk := func() *PostDetail {
		return &PostDetail{Post: &postgres.Post{AuthorID: owner, HideLikeCount: true}, Counts: &scylla.Counts{Likes: 9, Comments: 2}}
	}
	d := mk()
	applyLikeCountPrivacy(d, &owner, true)
	if d.LikeCount == nil || d.LikeCount.Hidden || d.LikeCount.Value != 9 || d.Counts.Likes != 9 {
		t.Fatalf("owner: %+v %+v", d.LikeCount, d.Counts)
	}
	for _, v := range []*uuid.UUID{&viewer, nil} {
		d := mk()
		shared := d.Counts
		applyLikeCountPrivacy(d, v, true)
		b, _ := json.Marshal(struct {
			L *LikeCount     `json:"like_count,omitempty"`
			C *scylla.Counts `json:"counts"`
		}{d.LikeCount, d.Counts})
		if string(b) != `{"like_count":null,"counts":{"likes":0,"comments":2}}` {
			t.Fatalf("viewer: %s", b)
		}
		if shared.Likes != 9 {
			t.Fatal("the shared counts struct was mutated")
		}
	}
	// Lists: absent when not hidden, null when hidden.
	open := &PostDetail{Post: &postgres.Post{AuthorID: owner}, Counts: &scylla.Counts{Likes: 3}}
	applyLikeCountPrivacy(open, &viewer, false)
	if open.LikeCount != nil || open.Counts.Likes != 3 {
		t.Fatalf("list, not hidden: %+v", open)
	}
	hidden := mk()
	applyLikeCountPrivacy(hidden, &viewer, false)
	if hidden.LikeCount == nil || !hidden.LikeCount.Hidden || hidden.Counts.Likes != 0 {
		t.Fatalf("list, hidden: %+v", hidden)
	}
}

func TestHubDetailNamesTheRelatedPostOnlyToWhoCanOpenIt(t *testing.T) {
	owner, viewer, related := uuid.New(), uuid.New(), uuid.New()
	s := &Service{} // no pgStore: the related card cannot be built, i.e. "not visible"
	mk := func() *PostDetail {
		return &PostDetail{Post: &postgres.Post{AuthorID: owner, RelatedPostID: &related}, Counts: &scylla.Counts{}}
	}
	d := mk()
	s.applyHubDetail(context.Background(), d, &viewer)
	if d.Post.RelatedPostID != nil || d.RelatedPost == nil || d.RelatedPost.Card != nil || d.NotifySubscribers != nil {
		t.Fatalf("viewer: related_post_id=%v related=%+v notify=%v", d.Post.RelatedPostID, d.RelatedPost, d.NotifySubscribers)
	}
	b, _ := json.Marshal(d.RelatedPost)
	if string(b) != "null" {
		t.Fatalf("related_post=%s want null", b)
	}
	d = mk()
	s.applyHubDetail(context.Background(), d, &owner)
	if d.Post.RelatedPostID == nil || d.NotifySubscribers == nil || !*d.NotifySubscribers {
		t.Fatalf("owner: related_post_id=%v notify=%v", d.Post.RelatedPostID, d.NotifySubscribers)
	}
}

// ── F. private sharing ──────────────────────────────────────────────────────

type fakeShareStore struct {
	lists    map[uuid.UUID][]postgres.PrivateShare
	err      error
	replaced [][]uuid.UUID
}

func newFakeShareStore() *fakeShareStore {
	return &fakeShareStore{lists: map[uuid.UUID][]postgres.PrivateShare{}}
}

func (f *fakeShareStore) ListPrivateShares(_ context.Context, postID uuid.UUID) ([]postgres.PrivateShare, error) {
	return f.lists[postID], f.err
}

func (f *fakeShareStore) ReplacePrivateShares(_ context.Context, postID, _ uuid.UUID, ids []uuid.UUID) ([]postgres.PrivateShare, error) {
	f.replaced = append(f.replaced, ids)
	rows := make([]postgres.PrivateShare, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, postgres.PrivateShare{UserID: id, AddedAt: hubNow})
	}
	f.lists[postID] = rows
	return rows, nil
}

func (f *fakeShareStore) PrivateSharedPostIDs(_ context.Context, viewer uuid.UUID, postIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[uuid.UUID]bool{}
	for _, id := range postIDs {
		for _, sh := range f.lists[id] {
			if sh.UserID == viewer {
				out[id] = true
			}
		}
	}
	return out, nil
}

// The single-post gate opens a private post to the users on its list and
// to nobody else; a share lookup failure denies.
func TestViewerMayViewPrivatePostOnlyWhenShared(t *testing.T) {
	author, shared, stranger := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{author.String(): true}, &calls, nil)
	defer srv.Close()
	s := newGatedService(srv.URL)
	shares := newFakeShareStore()
	s.privateShare = shares
	post := &postgres.Post{ID: uuid.New(), AuthorID: author, Visibility: "private"}
	shares.lists[post.ID] = []postgres.PrivateShare{{UserID: shared}}
	ctx := context.Background()
	if !s.viewerMayViewPost(ctx, post, &shared) {
		t.Fatal("shared viewer denied")
	}
	if s.viewerMayViewPost(ctx, post, &stranger) || s.viewerMayViewPost(ctx, post, nil) {
		t.Fatal("a viewer not on the list read a private post")
	}
	if !s.viewerMayViewPost(ctx, post, &author) {
		t.Fatal("owner denied")
	}
	shares.err = errors.New("db down")
	if s.viewerMayViewPost(ctx, post, &shared) {
		t.Fatal("a failed share lookup allowed the read")
	}
	s.privateShare = nil
	if s.viewerMayViewPost(ctx, post, &shared) {
		t.Fatal("no share store allowed the read")
	}
}

func TestPrivateSharesOwnerRules(t *testing.T) {
	svc, store, owner, post := newHubRig(t)
	shares := newFakeShareStore()
	svc.privateShare = shares
	known := uuid.New()
	var askedAs []string
	profiles := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		askedAs = append(askedAs, r.Header.Get("X-User-Id"))
		// The owner resolves too, so the own-id refusal is the guard under test.
		_ = json.NewEncoder(w).Encode(map[string]any{known.String(): map[string]any{"username": "call.b", "display_name": "Call B"},
			owner.String(): map[string]any{"username": "owner", "display_name": "Owner"}})
	}))
	defer profiles.Close()
	svc.profileServiceURL = profiles.URL
	svc.httpClient = http.DefaultClient
	ctx := context.Background()

	// 404 for a post the caller cannot see (a private post not shared with
	// them), 403 for one they can see but do not own.
	store.posts[post.ID].Visibility = "private"
	stranger := uuid.New()
	if _, err := svc.ListPrivateShares(ctx, stranger, post.ID); !errors.Is(err, ErrPostNotVisible) {
		t.Fatalf("stranger on private: %v", err)
	}
	shares.lists[post.ID] = []postgres.PrivateShare{{UserID: known, AddedAt: hubNow}}
	if _, err := svc.ListPrivateShares(ctx, known, post.ID); !errors.Is(err, ErrNotPostAuthor) {
		t.Fatalf("shared viewer must not read the list: %v", err)
	}
	if _, err := svc.SetPrivateShares(ctx, known, post.ID, []uuid.UUID{known}); !errors.Is(err, ErrNotPostAuthor) {
		t.Fatalf("shared viewer must not write the list: %v", err)
	}
	if _, err := svc.ListPrivateShares(ctx, owner, uuid.New()); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("missing: %v", err)
	}
	// 422s: the cap counts distinct ids; the owner's own id; an unknown id.
	many := make([]uuid.UUID, MaxPrivateShares+1)
	for i := range many {
		many[i] = uuid.New()
	}
	if _, err := svc.SetPrivateShares(ctx, owner, post.ID, many); !errors.Is(err, ErrTooManyShares) {
		t.Fatalf("51: %v", err)
	}
	dupes := append([]uuid.UUID{}, many[:MaxPrivateShares]...)
	dupes = append(dupes, many[0])
	if _, err := svc.SetPrivateShares(ctx, owner, post.ID, dupes); errors.Is(err, ErrTooManyShares) {
		t.Fatal("a duplicate counted toward the cap")
	}
	if _, err := svc.SetPrivateShares(ctx, owner, post.ID, []uuid.UUID{owner}); !errors.Is(err, ErrInvalidShareUser) {
		t.Fatalf("own id: %v", err)
	}
	if _, err := svc.SetPrivateShares(ctx, owner, post.ID, []uuid.UUID{known, uuid.New()}); !errors.Is(err, ErrInvalidShareUser) {
		t.Fatalf("unknown id: %v", err)
	}
	n := len(shares.replaced)
	view, err := svc.SetPrivateShares(ctx, owner, post.ID, []uuid.UUID{known, known})
	if err != nil || len(view.Users) != 1 || view.Users[0].Username != "call.b" || len(shares.replaced) != n+1 {
		t.Fatalf("valid: %+v %v", view, err)
	}
	for _, v := range askedAs {
		if v != owner.String() {
			t.Fatalf("profiles were resolved as %q, not the owner (all: %v)", v, askedAs)
		}
	}
	if len(askedAs) == 0 {
		t.Fatal("identity-profile was never asked")
	}
	// An empty list clears without asking identity-profile.
	if view, err := svc.SetPrivateShares(ctx, owner, post.ID, []uuid.UUID{}); err != nil || len(view.Users) != 0 {
		t.Fatalf("clear: %+v %v", view, err)
	}
	// Unverifiable users: the write refuses.
	svc.profileServiceURL = ""
	if _, err := svc.SetPrivateShares(ctx, owner, post.ID, []uuid.UUID{known}); !errors.Is(err, ErrShareUsersUnknown) {
		t.Fatalf("no profile service: %v", err)
	}
}

func TestUploadDescriptionIsTheFirst200Runes(t *testing.T) {
	if got := uploadDescription(""); got != "" {
		t.Fatalf("empty: %q", got)
	}
	long := strings.Repeat("ఆ", 250)
	if got := uploadDescription(long); len([]rune(got)) != 200 {
		t.Fatalf("runes=%d", len([]rune(got)))
	}
}

// Threads: a private root opens to its share list, after the block check.
func TestCanViewThreadHonoursTheShareList(t *testing.T) {
	author, shared, blocked, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := map[string]bool{"follows": true}
		if r.URL.Query().Get("user_id") == blocked.String() {
			rel["blocked_by"] = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rel})
	}))
	defer srv.Close()
	shares := newFakeShareStore()
	s := &Service{graphServiceURL: srv.URL, httpClient: http.DefaultClient, privateShare: shares}
	root := &postgres.Post{ID: uuid.New(), AuthorID: author, Visibility: "private"}
	shares.lists[root.ID] = []postgres.PrivateShare{{UserID: shared}, {UserID: blocked}}
	ctx := context.Background()
	if !s.canViewThread(ctx, root, &shared) {
		t.Fatal("shared viewer denied the thread")
	}
	if s.canViewThread(ctx, root, &stranger) || s.canViewThread(ctx, root, nil) {
		t.Fatal("a viewer not on the list read a private thread")
	}
	if s.canViewThread(ctx, root, &blocked) {
		t.Fatal("a blocked viewer on the list read the thread")
	}
}

// The media decisions' share lookup fails closed: an error shares nothing.
func TestPrivateSharedSetFailsClosed(t *testing.T) {
	viewer, postID := uuid.New(), uuid.New()
	shares := newFakeShareStore()
	shares.lists[postID] = []postgres.PrivateShare{{UserID: viewer}}
	s := &Service{privateShare: shares}
	if !s.privateSharedSet(context.Background(), viewer, []uuid.UUID{postID})[postID] {
		t.Fatal("a shared post read as not shared")
	}
	shares.err = errors.New("db down")
	if s.privateSharedSet(context.Background(), viewer, []uuid.UUID{postID})[postID] {
		t.Fatal("a failed share lookup shared the post")
	}
	if (&Service{}).privateSharedSet(context.Background(), viewer, []uuid.UUID{postID})[postID] {
		t.Fatal("no share store shared the post")
	}
}

// The list page filter (by-author, recent, bookmarks, trending, hashtag,
// live recordings) applies both Creator Hub read rules. Text posts carry no
// media, so no store is needed.
func TestListPageFilterAppliesAgeAndHiddenLikes(t *testing.T) {
	author, adult, minor := uuid.New(), uuid.New(), uuid.New()
	s := &Service{now: func() time.Time { return hubNow },
		birthDates: &fakeBirthDates{dob: map[uuid.UUID]*time.Time{adult: dayp(1990, 1, 1), minor: dayp(2012, 1, 1)}}}
	page := func() []PostDetail {
		return []PostDetail{
			{Post: &postgres.Post{ID: uuid.New(), AuthorID: author}, Counts: &scylla.Counts{Likes: 5}},
			{Post: &postgres.Post{ID: uuid.New(), AuthorID: author, AgeRestricted: true}, Counts: &scylla.Counts{Likes: 6}},
			{Post: &postgres.Post{ID: uuid.New(), AuthorID: author, HideLikeCount: true}, Counts: &scylla.Counts{Likes: 7}},
		}
	}
	ctx := context.Background()
	for viewer, want := range map[*uuid.UUID]int{nil: 2, &minor: 2, &adult: 3, &author: 3} {
		got, err := s.attachMediaStateToDetails(ctx, page(), viewer)
		if err != nil || len(got) != want {
			t.Fatalf("viewer %v: %d rows want %d (%v)", viewer, len(got), want, err)
		}
		last := got[len(got)-1]
		hidden := viewer == nil || *viewer != author
		if hidden && (last.Counts.Likes != 0 || last.LikeCount == nil || !last.LikeCount.Hidden) {
			t.Fatalf("viewer %v: hidden like count leaked: %+v %+v", viewer, last.Counts, last.LikeCount)
		}
		if !hidden && (last.Counts.Likes != 7 || last.LikeCount != nil) {
			t.Fatalf("owner lost their like count: %+v %+v", last.Counts, last.LikeCount)
		}
	}
}
