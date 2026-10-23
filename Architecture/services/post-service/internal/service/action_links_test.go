package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/actionlinks"
	"github.com/atpost/post-service/internal/service/actionfake"
	"github.com/atpost/post-service/internal/standing"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The Action button (action_links.go): the three write steps, the read
// shape, the redirect's tap count, the report threshold and the recheck.

var actionNow = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

type fakeAccounts struct {
	info  actionlinks.AccountInfo
	err   error
	calls int
}

func (f *fakeAccounts) Account(context.Context, uuid.UUID) (actionlinks.AccountInfo, error) {
	f.calls++
	return f.info, f.err
}

type fakeEmails struct {
	ok    bool
	err   error
	calls int
}

func (f *fakeEmails) EmailVerified(context.Context, uuid.UUID) (bool, error) {
	f.calls++
	return f.ok, f.err
}

type fakeStanding struct {
	res *standing.Result
	err error
}

func (f *fakeStanding) Check(context.Context, uuid.UUID) (*standing.Result, error) {
	return f.res, f.err
}

type fakeChecker struct {
	unsafe map[string]bool
	err    error
	calls  int
}

func (f *fakeChecker) Name() string { return "fake" }
func (f *fakeChecker) Check(_ context.Context, url string) (actionlinks.Verdict, error) {
	f.calls++
	if f.err != nil {
		return actionlinks.Verdict{}, f.err
	}
	return actionlinks.Verdict{Unsafe: f.unsafe[url]}, nil
}

type fakeTaps struct {
	seen map[string]bool
	err  error
}

func (f *fakeTaps) FirstTap(_ context.Context, key string, _ time.Duration) (bool, error) {
	if f.err != nil {
		return true, f.err // adversarial: an answer that errs must not count
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[key] {
		return false, nil
	}
	f.seen[key] = true
	return true, nil
}

// actionRig is a Service with every Action-button dependency faked and
// every eligibility fact true.
type actionRig struct {
	svc      *Service
	store    *actionfake.Store
	accounts *fakeAccounts
	emails   *fakeEmails
	standing *fakeStanding
	checker  *fakeChecker
	taps     *fakeTaps
	author   uuid.UUID
}

func newActionRig(t *testing.T) *actionRig {
	t.Helper()
	r := &actionRig{
		store:    actionfake.New(),
		accounts: &fakeAccounts{info: actionlinks.AccountInfo{Found: true, Active: true, CreatedAt: actionNow.AddDate(0, 0, -30)}},
		emails:   &fakeEmails{ok: true},
		standing: &fakeStanding{res: &standing.Result{Standing: "ok", ActiveStrikes: []standing.Strike{}}},
		checker:  &fakeChecker{unsafe: map[string]bool{}},
		taps:     &fakeTaps{},
		author:   uuid.New(),
	}
	r.store.Now = func() time.Time { return actionNow }
	r.svc = &Service{now: func() time.Time { return actionNow }}
	r.svc.SetStandingChecker(r.standing)
	r.svc.SetRequireStandingCheck(true)
	r.configure(false, r.checker)
	return r
}

func (r *actionRig) configure(production bool, checker actionlinks.Checker) {
	r.svc.SetActionLinks(ActionLinkConfig{
		Checker: checker, Production: production, OwnDomains: []string{"cleestudio.com"},
		Accounts: r.accounts, Emails: r.emails, TapDedupe: r.taps, Store: r.store,
	})
}

func (r *actionRig) post() uuid.UUID {
	id := uuid.New()
	r.store.Posts[id] = r.author
	return id
}

func notEligible(t *testing.T, err error) []string {
	t.Helper()
	var e *ActionNotEligibleError
	if !errors.As(err, &e) || !errors.Is(err, ErrActionNotEligible) {
		t.Fatalf("err = %v, want ActionNotEligibleError", err)
	}
	return e.Missing
}

var applyNow = &ActionInput{Label: "apply_now", URL: "https://www.epfindia.gov.in/apply"}

// Each eligibility item, alone, and each way of not establishing it.
func TestActionEligibilityPerItem(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r *actionRig)
		want  []string
	}{
		{"all met", func(*actionRig) {}, nil},
		{"account 6 days old", func(r *actionRig) { r.accounts.info.CreatedAt = actionNow.AddDate(0, 0, -6) }, []string{"account_age"}},
		{"account exactly 7 days old", func(r *actionRig) { r.accounts.info.CreatedAt = actionNow.AddDate(0, 0, -7) }, nil},
		{"account not active", func(r *actionRig) { r.accounts.info.Active = false }, []string{"account_age"}},
		{"account unknown", func(r *actionRig) { r.accounts.info.Found = false }, []string{"account_age"}},
		{"account read fails", func(r *actionRig) { r.accounts.err = errors.New("down") }, []string{"account_age"}},
		{"email not verified", func(r *actionRig) { r.emails.ok = false }, []string{"email_verified"}},
		{"email read fails", func(r *actionRig) { r.emails.ok, r.emails.err = true, errors.New("down") }, []string{"email_verified"}},
		{"an active strike", func(r *actionRig) {
			r.standing.res = &standing.Result{Standing: "ok", ActiveStrikes: []standing.Strike{{ID: "s1"}}}
		}, []string{"no_active_strikes"}},
		// The real client answers a denial with the result AND the error; a
		// suspension need not list a strike, so the error alone must refuse.
		{"suspended", func(r *actionRig) {
			r.standing.res = &standing.Result{Standing: "suspended", ActiveStrikes: []standing.Strike{}}
			r.standing.err = &standing.DeniedError{Standing: "suspended"}
		}, []string{"no_active_strikes"}},
		{"standing unknown", func(r *actionRig) { r.standing.res, r.standing.err = nil, standing.ErrUnknown }, []string{"no_active_strikes"}},
		{"no standing client, check required", func(r *actionRig) { r.svc.SetStandingChecker(nil) }, []string{"no_active_strikes"}},
		{"no standing client, dev opt-out", func(r *actionRig) {
			r.svc.SetStandingChecker(nil)
			r.svc.SetRequireStandingCheck(false)
		}, nil},
		{"no identity sources", func(r *actionRig) {
			r.svc.SetActionLinks(ActionLinkConfig{Checker: r.checker, Store: r.store})
		}, []string{"account_age", "email_verified"}},
		{"everything missing", func(r *actionRig) {
			r.accounts.info.CreatedAt = actionNow.Add(-time.Hour)
			r.emails.ok = false
			r.standing.res.ActiveStrikes = []standing.Strike{{ID: "s"}}
		}, []string{"account_age", "email_verified", "no_active_strikes"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newActionRig(t)
			tc.setup(r)
			_, err := r.svc.SetPostAction(context.Background(), r.author, r.post(), applyNow, false)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("err = %v, want eligible", err)
				}
				return
			}
			if got := notEligible(t, err); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("missing = %v, want %v", got, tc.want)
			}
			if r.store.Replaces != 0 {
				t.Fatal("a refused action was written")
			}
		})
	}
}

