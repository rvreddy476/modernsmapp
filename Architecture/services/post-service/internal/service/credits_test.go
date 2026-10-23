package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Video credits (2026-10-23): validation, the link decision, the read shape
// and the guards on PATCH, consent and "Appears in", without a database.

func idp() *uuid.UUID { id := uuid.New(); return &id }

func TestValidateCredits(t *testing.T) {
	ok, err := ValidateCredits([]postgres.CreditInput{{Name: "  Asha Rao ", Role: " Director "}})
	if err != nil || ok[0].Name != "Asha Rao" || ok[0].Role != "Director" {
		t.Fatalf("trim: %+v %v", ok, err)
	}
	if got, err := ValidateCredits(nil); got != nil || err != nil {
		t.Fatal("nil list must stay nil (omitted = unchanged)")
	}
	many := make([]postgres.CreditInput, MaxCredits+1)
	for i := range many {
		many[i] = postgres.CreditInput{Name: "n", Role: "r"}
	}
	if _, err := ValidateCredits(many); !errors.Is(err, ErrTooManyCredits) {
		t.Fatalf("31 credits: %v", err)
	}
	if _, err := ValidateCredits(many[:MaxCredits]); err != nil {
		t.Fatalf("30 credits refused: %v", err)
	}
	nilID := uuid.Nil
	for name, c := range map[string]postgres.CreditInput{
		"empty name":      {Name: "  ", Role: "Actor"},
		"empty role":      {Name: "Asha", Role: ""},
		"long name":       {Name: strings.Repeat("a", MaxCreditNameRunes+1), Role: "Actor"},
		"long role":       {Name: "Asha", Role: strings.Repeat("r", MaxCreditRoleRunes+1)},
		"control char":    {Name: "As\x07ha", Role: "Actor"},
		"newline in role": {Name: "Asha", Role: "Act\nor"},
		"bidi override":   {Name: "Asha‮", Role: "Actor"},
		"nil user":        {Name: "Asha", Role: "Actor", UserID: &nilID},
	} {
		if _, err := ValidateCredits([]postgres.CreditInput{c}); !errors.Is(err, ErrInvalidCredit) {
			t.Errorf("%s: %v, want ErrInvalidCredit", name, err)
		}
	}
	// 80 runes of a multi-byte script is fine: the limit is runes, not bytes.
	if _, err := ValidateCredits([]postgres.CreditInput{{Name: strings.Repeat("అ", MaxCreditNameRunes), Role: "నటుడు"}}); err != nil {
		t.Fatalf("80 Telugu runes refused: %v", err)
	}
}

func TestNormalizeGenresYearHouse(t *testing.T) {
	g, err := NormalizeGenres([]string{" Drama", "thriller", "drama"})
	if err != nil || len(g) != 2 || g[0] != "drama" {
		t.Fatalf("genres = %v %v", g, err)
	}
	if _, err := NormalizeGenres([]string{"drama", "zombie-western"}); !errors.Is(err, ErrInvalidGenre) {
		t.Fatalf("unknown genre: %v", err)
	}
	if _, err := NormalizeGenres([]string{"drama", "comedy", "action", "horror"}); !errors.Is(err, ErrInvalidGenre) {
		t.Fatalf("4 genres: %v", err)
	}
	if g, err := NormalizeGenres([]string{}); err != nil || g == nil || len(g) != 0 {
		t.Fatal("[] must stay [] (clears on PATCH)")
	}
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	for y, want := range map[int]bool{1899: false, 1900: true, 2027: true, 2028: false} {
		if got := ValidateReleaseYear(y, now) == nil; got != want {
			t.Errorf("year %d ok=%v, want %v", y, got, want)
		}
	}
	if h, err := NormalizeProductionHouse("  "); h != nil || err != nil {
		t.Fatal("blank house is none")
	}
	if _, err := NormalizeProductionHouse(strings.Repeat("h", MaxProductionHouseRunes+1)); !errors.Is(err, ErrInvalidProductionHouse) {
		t.Fatal("101-character house accepted")
	}
	if len(VideoGenres()) != 20 {
		t.Fatalf("genre list has %d entries", len(VideoGenres()))
	}
}

