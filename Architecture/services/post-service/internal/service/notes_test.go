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

	"github.com/atpost/post-service/internal/actionlinks"
	"github.com/atpost/post-service/internal/notetest"
	"github.com/atpost/post-service/internal/standing"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/postclassify"
	"github.com/google/uuid"
)

// Echo: one test per rule in the contract's rules table
// (docs/designs/echo-contract.md), driven through the service's public
// paths over the in-memory store (internal/notetest).

var noteNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

type noteAccounts struct {
	created map[uuid.UUID]time.Time
	err     error
}

func (f *noteAccounts) Account(_ context.Context, id uuid.UUID) (actionlinks.AccountInfo, error) {
	if f.err != nil {
		return actionlinks.AccountInfo{}, f.err
	}
	at, ok := f.created[id]
	if !ok {
		return actionlinks.AccountInfo{}, nil
	}
	return actionlinks.AccountInfo{Found: true, Active: true, CreatedAt: at}, nil
}

type noteStanding struct{ suspended map[uuid.UUID]bool }

func (f *noteStanding) Check(_ context.Context, id uuid.UUID) (*standing.Result, error) {
	if f.suspended[id] {
		return nil, &standing.DeniedError{Standing: "suspended"}
	}
	return &standing.Result{Standing: "ok"}, nil
}

type noteRig struct {
	svc       *Service
	store     *notetest.Store
	premium   *notetest.Premium
	accounts  *noteAccounts
	relations *notetest.Relations
	limiter   *notetest.Limiter
	standing  *noteStanding
	deleted   []uuid.UUID
	alice     uuid.UUID // premium, old account
	bob       uuid.UUID // not premium, old account
	carol     uuid.UUID // not premium, 6 days old
}

func newNoteRig(t *testing.T) *noteRig {
	t.Helper()
	r := &noteRig{
		store: notetest.New(), limiter: notetest.NewLimiter(),
		alice: uuid.New(), bob: uuid.New(), carol: uuid.New(),
		relations: &notetest.Relations{Follows: map[uuid.UUID]map[uuid.UUID]bool{}, Usernames: map[string]uuid.UUID{}},
		standing:  &noteStanding{suspended: map[uuid.UUID]bool{}},
	}
	r.premium = &notetest.Premium{Users: map[uuid.UUID]bool{r.alice: true}}
	r.accounts = &noteAccounts{created: map[uuid.UUID]time.Time{
		r.alice: noteNow.AddDate(-1, 0, 0), r.bob: noteNow.AddDate(0, -2, 0), r.carol: noteNow.Add(-6 * 24 * time.Hour),
	}}
	r.svc = NewForHandlerTests(HandlerTestDeps{
		Now: func() time.Time { return noteNow },
		BatchPosts: func(_ context.Context, ids []uuid.UUID, viewer *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {
			out := map[uuid.UUID]*PostDetail{}
			for _, id := range ids {
				if p, ok := r.store.Visible(id, viewer); ok {
					out[id] = &PostDetail{Post: p, Counts: &scylla.Counts{Likes: 3}, RepostCount: 1}
				}
			}
			return out, nil
		},
		BulkDelete: func(_ context.Context, id, _ uuid.UUID) error {
			r.deleted = append(r.deleted, id)
			r.store.Delete(id)
			return nil
		},
	}).WithNoteTestDeps(NoteTestDeps{
		Config:     NoteConfig{Enabled: true, Premium: r.premium, Accounts: r.accounts},
		Store:      r.store,
		Limiter:    r.limiter,
		Relations:  r.relations,
		Standing:   r.standing,
		EmitEvents: true,
	})
	return r
}

// note stores a live top-level note by author.
func (r *noteRig) note(author uuid.UUID, mod func(*postgres.Note)) *postgres.Note {
	n := postgres.Note{Post: postgres.Post{ID: uuid.New(), AuthorID: author, Text: "hello", CreatedAt: noteNow.Add(-time.Hour)}}
	if mod != nil {
		mod(&n)
	}
	return r.store.Put(n)
}

func (r *noteRig) create(author uuid.UUID, mod func(*CreateNoteInput)) (*NoteView, error) {
	in := &CreateNoteInput{AuthorID: author, Text: "a note", IdempotencyKey: uuid.NewString()}
	if mod != nil {
		mod(in)
	}
	return r.svc.CreateNote(context.Background(), in)
}

func (r *noteRig) image(owner uuid.UUID, mime, kind string) uuid.UUID {
	id := uuid.New()
	r.store.Media[id] = postgres.NoteMediaRow{UploaderID: owner, Kind: kind, MimeType: mime,
		ProcessingStatus: "ready", ModerationStatus: "passed"}
	return id
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got %v, want %v", err, target)
	}
}

// ── Text ────────────────────────────────────────────────────────────────