func TestActionEligibleAtIsWhenTheAccountTurnsSevenDays(t *testing.T) {
	r := newActionRig(t)
	created := actionNow.AddDate(0, 0, -2)
	r.accounts.info.CreatedAt = created
	_, err := r.svc.SetPostAction(context.Background(), r.author, r.post(), applyNow, false)
	var e *ActionNotEligibleError
	if !errors.As(err, &e) || e.EligibleAt == nil || !e.EligibleAt.Equal(created.AddDate(0, 0, 7)) {
		t.Fatalf("eligible_at = %+v (%v)", e, err)
	}
}

// The safety step: unsafe, safe, unavailable, no checker in dev and in
// production, and our own domains.
func TestActionSafetyCheck(t *testing.T) {
	ctx := context.Background()
	t.Run("unsafe is refused", func(t *testing.T) {
		r := newActionRig(t)
		r.checker.unsafe["https://phish.example.com/job"] = true
		if _, err := r.svc.SetPostAction(ctx, r.author, r.post(), &ActionInput{Label: "apply_now", URL: "https://phish.example.com/job"}, false); !errors.Is(err, ErrActionLinkUnsafe) {
			t.Fatalf("err = %v", err)
		}
		if r.store.Replaces != 0 {
			t.Fatal("an unsafe link was written")
		}
	})
	t.Run("safe is stored safe", func(t *testing.T) {
		r := newActionRig(t)
		v, err := r.svc.SetPostAction(ctx, r.author, r.post(), applyNow, false)
		if err != nil {
			t.Fatal(err)
		}
		if l := r.store.Get(v.ID); l.SafetyStatus != postgres.ActionSafetySafe || l.SafetyCheckedAt == nil {
			t.Fatalf("stored %+v", l)
		}
	})
	t.Run("checker down is 503, dev and production alike", func(t *testing.T) {
		for _, prod := range []bool{false, true} {
			r := newActionRig(t)
			r.checker.err = actionlinks.ErrCheckUnavailable
			r.configure(prod, r.checker)
			if _, err := r.svc.SetPostAction(ctx, r.author, r.post(), applyNow, false); !errors.Is(err, ErrActionLinkCheckUnavailable) {
				t.Fatalf("production=%v: err = %v", prod, err)
			}
		}
	})
	t.Run("no checker in production refuses", func(t *testing.T) {
		r := newActionRig(t)
		r.configure(true, nil)
		if _, err := r.svc.SetPostAction(ctx, r.author, r.post(), applyNow, false); !errors.Is(err, ErrActionLinkCheckUnavailable) {
			t.Fatalf("err = %v", err)
		}
		if r.store.Replaces != 0 {
			t.Fatal("production stored an unchecked link")
		}
	})
	t.Run("no checker in dev stores unchecked", func(t *testing.T) {
		r := newActionRig(t)
		r.configure(false, nil)
		v, err := r.svc.SetPostAction(ctx, r.author, r.post(), applyNow, false)
		if err != nil {
			t.Fatal(err)
		}
		if l := r.store.Get(v.ID); l.SafetyStatus != postgres.ActionSafetyUnchecked {
			t.Fatalf("stored %+v", l)
		}
	})
	t.Run("own domain skips the lookup", func(t *testing.T) {
		r := newActionRig(t)
		r.configure(true, r.checker)
		v, err := r.svc.SetPostAction(ctx, r.author, r.post(), &ActionInput{Label: "watch_more", URL: "https://cleestudio.com/@ravi"}, false)
		if err != nil || r.checker.calls != 0 {
			t.Fatalf("err = %v, lookups = %d", err, r.checker.calls)
		}
		if r.store.Get(v.ID).SafetyStatus != postgres.ActionSafetySafe {
			t.Fatal("own domain not stored safe")
		}
	})
}