func TestValidatedVideoCredits_LongVideoOnly(t *testing.T) {
	now := time.Now()
	year := 2024
	f := VideoCreditFields{Credits: []postgres.CreditInput{{Name: "A", Role: "Actor"}}}
	for _, kind := range []string{"flick", "post", "poll", "voice", ""} {
		if _, _, err := validatedVideoCredits(kind, f, now); !errors.Is(err, ErrCreditsNotAllowed) {
			t.Errorf("credits on %q: %v, want CREDITS_NOT_ALLOWED", kind, err)
		}
		if _, _, err := validatedVideoCredits(kind, VideoCreditFields{ReleaseYear: &year}, now); !errors.Is(err, ErrCreditsNotAllowed) {
			t.Errorf("year on %q accepted", kind)
		}
		if _, _, err := validatedVideoCredits(kind, VideoCreditFields{Credits: []postgres.CreditInput{}}, now); err != nil {
			t.Errorf("an empty list on %q refused: %v", kind, err)
		}
	}
	out, house, err := validatedVideoCredits("long_video", VideoCreditFields{Credits: f.Credits, Genres: []string{"drama"}, ReleaseYear: &year, ProductionHouse: " Blue Hour "}, now)
	if err != nil || len(out.Credits) != 1 || house == nil || *house != "Blue Hour" {
		t.Fatalf("long video: %+v %v %v", out, house, err)
	}
	// A malformed value is refused for what it is, even on a flick.
	if _, _, err := validatedVideoCredits("flick", VideoCreditFields{Genres: []string{"nope"}}, now); !errors.Is(err, ErrInvalidGenre) {
		t.Fatalf("bad genre on a flick: %v", err)
	}
}

type fakeLinker struct {
	linkable map[uuid.UUID]bool
	err      error
	asked    []uuid.UUID
}

func (f *fakeLinker) Linkable(_ context.Context, _ uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, string, error) {
	f.asked = append(f.asked, ids...)
	if f.err != nil {
		return nil, "", f.err
	}
	return f.linkable, "Uploader", nil
}

func TestResolveCreditCandidates_BlockedOrUnknownIsSilentlyText(t *testing.T) {
	uploader := uuid.New()
	ok, blocked, unknown := idp(), idp(), idp()
	l := &fakeLinker{linkable: map[uuid.UUID]bool{*ok: true, *blocked: false}}
	s := &Service{creditLinks: l}
	w := s.resolveCreditCandidates(context.Background(), uploader, []postgres.CreditInput{
		{Name: "Me", Role: "Host", UserID: &uploader},
		{Name: "Ok", Role: "Actor", UserID: ok},
		{Name: "Blocked", Role: "Actor", UserID: blocked},
		{Name: "Unknown", Role: "Actor", UserID: unknown},
		{Name: "Text", Role: "Writer"},
	})
	c := w.Candidates
	if !c[0].Self || c[0].UserID == nil {
		t.Fatalf("self-credit = %+v", c[0])
	}
	if c[1].UserID == nil || *c[1].UserID != *ok {
		t.Fatalf("linkable tag dropped: %+v", c[1])
	}
	if c[2].UserID != nil || c[3].UserID != nil || c[4].UserID != nil {
		t.Fatalf("blocked / unknown / text kept a link: %+v %+v %+v", c[2], c[3], c[4])
	}
	if c[2].Name != "Blocked" {
		t.Fatal("the credit itself must stay, as plain text")
	}
	for _, id := range l.asked {
		if id == uploader {
			t.Fatal("the uploader's own id was sent to the block check")
		}
	}
	if w.UploaderName != "Uploader" {
		t.Fatalf("uploader name = %q", w.UploaderName)
	}

	// Checks that cannot be made link nobody.
	down := &Service{creditLinks: &fakeLinker{err: errors.New("graph down")}}
	w = down.resolveCreditCandidates(context.Background(), uploader, []postgres.CreditInput{{Name: "Ok", Role: "Actor", UserID: ok}})
	if w.Candidates[0].UserID != nil {
		t.Fatal("an unchecked tag was linked")
	}
	none := &Service{}
	if w := none.resolveCreditCandidates(context.Background(), uploader, []postgres.CreditInput{{Name: "Ok", Role: "Actor", UserID: ok}}); w.Candidates[0].UserID != nil {
		t.Fatal("no linker configured, yet a tag was linked")
	}
}

