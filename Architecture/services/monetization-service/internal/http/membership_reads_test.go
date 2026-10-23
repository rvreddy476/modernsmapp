package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/service/membershiptest"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The membership read routes (membership_reads.go): who the creator of a
// membership is, one membership, and a creator's members.

type fakeProfiles struct {
	byID  map[uuid.UUID]service.PublicProfile
	err   error
	calls int
}

func (f *fakeProfiles) Profiles(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]service.PublicProfile, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := map[uuid.UUID]service.PublicProfile{}
	for _, id := range ids {
		if p, ok := f.byID[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}

func readsRig(profiles service.ProfileSource, store service.MembershipStore) *Handler {
	elig := membershiptest.Eligible()
	svc := service.New(nil, nil).WithMembershipStore(store).WithMembershipPayments(membershiptest.NewPayments(), false).
		WithEligibilitySources(service.EligibilitySources{BirthDates: elig, Followers: elig, Standing: elig})
	if profiles != nil {
		svc.WithProfiles(profiles)
	}
	return New(svc).WithMembershipsEnabled(true)
}

func activeSub(fake *membershiptest.Store, fan, creator uuid.UUID, tier *postgres.CreatorTier, start time.Time) *postgres.Subscription {
	return fake.AddSubscription(postgres.Subscription{
		SubscriberID: fan, CreatorID: creator, TierID: tier.ID, TierName: tier.Name, PricePaise: tier.PricePaise,
		Status: membership.StatusActive, CurrentPeriodStart: start, CurrentPeriodEnd: start.AddDate(0, 1, 0),
	})
}

type creatorJSON struct {
	ID          uuid.UUID `json:"id"`
	Username    *string   `json:"username"`
	DisplayName *string   `json:"display_name"`
	AvatarURL   *string   `json:"avatar_url"`
}

func TestMyMembershipsNameTheCreator(t *testing.T) {
	fake := membershiptest.New()
	fan, named, unnamed := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	activeSub(fake, fan, named, fake.AddTier(named, "Fan", 4_900), now.Add(-time.Hour))
	activeSub(fake, fan, unnamed, fake.AddTier(unnamed, "Fan", 2_900), now.Add(-2*time.Hour))
	prof := &fakeProfiles{byID: map[uuid.UUID]service.PublicProfile{
		named: {Username: "ravi", DisplayName: "Ravi K", AvatarURL: "https://cdn.example/ravi.jpg"},
	}}
	w := serveJSON(readsRig(prof, fake), http.MethodGet, "/v1/monetization/memberships", fan.String(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var rows []struct {
		Creator creatorJSON `json:"creator"`
	}
	decodeData(t, w, &rows)
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	for _, r := range rows {
		switch r.Creator.ID {
		case named:
			if r.Creator.Username == nil || *r.Creator.Username != "ravi" || r.Creator.DisplayName == nil || *r.Creator.DisplayName != "Ravi K" ||
				r.Creator.AvatarURL == nil || *r.Creator.AvatarURL != "https://cdn.example/ravi.jpg" {
				t.Fatalf("named creator = %+v", r.Creator)
			}
		case unnamed:
			if r.Creator.Username != nil || r.Creator.DisplayName != nil || r.Creator.AvatarURL != nil {
				t.Fatalf("a creator the lookup did not return carries profile keys: %s", w.Body.String())
			}
		default:
			t.Fatalf("unexpected creator %s", r.Creator.ID)
		}
	}
	if prof.calls != 1 {
		t.Fatalf("profile lookups = %d, want one batch per list", prof.calls)
	}
}

func TestMyMembershipsDegradeToIDsWhenProfilesFail(t *testing.T) {
	for name, src := range map[string]service.ProfileSource{
		"lookup failed": &fakeProfiles{err: errors.New("identity-profile down")},
		"no source":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			fake := membershiptest.New()
			fan, creator := uuid.New(), uuid.New()
			activeSub(fake, fan, creator, fake.AddTier(creator, "Fan", 4_900), time.Now().Add(-time.Hour))
			w := serveJSON(readsRig(src, fake), http.MethodGet, "/v1/monetization/memberships", fan.String(), "")
			if w.Code != http.StatusOK {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			var rows []struct {
				Creator map[string]any `json:"creator"`
			}
			decodeData(t, w, &rows)
			if len(rows) != 1 || len(rows[0].Creator) != 1 || rows[0].Creator["id"] != creator.String() {
				t.Fatalf("creator = %v, want {id} only", rows)
			}
		})
	}
}

func TestMyMembershipIsTheSubscribersOwn(t *testing.T) {
	fake := membershiptest.New()
	fan, other, creator := uuid.New(), uuid.New(), uuid.New()
	gold := fake.AddTier(creator, "Gold", 9_900)
	gold.Perks = json.RawMessage(`["Badge","Early access"]`)
	fake.Tiers[gold.ID].Perks = gold.Perks
	silver := fake.AddTier(creator, "Silver", 4_900)
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	sub := activeSub(fake, fan, creator, gold, start)
	pendingPrice := int64(4_900)
	fake.Subs[sub.ID].PendingTierID, fake.Subs[sub.ID].PendingPricePaise = &silver.ID, &pendingPrice
	prof := &fakeProfiles{byID: map[uuid.UUID]service.PublicProfile{creator: {Username: "maya", DisplayName: "Maya"}}}
	h := readsRig(prof, fake)
	path := "/v1/monetization/memberships/" + sub.ID.String()

	w := serveJSON(h, http.MethodGet, path, fan.String(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("owner: %d %s", w.Code, w.Body.String())
	}
	var v struct {
		SubscriptionID uuid.UUID   `json:"subscription_id"`
		Creator        creatorJSON `json:"creator"`
		Tier           struct {
			ID         uuid.UUID `json:"id"`
			Name       string    `json:"name"`
			PricePaise int64     `json:"price_paise"`
			Perks      []string  `json:"perks"`
		} `json:"tier"`
		Status           string     `json:"status"`
		CurrentPeriodEnd time.Time  `json:"current_period_end"`
		AccessUntil      *time.Time `json:"access_until"`
		RenewAvailable   bool       `json:"renew_available"`
		PendingDowngrade *struct {
			TierID      uuid.UUID `json:"tier_id"`
			TierName    string    `json:"tier_name"`
			PricePaise  int64     `json:"price_paise"`
			EffectiveAt time.Time `json:"effective_at"`
		} `json:"pending_downgrade"`
	}
	decodeData(t, w, &v)
	if v.SubscriptionID != sub.ID || v.Creator.ID != creator || v.Creator.Username == nil || *v.Creator.Username != "maya" ||
		v.Tier.ID != gold.ID || v.Tier.Name != "Gold" || v.Tier.PricePaise != 9_900 || len(v.Tier.Perks) != 2 ||
		v.Status != "active" || v.AccessUntil == nil {
		t.Fatalf("detail = %+v (%s)", v, w.Body.String())
	}
	if d := v.PendingDowngrade; d == nil || d.TierID != silver.ID || d.TierName != "Silver" || d.PricePaise != 4_900 ||
		!d.EffectiveAt.Equal(sub.CurrentPeriodEnd) {
		t.Fatalf("pending_downgrade = %+v", v.PendingDowngrade)
	}

	// Someone else's, an unknown id and a non-id: all 404, never 403.
	wantCode(t, "another user", serveJSON(h, http.MethodGet, path, other.String(), ""), http.StatusNotFound, "NOT_FOUND")
	wantCode(t, "the creator", serveJSON(h, http.MethodGet, path, creator.String(), ""), http.StatusNotFound, "NOT_FOUND")
	wantCode(t, "unknown", serveJSON(h, http.MethodGet, "/v1/monetization/memberships/"+uuid.NewString(), fan.String(), ""), http.StatusNotFound, "NOT_FOUND")
	wantCode(t, "not an id", serveJSON(h, http.MethodGet, "/v1/monetization/memberships/nope", fan.String(), ""), http.StatusNotFound, "NOT_FOUND")

	// No downgrade scheduled: null; a tier without perks: [].
	plain := activeSub(fake, uuid.New(), creator, silver, start)
	w = serveJSON(h, http.MethodGet, "/v1/monetization/memberships/"+plain.ID.String(), plain.SubscriberID.String(), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"pending_downgrade":null`) || !strings.Contains(w.Body.String(), `"perks":[]`) {
		t.Fatalf("plain: %d %s", w.Code, w.Body.String())
	}

	// The payment status route still resolves beside the new one.
	w = serveJSON(h, http.MethodGet, "/v1/monetization/memberships/payments/"+uuid.NewString(), fan.String(), "")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "Payment not found") {
		t.Fatalf("payments route: %d %s", w.Code, w.Body.String())
	}
}

type memberRow struct {
	SubscriptionID uuid.UUID   `json:"subscription_id"`
	Subscriber     creatorJSON `json:"subscriber"`
	Tier           struct {
		ID         uuid.UUID `json:"id"`
		Name       string    `json:"name"`
		PricePaise int64     `json:"price_paise"`
	} `json:"tier"`
	Status      string     `json:"status"`
	Since       time.Time  `json:"since"`
	AccessUntil *time.Time `json:"access_until"`
}

func membersPage(t *testing.T, h *Handler, caller uuid.UUID, query string) ([]memberRow, string, string) {
	t.Helper()
	w := serveJSON(h, http.MethodGet, "/v1/monetization/members"+query, caller.String(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("members%s: %d %s", query, w.Code, w.Body.String())
	}
	var env struct {
		Data []memberRow `json:"data"`
		Meta struct {
			NextCursor string `json:"next_cursor"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data, env.Meta.NextCursor, w.Body.String()
}

// membersFixture: creator A with three paying members (one of them also a
// member of creator B at B's price), one join still pending, one expired
// and one live row that was never paid for.
func membersFixture() (*membershiptest.Store, uuid.UUID, uuid.UUID, []uuid.UUID) {
	fake := membershiptest.New()
	a, b := uuid.New(), uuid.New()
	tierA, tierB := fake.AddTier(a, "Fan", 4_900), fake.AddTier(b, "Patron", 77_700)
	base := time.Now().UTC().Add(-24 * time.Hour)
	var want []uuid.UUID // newest first
	fans := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, fan := range fans {
		s := activeSub(fake, fan, a, tierA, base.Add(time.Duration(i)*time.Hour))
		want = append([]uuid.UUID{s.ID}, want...)
	}
	activeSub(fake, fans[0], b, tierB, base)
	fake.AddSubscription(postgres.Subscription{SubscriberID: uuid.New(), CreatorID: a, TierID: tierA.ID, TierName: "Fan",
		PricePaise: 4_900, Status: membership.StatusPendingPayment, CurrentPeriodStart: base, CurrentPeriodEnd: base.AddDate(0, 1, 0)})
	fake.AddSubscription(postgres.Subscription{SubscriberID: uuid.New(), CreatorID: a, TierID: tierA.ID, TierName: "Fan",
		PricePaise: 4_900, Status: membership.StatusExpired, CurrentPeriodStart: base.AddDate(0, -2, 0), CurrentPeriodEnd: base.AddDate(0, -1, 0)})
	unpaid := activeSub(fake, uuid.New(), a, tierA, base.Add(5*time.Hour))
	fake.Subs[unpaid.ID].ActivatedAt = nil
	return fake, a, b, want
}

func TestMembersListIsTheCreatorsOwn(t *testing.T) {
	fake, a, b, want := membersFixture()
	prof := &fakeProfiles{byID: map[uuid.UUID]service.PublicProfile{}}
	for _, s := range fake.Subs {
		prof.byID[s.SubscriberID] = service.PublicProfile{Username: "u" + s.SubscriberID.String()[:6], DisplayName: "Fan"}
	}
	h := readsRig(prof, fake)

	rows, next, body := membersPage(t, h, a, "")
	if next != "" || len(rows) != len(want) {
		t.Fatalf("rows = %d next = %q: %s", len(rows), next, body)
	}
	for i, r := range rows {
		if r.SubscriptionID != want[i] {
			t.Fatalf("row %d = %s, want %s (newest first)", i, r.SubscriptionID, want[i])
		}
		if r.Tier.PricePaise != 4_900 || r.Tier.Name != "Fan" || r.Status != "active" || r.Since.IsZero() || r.AccessUntil == nil ||
			r.Subscriber.Username == nil || *r.Subscriber.Username == "" {
			t.Fatalf("row %d = %+v", i, r)
		}
	}
	if strings.Contains(body, "77700") || strings.Contains(body, "Patron") {
		t.Fatalf("A's member list carries B's price or tier: %s", body)
	}
	// B sees only B's member; A sees nothing of B's.
	rowsB, _, _ := membersPage(t, h, b, "")
	if len(rowsB) != 1 || rowsB[0].Tier.PricePaise != 77_700 {
		t.Fatalf("B's members = %+v", rowsB)
	}
	// Someone with no members gets an empty list.
	if none, _, _ := membersPage(t, h, uuid.New(), ""); len(none) != 0 {
		t.Fatalf("a stranger saw %d members", len(none))
	}
}

func TestMembersListPages(t *testing.T) {
	fake, a, _, want := membersFixture()
	h := readsRig(nil, fake)
	first, next, _ := membersPage(t, h, a, "?limit=2")
	if len(first) != 2 || next == "" || first[0].SubscriptionID != want[0] || first[1].SubscriptionID != want[1] {
		t.Fatalf("page 1 = %+v next %q", first, next)
	}
	second, last, _ := membersPage(t, h, a, "?limit=2&cursor="+url.QueryEscape(next))
	if len(second) != 1 || last != "" || second[0].SubscriptionID != want[2] {
		t.Fatalf("page 2 = %+v next %q", second, last)
	}
	// Ids only without a profile source.
	if first[0].Subscriber.Username != nil {
		t.Fatalf("profile keys with no source: %+v", first[0].Subscriber)
	}
	for _, bad := range []string{
		"not-a-cursor", // decodes, but is no position
		"%21%21%21",    // not base64
		base64.RawURLEncoding.EncodeToString([]byte("yesterday|" + uuid.NewString())),
		base64.RawURLEncoding.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano) + "|not-an-id")),
	} {
		wantCode(t, "bad cursor "+bad, serveJSON(h, http.MethodGet, "/v1/monetization/members?cursor="+bad, a.String(), ""), http.StatusBadRequest, "INVALID_CURSOR")
	}
	wantCode(t, "bad limit", serveJSON(h, http.MethodGet, "/v1/monetization/members?limit=abc", a.String(), ""), http.StatusBadRequest, "INVALID_REQUEST")
	wantCode(t, "signed out", serveJSON(h, http.MethodGet, "/v1/monetization/members", "", ""), http.StatusUnauthorized, "UNAUTHORIZED")
}

// leakyMembers answers every subscription it holds, whoever's: the service
// must still answer only the caller's paying, live members.
type leakyMembers struct{ *membershiptest.Store }

func (l leakyMembers) ListCreatorMembers(_ context.Context, _ uuid.UUID, _ *postgres.MemberCursor, _ int) ([]postgres.Subscription, error) {
	var out []postgres.Subscription
	for _, s := range l.Subs {
		out = append(out, *s)
	}
	return out, nil
}

func TestMembersListRechecksEveryRow(t *testing.T) {
	fake, a, _, want := membersFixture()
	h := readsRig(nil, leakyMembers{fake})
	rows, _, body := membersPage(t, h, a, "?limit=50")
	if len(rows) != len(want) {
		t.Fatalf("rows = %d, want %d: %s", len(rows), len(want), body)
	}
	for _, r := range rows {
		s := fake.Subs[r.SubscriptionID]
		if s.CreatorID != a || s.ActivatedAt == nil || !postgres.IsLiveSubscriptionStatus(s.Status) {
			t.Fatalf("leaked row %+v", s)
		}
	}
}

// Both new reads are membership routes: closed with MEMBERSHIPS_ENABLED off
// whatever the writes flag says.
func TestMembershipReadsFollowTheMembershipsSwitch(t *testing.T) {
	fake := membershiptest.New()
	h := readsRig(nil, fake).WithMembershipsEnabled(false).WithWritesEnabled(true)
	for _, path := range []string{"/v1/monetization/members", "/v1/monetization/memberships/" + uuid.NewString()} {
		wantCode(t, path, serveJSON(h, http.MethodGet, path, uuid.NewString(), ""), http.StatusServiceUnavailable, "MEMBERSHIPS_NOT_ENABLED")
	}
}