// The cheap refusals come first: a bad label or URL asks no one.
func TestActionShapeRefusedBeforeAnyLookup(t *testing.T) {
	for _, tc := range []struct {
		in   ActionInput
		want error
	}{
		{ActionInput{Label: "buy_now", URL: "https://example.com"}, ErrInvalidActionLabel},
		{ActionInput{Label: "", URL: "https://example.com"}, ErrInvalidActionLabel},
		{ActionInput{Label: "apply_now", URL: "http://example.com"}, actionlinks.ErrInvalidURL},
		{ActionInput{Label: "apply_now", URL: ""}, actionlinks.ErrInvalidURL},
		{ActionInput{Label: "apply_now", URL: "https://bit.ly/x"}, actionlinks.ErrShortener},
		{ActionInput{Label: "apply_now", URL: "https://chat.whatsapp.com/x"}, actionlinks.ErrGroupInvite},
	} {
		r := newActionRig(t)
		in := tc.in
		_, err := r.svc.SetPostAction(context.Background(), r.author, r.post(), &in, false)
		if !errors.Is(err, tc.want) {
			t.Errorf("%+v: err = %v, want %v", tc.in, err, tc.want)
		}
		if r.accounts.calls+r.emails.calls+r.checker.calls != 0 {
			t.Errorf("%+v: refused after %d lookups", tc.in, r.accounts.calls+r.emails.calls+r.checker.calls)
		}
		if !IsTerminalActionError(err) {
			t.Errorf("%+v: not terminal", tc.in)
		}
	}
	if IsTerminalActionError(ErrActionLinkCheckUnavailable) {
		t.Fatal("an unavailable check must stay retryable")
	}
}

// Replace semantics: same URL keeps the id, a new URL is a new id (the old
// one stops resolving), null removes; someone else's post is refused.
func TestSetPostActionReplace(t *testing.T) {
	ctx := context.Background()
	r := newActionRig(t)
	post := r.post()
	v1, err := r.svc.SetPostAction(ctx, r.author, post, applyNow, false)
	if err != nil {
		t.Fatal(err)
	}
	if v1.URL != "https://www.epfindia.gov.in/apply" || v1.Status != "active" || v1.Domain != "epfindia.gov.in" || v1.Href != "/l/"+v1.ID.String() {
		t.Fatalf("author view = %+v", v1)
	}
	v2, err := r.svc.SetPostAction(ctx, r.author, post, &ActionInput{Label: "learn_more", URL: applyNow.URL}, false)
	if err != nil || v2.ID != v1.ID || v2.Label != "learn_more" || v2.LabelText != "Learn more" {
		t.Fatalf("label change: %+v %v, want the same id", v2, err)
	}
	v3, err := r.svc.SetPostAction(ctx, r.author, post, &ActionInput{Label: "learn_more", URL: "https://example.org/jobs"}, false)
	if err != nil || v3.ID == v1.ID {
		t.Fatalf("URL change: %+v %v, want a new id", v3, err)
	}
	if r.store.Get(v1.ID).Status != postgres.ActionStatusRemoved {
		t.Fatal("the replaced link is still live")
	}
	if _, err := r.svc.ResolveActionLink(ctx, v1.ID, uuid.Nil, "k"); !errors.Is(err, ErrActionLinkNotFound) {
		t.Fatalf("old share still resolves: %v", err)
	}
	if v, err := r.svc.SetPostAction(ctx, r.author, post, nil, true); err != nil || v != nil {
		t.Fatalf("remove: %+v %v", v, err)
	}
	if r.store.Get(v3.ID).Status != postgres.ActionStatusRemoved {
		t.Fatal("removed link is still live")
	}
	if _, err := r.svc.SetPostAction(ctx, uuid.New(), post, applyNow, false); !errors.Is(err, ErrNotPostAuthor) {
		t.Fatalf("stranger: %v", err)
	}
	if _, err := r.svc.SetPostAction(ctx, r.author, uuid.New(), applyNow, false); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("missing post: %v", err)
	}
}