func TestBuildCreditViews_PendingIsHiddenFromViewers(t *testing.T) {
	pendingUser, acceptedUser, hiddenUser, declinedUser := idp(), idp(), idp(), idp()
	credits := []postgres.PostCredit{
		{ID: uuid.New(), Name: "Pat", Role: "Actor", UserID: pendingUser, Status: postgres.CreditPending},
		{ID: uuid.New(), Name: "Asha", Role: "Director", UserID: acceptedUser, Status: postgres.CreditAccepted},
		{ID: uuid.New(), Name: "Dee", Role: "Writer", UserID: declinedUser, Status: postgres.CreditDeclined},
		{ID: uuid.New(), Name: "Hid", Role: "Music", UserID: hiddenUser, Status: postgres.CreditAccepted},
		{ID: uuid.New(), Name: "Text", Role: "Editor", Status: postgres.CreditUnlinked},
	}
	// Every tagged account resolves, so only the status can hide one.
	profiles := map[uuid.UUID]commentProfile{*acceptedUser: {Username: "asha", DisplayName: "Asha Rao"},
		*pendingUser: {Username: "pat"}, *declinedUser: {Username: "dee"}}

	viewer := BuildCreditViews(credits, false, profiles)
	for i, v := range viewer {
		if v.Status != "" || v.UserID != nil {
			t.Fatalf("viewer row %d carries owner fields: %+v", i, v)
		}
	}
	if viewer[0].User != nil || viewer[2].User != nil {
		t.Fatal("a pending or declined tag showed its account to a viewer")
	}
	if viewer[1].User == nil || viewer[1].User.Username != "asha" {
		t.Fatalf("accepted credit = %+v, want the account", viewer[1])
	}
	if viewer[3].User != nil {
		t.Fatal("an accepted account the viewer cannot resolve must read as plain text")
	}

	owner := BuildCreditViews(credits, true, profiles)
	if owner[0].Status != postgres.CreditPending || owner[0].UserID == nil || *owner[0].UserID != *pendingUser || owner[0].User != nil {
		t.Fatalf("owner pending row = %+v", owner[0])
	}
	if owner[2].Status != postgres.CreditDeclined || owner[2].UserID == nil {
		t.Fatalf("owner declined row = %+v (the edit form needs user_id)", owner[2])
	}
	if owner[4].Status != postgres.CreditUnlinked || owner[4].UserID != nil {
		t.Fatalf("owner text row = %+v", owner[4])
	}
}

// PATCH: owner only, long video only, and the clears.
func TestUpdatePost_CreditGuards(t *testing.T) {
	store := newFakePostEditStore()
	owner, stranger := uuid.New(), uuid.New()
	long, flick := uuid.New(), uuid.New()
	store.posts[long] = &postgres.Post{ID: long, AuthorID: owner, ContentType: "long_video", Title: "T"}
	store.posts[flick] = &postgres.Post{ID: flick, AuthorID: owner, ContentType: "flick"}
	s := &Service{postEdits: store}
	credits := []postgres.CreditInput{{Name: "A", Role: "Actor"}}

	if _, err := s.UpdatePost(context.Background(), stranger, long, PostEditInput{Credits: &credits}); !errors.Is(err, ErrNotPostAuthor) {
		t.Fatalf("non-owner edit: %v", err)
	}
	if len(store.updates) != 0 {
		t.Fatal("a refused edit wrote")
	}
	if _, err := s.UpdatePost(context.Background(), owner, flick, PostEditInput{Credits: &credits}); !errors.Is(err, ErrCreditsNotAllowed) {
		t.Fatalf("credits on a flick: %v", err)
	}
	g := []string{"drama"}
	if _, err := s.UpdatePost(context.Background(), owner, flick, PostEditInput{Genres: &g}); !errors.Is(err, ErrCreditsNotAllowed) {
		t.Fatalf("genres on a flick: %v", err)
	}
	empty := []postgres.CreditInput{}
	if _, err := s.UpdatePost(context.Background(), owner, flick, PostEditInput{Credits: &empty, ClearReleaseYear: true}); err != nil {
		t.Fatalf("clearing on a flick refused: %v", err)
	}

	year, house := 2024, ""
	if _, err := s.UpdatePost(context.Background(), owner, long, PostEditInput{Credits: &credits, Genres: &g, ReleaseYear: &year}); err != nil {
		t.Fatal(err)
	}
	p := store.updates[len(store.updates)-1]
	if p.Credits == nil || len(p.Credits.Candidates) != 1 || p.Genres == nil || p.ReleaseYear == nil || *p.ReleaseYear != 2024 {
		t.Fatalf("long-video patch = %+v", p)
	}
	if _, err := s.UpdatePost(context.Background(), owner, long, PostEditInput{ClearReleaseYear: true, ReleaseYear: &year, ProductionHouse: &house}); err != nil {
		t.Fatal(err)
	}
	p = store.updates[len(store.updates)-1]
	if !p.ClearReleaseYear || p.ReleaseYear != nil || !p.ClearProductionHouse || p.Credits != nil {
		t.Fatalf("clear patch = %+v (null year / \"\" house clear; omitted credits unchanged)", p)
	}
	bad := 1800
	if _, err := s.UpdatePost(context.Background(), owner, long, PostEditInput{ReleaseYear: &bad}); !errors.Is(err, ErrInvalidReleaseYear) {
		t.Fatalf("year 1800: %v", err)
	}
}