func TestNoteRule_Text(t *testing.T) {
	r := newNoteRig(t)
	wantErr(t, ValidateNoteText("   ", 0), ErrEmptyNote)
	wantErr(t, ValidateNoteText(strings.Repeat("न", NoteMaxRunes+1), 0), ErrNoteTooLong)
	if err := ValidateNoteText(strings.Repeat("न", NoteMaxRunes), 0); err != nil {
		t.Fatalf("500 runes must pass (code points, not bytes): %v", err)
	}
	if err := ValidateNoteText("  "+strings.Repeat("a", NoteMaxRunes)+"  ", 0); err != nil {
		t.Fatalf("the limit is counted after trim: %v", err)
	}
	if err := ValidateNoteText("", 1); err != nil {
		t.Fatalf("a note with an image may have empty text: %v", err)
	}
	_, err := r.create(r.alice, func(in *CreateNoteInput) { in.Text = strings.Repeat("x", 501) })
	wantErr(t, err, ErrNoteTooLong)
	_, err = r.create(r.alice, func(in *CreateNoteInput) { in.Text = " \n" })
	wantErr(t, err, ErrEmptyNote)
	if len(r.store.Writes) != 0 {
		t.Fatal("a refused note was written")
	}
}

// ── Media ───────────────────────────────────────────────────────────────

