package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M4 — who liked you (DATING_LIKED_YOU_GATE_ENABLED): golden
// fixtures and the guards behind them. Same harness as m1_deck_test.go.

var m4Fixtures = []string{
	"liked_you_get_200_locked",
	"liked_you_get_200_unlocked",
	"sparks_incoming_get_200_locked",
	"spark_accept_403_liked_you_locked",
}

func m4Config(gate bool) service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50,
		SuperSpark: true, SuperSparkDailyLimitFree: 1, SuperSparkDailyLimitPass: 5, LikedYouGate: gate}
}

// variantMedia is d10Media that records which variant each delivery asked for.
type variantMedia struct {
	*d10Media
	mu       sync.Mutex
	variants []string
}

func (m *variantMedia) PhotoDeliveryURL(_ context.Context, _ uuid.UUID, _ uuid.UUID, variant string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.variants = append(m.variants, variant)
	return "https://media.example/signed/" + variant, nil
}

// admirers seeds n people who spark the viewer; the first sends a Super
// Spark, so the server lists it first.
func (d *m1Deck) admirers(n int) []uuid.UUID {
	d.t.Helper()
	out := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		id := uuid.New()
		mustSeedActiveProfile(d.t, d.env.st, id)
		body := `{"to_user_id":"` + d.viewer.String() + `","target_kind":"prompt","target_ref":"m4","note":"Loved your answer"}`
		if i == 0 {
			body = `{"to_user_id":"` + d.viewer.String() + `","target_kind":"prompt","target_ref":"m4","note":"Loved your answer","super":true}`
		}
		if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", body, id); rec.Code != http.StatusCreated {
			d.t.Fatalf("admirer spark: status %d body %s", rec.Code, rec.Body.String())
		}
		out = append(out, id)
	}
	return out
}

// incomingSparkIDs lists the viewer's incoming spark ids straight from the
// store, so a test can act on one without the grid revealing it.
func (d *m1Deck) incomingSparkIDs() map[uuid.UUID]uuid.UUID {
	d.t.Helper()
	sparks, err := d.env.st.ListIncomingSparks(context.Background(), d.viewer, 50, 0)
	if err != nil {
		d.t.Fatal(err)
	}
	out := map[uuid.UUID]uuid.UUID{}
	for _, sp := range sparks {
		out[sp.FromUserID] = sp.ID
	}
	return out
}

// assertNothingIdentifies fails when a locked body carries any of the
// senders' ids, their name, the note, a photo id route or a full image.
func assertNothingIdentifies(t *testing.T, label, body string, senders []uuid.UUID, st interface {
	PrimaryPhotoIDs(t *testing.T, ids []uuid.UUID) []uuid.UUID
}) {
	t.Helper()
	for _, s := range senders {
		if strings.Contains(body, s.String()) {
			t.Fatalf("%s: carries a sender's user id: %s", label, body)
		}
	}
	for _, p := range st.PrimaryPhotoIDs(t, senders) {
		if strings.Contains(body, p.String()) {
			t.Fatalf("%s: carries a sender's photo id: %s", label, body)
		}
	}
	for _, leak := range []string{"Asha", "Loved your answer", "/full", "/v1/dating/photos/", "first_name", "from_user_id", `"person"`, `"note"`} {
		if strings.Contains(body, leak) {
			t.Fatalf("%s: carries %q: %s", label, leak, body)
		}
	}
}

type photoIDs struct{ d *m1Deck }

func (p photoIDs) PrimaryPhotoIDs(t *testing.T, ids []uuid.UUID) []uuid.UUID {
	t.Helper()
	out := []uuid.UUID{}
	for _, id := range ids {
		photo, err := p.d.env.st.PrimaryApprovedPhoto(context.Background(), id)
		if err != nil {
			t.Fatalf("primary photo: %v", err)
		}
		out = append(out, photo.ID)
	}
	return out
}