type fakeCreditStore struct {
	credits     map[uuid.UUID][]postgres.PostCredit
	answers     map[uuid.UUID]uuid.UUID // credit -> tagged user
	appearances []uuid.UUID
}

func (f *fakeCreditStore) ListPostCredits(_ context.Context, postID uuid.UUID) ([]postgres.PostCredit, error) {
	return f.credits[postID], nil
}
func (f *fakeCreditStore) RespondToCredit(_ context.Context, creditID, callerID uuid.UUID, action string) (string, error) {
	if f.answers[creditID] != callerID {
		return "", postgres.ErrCreditNotFound
	}
	to, _, _ := postgres.NextCreditStatus(postgres.CreditPending, action)
	return to, nil
}
func (f *fakeCreditStore) ListCreditRequests(context.Context, uuid.UUID, *postgres.CreditCursor, int) ([]postgres.CreditRequestRow, *postgres.CreditCursor, error) {
	return nil, nil, nil
}
func (f *fakeCreditStore) ListAppearancePostIDs(context.Context, uuid.UUID, *postgres.CreditCursor, int) ([]uuid.UUID, *postgres.CreditCursor, error) {
	return f.appearances, nil, nil
}

func TestRespondToCredit_OnlyTheTaggedPerson(t *testing.T) {
	credit, tagged, other := uuid.New(), uuid.New(), uuid.New()
	s := &Service{credits: &fakeCreditStore{answers: map[uuid.UUID]uuid.UUID{credit: tagged}}}
	if a, err := s.RespondToCredit(context.Background(), tagged, credit, postgres.CreditActionAccept); err != nil || a.Status != "accepted" {
		t.Fatalf("tagged accept = %+v %v", a, err)
	}
	for _, who := range []uuid.UUID{other, uuid.Nil} {
		if _, err := s.RespondToCredit(context.Background(), who, credit, postgres.CreditActionAccept); !errors.Is(err, ErrCreditNotFound) {
			t.Fatalf("%s answered someone else's credit: %v", who, err)
		}
	}
}

