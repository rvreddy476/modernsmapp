package http

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Lane D10 — POST /v1/dating/sparks/:id/accept.
func TestAcceptSparkFormsTheMatch(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()
	sender, recipient, stranger := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{sender, recipient, stranger} {
		mustSeedActiveProfile(t, st, id)
	}
	sp, err := st.CreateSpark(ctx, sender, recipient, "prompt", "p1", "hello")
	if err != nil {
		t.Fatalf("seed spark: %v", err)
	}
	path := "/v1/dating/sparks/" + sp.ID.String() + "/accept"

	// Only the recipient may accept.
	if rec := contractDo(r, http.MethodPost, path, ``, sender); rec.Code != http.StatusNotFound {
		t.Fatalf("sender accept: status %d, want 404", rec.Code)
	}
	if rec := contractDo(r, http.MethodPost, path, ``, stranger); rec.Code != http.StatusNotFound {
		t.Fatalf("stranger accept: status %d, want 404", rec.Code)
	}

	rec := contractDo(r, http.MethodPost, path, ``, recipient)
	if rec.Code != http.StatusCreated {
		t.Fatalf("accept: status %d body %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Spark   map[string]any `json:"spark"`
			MatchID string         `json:"match_id"`
			Matched bool           `json:"matched"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode accept: %v (%s)", err, rec.Body.String())
	}
	if !env.Data.Matched || env.Data.MatchID == "" {
		t.Fatalf("accept did not form a match: %s", rec.Body.String())
	}
	if env.Data.Spark["from_user_id"] != recipient.String() || env.Data.Spark["to_user_id"] != sender.String() {
		t.Fatalf("accept spark is not the reverse spark: %v", env.Data.Spark)
	}

	// Idempotent: a repeat answers with the same match.
	rec2 := contractDo(r, http.MethodPost, path, ``, recipient)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("repeat accept: status %d body %s", rec2.Code, rec2.Body.String())
	}
	var env2 struct {
		Data struct {
			MatchID string `json:"match_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &env2)
	if env2.Data.MatchID != env.Data.MatchID {
		t.Fatalf("repeat accept formed a second match: %s vs %s", env2.Data.MatchID, env.Data.MatchID)
	}

	// A declined spark cannot be accepted afterwards.
	other := uuid.New()
	mustSeedActiveProfile(t, st, other)
	sp2, err := st.CreateSpark(ctx, other, recipient, "photo", "0", "")
	if err != nil {
		t.Fatalf("seed spark: %v", err)
	}
	if _, err := st.DeclineSpark(ctx, sp2.ID, recipient); err != nil {
		t.Fatalf("decline: %v", err)
	}
	if rec := contractDo(r, http.MethodPost, "/v1/dating/sparks/"+sp2.ID.String()+"/accept", ``, recipient); rec.Code != http.StatusNotFound {
		t.Fatalf("declined accept: status %d, want 404", rec.Code)
	}
}
