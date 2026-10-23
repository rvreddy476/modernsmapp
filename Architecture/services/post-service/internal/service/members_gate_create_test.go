package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/post-service/internal/spam"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Members-only from birth (2026-10-23, members_tiers.go). Clients used to
// create a post and then PUT its gate, so for a moment every members-only
// post was public — feed fan-out, search and the subscriber pushes all saw
// it open. These pin the gate on the create itself.

// tierFake is monetization-service's public tier list for the tests: the
// tiers each creator has (active or not), or an error, and how often it was
// asked.
type tierFake struct {
	tiers map[uuid.UUID][]CreatorTierRef
	err   error
	asks  map[uuid.UUID]int
}

func (f *tierFake) list(_ context.Context, creator uuid.UUID) ([]CreatorTierRef, error) {
	if f.asks == nil {
		f.asks = map[uuid.UUID]int{}
	}
	f.asks[creator]++
	if f.err != nil {
		return nil, f.err
	}
	return f.tiers[creator], nil
}

// createRig is a Service that can run CreatePost to the end without a
// database: the one write is captured, and the producer is set so the
// PostCreated event is built exactly as in production.
type createRig struct {
	svc      *Service
	tiers    *tierFake
	author   uuid.UUID
	active   uuid.UUID // the author's active tier
	inactive uuid.UUID // the author's switched-off tier
	foreign  uuid.UUID // another creator's active tier
	written  []*postgres.Post
	events   []string
	payloads []interface{}
}

func newCreateRig(t *testing.T) *createRig {
	t.Helper()
	r := &createRig{author: uuid.New(), active: uuid.New(), inactive: uuid.New(), foreign: uuid.New()}
	other := uuid.New()
	r.tiers = &tierFake{tiers: map[uuid.UUID][]CreatorTierRef{
		r.author: {
			{ID: r.active, CreatorID: r.author, PricePaise: 4900, IsActive: true},
			{ID: r.inactive, CreatorID: r.author, PricePaise: 9900, IsActive: false},
		},
		other: {{ID: r.foreign, CreatorID: other, PricePaise: 2900, IsActive: true}},
	}}
	r.svc = &Service{
		creatorTiersSeam: r.tiers.list,
		spam:             spam.New(nil),
		producer:         postEvents.NewProducer([]string{"127.0.0.1:1"}, "post.events.test"),
		createPostWriteSeam: func(_ context.Context, p *postgres.Post, eventType string, payload interface{}, _ *postgres.CreateIdempotency) error {
			cp := *p
			r.written = append(r.written, &cp)
			r.events = append(r.events, eventType)
			r.payloads = append(r.payloads, payload)
			return nil
		},
	}
	return r
}

func (r *createRig) input(contentType string, tier *uuid.UUID) *CreatePostInput {
	in := &CreatePostInput{
		AuthorID: r.author, Text: "the paid body, for members", Visibility: "public",
		ContentType: contentType, AllowDownload: true, TierRequiredID: tier,
	}
	if contentType == "poll" {
		in.Poll = &CreatePollInput{Question: "Which episode next?", Options: []string{"One", "Two"}}
	}
	return in
}

// A gated create writes the gate on the row in the one write that also
// carries PostCreated, and that event already says tier_required_id and
// carries no body: there is no instant at which the post is open.
func TestCreatePostIsGatedFromBirth(t *testing.T) {
	for _, kind := range []string{"post", "poll"} {
		t.Run(kind, func(t *testing.T) {
			r := newCreateRig(t)
			tier := r.active
			p, err := r.svc.CreatePost(context.Background(), r.input(kind, &tier))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.written) != 1 {
				t.Fatalf("writes = %d, want exactly one", len(r.written))
			}
			row := r.written[0]
			if row.TierRequiredID == nil || *row.TierRequiredID != r.active {
				t.Fatalf("the row was written without its gate: %v", row.TierRequiredID)
			}
			if p.TierRequiredID == nil || *p.TierRequiredID != r.active {
				t.Fatalf("the create response lost the gate: %v", p.TierRequiredID)
			}
			if r.events[0] != events.PostCreated {
				t.Fatalf("event type = %q, want PostCreated in the same write", r.events[0])
			}
			pc, ok := r.payloads[0].(events.PostCreatedPayload)
			if !ok {
				t.Fatalf("payload is %T", r.payloads[0])
			}
			if pc.TierRequiredID != r.active.String() {
				t.Fatalf("PostCreated is ungated: tier_required_id = %q", pc.TierRequiredID)
			}
			if pc.Text != "" || pc.Media != nil {
				t.Fatalf("a gated PostCreated carries its body: text=%q media=%v", pc.Text, pc.Media)
			}
			raw, _ := json.Marshal(pc)
			if strings.Contains(string(raw), "paid body") {
				t.Fatalf("the paid text rode the bus: %s", raw)
			}
		})
	}
}