func TestNoteRule_Media(t *testing.T) {
	r := newNoteRig(t)
	img := func() uuid.UUID { return r.image(r.alice, "image/jpeg", "image") }
	gif := r.image(r.alice, "image/gif", "image")
	video := r.image(r.alice, "video/mp4", "video")
	audio := r.image(r.alice, "audio/mpeg", "audio")
	foreign := r.image(r.bob, "image/png", "image")

	cases := []struct {
		name  string
		media []uuid.UUID
		want  error
	}{
		{"five images", []uuid.UUID{img(), img(), img(), img(), img()}, ErrTooManyNoteMedia},
		{"a video", []uuid.UUID{video}, ErrNoteMediaNotAllowed},
		{"audio", []uuid.UUID{audio}, ErrNoteMediaNotAllowed},
		{"a GIF with an image", []uuid.UUID{gif, img()}, ErrTooManyNoteMedia},
		{"someone else's image", []uuid.UUID{foreign}, ErrMediaNotOwned},
		{"a missing asset", []uuid.UUID{uuid.New()}, ErrMediaNotFound},
	}
	for _, c := range cases {
		_, err := r.create(r.alice, func(in *CreateNoteInput) { in.MediaIDs = c.media; in.Text = "" })
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	for name, media := range map[string][]uuid.UUID{
		"four images": {img(), img(), img(), img()},
		"one GIF":     {gif},
	} {
		v, err := r.create(r.alice, func(in *CreateNoteInput) { in.MediaIDs = media; in.Text = "" })
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(v.Media) != len(media) || v.Post.PostType != "image" {
			t.Fatalf("%s: media %+v post_type %q", name, v.Media, v.Post.PostType)
		}
	}
}

// ── Premium ─────────────────────────────────────────────────────────────

func TestNoteRule_PremiumGatesNotesThreadsAndQuotes(t *testing.T) {
	r := newNoteRig(t)
	target := r.note(r.alice, nil)
	_, err := r.create(r.bob, nil)
	wantErr(t, err, ErrPremiumRequired)
	_, err = r.create(r.bob, func(in *CreateNoteInput) { in.QuoteOfID = &target.Post.ID })
	wantErr(t, err, ErrPremiumRequired)
	_, err = r.svc.CreateNoteThread(context.Background(), &CreateNoteThreadInput{AuthorID: r.bob,
		Entries: []ThreadEntryInput{{Text: "one"}, {Text: "two"}}})
	wantErr(t, err, ErrPremiumRequired)
	if _, err := r.create(r.alice, nil); err != nil {
		t.Fatalf("premium author: %v", err)
	}
	// A reply does not need Premium.
	if _, err := r.create(r.bob, func(in *CreateNoteInput) { in.ReplyToID = &target.Post.ID }); err != nil {
		t.Fatalf("reply by a non-premium account: %v", err)
	}
}

func TestNoteRule_PremiumFailsClosed(t *testing.T) {
	r := newNoteRig(t)
	r.premium.Err = errors.New("auth down")
	_, err := r.create(r.alice, nil)
	wantErr(t, err, ErrPremiumCheckUnavailable)
	r.svc.noteCfg.Premium = nil
	_, err = r.create(r.alice, nil)
	wantErr(t, err, ErrPremiumCheckUnavailable)
	if len(r.store.Writes) != 0 {
		t.Fatal("a note was written without a premium answer")
	}
}

// ── Reply: standing, account age, reply_policy, blocks ─────────────────

func TestNoteRule_ReplyNeedsStanding(t *testing.T) {
	r := newNoteRig(t)
	target := r.note(r.alice, nil)
	r.standing.suspended[r.bob] = true
	_, err := r.create(r.bob, func(in *CreateNoteInput) { in.ReplyToID = &target.Post.ID })
	wantErr(t, err, ErrAuthorSuspended)
}

func TestNoteRule_ReplyNeedsAWeekOldAccount(t *testing.T) {
	r := newNoteRig(t)
	target := r.note(r.alice, nil)
	_, err := r.create(r.carol, func(in *CreateNoteInput) { in.ReplyToID = &target.Post.ID })
	wantErr(t, err, ErrAccountTooNew)

	r.accounts.created[r.carol] = noteNow.Add(-NoteMinAccountAge)
	if _, err := r.create(r.carol, func(in *CreateNoteInput) { in.ReplyToID = &target.Post.ID }); err != nil {
		t.Fatalf("exactly 7 days old: %v", err)
	}
	r.svc.noteCfg.Accounts = nil
	_, err = r.create(r.bob, func(in *CreateNoteInput) { in.ReplyToID = &target.Post.ID })
	wantErr(t, err, ErrAccountCheckUnavailable)
	r.svc.noteCfg.Accounts = r.accounts
	r.accounts.err = errors.New("identity down")
	_, err = r.create(r.bob, func(in *CreateNoteInput) { in.ReplyToID = &target.Post.ID })
	wantErr(t, err, ErrAccountCheckUnavailable)
	if NoteMinAccountAge != 7*24*time.Hour {
		t.Fatalf("NoteMinAccountAge = %v", NoteMinAccountAge)
	}
}

func TestNoteRule_ReplyPolicy(t *testing.T) {
	r := newNoteRig(t)
	following := r.note(r.alice, func(n *postgres.Note) { n.ReplyPolicy = ReplyPolicyFollowing })
	mentioned := r.note(r.alice, func(n *postgres.Note) {
		n.ReplyPolicy = ReplyPolicyMentioned
		n.Post.MentionUsernames = []string{"carolyn"}
	})
	reply := func(by uuid.UUID, to *postgres.Note) error {
		_, err := r.create(by, func(in *CreateNoteInput) { in.ReplyToID = &to.Post.ID })
		return err
	}

	wantErr(t, reply(r.bob, following), ErrReplyNotAllowed)
	r.relations.Follows[r.alice] = map[uuid.UUID]bool{r.bob: true}
	if err := reply(r.bob, following); err != nil {
		t.Fatalf("the author follows the replier: %v", err)
	}
	// The reverse edge (the replier follows the author) is not enough.
	r.relations.Follows = map[uuid.UUID]map[uuid.UUID]bool{r.bob: {r.alice: true}}
	wantErr(t, reply(r.bob, following), ErrReplyNotAllowed)

	wantErr(t, reply(r.bob, mentioned), ErrReplyNotAllowed)
	r.relations.Usernames["carolyn"] = r.bob
	if err := reply(r.bob, mentioned); err != nil {
		t.Fatalf("the replier is mentioned: %v", err)
	}

	// The note's author may always reply.
	r.relations.Follows = map[uuid.UUID]map[uuid.UUID]bool{}
	if err := reply(r.alice, following); err != nil {
		t.Fatalf("the author replying to their own note: %v", err)
	}
	// An unanswerable graph denies.
	r.relations.Err = errors.New("graph down")
	wantErr(t, reply(r.bob, mentioned), ErrReplyNotAllowed)
}

func TestNoteRule_BlocksDenyReplies(t *testing.T) {
	r := newNoteRig(t)
	// The target's author blocked the replier: the target reads as absent.
	hidden := r.note(r.alice, nil)
	r.store.Hidden[hidden.Post.ID] = true
	_, err := r.create(r.bob, func(in *CreateNoteInput) { in.ReplyToID = &hidden.Post.ID })
	wantErr(t, err, ErrNoteNotFound)
	_, err = r.create(r.alice, func(in *CreateNoteInput) { in.QuoteOfID = &hidden.Post.ID })
	if err != nil {
		t.Fatalf("the author may quote their own hidden note: %v", err)
	}

	// The conversation's top author blocked the replier, deeper in the
	// thread: graph says no.
	dave := uuid.New()
	r.accounts.created[dave] = noteNow.AddDate(-1, 0, 0)
	top := r.note(r.alice, nil)
	mid := r.note(dave, func(n *postgres.Note) { n.ReplyToID = &top.Post.ID; n.ConversationID = &top.Post.ID })
	graph := denyGraph(t, map[[2]uuid.UUID]bool{{r.bob, r.alice}: true})
	r.svc.graphServiceURL = graph.URL
	_, err = r.create(r.bob, func(in *CreateNoteInput) { in.ReplyToID = &mid.Post.ID })
	wantErr(t, err, ErrReplyNotAllowed)
	r.svc.graphServiceURL = ""
	if _, err := r.create(r.bob, func(in *CreateNoteInput) { in.ReplyToID = &mid.Post.ID }); err != nil {
		t.Fatalf("no block: %v", err)
	}
}

// denyGraph is graph-service's /can answering false for (viewer, target)
// pairs in deny and true otherwise.
func denyGraph(t *testing.T, deny map[[2]uuid.UUID]bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Viewer  string   `json:"viewer_id"`
			Targets []string `json:"target_ids"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		v := uuid.MustParse(body.Viewer)
		out := map[string]bool{}
		for _, ts := range body.Targets {
			out[ts] = !deny[[2]uuid.UUID{v, uuid.MustParse(ts)}]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ── Reply / quote target ────────────────────────────────────────────────

func TestNoteRule_TargetMustBeALiveVisibleNote(t *testing.T) {
	r := newNoteRig(t)
	gone := r.note(r.alice, nil)
	r.store.Delete(gone.Post.ID)
	missing := uuid.New()
	for _, id := range []uuid.UUID{gone.Post.ID, missing} {
		id := id
		_, err := r.create(r.bob, func(in *CreateNoteInput) { in.ReplyToID = &id })
		wantErr(t, err, ErrNoteNotFound)
		_, err = r.create(r.alice, func(in *CreateNoteInput) { in.QuoteOfID = &id })
		wantErr(t, err, ErrNoteNotFound)
	}
	both := r.note(r.alice, nil)
	_, err := r.create(r.alice, func(in *CreateNoteInput) { in.ReplyToID = &both.Post.ID; in.QuoteOfID = &both.Post.ID })
	wantErr(t, err, ErrInvalidNoteRequest)
	_, err = r.create(r.alice, func(in *CreateNoteInput) { in.ReplyPolicy = "friends" })
	wantErr(t, err, ErrInvalidNoteRequest)
}

// ── Thread ──────────────────────────────────────────────────────────────

func TestNoteRule_Thread(t *testing.T) {
	r := newNoteRig(t)
	thread := func(entries ...ThreadEntryInput) ([]*NoteView, error) {
		return r.svc.CreateNoteThread(context.Background(), &CreateNoteThreadInput{
			AuthorID: r.alice, Entries: entries, IdempotencyKey: uuid.NewString()})
	}
	e := func(text string) ThreadEntryInput { return ThreadEntryInput{Text: text} }
	_, err := thread(e("only one"))
	wantErr(t, err, ErrInvalidNoteThread)
	many := make([]ThreadEntryInput, NoteThreadMax+1)
	for i := range many {
		many[i] = e("x")
	}
	_, err = thread(many...)
	wantErr(t, err, ErrInvalidNoteThread)
	_, err = thread(e("ok"), e(strings.Repeat("y", 501)))
	wantErr(t, err, ErrInvalidNoteThread)
	_, err = thread(e("ok"), e("  "))
	wantErr(t, err, ErrInvalidNoteThread)
	video := r.image(r.alice, "video/mp4", "video")
	_, err = thread(e("ok"), ThreadEntryInput{Text: "v", MediaIDs: []uuid.UUID{video}})
	wantErr(t, err, ErrInvalidNoteThread)
	if len(r.store.Writes) != 0 {
		t.Fatal("a refused thread was written")
	}

	views, err := thread(e("one"), e("two"), e("three"))
	if err != nil {
		t.Fatal(err)
	}
	root := views[0].Post.ID
	w := r.store.LastWrite()
	if len(w.Notes) != 3 || len(w.Events) != 3 {
		t.Fatalf("thread write: %d notes, %d events", len(w.Notes), len(w.Events))
	}
	for i, p := range w.Notes {
		f := w.Fields[i]
		if *f.ConversationID != root || *p.ThreadRootID != root || p.ThreadSeq != i || p.ContentType != NoteContentType {
			t.Fatalf("entry %d: %+v %+v", i, f, p)
		}
		if (i == 0) != (f.ReplyToID == nil) || (i > 0 && *f.ReplyToID != w.Notes[i-1].ID) {
			t.Fatalf("entry %d reply_to_id %v", i, f.ReplyToID)
		}
		if string(p.Distribution) != `{"version":1,"main_feed":false}` {
			t.Fatalf("entry %d distribution %s", i, p.Distribution)
		}
	}
	if NoteThreadMin != 2 || NoteThreadMax != 25 {
		t.Fatal("thread bounds drifted from the contract")
	}
}

// ── Rate limits ─────────────────────────────────────────────────────────

func TestNoteRule_RateLimits(t *testing.T) {
	r := newNoteRig(t)
	r.svc.noteCfg.Limits = NoteLimits{NotesPerHour: 2, NotesPerDay: 3, RepliesPerHour: 3, RepliesPerHourFree: 1}
	for i := 0; i < 2; i++ {
		if _, err := r.create(r.alice, nil); err != nil {
			t.Fatal(err)
		}
	}
	_, err := r.create(r.alice, nil)
	wantErr(t, err, ErrNoteRateLimited)

	target := r.note(r.alice, nil)
	reply := func(by uuid.UUID) error {
		_, err := r.create(by, func(in *CreateNoteInput) { in.ReplyToID = &target.Post.ID })
		return err
	}
	if err := reply(r.bob); err != nil {
		t.Fatal(err)
	}
	wantErr(t, reply(r.bob), ErrNoteRateLimited) // free: 1 per hour
	for i := 0; i < 3; i++ {
		if err := reply(r.alice); err != nil {
			t.Fatalf("premium reply %d: %v", i, err)
		}
	}
	wantErr(t, reply(r.alice), ErrNoteRateLimited) // premium: 3 per hour

	// The daily budget, on its own key.
	r2 := newNoteRig(t)
	r2.svc.noteCfg.Limits = NoteLimits{NotesPerHour: 100, NotesPerDay: 1, RepliesPerHour: 1, RepliesPerHourFree: 1}
	if _, err := r2.create(r2.alice, nil); err != nil {
		t.Fatal(err)
	}
	_, err = r2.create(r2.alice, nil)
	wantErr(t, err, ErrNoteRateLimited)
}

func TestNoteLimitsFromEnv(t *testing.T) {
	if DefaultNoteLimits != (NoteLimits{30, 200, 60, 20}) {
		t.Fatalf("defaults drifted from the contract: %+v", DefaultNoteLimits)
	}
	env := map[string]string{"ECHO_NOTES_PER_HOUR": "5", "ECHO_NOTES_PER_DAY": "-1",
		"ECHO_REPLIES_PER_HOUR": "x", "ECHO_REPLIES_PER_HOUR_FREE": "7"}
	got := NoteLimitsFromEnv(func(k string) string { return env[k] })
	if got != (NoteLimits{5, 200, 60, 7}) {
		t.Fatalf("env limits: %+v", got)
	}
	for v, want := range map[string]bool{"": false, "false": false, "true": true, "TRUE": true, "1": true, "yes": false} {
		if got := EchoEnabledFromEnv(func(string) string { return v }); got != want {
			t.Errorf("ECHO_ENABLED=%q: %v", v, got)
		}
	}
}

// ── Edit ────────────────────────────────────────────────────────────────

func TestNoteRule_Edit(t *testing.T) {
	r := newNoteRig(t)
	fresh := r.note(r.alice, func(n *postgres.Note) { n.Post.CreatedAt = noteNow.Add(-NoteEditWindow) })
	old := r.note(r.alice, func(n *postgres.Note) { n.Post.CreatedAt = noteNow.Add(-NoteEditWindow - time.Second) })
	text := "edited"
	policy := ReplyPolicyMentioned

	_, err := r.svc.PatchNote(context.Background(), fresh.Post.ID, r.bob, NotePatch{Text: &text})
	wantErr(t, err, ErrNotNoteAuthor)
	_, err = r.svc.PatchNote(context.Background(), old.Post.ID, r.alice, NotePatch{Text: &text})
	wantErr(t, err, ErrEditWindowClosed)
	long := strings.Repeat("z", 501)
	_, err = r.svc.PatchNote(context.Background(), fresh.Post.ID, r.alice, NotePatch{Text: &long})
	wantErr(t, err, ErrNoteTooLong)

	v, err := r.svc.PatchNote(context.Background(), fresh.Post.ID, r.alice, NotePatch{Text: &text})
	if err != nil {
		t.Fatal(err)
	}
	if v.Post.Text != "edited" || v.EditedAt == nil || !v.EditedAt.Equal(noteNow) {
		t.Fatalf("edit: text %q edited_at %v", v.Post.Text, v.EditedAt)
	}
	// reply_policy can change at any time; it does not stamp edited_at.
	v, err = r.svc.PatchNote(context.Background(), old.Post.ID, r.alice, NotePatch{ReplyPolicy: &policy})
	if err != nil || v.ReplyPolicy != policy || v.EditedAt != nil {
		t.Fatalf("policy change after the window: %+v %v", v, err)
	}
	_, err = r.svc.PatchNote(context.Background(), old.Post.ID, r.bob, NotePatch{ReplyPolicy: &policy})
	wantErr(t, err, ErrNotNoteAuthor)
}

// ── Delete ──────────────────────────────────────────────────────────────

func TestNoteDelete_AuthorOnlyAndGivesTheReplyCountBack(t *testing.T) {
	r := newNoteRig(t)
	parent := r.note(r.alice, nil)
	v, err := r.create(r.bob, func(in *CreateNoteInput) { in.ReplyToID = &parent.Post.ID })
	if err != nil {
		t.Fatal(err)
	}
	if got := r.store.Note(parent.Post.ID).ReplyCount; got != 1 {
		t.Fatalf("reply_count after a reply = %d", got)
	}
	wantErr(t, r.svc.DeleteNote(context.Background(), v.Post.ID, r.alice), ErrNotNoteAuthor)
	if err := r.svc.DeleteNote(context.Background(), v.Post.ID, r.bob); err != nil {
		t.Fatal(err)
	}
	if got := r.store.Note(parent.Post.ID).ReplyCount; got != 0 || len(r.deleted) != 1 {
		t.Fatalf("after delete: reply_count %d, deletes %v", got, r.deleted)
	}
	wantErr(t, r.svc.DeleteNote(context.Background(), v.Post.ID, r.bob), ErrNoteNotFound)
}

// ── Events, distribution, quotes ────────────────────────────────────────

func TestNoteCreate_EventsAndDistribution(t *testing.T) {
	r := newNoteRig(t)
	top, err := r.create(r.alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := r.store.LastWrite()
	if got := notetest.EventTypes(w); len(got) != 1 || got[0] != events.PostCreated {
		t.Fatalf("top-level events %v", got)
	}
	pc := w.Events[0].Payload.(events.PostCreatedPayload)
	if pc.ContentType != NoteContentType || pc.MainFeed == nil || *pc.MainFeed || pc.ConversationID != top.Post.ID.String() ||
		pc.ReplyToID != "" || pc.QuoteOfID != "" {
		t.Fatalf("top-level PostCreated %+v", pc)
	}
	if string(w.Notes[0].Distribution) != `{"version":1,"main_feed":false}` || top.ConversationID != top.Post.ID {
		t.Fatalf("distribution %s conversation %v", w.Notes[0].Distribution, top.ConversationID)
	}

	// A reply by someone else: PostCreated with the conversation, and
	// note.replied to the parent's author.
	reply, err := r.create(r.bob, func(in *CreateNoteInput) { in.ReplyToID = &top.Post.ID })
	if err != nil {
		t.Fatal(err)
	}
	w = r.store.LastWrite()
	if got := notetest.EventTypes(w); len(got) != 2 || got[1] != events.EventNoteReplied {
		t.Fatalf("reply events %v", got)
	}
	nr := w.Events[1].Payload.(events.NoteRepliedPayload)
	if nr.RecipientID != r.alice.String() || nr.ActorID != r.bob.String() || nr.ReplyToID != top.Post.ID.String() ||
		nr.ConversationID != top.Post.ID.String() || nr.NoteID != reply.Post.ID.String() {
		t.Fatalf("note.replied %+v", nr)
	}
	pc = w.Events[0].Payload.(events.PostCreatedPayload)
	if pc.ReplyToID != top.Post.ID.String() || pc.ConversationID != top.Post.ID.String() {
		t.Fatalf("reply PostCreated %+v", pc)
	}
	// A reply to the reply stays in the top note's conversation.
	deep, err := r.create(r.alice, func(in *CreateNoteInput) { in.ReplyToID = &reply.Post.ID })
	if err != nil || deep.ConversationID != top.Post.ID {
		t.Fatalf("deep reply conversation %v %v", deep, err)
	}

	// Self rule: replying to your own note emits nothing but PostCreated.
	if _, err := r.create(r.alice, func(in *CreateNoteInput) { in.ReplyToID = &top.Post.ID }); err != nil {
		t.Fatal(err)
	}
	if got := notetest.EventTypes(r.store.LastWrite()); len(got) != 1 {
		t.Fatalf("self reply events %v", got)
	}

	// A quote: note.quoted, the quote repost row, quoted_note filled.
	bobs := r.note(r.bob, nil)
	q, err := r.create(r.alice, func(in *CreateNoteInput) { in.QuoteOfID = &bobs.Post.ID; in.Text = "so true" })
	if err != nil {
		t.Fatal(err)
	}
	w = r.store.LastWrite()
	if got := notetest.EventTypes(w); len(got) != 2 || got[1] != events.EventNoteQuoted {
		t.Fatalf("quote events %v", got)
	}
	nq := w.Events[1].Payload.(events.NoteQuotedPayload)
	if nq.RecipientID != r.bob.String() || nq.QuoteOfID != bobs.Post.ID.String() {
		t.Fatalf("note.quoted %+v", nq)
	}
	if w.QuoteRepost == nil || w.QuoteRepost.OriginalPostID != bobs.Post.ID || w.QuoteRepost.QuoteText != "so true" {
		t.Fatalf("quote repost row %+v", w.QuoteRepost)
	}
	if q.QuotedNote == nil || q.QuotedNote.Post.ID != bobs.Post.ID || q.QuoteOfID == nil {
		t.Fatalf("quoted_note %+v", q.QuotedNote)
	}
	// Quoting your own note: no note.quoted.
	if _, err := r.create(r.alice, func(in *CreateNoteInput) { in.QuoteOfID = &top.Post.ID }); err != nil {
		t.Fatal(err)
	}
	if got := notetest.EventTypes(r.store.LastWrite()); len(got) != 1 {
		t.Fatalf("self quote events %v", got)
	}
}

func TestNoteRead_QuotedNoteIsOneLevelDeep(t *testing.T) {
	r := newNoteRig(t)
	a := r.note(r.alice, nil)
	b := r.note(r.alice, func(n *postgres.Note) { n.QuoteOfID = &a.Post.ID })
	c := r.note(r.alice, func(n *postgres.Note) { n.QuoteOfID = &b.Post.ID })
	v, err := r.svc.GetNote(context.Background(), c.Post.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.QuotedNote == nil || v.QuotedNote.Post.ID != b.Post.ID || v.QuotedNote.QuotedNote != nil {
		t.Fatalf("quoted_note depth: %+v", v.QuotedNote)
	}
	// A deleted target reads as null, quote_of_id kept.
	r.store.Delete(b.Post.ID)
	v, err = r.svc.GetNote(context.Background(), c.Post.ID, nil)
	if err != nil || v.QuotedNote != nil || v.QuoteOfID == nil || *v.QuoteOfID != b.Post.ID {
		t.Fatalf("deleted quote target: %+v %v", v, err)
	}
}

func TestNoteCreate_Idempotency(t *testing.T) {
	r := newNoteRig(t)
	key := uuid.NewString()
	first, err := r.create(r.alice, func(in *CreateNoteInput) { in.IdempotencyKey = key })
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.create(r.alice, func(in *CreateNoteInput) { in.IdempotencyKey = key })
	if err != nil || again.Post.ID != first.Post.ID || len(r.store.Writes) != 1 {
		t.Fatalf("replay: %v %v, %d writes", again, err, len(r.store.Writes))
	}
	_, err = r.create(r.alice, func(in *CreateNoteInput) { in.IdempotencyKey = key; in.Text = "different" })
	wantErr(t, err, ErrCreateKeyReused)
}

// ── Eligibility ─────────────────────────────────────────────────────────

func TestNoteEligibility(t *testing.T) {
	r := newNoteRig(t)
	ctx := context.Background()
	check := func(id uuid.UUID, want NoteEligibility) {
		t.Helper()
		got, err := r.svc.NoteEligibility(ctx, id)
		if err != nil || *got != want {
			t.Fatalf("%v: got %+v %v, want %+v", id, got, err, want)
		}
	}
	check(r.alice, NoteEligibility{CanPost: true, CanReply: true})
	check(r.bob, NoteEligibility{CanPost: false, CanReply: true, Reason: EligibilityPremiumRequired})
	r.premium.Users[r.carol] = true
	check(r.carol, NoteEligibility{CanPost: true, CanReply: false, Reason: EligibilityAccountTooNew})
	r.standing.suspended[r.alice] = true
	check(r.alice, NoteEligibility{Reason: EligibilitySuspended})

	r.premium.Err = errors.New("down")
	_, err := r.svc.NoteEligibility(ctx, r.bob)
	wantErr(t, err, ErrPremiumCheckUnavailable)
}

// ── Reads ───────────────────────────────────────────────────────────────

func TestNoteReads_RepliesConversationAndTabs(t *testing.T) {
	r := newNoteRig(t)
	top := r.note(r.alice, nil)
	mid := r.note(r.bob, func(n *postgres.Note) {
		n.ReplyToID = &top.Post.ID
		n.ConversationID = &top.Post.ID
		n.Post.CreatedAt = noteNow.Add(-30 * time.Minute)
	})
	leaf := r.note(r.alice, func(n *postgres.Note) {
		n.ReplyToID = &mid.Post.ID
		n.ConversationID = &top.Post.ID
		n.Post.CreatedAt = noteNow.Add(-10 * time.Minute)
	})
	ctx := context.Background()

	page, err := r.svc.ListNoteReplies(ctx, top.Post.ID, nil, "", 0)
	if err != nil || len(page.Items) != 1 || page.Items[0].Post.ID != mid.Post.ID {
		t.Fatalf("replies %+v %v", page, err)
	}
	conv, err := r.svc.NoteConversation(ctx, leaf.Post.ID, nil)
	if err != nil || len(conv) != 2 || conv[0].Post.ID != top.Post.ID || conv[1].Post.ID != mid.Post.ID {
		t.Fatalf("conversation %v %v", conv, err)
	}
	notes, _ := r.svc.ListUserNotes(ctx, r.alice, nil, "notes", "", 0)
	replies, _ := r.svc.ListUserNotes(ctx, r.alice, nil, "replies", "", 0)
	if len(notes.Items) != 1 || notes.Items[0].Post.ID != top.Post.ID || len(replies.Items) != 1 || replies.Items[0].Post.ID != leaf.Post.ID {
		t.Fatalf("tabs: notes %d replies %d", len(notes.Items), len(replies.Items))
	}
	_, err = r.svc.ListUserNotes(ctx, r.alice, nil, "likes", "", 0)
	wantErr(t, err, ErrInvalidNoteTab)

	// A hidden note is not readable, and not listed.
	r.store.Hidden[mid.Post.ID] = true
	_, err = r.svc.GetNote(ctx, mid.Post.ID, &r.alice)
	wantErr(t, err, ErrNoteNotFound)
	page, _ = r.svc.ListNoteReplies(ctx, top.Post.ID, &r.alice, "", 0)
	if len(page.Items) != 0 {
		t.Fatal("a hidden reply was listed")
	}
	// viewer_can_reply follows the policy.
	r.relations.Follows[r.alice] = map[uuid.UUID]bool{}
	pol := r.note(r.alice, func(n *postgres.Note) { n.ReplyPolicy = ReplyPolicyFollowing })
	v, _ := r.svc.GetNote(ctx, pol.Post.ID, &r.bob)
	own, _ := r.svc.GetNote(ctx, pol.Post.ID, &r.alice)
	anon, _ := r.svc.GetNote(ctx, pol.Post.ID, nil)
	if v.ViewerCanReply || !own.ViewerCanReply || anon.ViewerCanReply {
		t.Fatalf("viewer_can_reply: stranger %v author %v signed-out %v", v.ViewerCanReply, own.ViewerCanReply, anon.ViewerCanReply)
	}
}

// ── A note is created only by the notes path ────────────────────────────

func TestNoteIsNotAnOrdinaryContentType(t *testing.T) {
	if _, ok := CanonicalContentType("note"); ok {
		t.Fatal("POST /v1/posts would accept content_type=note")
	}
	if validContentTypes["note"] {
		t.Fatal("validContentTypes holds note")
	}
	_, err := (&Service{}).CreatePost(context.Background(), &CreatePostInput{
		AuthorID: uuid.New(), Text: "x", Visibility: "public", ContentType: "note"})
	if !errors.Is(err, ErrInvalidContentType) {
		t.Fatalf("CreatePost(note): %v, want ErrInvalidContentType", err)
	}
}

func TestNoteIsNotMonetizable(t *testing.T) {
	if c, ok := postclassify.CanonicalMonetizationType(NoteContentType); ok {
		t.Fatalf("a note is monetizable as %q", c)
	}
}

func TestNoteMediaHelpers(t *testing.T) {
	ids := func(n int) []uuid.UUID {
		out := make([]uuid.UUID, n)
		for i := range out {
			out[i] = uuid.New()
		}
		return out
	}
	if err := ValidateNoteMediaCount(ids(4)); err != nil {
		t.Fatalf("four ids: %v", err)
	}
	wantErr(t, ValidateNoteMediaCount(ids(5)), ErrTooManyNoteMedia)
	dup := ids(1)
	wantErr(t, ValidateNoteMediaCount(append(dup, dup[0])), ErrInvalidNoteRequest)

	img := postgres.NoteMediaRow{Kind: "image", MimeType: "image/png"}
	gif := postgres.NoteMediaRow{Kind: "image", MimeType: "IMAGE/GIF"}
	if err := NoteMediaRule([]postgres.NoteMediaRow{img, img, img, img}); err != nil {
		t.Fatalf("four images: %v", err)
	}
	if err := NoteMediaRule([]postgres.NoteMediaRow{gif}); err != nil {
		t.Fatalf("one GIF: %v", err)
	}
	wantErr(t, NoteMediaRule([]postgres.NoteMediaRow{img, img, img, img, img}), ErrTooManyNoteMedia)
	wantErr(t, NoteMediaRule([]postgres.NoteMediaRow{img, gif}), ErrTooManyNoteMedia)
	wantErr(t, NoteMediaRule([]postgres.NoteMediaRow{{Kind: "video", MimeType: "video/mp4"}}), ErrNoteMediaNotAllowed)
	wantErr(t, NoteMediaRule([]postgres.NoteMediaRow{{Kind: "audio", MimeType: "audio/mpeg"}}), ErrNoteMediaNotAllowed)
}

func TestNoteRule_ThreadSpendsTheNotesBudget(t *testing.T) {
	r := newNoteRig(t)
	r.svc.noteCfg.Limits = NoteLimits{NotesPerHour: 1, NotesPerDay: 10, RepliesPerHour: 5, RepliesPerHourFree: 5}
	thread := func() error {
		_, err := r.svc.CreateNoteThread(context.Background(), &CreateNoteThreadInput{AuthorID: r.alice,
			Entries: []ThreadEntryInput{{Text: "a"}, {Text: "b"}}, IdempotencyKey: uuid.NewString()})
		return err
	}
	if err := thread(); err != nil {
		t.Fatal(err)
	}
	wantErr(t, thread(), ErrNoteRateLimited)
}
