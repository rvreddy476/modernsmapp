package http

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Lane D10 — the person card's access rule: a current match, a live incoming
// spark, or someone in the viewer's own deck. Everything else is 404.

// personCardOf decodes the card out of the envelope.
func personCardOf(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode person card: %v (%s)", err, body)
	}
	return env.Data
}

func TestPersonCardAccessRule(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()

	viewer, partner, sparker, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{viewer, partner, sparker, stranger} {
		mustSeedActiveProfile(t, st, id)
	}

	// A current match: readable, and the photo is full (lane D6).
	matchID, _, err := st.CreateOrGetOpenMatch(ctx, viewer, partner, map[string]any{"target_kind": "photo", "target_ref": "0"})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if err := st.MarkMatchActive(ctx, matchID, uuid.New()); err != nil {
		t.Fatalf("activate: %v", err)
	}
	rec := contractDo(r, http.MethodGet, "/v1/dating/people/"+partner.String(), ``, viewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("matched person: status %d body %s", rec.Code, rec.Body.String())
	}
	card := personCardOf(t, rec.Body.Bytes())
	if card["first_name"] != "Asha" || card["photo_state"] != "full" {
		t.Fatalf("matched card: %v", card)
	}
	if _, leaked := card["religion"]; leaked {
		t.Fatalf("person card carries religion: %v", card)
	}

	// A live incoming spark: readable.
	if _, err := st.CreateSpark(ctx, sparker, viewer, "photo", "0", ""); err != nil {
		t.Fatalf("seed spark: %v", err)
	}
	if rec := contractDo(r, http.MethodGet, "/v1/dating/people/"+sparker.String(), ``, viewer); rec.Code != http.StatusOK {
		t.Fatalf("incoming spark person: status %d body %s", rec.Code, rec.Body.String())
	}

	// A stranger with no relationship: 404.
	if rec := contractDo(r, http.MethodGet, "/v1/dating/people/"+stranger.String(), ``, viewer); rec.Code != http.StatusNotFound {
		t.Fatalf("stranger: status %d body %s; want 404", rec.Code, rec.Body.String())
	}
	// Yourself: 404 too (the profile route is the way to read your own card).
	if rec := contractDo(r, http.MethodGet, "/v1/dating/people/"+viewer.String(), ``, viewer); rec.Code != http.StatusNotFound {
		t.Fatalf("self: status %d; want 404", rec.Code)
	}

	// A block either way hides the person, even with a live spark.
	blocked := uuid.New()
	mustSeedActiveProfile(t, st, blocked)
	if _, err := st.CreateSpark(ctx, blocked, viewer, "photo", "0", ""); err != nil {
		t.Fatalf("seed spark: %v", err)
	}
	if err := st.BlockUser(ctx, blocked, viewer); err != nil {
		t.Fatalf("block: %v", err)
	}
	if rec := contractDo(r, http.MethodGet, "/v1/dating/people/"+blocked.String(), ``, viewer); rec.Code != http.StatusNotFound {
		t.Fatalf("blocked person: status %d; want 404", rec.Code)
	}
}

// TestMatchesAndSparksCarryPerson: the list responses carry the compact card.
func TestMatchesAndSparksCarryPerson(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()
	viewer, partner, sparker := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{viewer, partner, sparker} {
		mustSeedActiveProfile(t, st, id)
	}
	matchID, _, err := st.CreateOrGetOpenMatch(ctx, viewer, partner, nil)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if err := st.MarkMatchActive(ctx, matchID, uuid.New()); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := st.CreateSpark(ctx, sparker, viewer, "prompt", "p1", ""); err != nil {
		t.Fatalf("seed spark: %v", err)
	}

	var list struct {
		Data []struct {
			ID     uuid.UUID `json:"id"`
			Person *struct {
				UserID     uuid.UUID `json:"user_id"`
				FirstName  string    `json:"first_name"`
				Age        int       `json:"age"`
				PhotoState string    `json:"photo_state"`
			} `json:"person"`
		} `json:"data"`
	}
	rec := contractDo(r, http.MethodGet, "/v1/dating/matches", ``, viewer)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("matches: %v (%s)", err, rec.Body.String())
	}
	if len(list.Data) != 1 || list.Data[0].Person == nil {
		t.Fatalf("matches carry no person: %s", rec.Body.String())
	}
	if list.Data[0].Person.UserID != partner || list.Data[0].Person.Age < 18 || list.Data[0].Person.PhotoState != "full" {
		t.Fatalf("match person: %+v", *list.Data[0].Person)
	}

	// The match detail carries it too.
	rec = contractDo(r, http.MethodGet, "/v1/dating/matches/"+matchID.String(), ``, viewer)
	var detail struct {
		Data struct {
			Person *struct {
				UserID uuid.UUID `json:"user_id"`
			} `json:"person"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail.Data.Person == nil || detail.Data.Person.UserID != partner {
		t.Fatalf("match detail person: %s", rec.Body.String())
	}

	rec = contractDo(r, http.MethodGet, "/v1/dating/sparks/incoming", ``, viewer)
	var sparks struct {
		Data []struct {
			FromUserID uuid.UUID `json:"from_user_id"`
			Person     *struct {
				UserID    uuid.UUID `json:"user_id"`
				FirstName string    `json:"first_name"`
			} `json:"person"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sparks); err != nil {
		t.Fatalf("incoming: %v (%s)", err, rec.Body.String())
	}
	if len(sparks.Data) != 1 || sparks.Data[0].Person == nil || sparks.Data[0].Person.UserID != sparker {
		t.Fatalf("incoming sparks carry no person: %s", rec.Body.String())
	}
}