// The read shape: viewers see an active link only and never its URL; the
// author sees every live state with url, status and why.
func TestActionReadShape(t *testing.T) {
	r := newActionRig(t)
	viewer := uuid.New()
	mk := func(status, reason string) (*postgres.Post, uuid.UUID) {
		post := r.post()
		var why *string
		if reason != "" {
			why = &reason
		}
		l := r.store.Insert(postgres.ActionLink{PostID: &post, AuthorID: r.author, URL: "https://example.com/" + status,
			Domain: "example.com", Label: "sign_up", Status: status, StatusReason: why})
		return &postgres.Post{ID: post, AuthorID: r.author}, l.ID
	}
	active, activeID := mk("active", "")
	blocked, _ := mk("blocked", "unsafe")
	review, _ := mk("under_review", "reports")
	removed, _ := mk("removed", "")
	none := &postgres.Post{ID: r.post(), AuthorID: r.author}
	rows := func() []*postgres.Post {
		return []*postgres.Post{
			{ID: active.ID, AuthorID: r.author}, {ID: blocked.ID, AuthorID: r.author},
			{ID: review.ID, AuthorID: r.author}, {ID: removed.ID, AuthorID: r.author}, {ID: none.ID, AuthorID: r.author}, nil,
		}
	}

	seen := rows()
	r.svc.redactForViewer(context.Background(), &viewer, seen)
	want := &postgres.ActionView{ID: activeID, Label: "sign_up", LabelText: "Sign up", Domain: "example.com", Href: "/l/" + activeID.String()}
	if !reflect.DeepEqual(seen[0].Action, want) {
		t.Fatalf("viewer's active action = %+v, want %+v", seen[0].Action, want)
	}
	for i := 1; i < 5; i++ {
		if seen[i].Action != nil {
			t.Errorf("viewer row %d carries %+v", i, seen[i].Action)
		}
	}
	raw, _ := json.Marshal(seen[0])
	var wire map[string]any
	_ = json.Unmarshal(raw, &wire)
	act := wire["action"].(map[string]any)
	for _, k := range []string{"url", "status", "status_reason"} {
		if _, ok := act[k]; ok {
			t.Errorf("viewer wire carries %q: %v", k, act)
		}
	}
	if r.store.Lookups != 1 {
		t.Fatalf("lookups = %d for one page, want 1", r.store.Lookups)
	}

	own := rows()
	r.svc.redactForViewer(context.Background(), &r.author, own)
	for i, wantStatus := range []string{"active", "blocked", "under_review"} {
		a := own[i].Action
		if a == nil || a.Status != wantStatus || a.URL != "https://example.com/"+wantStatus {
			t.Errorf("author row %d = %+v, want status %s with url", i, a, wantStatus)
		}
	}
	if own[1].Action.StatusReason != "unsafe" || own[2].Action.StatusReason != "reports" || own[0].Action.StatusReason != "" {
		t.Errorf("status reasons: %q %q %q", own[0].Action.StatusReason, own[1].Action.StatusReason, own[2].Action.StatusReason)
	}
	if own[3].Action != nil || own[4].Action != nil {
		t.Error("removed / none carries an action for the author")
	}

	signedOut := rows()
	r.svc.redactForViewer(context.Background(), nil, signedOut)
	if signedOut[0].Action == nil || signedOut[0].Action.URL != "" || signedOut[1].Action != nil {
		t.Fatalf("signed out: %+v %+v", signedOut[0].Action, signedOut[1].Action)
	}

	// A store that fails shows no button anywhere (never a stale one).
	r.store.Err = actionfake.ErrDown
	failed := rows()
	failed[0].Action = want
	r.svc.redactForViewer(context.Background(), &viewer, failed)
	if failed[0].Action != nil {
		t.Fatal("a failed lookup left a button on the row")
	}
}

// Members-only: a non-member loses the action with the body; a member and
// the author keep it.
func TestActionMembersOnlyRedaction(t *testing.T) {
	author, tier, member, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f := &membersFake{member: map[uuid.UUID]map[uuid.UUID]bool{member: {tier: true}}}
	store := actionfake.New()
	for _, viewer := range []struct {
		id   *uuid.UUID
		want bool
	}{{&stranger, false}, {nil, false}, {&member, true}, {&author, true}} {
		s := f.svc()
		s.SetActionLinks(ActionLinkConfig{Store: store})
		row := gatedRow(author, tier)
		store.Posts[row.ID] = author
		store.Insert(postgres.ActionLink{PostID: &row.ID, AuthorID: author, URL: "https://example.com/paid", Domain: "example.com", Label: "shop_now"})
		s.redactForViewer(context.Background(), viewer.id, []*postgres.Post{row})
		if (row.Action != nil) != viewer.want {
			t.Errorf("viewer %v: action = %+v, want present=%v", viewer.id, row.Action, viewer.want)
		}
	}
}