// An open create is unchanged: no tier lookup, no gate, the body on the
// event as before.
func TestCreatePostOpenIsUnchanged(t *testing.T) {
	r := newCreateRig(t)
	if _, err := r.svc.CreatePost(context.Background(), r.input("post", nil)); err != nil {
		t.Fatal(err)
	}
	if len(r.tiers.asks) != 0 {
		t.Fatalf("an open post asked monetization-service for tiers: %v", r.tiers.asks)
	}
	pc := r.payloads[0].(events.PostCreatedPayload)
	if r.written[0].TierRequiredID != nil || pc.TierRequiredID != "" || pc.Text != "the paid body, for members" {
		t.Fatalf("open post: row tier %v, event %+v", r.written[0].TierRequiredID, pc)
	}
}

// Not one of the author's active tiers — another creator's, a switched-off
// one, an unknown id, the nil id — is ErrInvalidTier; a tier list
// monetization-service cannot answer is ErrTierLookupUnavailable. Nothing is
// written either way: never a post created open.
func TestCreatePostRefusesABadTierBeforeWriting(t *testing.T) {
	cases := []struct {
		name string
		tier func(r *createRig) uuid.UUID
		down bool
		want error
	}{
		{"another creator's tier", func(r *createRig) uuid.UUID { return r.foreign }, false, ErrInvalidTier},
		{"a switched-off tier", func(r *createRig) uuid.UUID { return r.inactive }, false, ErrInvalidTier},
		{"an unknown tier", func(*createRig) uuid.UUID { return uuid.New() }, false, ErrInvalidTier},
		{"the nil id", func(*createRig) uuid.UUID { return uuid.Nil }, false, ErrInvalidTier},
		{"monetization down", func(r *createRig) uuid.UUID { return r.active }, true, ErrTierLookupUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newCreateRig(t)
			if tc.down {
				r.tiers.err = errors.New("dial tcp: connection refused")
			}
			tier := tc.tier(r)
			p, err := r.svc.CreatePost(context.Background(), r.input("post", &tier))
			if !errors.Is(err, tc.want) || p != nil {
				t.Fatalf("post=%v err=%v, want %v", p, err, tc.want)
			}
			if len(r.written) != 0 {
				t.Fatalf("a refused create wrote %d rows", len(r.written))
			}
			// The nil id is refused without asking monetization-service.
			if tier == uuid.Nil && len(r.tiers.asks) != 0 {
				t.Fatalf("the nil id was looked up: %v", r.tiers.asks)
			}
		})
	}
	// A tier list that (wrongly) names someone else's tier under the
	// author is not believed.
	r := newCreateRig(t)
	smuggled := uuid.New()
	r.tiers.tiers[r.author] = append(r.tiers.tiers[r.author], CreatorTierRef{ID: smuggled, CreatorID: uuid.New(), PricePaise: 100, IsActive: true})
	if _, err := r.svc.CreatePost(context.Background(), r.input("post", &smuggled)); !errors.Is(err, ErrInvalidTier) || len(r.written) != 0 {
		t.Fatalf("a tier of another creator in the author's list: err=%v writes=%d", err, len(r.written))
	}
}