func TestListAppearances_Gates(t *testing.T) {
	user, viewer := uuid.New(), uuid.New()
	ok, private, flick, members := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tier := uuid.New()
	posts := map[uuid.UUID]*PostDetail{
		ok:      {Post: &postgres.Post{ID: ok, ContentType: "long_video", Visibility: "public", ReviewStatus: "approved"}},
		private: {Post: &postgres.Post{ID: private, ContentType: "long_video", Visibility: "private", ReviewStatus: "approved"}},
		flick:   {Post: &postgres.Post{ID: flick, ContentType: "flick", Visibility: "public", ReviewStatus: "approved"}},
		members: {Post: &postgres.Post{ID: members, ContentType: "long_video", Visibility: "public", ReviewStatus: "approved", TierRequiredID: &tier}},
	}
	gone := uuid.New() // the batch gate dropped it (blocked uploader, ...)
	visible := true
	s := &Service{
		credits: &fakeCreditStore{appearances: []uuid.UUID{ok, private, gone, flick, members}},
		appearancePosts: func(_ context.Context, ids []uuid.UUID, _ *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {
			return posts, nil
		},
		appearanceProfileGate: func(context.Context, *uuid.UUID, uuid.UUID) bool { return visible },
	}
	got, _, err := s.ListAppearances(context.Background(), user, &viewer, "", 20)
	if err != nil || len(got) != 1 || got[0].ID != ok {
		t.Fatalf("appearances = %v %v, want only the public long video", ids(got), err)
	}
	visible = false
	if got, _, _ := s.ListAppearances(context.Background(), user, &viewer, "", 20); len(got) != 0 {
		t.Fatal("a profile the viewer may not see listed appearances")
	}
	if got, _, _ := s.ListAppearances(context.Background(), user, &user, "", 20); len(got) != 1 {
		t.Fatal("the person themself must see their own appearances")
	}
	if _, _, err := s.ListAppearances(context.Background(), user, nil, "garbage!", 20); !errors.Is(err, postgres.ErrInvalidCreditCursor) {
		t.Fatalf("bad cursor: %v", err)
	}
}

func ids(ds []PostDetail) []uuid.UUID {
	out := make([]uuid.UUID, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}

// The production link check: identity-profile (as the uploader) and the
// either-direction block lookup must both answer; an account that does not
// resolve or has a block either way is not linkable, and any failure is an
// error (the caller then links nobody).
func TestDefaultCreditLinker(t *testing.T) {
	uploader, ok, blocked, unknown := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	profiles := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-Id") != uploader.String() {
			t.Errorf("profiles asked as %q, want the uploader", r.Header.Get("X-User-Id"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			uploader.String(): map[string]any{"username": "kiran", "display_name": "Kiran"},
			ok.String():       map[string]any{"username": "ok"},
			blocked.String():  map[string]any{"username": "blocked"},
		})
	}))
	defer profiles.Close()
	graphStatus := http.StatusOK
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != blockedAnyPath || r.Header.Get("X-Internal-Service-Key") != "k" {
			t.Errorf("graph call %s key=%q", r.URL.Path, r.Header.Get("X-Internal-Service-Key"))
		}
		w.WriteHeader(graphStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{"blocked_user_ids": []string{blocked.String()}})
	}))
	defer graph.Close()
	s := &Service{profileServiceURL: profiles.URL, graphServiceURL: graph.URL, internalServiceKey: "k", httpClient: &http.Client{Timeout: 2 * time.Second}}
	l := defaultCreditLinker{s: s}
	got, name, err := l.Linkable(context.Background(), uploader, []uuid.UUID{ok, blocked, unknown})
	if err != nil || !got[ok] || got[blocked] || got[unknown] || name != "Kiran" {
		t.Fatalf("linkable = %v %q %v", got, name, err)
	}
	graphStatus = http.StatusServiceUnavailable
	if _, _, err := l.Linkable(context.Background(), uploader, []uuid.UUID{ok}); err == nil {
		t.Fatal("a graph failure must be an error, never 'not blocked'")
	}
	if _, _, err := (defaultCreditLinker{s: &Service{profileServiceURL: profiles.URL}}).Linkable(context.Background(), uploader, []uuid.UUID{ok}); err == nil {
		t.Fatal("no graph configured must be an error")
	}
}

// Reel-draft patches hold the Credits step under the create rules; a draft
// that is (or is being made) a flick refuses credit data, and the publish
// carries credits only for a long video.
func TestDraftCredits(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	creds := []postgres.CreditInput{{Name: " Asha ", Role: "Director"}}
	updates := map[string]interface{}{}
	in := &UpdateDraftInput{Credits: &creds, Genres: &[]string{"Drama"}, ReleaseYear: json.RawMessage(`2024`), ProductionHouse: json.RawMessage(`null`)}
	if err := draftCreditUpdates(in, "long_video", now, updates); err != nil {
		t.Fatal(err)
	}
	if updates["release_year"] != 2024 || updates["production_house"] != nil || len(updates["genres"].([]string)) != 1 {
		t.Fatalf("updates = %v", updates)
	}
	flick := "flick"
	if err := draftCreditUpdates(&UpdateDraftInput{Credits: &creds}, "flick", now, map[string]interface{}{}); !errors.Is(err, ErrCreditsNotAllowed) {
		t.Fatalf("credits on a flick draft: %v", err)
	}
	if err := draftCreditUpdates(&UpdateDraftInput{Credits: &creds, ContentType: &flick}, "", now, map[string]interface{}{}); !errors.Is(err, ErrCreditsNotAllowed) {
		t.Fatalf("credits with content_type flick: %v", err)
	}
	if err := draftCreditUpdates(&UpdateDraftInput{ReleaseYear: json.RawMessage(`"x"`)}, "", now, map[string]interface{}{}); !errors.Is(err, ErrInvalidReleaseYear) {
		t.Fatalf("bad year: %v", err)
	}
	empty := []postgres.CreditInput{}
	if err := draftCreditUpdates(&UpdateDraftInput{Credits: &empty}, "flick", now, map[string]interface{}{}); err != nil {
		t.Fatalf("clearing on a flick draft refused: %v", err)
	}

	d := typicalDraft()
	d.Credits = creds
	if got := reelDraftPostInput(&d, d.AuthorID, "flick"); len(got.VideoCredits.Credits) != 0 {
		t.Fatal("a flick draft published credits")
	}
	if got := reelDraftPostInput(&d, d.AuthorID, "long_video"); len(got.VideoCredits.Credits) != 1 {
		t.Fatal("a long-video draft lost its credits")
	}
}