// The profile button: the owner's view, a viewer's, and none.
func TestProfileActionView(t *testing.T) {
	ctx := context.Background()
	r := newActionRig(t)
	v, err := r.svc.SetProfileAction(ctx, r.author, &ActionInput{Label: "contact_us", URL: "https://example.com/contact"}, false)
	if err != nil || v.Status != "active" || v.URL == "" {
		t.Fatalf("set: %+v %v", v, err)
	}
	if got := r.svc.ProfileActionView(ctx, r.author, uuid.New()); got == nil || got.URL != "" || got.Status != "" || got.ID != v.ID {
		t.Fatalf("viewer view: %+v", got)
	}
	if got := r.svc.ProfileActionView(ctx, r.author, r.author); got == nil || got.URL != "https://example.com/contact" {
		t.Fatalf("owner view: %+v", got)
	}
	reason := postgres.ActionReasonModerator
	l := r.store.Links[v.ID]
	l.Status, l.StatusReason = postgres.ActionStatusBlocked, &reason
	if got := r.svc.ProfileActionView(ctx, r.author, uuid.New()); got != nil {
		t.Fatalf("a blocked profile button shows: %+v", got)
	}
	if got := r.svc.ProfileActionView(ctx, r.author, r.author); got == nil || got.Status != "blocked" || got.StatusReason != "moderator" {
		t.Fatalf("owner of a blocked button: %+v", got)
	}
	if _, err := r.svc.SetProfileAction(ctx, r.author, nil, true); err != nil {
		t.Fatal(err)
	}
	if got := r.svc.ProfileActionView(ctx, r.author, r.author); got != nil {
		t.Fatalf("removed: %+v", got)
	}
	r.emails.ok = false
	if _, err := r.svc.SetProfileAction(ctx, r.author, &ActionInput{Label: "contact_us", URL: "https://example.com"}, false); !errors.Is(err, ErrActionNotEligible) {
		t.Fatalf("profile eligibility: %v", err)
	}
}