// The tier list over the wire: the public route, the internal key, only the
// creator's own active tiers counted, and every failure (5xx, unreachable,
// malformed, URL unset) unavailable — never a yes.
func TestValidateTierOverTheTierList(t *testing.T) {
	author, active, inactive, foreign := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	var gotKey, gotPath string
	mon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotPath = r.Header.Get("X-Internal-Service-Key"), r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"id": active, "creator_id": author, "price_paise": 4900, "is_active": true},
			{"id": inactive, "creator_id": author, "price_paise": 9900, "is_active": false},
			// A route that answered someone else's tier is not believed.
			{"id": foreign, "creator_id": uuid.New(), "price_paise": 2900, "is_active": true},
		}})
	}))
	defer mon.Close()
	// A 5xx is unavailable even when its body looks like a tier list.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"id": active, "creator_id": author, "price_paise": 4900, "is_active": true},
		}})
	}))
	defer broken.Close()
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":{"allowed":true}}`)) }))
	defer garbage.Close()
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	downURL := down.URL
	down.Close()

	svc := func(url string) *Service {
		return &Service{monetizationServiceURL: url, internalServiceKey: "k-test", httpClient: http.DefaultClient}
	}
	ctx := context.Background()
	if err := svc(mon.URL).ValidateTierForAuthor(ctx, author, &active); err != nil {
		t.Fatalf("own active tier: %v", err)
	}
	if gotKey != "k-test" || gotPath != "/v1/monetization/creators/"+author.String()+"/tiers" {
		t.Fatalf("asked %q with key %q", gotPath, gotKey)
	}
	for name, tier := range map[string]uuid.UUID{"inactive": inactive, "foreign": foreign, "unknown": uuid.New()} {
		tier := tier
		if err := svc(mon.URL).ValidateTierForAuthor(ctx, author, &tier); !errors.Is(err, ErrInvalidTier) {
			t.Errorf("%s: %v, want ErrInvalidTier", name, err)
		}
	}
	for name, url := range map[string]string{"503": broken.URL, "malformed": garbage.URL, "unreachable": downURL, "unset": ""} {
		if err := svc(url).ValidateTierForAuthor(ctx, author, &active); !errors.Is(err, ErrTierLookupUnavailable) {
			t.Errorf("%s: %v, want ErrTierLookupUnavailable", name, err)
		}
	}
	if err := svc("").ValidateTierForAuthor(ctx, author, nil); err != nil {
		t.Errorf("no gate needs no lookup: %v", err)
	}
}