func TestM4LikedYouContracts(t *testing.T) {
	d := newM1Deck(t, m4Config(true))
	senders := d.admirers(2)
	labels := map[uuid.UUID]string{d.viewer: "<viewer>", senders[0]: "<super_sender>", senders[1]: "<sender>"}

	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/liked-you", ``, d.viewer)
	assertNothingIdentifies(t, "locked grid", rec.Body.String(), senders, photoIDs{d})
	assertContract(t, rec, http.StatusOK, "liked_you_get_200_locked", labels)

	rec = contractDo(d.env.r, http.MethodGet, "/v1/dating/sparks/incoming", ``, d.viewer)
	assertNothingIdentifies(t, "locked incoming", rec.Body.String(), senders, photoIDs{d})
	assertContract(t, rec, http.StatusOK, "sparks_incoming_get_200_locked", labels)

	spark := d.incomingSparkIDs()[senders[1]]
	assertContract(t, contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks/"+spark.String()+"/accept", ``, d.viewer),
		http.StatusForbidden, "spark_accept_403_liked_you_locked", labels)

	d.grantPass()
	rec = contractDo(d.env.r, http.MethodGet, "/v1/dating/liked-you", ``, d.viewer)
	d7AssertBucketsOnly(t, "unlocked grid", rec.Body.Bytes())
	assertContract(t, rec, http.StatusOK, "liked_you_get_200_unlocked", labels)
}

func TestM4ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m4Fixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: not JSON: %v", name, err)
		}
		if contractUUIDRe.Match(raw) || contractTimeRe.Match(raw) {
			t.Fatalf("%s: carries a raw uuid or timestamp", name)
		}
		if strings.Contains(name, "locked") && !strings.Contains(name, "unlocked") {
			for _, leak := range []string{"Asha", "Loved your answer", "/full", "first_name", "from_user_id"} {
				if strings.Contains(string(raw), leak) {
					t.Fatalf("%s: a locked fixture carries %q", name, leak)
				}
			}
		}
	}
}

// Guard: the locked grid gives the count and blurred cards only, with the
// Super Spark first.
func TestLikedYouLockedGivesCountAndBlurredCardsOnly(t *testing.T) {
	d := newM1Deck(t, m4Config(true))
	senders := d.admirers(3)
	got := d.likedYou()
	if got.Unlocked || got.Total != 3 || len(got.Items) != 3 {
		t.Fatalf("locked grid = %+v, want 3 locked items", got)
	}
	if !got.Items[0].Super {
		t.Fatalf("the Super Spark is not first: %+v", got.Items)
	}
	for _, it := range got.Items {
		if it.Person != nil || it.Note != nil || it.PhotoURL != service.LikedYouPhotoPath(it.SparkID) {
			t.Fatalf("a locked item carries more than a blurred card: %+v", it)
		}
	}
	_ = senders
}

// Guard: the locked photo route only ever asks media-service for the blurred
// variant, and only for the spark's recipient.
func TestLikedYouPhotoIsAlwaysBlurredAndRecipientOnly(t *testing.T) {
	d := newM1Deck(t, m4Config(true))
	media := &variantMedia{d10Media: &d10Media{}}
	d.env.svc.SetMediaPhotoClient(media)
	senders := d.admirers(1)
	spark := d.incomingSparkIDs()[senders[0]]

	rec := contractDo(d.env.r, http.MethodGet, service.LikedYouPhotoPath(spark), ``, d.viewer)
	if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "https://media.example/signed/blurred" {
		t.Fatalf("recipient: status %d location %q", rec.Code, rec.Header().Get("Location"))
	}
	// A pass holder still gets the blurred image from this route.
	d.grantPass()
	rec = contractDo(d.env.r, http.MethodGet, service.LikedYouPhotoPath(spark), ``, d.viewer)
	if rec.Header().Get("Location") != "https://media.example/signed/blurred" {
		t.Fatalf("pass holder: location %q, want the blurred image", rec.Header().Get("Location"))
	}
	for _, v := range media.variants {
		if v != service.PhotoVariantBlurred {
			t.Fatalf("the liked-you photo route asked for %q", v)
		}
	}
	// Anyone else, including the sender, gets a not-found.
	for _, other := range []uuid.UUID{senders[0], uuid.New()} {
		if rec := contractDo(d.env.r, http.MethodGet, service.LikedYouPhotoPath(spark), ``, other); rec.Code != http.StatusNotFound {
			t.Fatalf("non-recipient: status %d, want 404", rec.Code)
		}
	}
}