// The redirect: active only, one count per tapper per window, the author's
// own taps uncounted, a failed dedupe counts nothing, members-only gated.
func TestResolveActionLinkCountsDeduplicatedTaps(t *testing.T) {
	ctx := context.Background()
	r := newActionRig(t)
	post := r.post()
	v, err := r.svc.SetPostAction(ctx, r.author, post, applyNow, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.svc.ResolveActionLink(ctx, v.ID, uuid.Nil, "h:abc")
	if err != nil || got.URL != applyNow.URL || got.Domain != "epfindia.gov.in" || got.Label != "apply_now" || got.Status != "active" {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	_, _ = r.svc.ResolveActionLink(ctx, v.ID, uuid.Nil, "h:abc")
	_, _ = r.svc.ResolveActionLink(ctx, v.ID, uuid.Nil, "h:def")
	_, _ = r.svc.ResolveActionLink(ctx, v.ID, r.author, "u:"+r.author.String())
	if n := r.store.Get(v.ID).ClicksTotal; n != 2 {
		t.Fatalf("clicks = %d, want 2 (two tappers; a repeat and the author's own not counted)", n)
	}
	r.taps.err = errors.New("redis down")
	_, _ = r.svc.ResolveActionLink(ctx, v.ID, uuid.Nil, "h:ghi")
	if n := r.store.Get(v.ID).ClicksTotal; n != 2 {
		t.Fatalf("clicks = %d after a dedupe outage, want 2", n)
	}
	r.taps.err = nil

	// Stats: author only.
	r.svc.postGateSeam = func(context.Context, uuid.UUID) (uuid.UUID, *uuid.UUID, error) { return r.author, nil, nil }
	stats, err := r.svc.PostActionStats(ctx, r.author, post)
	if err != nil || stats.ClicksTotal != 2 || stats.Clicks7d != 2 {
		t.Fatalf("stats: %+v %v", stats, err)
	}
	if _, err := r.svc.PostActionStats(ctx, uuid.New(), post); !errors.Is(err, ErrNotPostAuthor) {
		t.Fatalf("stranger stats: %v", err)
	}

	// Not active, or the post deleted: 404, nothing counted.
	r.store.Deleted[post] = true
	if _, err := r.svc.ResolveActionLink(ctx, v.ID, uuid.Nil, "h:new"); !errors.Is(err, ErrActionLinkNotFound) {
		t.Fatalf("deleted post: %v", err)
	}
	delete(r.store.Deleted, post)
	reason := postgres.ActionReasonReports
	r.store.Links[v.ID].Status, r.store.Links[v.ID].StatusReason = postgres.ActionStatusUnderReview, &reason
	if _, err := r.svc.ResolveActionLink(ctx, v.ID, uuid.Nil, "h:new"); !errors.Is(err, ErrActionLinkNotFound) {
		t.Fatalf("under review: %v", err)
	}
	if _, err := r.svc.ResolveActionLink(ctx, uuid.New(), uuid.Nil, "h:new"); !errors.Is(err, ErrActionLinkNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestResolveActionLinkOnMembersOnlyPost(t *testing.T) {
	ctx := context.Background()
	author, tier, member := uuid.New(), uuid.New(), uuid.New()
	f := &membersFake{member: map[uuid.UUID]map[uuid.UUID]bool{member: {tier: true}}}
	s := f.svc()
	store := actionfake.New()
	s.SetActionLinks(ActionLinkConfig{Store: store, TapDedupe: &fakeTaps{}})
	post := uuid.New()
	store.Posts[post], store.Tiers[post] = author, tier
	l := store.Insert(postgres.ActionLink{PostID: &post, AuthorID: author, URL: "https://example.com/paid", Domain: "example.com", Label: "shop_now"})
	if _, err := s.ResolveActionLink(ctx, l.ID, uuid.New(), "u:x"); !errors.Is(err, ErrActionLinkNotFound) {
		t.Fatalf("non-member resolved a members-only link: %v", err)
	}
	if _, err := s.ResolveActionLink(ctx, l.ID, uuid.Nil, "h:x"); !errors.Is(err, ErrActionLinkNotFound) {
		t.Fatalf("signed-out viewer resolved a members-only link: %v", err)
	}
	if _, err := s.ResolveActionLink(ctx, l.ID, member, "u:m"); err != nil {
		t.Fatalf("member: %v", err)
	}
}

// Three distinct reporters within the window put a live link under review;
// the author's own report, a repeat, and reports older than the link do
// not count; after a moderator's restore three NEW reporters are needed.
func TestScamLinkReportThreshold(t *testing.T) {
	ctx := context.Background()
	r := newActionRig(t)
	post := r.post()
	v, err := r.svc.SetPostAction(ctx, r.author, post, applyNow, false)
	if err != nil {
		t.Fatal(err)
	}
	report := func(reporter uuid.UUID, at time.Time) postgres.ActionLinkReportOutcome {
		out, err := r.svc.ApplyScamLinkReport(ctx, uuid.New(), reporter, "post", post, at)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	report(r.author, actionNow)               // the author: not counted
	report(a, actionNow)                      // 1
	report(a, actionNow)                      // the same reporter again: still 1
	report(b, actionNow.Add(-8*24*time.Hour)) // before the link existed / outside the window
	if out := report(b, actionNow); out.Held || out.Reporters != 2 {
		t.Fatalf("two reporters: %+v", out)
	}
	if r.store.Get(v.ID).Status != postgres.ActionStatusActive {
		t.Fatal("held below the threshold")
	}
	out := report(c, actionNow)
	if !out.Held || out.Reporters != 3 {
		t.Fatalf("third reporter: %+v", out)
	}
	if l := r.store.Get(v.ID); l.Status != postgres.ActionStatusUnderReview || *l.StatusReason != "reports" {
		t.Fatalf("link = %+v, want under_review / reports", l)
	}
	if _, err := r.svc.ResolveActionLink(ctx, v.ID, uuid.Nil, "h:z"); !errors.Is(err, ErrActionLinkNotFound) {
		t.Fatal("a link under review still resolves")
	}

	// A moderator restores it: the old reports are spent.
	later := actionNow.Add(time.Minute)
	r.store.Now = func() time.Time { return later }
	r.svc.now = func() time.Time { return later }
	if _, err := r.svc.ReviewActionLink(ctx, uuid.New(), v.ID, postgres.ActionDecisionRestore, "checked, genuine"); err != nil {
		t.Fatal(err)
	}
	r.svc.now = func() time.Time { return later.Add(2 * time.Second) }
	if out := report(uuid.New(), later.Add(time.Second)); out.Held || out.Reporters != 1 {
		t.Fatalf("after restore: %+v, want a fresh count", out)
	}
	if _, err := r.svc.ReviewActionLink(ctx, uuid.New(), v.ID, postgres.ActionDecisionRestore, ""); !errors.Is(err, postgres.ErrActionLinkTransition) {
		t.Fatalf("restore of an active link: %v", err)
	}
	if out, err := r.svc.ApplyScamLinkReport(ctx, uuid.New(), a, "user", uuid.New(), actionNow); err != nil || out.Found {
		t.Fatalf("a profile without a button: %+v %v", out, err)
	}
}

// The daily recheck: a link turning unsafe is blocked; a failed lookup
// changes nothing; own domains need no lookup; nothing runs without a
// checker.
func TestRecheckBlocksUnsafeLinks(t *testing.T) {
	ctx := context.Background()
	r := newActionRig(t)
	old := actionNow.Add(-25 * time.Hour)
	mk := func(url, domain string) uuid.UUID {
		p := r.post()
		return r.store.Insert(postgres.ActionLink{PostID: &p, AuthorID: r.author, URL: url, Domain: domain, Label: "apply_now",
			SafetyStatus: postgres.ActionSafetySafe, SafetyCheckedAt: &old}).ID
	}
	turned := mk("https://turned.example.com/job", "turned.example.com")
	fine := mk("https://fine.example.com", "fine.example.com")
	own := mk("https://cleestudio.com/x", "cleestudio.com")
	fresh := actionNow.Add(-time.Hour)
	p := r.post()
	recent := r.store.Insert(postgres.ActionLink{PostID: &p, AuthorID: r.author, URL: "https://recent.example.com", Domain: "recent.example.com",
		Label: "apply_now", SafetyStatus: postgres.ActionSafetySafe, SafetyCheckedAt: &fresh}).ID
	r.checker.unsafe["https://turned.example.com/job"] = true

	rep, err := r.svc.RecheckActionLinks(ctx, 50)
	if err != nil || rep.Checked != 3 || rep.Blocked != 1 {
		t.Fatalf("report = %+v %v", rep, err)
	}
	if r.checker.calls != 2 {
		t.Fatalf("lookups = %d, want 2 (own domain skipped, a fresh verdict not rechecked)", r.checker.calls)
	}
	if l := r.store.Get(turned); l.Status != postgres.ActionStatusBlocked || *l.StatusReason != "unsafe" || l.SafetyStatus != "unsafe" {
		t.Fatalf("turned unsafe: %+v", l)
	}
	for _, id := range []uuid.UUID{fine, own, recent} {
		if l := r.store.Get(id); l.Status != postgres.ActionStatusActive {
			t.Fatalf("%s: %+v", l.URL, l)
		}
	}
	// The author sees why; viewers see nothing.
	row := []*postgres.Post{{ID: *r.store.Get(turned).PostID, AuthorID: r.author}}
	r.svc.redactForViewer(ctx, &r.author, row)
	if row[0].Action == nil || row[0].Action.Status != "blocked" || row[0].Action.StatusReason != "unsafe" {
		t.Fatalf("author view of a blocked link: %+v", row[0].Action)
	}

	// An outage: unchanged, counted as unavailable, claimed (not retried at once).
	r2 := newActionRig(t)
	p2 := r2.post()
	id := r2.store.Insert(postgres.ActionLink{PostID: &p2, AuthorID: r2.author, URL: "https://x.example.com", Domain: "x.example.com", Label: "apply_now"}).ID
	r2.checker.err = actionlinks.ErrCheckUnavailable
	if rep, _ := r2.svc.RecheckActionLinks(ctx, 50); rep.Unavailable != 1 || rep.Checked != 0 {
		t.Fatalf("outage report = %+v", rep)
	}
	if l := r2.store.Get(id); l.Status != postgres.ActionStatusActive || l.SafetyCheckedAt != nil {
		t.Fatalf("outage changed the link: %+v", l)
	}
	if rep, _ := r2.svc.RecheckActionLinks(ctx, 50); rep.Unavailable != 0 {
		t.Fatalf("a claimed link was retried at once: %+v", rep)
	}
	r2.configure(false, nil)
	if rep, _ := r2.svc.RecheckActionLinks(ctx, 50); rep != (ActionRecheckReport{}) {
		t.Fatalf("no checker ran a recheck: %+v", rep)
	}
}

// CreatePost writes the action in the post's own write and answers the
// author's view; a refusal writes nothing.
func TestCreatePostWithAction(t *testing.T) {
	r := newCreateRig(t)
	a := newActionRig(t)
	r.svc.now = a.svc.now
	r.svc.SetStandingChecker(a.standing)
	r.svc.SetActionLinks(ActionLinkConfig{Checker: a.checker, Accounts: a.accounts, Emails: a.emails, Store: a.store})
	in := r.input("post", nil)
	in.Action = &ActionInput{Label: "register", URL: "https://Example.com/Register"}
	p, err := r.svc.CreatePost(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	w := r.written[0].ActionWrite
	if w == nil || w.ID == uuid.Nil || w.URL != "https://example.com/Register" || w.Domain != "example.com" || w.Label != "register" || w.SafetyStatus != "safe" {
		t.Fatalf("the write carries %+v", w)
	}
	if p.Action == nil || p.Action.ID != w.ID || p.Action.URL != w.URL || p.Action.Status != "active" || p.Action.Href != "/l/"+w.ID.String() {
		t.Fatalf("the create answer = %+v", p.Action)
	}

	a.emails.ok = false
	in2 := r.input("post", nil)
	in2.Action = &ActionInput{Label: "register", URL: "https://example.com"}
	if _, err := r.svc.CreatePost(context.Background(), in2); !errors.Is(err, ErrActionNotEligible) {
		t.Fatalf("ineligible create: %v", err)
	}
	if len(r.written) != 1 {
		t.Fatalf("a refused create wrote a post (%d writes)", len(r.written))
	}
	if p, err := r.svc.CreatePost(context.Background(), r.input("post", nil)); err != nil || p.Action != nil || r.written[1].ActionWrite != nil {
		t.Fatalf("no action: %+v %v", p, err)
	}
}

func TestParseActionPatch(t *testing.T) {
	for raw, want := range map[string]struct{ present, clear, ok bool }{
		``:       {false, false, true},
		`null`:   {true, true, true},
		` null `: {true, true, true},
		`{"label":"apply_now","url":"https://x.com"}`: {true, false, true},
		`"apply_now"`: {true, false, false},
		`[]`:          {true, false, false},
		`{"label":"apply_now","url":"https://x.com","extra":1}`: {true, false, false},
	} {
		in, clear, present, err := ParseActionPatch(json.RawMessage(raw))
		if present != want.present || clear != want.clear || (err == nil) != want.ok {
			t.Errorf("%q: in=%+v clear=%v present=%v err=%v", raw, in, clear, present, err)
		}
		if err != nil && !errors.Is(err, ErrInvalidActionShape) {
			t.Errorf("%q: err = %v", raw, err)
		}
	}
}

// A removed link is never shown, to its author or anyone; a page whose
// rows are all redacted for this viewer asks the store nothing.
func TestActionViewForRemovedAndRedactedPages(t *testing.T) {
	removed := &postgres.ActionLink{ID: uuid.New(), Label: "apply_now", Domain: "example.com", URL: "https://example.com", Status: postgres.ActionStatusRemoved}
	if ActionViewFor(removed, true) != nil || ActionViewFor(removed, false) != nil || ActionViewFor(nil, true) != nil {
		t.Fatal("a removed (or missing) link has a view")
	}
	r := newActionRig(t)
	rows := []*postgres.Post{{ID: r.post(), AuthorID: r.author, BodyRedacted: true}, {ID: r.post(), AuthorID: r.author, BodyRedacted: true}}
	r.svc.attachActions(context.Background(), uuid.New(), rows)
	if r.store.Lookups != 0 {
		t.Fatalf("lookups = %d for a page with nothing to show", r.store.Lookups)
	}
}

// leakyStore answers a resolvable link whatever its status: the service
// must still refuse anything but active.
type leakyStore struct{ *actionfake.Store }

func (l leakyStore) GetResolvableActionLink(_ context.Context, id uuid.UUID) (*postgres.ResolvableActionLink, error) {
	if link := l.Get(id); link != nil {
		return &postgres.ResolvableActionLink{Link: link}, nil
	}
	return nil, nil
}

func TestResolveRefusesANonActiveLinkWhateverTheStoreSays(t *testing.T) {
	store := actionfake.New()
	owner := uuid.New()
	reason := postgres.ActionReasonModerator
	l := store.Insert(postgres.ActionLink{ProfileOwnerID: &owner, AuthorID: owner, URL: "https://example.com", Domain: "example.com",
		Label: "contact_us", Status: postgres.ActionStatusBlocked, StatusReason: &reason})
	s := &Service{}
	s.SetActionLinks(ActionLinkConfig{Store: leakyStore{store}})
	if _, err := s.ResolveActionLink(context.Background(), l.ID, uuid.Nil, "h:x"); !errors.Is(err, ErrActionLinkNotFound) {
		t.Fatalf("a blocked link resolved: %v", err)
	}
}

// Reports older than the window do not count, however old the link is.
func TestScamLinkReportWindow(t *testing.T) {
	ctx := context.Background()
	r := newActionRig(t)
	post := r.post()
	r.store.Insert(postgres.ActionLink{PostID: &post, AuthorID: r.author, URL: "https://example.com/job", Domain: "example.com",
		Label: "apply_now", CreatedAt: actionNow.AddDate(0, -1, 0)})
	for i := 0; i < 2; i++ {
		if _, err := r.svc.ApplyScamLinkReport(ctx, uuid.New(), uuid.New(), "post", post, actionNow.Add(-8*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := r.svc.ApplyScamLinkReport(ctx, uuid.New(), uuid.New(), "post", post, actionNow)
	if err != nil || out.Held || out.Reporters != 1 {
		t.Fatalf("two reports 8 days old + one now: %+v %v, want 1 reporter in the window", out, err)
	}
}

// Without a checker the recheck does nothing at all (and never calls one).
func TestRecheckWithoutAChecker(t *testing.T) {
	r := newActionRig(t)
	p := r.post()
	id := r.store.Insert(postgres.ActionLink{PostID: &p, AuthorID: r.author, URL: "https://example.com", Domain: "example.com", Label: "apply_now"}).ID
	r.configure(false, nil)
	rep, err := r.svc.RecheckActionLinks(context.Background(), 50)
	if err != nil || rep != (ActionRecheckReport{}) {
		t.Fatalf("report = %+v %v", rep, err)
	}
	if _, claimed := r.store.Claims[id]; claimed {
		t.Fatal("a recheck without a checker claimed a link")
	}
}