// Both draft kinds keep the tier and hand it to CreatePost at publication.
func TestDraftsKeepAndPublishTheTier(t *testing.T) {
	tier := uuid.New()
	// Composer drafts: the payload field survives the strict decode and
	// reaches the create input.
	payload, err := ValidateDraftPayload("post", json.RawMessage(`{"text":"hi","tier_required_id":"`+tier.String()+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	d := &postgres.PostDraft{ID: uuid.New(), AuthorID: uuid.New(), PostType: "post"}
	if in := buildDraftCreateInput(d, payload); in.TierRequiredID == nil || *in.TierRequiredID != tier {
		t.Fatalf("composer draft publishes without its tier: %v", in.TierRequiredID)
	}
	if _, err := ValidateDraftPayload("post", json.RawMessage(`{"text":"hi","tier_required_id":"`+uuid.Nil.String()+`"}`)); !errors.Is(err, ErrInvalidTier) || !errors.Is(err, ErrInvalidDraft) {
		t.Fatalf("nil tier on a draft: %v", err)
	}

	// Reel / long-video drafts: the patch writes the column, and the
	// publish input carries it.
	for raw, want := range map[string]any{
		`"` + tier.String() + `"`: tier,
		`null`:                    nil,
		`""`:                      nil,
	} {
		updates := map[string]interface{}{}
		if err := draftTierUpdates(&UpdateDraftInput{TierRequiredID: json.RawMessage(raw)}, updates); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		got, ok := updates["tier_required_id"]
		if !ok {
			t.Fatalf("%s: no column write", raw)
		}
		if want == nil && got != nil || want != nil && got != want {
			t.Fatalf("%s: wrote %v, want %v", raw, got, want)
		}
	}
	for _, raw := range []string{`"gold"`, `42`, `{"id":"x"}`, `"` + uuid.Nil.String() + `"`} {
		updates := map[string]interface{}{}
		if err := draftTierUpdates(&UpdateDraftInput{TierRequiredID: json.RawMessage(raw)}, updates); !errors.Is(err, ErrInvalidTier) || len(updates) != 0 {
			t.Fatalf("%s: err=%v updates=%v, want ErrInvalidTier and no write", raw, err, updates)
		}
	}
	updates := map[string]interface{}{}
	if err := draftTierUpdates(&UpdateDraftInput{}, updates); err != nil || len(updates) != 0 {
		t.Fatalf("absent: err=%v updates=%v", err, updates)
	}
	rd := typicalDraft()
	rd.TierRequiredID = &tier
	if in := reelDraftPostInput(&rd, rd.AuthorID, "long_video"); in.TierRequiredID == nil || *in.TierRequiredID != tier {
		t.Fatalf("reel draft publishes without its tier: %v", in.TierRequiredID)
	}
}

// Edit after publish: a tier is checked like on create, null / "" clears
// without a lookup, and a refusal writes nothing.
func TestEditSetsAndClearsTheGate(t *testing.T) {
	svc, store, owner, post := newEditRig(t)
	active, foreign := uuid.New(), uuid.New()
	tiers := &tierFake{tiers: map[uuid.UUID][]CreatorTierRef{owner: {{ID: active, CreatorID: owner, PricePaise: 4900, IsActive: true}}}}
	svc.creatorTiersSeam = tiers.list
	ctx := context.Background()

	if _, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{TierRequiredID: &active}); err != nil {
		t.Fatal(err)
	}
	if n := len(store.updates); n != 1 || store.updates[0].TierRequiredID == nil || *store.updates[0].TierRequiredID != active {
		t.Fatalf("set: patches %+v", store.updates)
	}
	if _, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{TierRequiredID: &foreign}); !errors.Is(err, ErrInvalidTier) {
		t.Fatalf("foreign tier: %v", err)
	}
	tiers.err = errors.New("down")
	if _, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{TierRequiredID: &active}); !errors.Is(err, ErrTierLookupUnavailable) {
		t.Fatalf("tiers down: %v", err)
	}
	if len(store.updates) != 1 {
		t.Fatalf("a refused gate was written: %+v", store.updates)
	}
	if _, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{ClearTierRequired: true}); err != nil {
		t.Fatalf("clear while tiers down: %v", err)
	}
	if last := store.updates[len(store.updates)-1]; !last.ClearTierRequired || last.TierRequiredID != nil {
		t.Fatalf("clear: patch %+v", last)
	}
}

// Every gated row carries its tier's price — one tier list per creator per
// page, however many rows — and unknown (lookup failed, tier not active)
// leaves it off; an open row never has one.
func TestGatedRowsCarryTheRequiredPrice(t *testing.T) {
	a, b, down := uuid.New(), uuid.New(), uuid.New()
	tierA, tierB, offB := uuid.New(), uuid.New(), uuid.New()
	tiers := &tierFake{tiers: map[uuid.UUID][]CreatorTierRef{
		a: {{ID: tierA, CreatorID: a, PricePaise: 4900, IsActive: true}},
		b: {{ID: tierB, CreatorID: b, PricePaise: 19900, IsActive: true}, {ID: offB, CreatorID: b, PricePaise: 9900, IsActive: false}},
	}}
	f := &membersFake{}
	s := f.svc()
	s.creatorTiersSeam = func(ctx context.Context, creator uuid.UUID) ([]CreatorTierRef, error) {
		if creator == down {
			tiers.asks[creator]++
			return nil, errors.New("unreachable")
		}
		return tiers.list(ctx, creator)
	}
	tiers.asks = map[uuid.UUID]int{}
	tierDown := uuid.New()
	a1, a2, b1, bOff, d1, open := gatedRow(a, tierA), gatedRow(a, tierA), gatedRow(b, tierB), gatedRow(b, offB), gatedRow(down, tierDown), openRow(a)
	viewer := uuid.New()
	s.redactForViewer(context.Background(), &viewer, []*postgres.Post{a1, a2, b1, bOff, d1, open, nil})

	for name, c := range map[string]struct {
		p    *postgres.Post
		want int64
	}{"a1": {a1, 4900}, "a2": {a2, 4900}, "b1": {b1, 19900}} {
		if c.p.TierRequiredPricePaise == nil || *c.p.TierRequiredPricePaise != c.want {
			t.Errorf("%s: price %v, want %d", name, c.p.TierRequiredPricePaise, c.want)
		}
	}
	for name, p := range map[string]*postgres.Post{"switched-off tier": bOff, "lookup failed": d1, "open row": open} {
		if p.TierRequiredPricePaise != nil {
			t.Errorf("%s: price %d, want none", name, *p.TierRequiredPricePaise)
		}
	}
	for creator, n := range tiers.asks {
		if n != 1 {
			t.Errorf("creator %s asked %d times; want once per page", creator, n)
		}
	}
	raw, _ := json.Marshal(open)
	if strings.Contains(string(raw), "tier_required_price_paise") {
		t.Fatalf("open row carries the key: %s", raw)
	}
}

