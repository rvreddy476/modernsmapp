package service

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Echo v2 (docs/designs/echo-v2-contract.md §1-3), one test per rule,
// through the service's public paths over the in-memory store.

var bg = context.Background()

func (r *noteRig) at(t time.Time) { r.svc.now = func() time.Time { return t } }

func pollIn(opts ...string) *NotePollInput {
	return &NotePollInput{Options: opts, DurationMinutes: 60}
}

// ── Polls ───────────────────────────────────────────────────────────────

func TestNoteV2_PollRules(t *testing.T) {
	r := newNoteRig(t)
	img := r.image(r.alice, "image/jpeg", "image")
	long := strings.Repeat("x", NotePollLabelMaxRunes+1)
	cases := []struct {
		name string
		mod  func(*CreateNoteInput)
		want error
	}{
		{"one choice", func(in *CreateNoteInput) { in.Poll = pollIn("a") }, ErrInvalidNotePoll},
		{"five choices", func(in *CreateNoteInput) { in.Poll = pollIn("a", "b", "c", "d", "e") }, ErrInvalidNotePoll},
		{"26-character choice", func(in *CreateNoteInput) { in.Poll = pollIn("a", long) }, ErrInvalidNotePoll},
		{"empty choice", func(in *CreateNoteInput) { in.Poll = pollIn("a", "  ") }, ErrInvalidNotePoll},
		{"repeated choice", func(in *CreateNoteInput) { in.Poll = pollIn("Yes", "yes") }, ErrInvalidNotePoll},
		{"4 minutes", func(in *CreateNoteInput) { in.Poll = &NotePollInput{Options: []string{"a", "b"}, DurationMinutes: 4} }, ErrInvalidNotePoll},
		{"7 days and a minute", func(in *CreateNoteInput) {
			in.Poll = &NotePollInput{Options: []string{"a", "b"}, DurationMinutes: 7*24*60 + 1}
		}, ErrInvalidNotePoll},
		{"poll and media", func(in *CreateNoteInput) { in.Poll = pollIn("a", "b"); in.MediaIDs = []uuid.UUID{img} }, ErrNotePollMedia},
	}
	for _, c := range cases {
		_, err := r.create(r.alice, c.mod)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if len(r.store.Writes) != 0 {
		t.Fatal("a refused poll was written")
	}
	for _, d := range []int{5, 7 * 24 * 60} {
		v, err := r.create(r.alice, func(in *CreateNoteInput) {
			in.Poll = &NotePollInput{Options: []string{"Yes", strings.Repeat("y", 25), "Maybe", "No"}, DurationMinutes: d}
		})
		if err != nil {
			t.Fatalf("%d minutes: %v", d, err)
		}
		if v.Poll == nil || len(v.Poll.Options) != 4 || !v.Poll.EndsAt.Equal(noteNow.Add(time.Duration(d)*time.Minute)) {
			t.Fatalf("poll view %+v", v.Poll)
		}
		if v.Poll.Options[0].Votes == nil {
			t.Fatal("the author sees the counts")
		}
	}
}

func (r *noteRig) pollNote(author uuid.UUID, ends time.Time) *postgres.Note {
	n := r.note(author, nil)
	r.store.PutPoll(n.Post.ID, &postgres.PollData{EndsAt: &ends,
		Options: []postgres.PollOption{{Label: "Yes"}, {Label: "No"}}})
	return n
}

func TestNoteV2_VoteOncePerPersonFinal(t *testing.T) {
	r := newNoteRig(t)
	n := r.pollNote(r.alice, noteNow.Add(time.Hour))
	id := n.Post.ID

	before, err := r.svc.GetNote(bg, id, &r.bob)
	if err != nil {
		t.Fatal(err)
	}
	if before.Poll.Options[0].Votes != nil || before.Poll.ViewerVote != nil {
		t.Fatalf("counts are hidden before the viewer votes: %+v", before.Poll)
	}
	v, err := r.svc.VoteNotePoll(bg, id, r.bob, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Poll.ViewerVote == nil || *v.Poll.ViewerVote != 1 || v.Poll.Options[1].Votes == nil || *v.Poll.Options[1].Votes != 1 {
		t.Fatalf("after voting: %+v", v.Poll)
	}
	_, err = r.svc.VoteNotePoll(bg, id, r.bob, 0)
	wantErr(t, err, ErrNoteAlreadyVoted)
	_, err = r.svc.VoteNotePoll(bg, id, r.bob, 1)
	wantErr(t, err, ErrNoteAlreadyVoted)
	if got := r.store.Votes[[2]uuid.UUID{id, r.bob}]; len(got) != 1 {
		t.Fatalf("bob holds %d votes", len(got))
	}
	// Carol has not voted: total visible, choices not.
	cv, _ := r.svc.GetNote(bg, id, &r.carol)
	if cv.Poll.TotalVotes != 1 || cv.Poll.Options[1].Votes != nil {
		t.Fatalf("carol's view %+v", cv.Poll)
	}
	// The author always sees the counts.
	av, _ := r.svc.GetNote(bg, id, &r.alice)
	if av.Poll.Options[1].Votes == nil {
		t.Fatal("author view hides counts")
	}
	_, err = r.svc.VoteNotePoll(bg, id, r.carol, 2)
	wantErr(t, err, ErrInvalidNotePoll)
	_, err = r.svc.VoteNotePoll(bg, id, r.carol, -1)
	wantErr(t, err, ErrInvalidNotePoll)
}

func TestNoteV2_OwnPollRefused(t *testing.T) {
	r := newNoteRig(t)
	n := r.pollNote(r.alice, noteNow.Add(time.Hour))
	_, err := r.svc.VoteNotePoll(bg, n.Post.ID, r.alice, 0)
	wantErr(t, err, ErrOwnNotePoll)
	if len(r.store.Votes) != 0 {
		t.Fatal("own vote recorded")
	}
}

func TestNoteV2_ClosedPollRefusedAndRevealed(t *testing.T) {
	r := newNoteRig(t)
	n := r.pollNote(r.alice, noteNow)
	_, err := r.svc.VoteNotePoll(bg, n.Post.ID, r.bob, 0)
	wantErr(t, err, ErrNotePollClosed)
	v, _ := r.svc.GetNote(bg, n.Post.ID, nil)
	if !v.Poll.Closed || v.Poll.Options[0].Votes == nil {
		t.Fatalf("a closed poll shows counts to everyone: %+v", v.Poll)
	}
	// No poll, hidden note.
	plain := r.note(r.alice, nil)
	_, err = r.svc.VoteNotePoll(bg, plain.Post.ID, r.bob, 0)
	wantErr(t, err, ErrInvalidNotePoll)
	r.store.Hidden[n.Post.ID] = true
	_, err = r.svc.VoteNotePoll(bg, n.Post.ID, r.bob, 0)
	wantErr(t, err, ErrNoteNotFound)
}

// ── Scheduling ──────────────────────────────────────────────────────────

func (r *noteRig) scheduled(author uuid.UUID, at time.Time, mod func(*CreateNoteInput)) *NoteView {
	v, err := r.create(author, func(in *CreateNoteInput) {
		in.ScheduledAt = &at
		in.Text = "later @bob #Rain"
		if mod != nil {
			mod(in)
		}
	})
	if err != nil {
		panic(err)
	}
	return v
}

func TestNoteV2_ScheduleWindowAndReplies(t *testing.T) {
	r := newNoteRig(t)
	top := r.note(r.alice, nil)
	for name, at := range map[string]time.Time{
		"4 minutes":           noteNow.Add(4 * time.Minute),
		"past":                noteNow.Add(-time.Hour),
		"18 months and a day": noteNow.AddDate(0, 18, 1),
	} {
		_, err := r.create(r.alice, func(in *CreateNoteInput) { in.ScheduledAt = &at })
		if !errors.Is(err, ErrInvalidSchedule) {
			t.Errorf("%s: %v", name, err)
		}
	}
	at := noteNow.Add(time.Hour)
	_, err := r.create(r.alice, func(in *CreateNoteInput) { in.ScheduledAt = &at; in.ReplyToID = &top.Post.ID })
	wantErr(t, err, ErrInvalidSchedule)
	for _, at := range []time.Time{noteNow.Add(5 * time.Minute), noteNow.AddDate(0, 18, 0)} {
		if _, err := r.create(r.alice, func(in *CreateNoteInput) { in.ScheduledAt = &at }); err != nil {
			t.Fatalf("%v: %v", at, err)
		}
	}
}

func TestNoteV2_ScheduledIsInvisibleAndAnnouncesNothing(t *testing.T) {
	r := newNoteRig(t)
	quoted := r.note(r.bob, nil)
	at := noteNow.Add(2 * time.Hour)
	v := r.scheduled(r.alice, at, func(in *CreateNoteInput) { in.QuoteOfID = &quoted.Post.ID })
	if v.Status != NoteStatusScheduled || v.ScheduledAt == nil || !v.ScheduledAt.Equal(at) {
		t.Fatalf("create answer: status %q scheduled_at %v", v.Status, v.ScheduledAt)
	}
	w := r.store.LastWrite()
	if len(w.Events) != 0 || w.QuoteRepost != nil {
		t.Fatalf("a scheduled note announced itself: %v quote %v", eventTypesOf(w.Events), w.QuoteRepost)
	}
	id := v.Post.ID
	// Every read path, the author's included.
	for _, viewer := range []*uuid.UUID{nil, &r.alice, &r.bob} {
		if _, err := r.svc.GetNote(bg, id, viewer); !errors.Is(err, ErrNoteNotFound) {
			t.Fatalf("GET by %v: %v", viewer, err)
		}
		page, _ := r.svc.ListUserNotes(bg, r.alice, viewer, "notes", "", 20)
		if len(page.Items) != 0 {
			t.Fatalf("users tab lists the scheduled note for %v", viewer)
		}
		q, _ := r.svc.ListNoteQuotes(bg, quoted.Post.ID, viewer, "", 20)
		if len(q.Items) != 0 {
			t.Fatal("quotes list the scheduled quote")
		}
		h, _ := r.svc.NotesByHashtag(bg, "rain", "latest", viewer, "", 20)
		s, _ := r.svc.SearchNotes(bg, "later", "top", viewer, "", 20)
		if len(h.Items) != 0 || len(s.Items) != 0 {
			t.Fatal("hashtag page or search lists the scheduled note")
		}
		b, _ := r.svc.HydrateNotes(bg, []uuid.UUID{id}, viewer)
		if len(b) != 0 {
			t.Fatal("the internal batch hydrates the scheduled note")
		}
	}
	if _, err := r.svc.ListNoteReplies(bg, id, &r.alice, "", 20); !errors.Is(err, ErrNoteNotFound) {
		t.Fatalf("replies of a scheduled note: %v", err)
	}
	if _, err := r.svc.NoteConversation(bg, id, &r.alice); !errors.Is(err, ErrNoteNotFound) {
		t.Fatalf("conversation of a scheduled note: %v", err)
	}
	trends, _ := r.svc.NoteTrends(bg, 10)
	if len(trends) != 0 {
		t.Fatal("trends count the scheduled note")
	}
	if c, _ := r.store.NoteCountsFor(bg, []uuid.UUID{quoted.Post.ID}); c[quoted.Post.ID].Quotes != 0 {
		t.Fatal("quote_count counts the scheduled quote")
	}
	if _, err := r.svc.SetNoteBookmark(bg, id, r.bob, true); !errors.Is(err, ErrNoteNotFound) {
		t.Fatalf("bookmark a scheduled note: %v", err)
	}
	if _, err := r.svc.PatchNote(bg, id, r.alice, NotePatch{ReplyPolicy: strPtr("following")}); !errors.Is(err, ErrNoteNotFound) {
		t.Fatalf("patch a scheduled note: %v", err)
	}
	// Only the author's Scheduled list shows it.
	mine, err := r.svc.ListMyScheduledNotes(bg, r.alice, "", 20)
	if err != nil || len(mine.Items) != 1 || mine.Items[0].Post.ID != id {
		t.Fatalf("scheduled list: %v %+v", err, mine)
	}
	theirs, _ := r.svc.ListMyScheduledNotes(bg, r.bob, "", 20)
	if len(theirs.Items) != 0 {
		t.Fatal("someone else's scheduled list shows it")
	}
}


func eventTypesOf(evts []postgres.ThreadOutboxEvent) []string {
	var out []string
	for _, e := range evts {
		out = append(out, e.EventType)
	}
	return out
}

func TestNoteV2_HiddenWhileScheduledHidesANoteFromItsAuthor(t *testing.T) {
	at := noteNow
	author := uuid.New()
	note := &postgres.Post{AuthorID: author, ContentType: NoteContentType, PublishAt: &at}
	post := &postgres.Post{AuthorID: author, ContentType: "post", PublishAt: &at}
	if !hiddenWhileScheduled(note, &author) || !hiddenWhileScheduled(note, nil) {
		t.Fatal("a scheduled note is visible on a post read")
	}
	if hiddenWhileScheduled(post, &author) {
		t.Fatal("a scheduled post is hidden from its author")
	}
}

func TestNoteV2_RescheduleAndDeleteAreAuthorOnly(t *testing.T) {
	r := newNoteRig(t)
	v := r.scheduled(r.alice, noteNow.Add(time.Hour), func(in *CreateNoteInput) { in.Poll = pollIn("a", "b") })
	id := v.Post.ID
	later := noteNow.Add(3 * time.Hour)
	if _, err := r.svc.RescheduleNote(bg, id, r.bob, later); !errors.Is(err, ErrNoteNotFound) {
		t.Fatalf("stranger reschedule: %v", err)
	}
	if _, err := r.svc.RescheduleNote(bg, id, r.alice, noteNow.Add(time.Minute)); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("too soon: %v", err)
	}
	got, err := r.svc.RescheduleNote(bg, id, r.alice, later)
	if err != nil || !got.ScheduledAt.Equal(later) {
		t.Fatalf("reschedule: %v %+v", err, got)
	}
	if !got.Poll.EndsAt.Equal(later.Add(time.Hour)) {
		t.Fatalf("the poll keeps its duration: ends %v", got.Poll.EndsAt)
	}
	if err := r.svc.DeleteScheduledNote(bg, id, r.bob); !errors.Is(err, ErrNoteNotFound) {
		t.Fatalf("stranger delete: %v", err)
	}
	if err := r.svc.DeleteScheduledNote(bg, id, r.alice); err != nil {
		t.Fatal(err)
	}
	if len(r.deleted) != 1 || r.deleted[0] != id {
		t.Fatal("not deleted")
	}
	// A published note is not a scheduled one.
	pub := r.note(r.alice, nil)
	if err := r.svc.DeleteScheduledNote(bg, pub.Post.ID, r.alice); !errors.Is(err, ErrNoteNotFound) {
		t.Fatalf("delete schedule of a published note: %v", err)
	}
}

func TestNoteV2_PublisherPublishesWhenDueWithNoteEvents(t *testing.T) {
	r := newNoteRig(t)
	quoted := r.note(r.bob, nil)
	at := noteNow.Add(time.Hour)
	v := r.scheduled(r.alice, at, func(in *CreateNoteInput) {
		in.QuoteOfID = &quoted.Post.ID
		in.Poll = &NotePollInput{Options: []string{"a", "b"}, DurationMinutes: 30}
	})
	id := v.Post.ID

	rep, err := r.svc.PublishDueNotes(bg, 50)
	if err != nil || rep.Claimed != 0 {
		t.Fatalf("not due yet: %+v %v", rep, err)
	}
	pubAt := at.Add(90 * time.Second)
	r.at(pubAt)
	rep, err = r.svc.PublishDueNotes(bg, 50)
	if err != nil || rep.Published != 1 {
		t.Fatalf("due: %+v %v", rep, err)
	}
	if len(r.store.Publishes) != 1 {
		t.Fatal("no publish committed")
	}
	p := r.store.Publishes[0]
	types := eventTypesOf(p.Events)
	if len(types) != 2 || types[0] != events.PostCreated || types[1] != events.EventNoteQuoted {
		t.Fatalf("publish events %v", types)
	}
	pc := p.Events[0].Payload.(events.PostCreatedPayload)
	if pc.ContentType != NoteContentType || pc.ConversationID != id.String() || pc.QuoteOfID != quoted.Post.ID.String() ||
		!pc.CreatedAt.Equal(pubAt) || pc.SearchRev != 2 {
		t.Fatalf("PostCreated %+v", pc)
	}
	if p.Quote == nil {
		t.Fatal("the quote repost row waits for publication, then is written")
	}
	got, err := r.svc.GetNote(bg, id, &r.bob)
	if err != nil || got.Status != NoteStatusPublished || got.ScheduledAt != nil {
		t.Fatalf("published note: %v %+v", err, got)
	}
	if !got.Post.CreatedAt.Equal(pubAt) || !got.Poll.EndsAt.Equal(pubAt.Add(30*time.Minute)) {
		t.Fatalf("created_at %v ends_at %v", got.Post.CreatedAt, got.Poll.EndsAt)
	}
	// A second pass, on any replica, publishes nothing.
	rep, _ = r.svc.PublishDueNotes(bg, 50)
	if rep.Claimed != 0 || len(r.store.Publishes) != 1 {
		t.Fatalf("published twice: %+v", rep)
	}
}

func TestNoteV2_PublisherRechecksPremiumAndStanding(t *testing.T) {
	r := newNoteRig(t)
	at := noteNow.Add(time.Hour)
	v := r.scheduled(r.alice, at, nil)
	id := v.Post.ID
	delete(r.premium.Users, r.alice)

	r.at(at)
	if rep, _ := r.svc.PublishDueNotes(bg, 50); rep.Published != 0 || rep.Deferred != 1 {
		t.Fatalf("published without Premium: %+v", rep)
	}
	mine, _ := r.svc.ListMyScheduledNotes(bg, r.alice, "", 20)
	if len(mine.Items) != 1 || mine.Items[0].Status != NoteStatusScheduled || mine.Items[0].PublishError == nil ||
		*mine.Items[0].PublishError != NotePublishPremiumRequired {
		t.Fatalf("author's view after the failed check: %+v", mine.Items)
	}
	// Retried hourly: not again within the hour.
	r.at(at.Add(30 * time.Minute))
	if rep, _ := r.svc.PublishDueNotes(bg, 50); rep.Claimed != 0 {
		t.Fatal("retried within the hour")
	}
	r.at(at.Add(time.Hour))
	if rep, _ := r.svc.PublishDueNotes(bg, 50); rep.Claimed != 1 {
		t.Fatal("not retried after the hour")
	}
	// 24 hours after the first failure: failed, and never claimed again.
	for h := 2; h <= 24; h++ {
		r.at(at.Add(time.Duration(h) * time.Hour))
		r.svc.PublishDueNotes(bg, 50)
	}
	mine, _ = r.svc.ListMyScheduledNotes(bg, r.alice, "", 20)
	if mine.Items[0].Status != NoteStatusFailed {
		t.Fatalf("status after 24 h: %q", mine.Items[0].Status)
	}
	r.premium.Users[r.alice] = true
	r.at(at.Add(30 * time.Hour))
	if rep, _ := r.svc.PublishDueNotes(bg, 50); rep.Claimed != 0 {
		t.Fatal("a failed note was claimed")
	}
	// Rescheduling arms it again.
	next := at.Add(31 * time.Hour)
	if _, err := r.svc.RescheduleNote(bg, id, r.alice, next); err != nil {
		t.Fatal(err)
	}
	r.at(next)
	if rep, _ := r.svc.PublishDueNotes(bg, 50); rep.Published != 1 {
		t.Fatalf("rescheduled note not published: %+v", rep)
	}

	// Standing: a suspended author's note stays scheduled.
	v2 := r.scheduled(r.alice, next.Add(time.Hour), nil)
	r.standing.suspended[r.alice] = true
	r.at(next.Add(time.Hour))
	r.svc.PublishDueNotes(bg, 50)
	sn, _ := r.store.GetScheduledNote(bg, v2.Post.ID)
	if sn == nil || sn.PublishError == nil || *sn.PublishError != NotePublishSuspended {
		t.Fatalf("suspended author: %+v", sn)
	}
}

func TestNoteV2_PostScheduleWorkerNeverTakesANote(t *testing.T) {
	r := newNoteRig(t)
	at := noteNow.Add(time.Hour)
	v := r.scheduled(r.alice, at, nil)
	// No pgStore here: PublishScheduled reads the post through it, so the
	// guard is pinned on the source and on the store's due scan.
	_ = v
	if src := funcSourceOf(t, "schedule.go", "PublishScheduled"); !strings.Contains(src, "p.ContentType == NoteContentType") {
		t.Fatal("PublishScheduled no longer refuses notes")
	}
	if src := funcSourceOf(t, "schedule.go", "ReschedulePost"); !strings.Contains(src, "p.ContentType == postgres.NoteContentType") {
		t.Fatal("ReschedulePost no longer refuses notes")
	}
}

// ── Bookmarks and views ─────────────────────────────────────────────────

func TestNoteV2_BookmarksArePrivateAndCounted(t *testing.T) {
	r := newNoteRig(t)
	n := r.note(r.alice, nil)
	st, err := r.svc.SetNoteBookmark(bg, n.Post.ID, r.bob, true)
	if err != nil || !st.Bookmarked || st.Count != 1 {
		t.Fatalf("bookmark: %v %+v", err, st)
	}
	if st, _ := r.svc.SetNoteBookmark(bg, n.Post.ID, r.bob, true); st.Count != 1 {
		t.Fatal("a second save counts twice")
	}
	bv, _ := r.svc.GetNote(bg, n.Post.ID, &r.bob)
	cv, _ := r.svc.GetNote(bg, n.Post.ID, &r.carol)
	if !bv.ViewerHasBookmarked || cv.ViewerHasBookmarked || cv.BookmarkCount != 1 {
		t.Fatalf("bob %v carol %v count %d", bv.ViewerHasBookmarked, cv.ViewerHasBookmarked, cv.BookmarkCount)
	}
	if p, _ := r.svc.ListNoteBookmarks(bg, r.bob, "", 20); len(p.Items) != 1 {
		t.Fatal("bob's bookmarks")
	}
	if p, _ := r.svc.ListNoteBookmarks(bg, r.carol, "", 20); len(p.Items) != 0 {
		t.Fatal("carol sees bob's bookmarks")
	}
	r.store.Hidden[n.Post.ID] = true
	if p, _ := r.svc.ListNoteBookmarks(bg, r.bob, "", 20); len(p.Items) != 0 {
		t.Fatal("a note bob may no longer see stays in his list")
	}
	if _, err := r.svc.SetNoteBookmark(bg, uuid.New(), r.bob, true); !errors.Is(err, ErrNoteNotFound) {
		t.Fatalf("bookmark a missing note: %v", err)
	}
	st, err = r.svc.SetNoteBookmark(bg, n.Post.ID, r.bob, false)
	if err != nil || st.Bookmarked || st.Count != 0 {
		t.Fatalf("unbookmark (allowed even when hidden): %v %+v", err, st)
	}
}

func TestNoteV2_ViewsOncePerDayNeverTheAuthor(t *testing.T) {
	r := newNoteRig(t)
	n := r.note(r.alice, nil)
	ids := []uuid.UUID{n.Post.ID}
	view := func(v *uuid.UUID) int64 {
		if err := r.svc.RecordNoteViews(bg, v, ids); err != nil {
			t.Fatal(err)
		}
		return r.store.Note(n.Post.ID).ViewCount
	}
	if view(nil) != 0 {
		t.Fatal("a signed-out view counted")
	}
	if view(&r.alice) != 0 {
		t.Fatal("the author's view counted")
	}
	if view(&r.bob) != 1 || view(&r.bob) != 1 {
		t.Fatal("bob counts once")
	}
	r.at(noteNow.Add(23 * time.Hour))
	if view(&r.bob) != 1 {
		t.Fatal("bob counted again within 24 hours")
	}
	r.at(noteNow.Add(24 * time.Hour))
	if view(&r.bob) != 2 {
		t.Fatal("bob not counted after 24 hours")
	}
	got, _ := r.svc.GetNote(bg, n.Post.ID, nil)
	if got.ViewCount != 2 {
		t.Fatalf("view_count %d", got.ViewCount)
	}
	many := make([]uuid.UUID, NoteViewsMaxBatch+1)
	wantErr(t, r.svc.RecordNoteViews(bg, &r.bob, many), ErrInvalidNoteRequest)
}

// ── Quotes and discovery ────────────────────────────────────────────────

func TestNoteV2_QuotesAndQuoteCount(t *testing.T) {
	r := newNoteRig(t)
	n := r.note(r.alice, nil)
	q, err := r.create(r.alice, func(in *CreateNoteInput) { in.QuoteOfID = &n.Post.ID })
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.svc.ListNoteQuotes(bg, n.Post.ID, &r.bob, "", 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].Post.ID != q.Post.ID {
		t.Fatalf("quotes: %v %+v", err, page)
	}
	v, _ := r.svc.GetNote(bg, n.Post.ID, &r.bob)
	if v.QuoteCount != 1 {
		t.Fatalf("quote_count %d", v.QuoteCount)
	}
	if _, err := r.svc.ListNoteQuotes(bg, uuid.New(), nil, "", 20); !errors.Is(err, ErrNoteNotFound) {
		t.Fatalf("quotes of a missing note: %v", err)
	}
}

func TestNoteV2_TrendsNeedThreeAuthors(t *testing.T) {
	r := newNoteRig(t)
	dave := uuid.New()
	tag := func(author uuid.UUID, tags []string, mod func(*postgres.Note)) {
		r.note(author, func(n *postgres.Note) {
			n.Post.Hashtags = tags
			if mod != nil {
				mod(n)
			}
		})
	}
	tag(r.alice, []string{"rain"}, nil)
	tag(r.bob, []string{"rain"}, nil)
	tag(r.carol, []string{"rain", "duo"}, nil)
	tag(r.alice, []string{"duo"}, nil)
	tag(r.alice, []string{"duo"}, nil) // two authors, three notes: not a trend
	tag(dave, []string{"old"}, func(n *postgres.Note) { n.Post.CreatedAt = noteNow.Add(-25 * time.Hour) })
	parent := r.note(r.alice, nil)
	for _, a := range []uuid.UUID{r.bob, r.carol, dave} {
		tag(a, []string{"replies"}, func(n *postgres.Note) { n.ReplyToID = &parent.Post.ID })
	}
	got, err := r.svc.NoteTrends(bg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tag != "rain" || got[0].NoteCount != 3 || got[0].Rank != 1 {
		t.Fatalf("trends %+v", got)
	}
}

func TestNoteV2_HashtagAndSearchTabs(t *testing.T) {
	r := newNoteRig(t)
	r.note(r.alice, func(n *postgres.Note) { n.Post.Text = "Monsoon #rain"; n.Post.Hashtags = []string{"rain"} })
	if _, err := r.svc.NotesByHashtag(bg, "rain", "popular", nil, "", 20); !errors.Is(err, ErrInvalidNoteTab) {
		t.Fatalf("bad tab: %v", err)
	}
	for _, tab := range []string{"", "top", "latest"} {
		h, err := r.svc.NotesByHashtag(bg, "#Rain", tab, nil, "", 20)
		if err != nil || len(h.Items) != 1 {
			t.Fatalf("hashtag %q: %v %+v", tab, err, h)
		}
		s, err := r.svc.SearchNotes(bg, "monsoon", tab, nil, "", 20)
		if err != nil || len(s.Items) != 1 {
			t.Fatalf("search %q: %v", tab, err)
		}
	}
	if s, _ := r.svc.SearchNotes(bg, "  ", "", nil, "", 20); len(s.Items) != 0 {
		t.Fatal("empty query")
	}
}

// funcSourceOf is the printed source of function name in file, whitespace
// collapsed.
func funcSourceOf(t *testing.T, file, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name {
			continue
		}
		var b strings.Builder
		if err := printer.Fprint(&b, fset, fn); err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(b.String()), " ")
	}
	t.Fatalf("no function %s in %s", name, file)
	return ""
}

// The generic poll routes (/v1/posts/:id/poll, /vote, /poll/vote) and the
// post read never show or take a note's poll: they would reveal the counts
// before a vote and let the author vote. Store-backed, so pinned on the
// source here and run in CI.
func TestNoteV2_GenericPollPathsRefuseNotes(t *testing.T) {
	if src := funcSourceOf(t, "polls.go", "CastPollVote"); !strings.Contains(src, "} else if isNote { return ErrPollNotFound }") {
		t.Fatal("CastPollVote takes a note's vote")
	}
	if src := funcSourceOf(t, "post_engagement.go", "GetPoll"); !strings.Contains(src, "} else if isNote { return nil, nil }") {
		t.Fatal("GetPoll reads a note's poll")
	}
	if src := funcSourceOf(t, "post_reads.go", "GetPost"); !strings.Contains(src, "hasPoll && p.ContentType != NoteContentType") {
		t.Fatal("GET /v1/posts/:id attaches a note's poll")
	}
}