// Guard: accepting a locked spark is refused and forms no match; with a pass
// it forms the match.
func TestLikedYouLockedSparkCannotBeAccepted(t *testing.T) {
	d := newM1Deck(t, m4Config(true))
	senders := d.admirers(1)
	spark := d.incomingSparkIDs()[senders[0]]
	d.wantRefusal(contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks/"+spark.String()+"/accept", ``, d.viewer),
		http.StatusForbidden, "LIKED_YOU_LOCKED")
	if open, err := d.env.st.HasOpenMatch(context.Background(), d.viewer, senders[0]); err != nil || open {
		t.Fatalf("a refused accept formed a match: open=%v err=%v", open, err)
	}
	d.grantPass()
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks/"+spark.String()+"/accept", ``, d.viewer); rec.Code != http.StatusCreated {
		t.Fatalf("accept with a pass: status %d body %s", rec.Code, rec.Body.String())
	}
}

// Guard: an incoming spark alone no longer opens the sender's person card
// for a locked viewer.
func TestLikedYouLockedPersonCardNeedsAnotherReason(t *testing.T) {
	d := newM1Deck(t, m4Config(true))
	senders := d.admirers(1)
	d.wantRefusal(contractDo(d.env.r, http.MethodGet, "/v1/dating/people/"+senders[0].String(), ``, d.viewer),
		http.StatusNotFound, "CANDIDATE_UNAVAILABLE")
	d.grantPass()
	if rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/people/"+senders[0].String(), ``, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("person card with a pass: status %d body %s", rec.Code, rec.Body.String())
	}
}

// Guard: a block hides the sender from the grid, its count and the photo
// route, in both directions.
func TestLikedYouHonoursBlocksBothWays(t *testing.T) {
	d := newM1Deck(t, m4Config(true))
	senders := d.admirers(2)
	sparks := d.incomingSparkIDs()
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/block", `{"target_user_id":"`+d.viewer.String()+`"}`, senders[0]); rec.Code != http.StatusOK {
		t.Fatalf("sender blocks viewer: %d %s", rec.Code, rec.Body.String())
	}
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/block", `{"target_user_id":"`+senders[1].String()+`"}`, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("viewer blocks sender: %d %s", rec.Code, rec.Body.String())
	}
	if got := d.likedYou(); got.Total != 0 || len(got.Items) != 0 {
		t.Fatalf("grid after blocks = %+v, want empty", got)
	}
	for _, sp := range sparks {
		if rec := contractDo(d.env.r, http.MethodGet, service.LikedYouPhotoPath(sp), ``, d.viewer); rec.Code != http.StatusNotFound {
			t.Fatalf("photo of a blocked sender: status %d, want 404", rec.Code)
		}
	}
}

// With the gate off the grid is unlocked for everyone and the incoming list
// is the pilot's.
func TestLikedYouGateOffIsUnlocked(t *testing.T) {
	d := newM1Deck(t, m4Config(false))
	senders := d.admirers(1)
	if got := d.likedYou(); !got.Unlocked || len(got.Items) != 1 || got.Items[0].Person == nil {
		t.Fatalf("gate off grid = %+v, want unlocked with the person", got)
	}
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/sparks/incoming", ``, d.viewer)
	if !strings.Contains(rec.Body.String(), senders[0].String()) {
		t.Fatalf("gate off incoming list hides the sender: %s", rec.Body.String())
	}
}

func (d *m1Deck) likedYou() service.LikedYouResponse {
	d.t.Helper()
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/liked-you", ``, d.viewer)
	if rec.Code != http.StatusOK {
		d.t.Fatalf("liked you: status %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data service.LikedYouResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		d.t.Fatalf("liked you: %v", err)
	}
	return body.Data
}