// The comment gate's refusal names the gate it refused on.
func TestMembersOnlyErrorNamesTheGate(t *testing.T) {
	author, tier, stranger := uuid.New(), uuid.New(), uuid.New()
	f := &membersFake{member: map[uuid.UUID]map[uuid.UUID]bool{}}
	s := f.svc()
	err := s.requireMemberOf(context.Background(), &postgres.Post{ID: uuid.New(), AuthorID: author, TierRequiredID: &tier}, &stranger)
	var gate *MembersOnlyError
	if !errors.Is(err, ErrMembersOnly) || !errors.As(err, &gate) || gate.CreatorID != author || gate.TierRequiredID != tier {
		t.Fatalf("err = %#v", err)
	}
}

// The one place PostCreated is shaped (the create path and the schedule
// worker share it): a gated post's event carries the gate and what a
// non-member may see (title, kind) but not the text, the media or the cast &
// crew names; an open post's event is unchanged.
func TestGatedPostCreatedCarriesNoBody(t *testing.T) {
	svc := &Service{}
	tier := uuid.New()
	post := func(gate *uuid.UUID) *postgres.Post {
		return &postgres.Post{
			ID: uuid.New(), AuthorID: uuid.New(), ContentType: "long_video", Visibility: "public", ReviewStatus: "approved",
			Title: "Episode 1", Text: "the paid body", TierRequiredID: gate,
			Media:        []postgres.PostMedia{{MediaID: uuid.New(), Kind: "video"}},
			CreditsWrite: &postgres.CreditsWrite{Candidates: []postgres.CreditCandidate{{Name: "A. Director", Role: "Director"}}},
		}
	}
	gated := svc.buildPostCreatedPayload(context.Background(), post(&tier), nil, 61, 1080, 1)
	if gated.TierRequiredID != tier.String() || gated.Title != "Episode 1" || gated.ContentType != "long_video" {
		t.Fatalf("gated event lost its preview fields: %+v", gated)
	}
	if gated.Text != "" || gated.Media != nil || gated.CreditNames != nil || gated.CreditRoles != nil {
		t.Fatalf("gated event carries the body: text=%q media=%v credits=%v/%v", gated.Text, gated.Media, gated.CreditNames, gated.CreditRoles)
	}
	open := svc.buildPostCreatedPayload(context.Background(), post(nil), nil, 61, 1080, 1)
	if open.Text != "the paid body" || len(open.Media) != 1 || len(open.CreditNames) != 1 {
		t.Fatalf("open event changed: %+v", open)
	}
}

// A members-only post's hashtags bump no public trend and reach no public
// hashtag stream; an open post's do.
func TestGatedPostHasNoPublicHashtags(t *testing.T) {
	tier := uuid.New()
	if got := publicHashtags(&postgres.Post{Hashtags: []string{"secret"}, TierRequiredID: &tier}); got != nil {
		t.Fatalf("gated post hashtags = %v, want none", got)
	}
	if got := publicHashtags(&postgres.Post{Hashtags: []string{"open"}}); len(got) != 1 {
		t.Fatalf("open post hashtags = %v", got)
	}
}
