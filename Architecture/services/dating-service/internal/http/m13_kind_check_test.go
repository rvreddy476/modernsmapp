package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M13 — kind messages (DATING_KIND_CHECK_ENABLED): golden fixtures
// and the guards behind them. Same harness as m1_deck_test.go.

var m13Fixtures = []string{
	"kind_check_post_200_kind",
	"kind_check_post_200_unkind",
	"kind_check_post_400_invalid",
	"kind_check_post_404_not_enabled",
	"bothered_post_201",
	"comment_filter_get_200",
	"comment_filter_put_200",
	"comment_filter_put_400_invalid",
}

func m13Config(on bool) service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, KindCheck: on, LikedYouGate: false}
}

func (d *m1Deck) kindCheck(text string) (int, service.KindCheckResult) {
	d.t.Helper()
	body, _ := json.Marshal(map[string]string{"text": text})
	rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/kind-check", string(body), d.viewer)
	var out struct {
		Data service.KindCheckResult `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Data
}

func TestM13KindCheckContracts(t *testing.T) {
	d := newM1Deck(t, m13Config(true))
	other := d.candidate()
	match := d.matchWith(other)
	labels := map[uuid.UUID]string{d.viewer: "<viewer>", match: "<match>"}
	post := func(path, body string) *httptest.ResponseRecorder {
		return contractDo(d.env.r, http.MethodPost, path, body, d.viewer)
	}
	assertContract(t, post("/v1/dating/kind-check", `{"text":"Loved your answer about Goa. Coffee on Saturday?"}`), http.StatusOK, "kind_check_post_200_kind", labels)
	assertContract(t, post("/v1/dating/kind-check", `{"text":"you are such an idiot"}`), http.StatusOK, "kind_check_post_200_unkind", labels)
	assertContract(t, post("/v1/dating/kind-check", `{"text":"   "}`), http.StatusBadRequest, "kind_check_post_400_invalid", labels)
	assertContract(t, post("/v1/dating/matches/"+match.String()+"/bothered", `{"bothered":true}`), http.StatusCreated, "bothered_post_201", labels)
	assertContract(t, contractDo(d.env.r, http.MethodPut, "/v1/dating/comment-filter", `{"filter_unkind":true,"words":["Ex","  Cricket "]}`, d.viewer),
		http.StatusOK, "comment_filter_put_200", labels)
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/comment-filter", ``, d.viewer), http.StatusOK, "comment_filter_get_200", labels)
	assertContract(t, contractDo(d.env.r, http.MethodPut, "/v1/dating/comment-filter", `{"filter_unkind":true,"words":["a"]}`, d.viewer),
		http.StatusBadRequest, "comment_filter_put_400_invalid", labels)

	off := newM1Deck(t, m13Config(false))
	assertContract(t, contractDo(off.env.r, http.MethodPost, "/v1/dating/kind-check", `{"text":"hi"}`, off.viewer),
		http.StatusNotFound, "kind_check_post_404_not_enabled", map[uuid.UUID]string{off.viewer: "<viewer>"})
}

func TestM13ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m13Fixtures {
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
	}
}

// Guard: unkind wording is flagged, kind wording and a shared phone number
// are not (the dev mock LLM is not consulted).
func TestKindCheckFlagsUnkindOnly(t *testing.T) {
	d := newM1Deck(t, m13Config(true))
	for text, want := range map[string]bool{
		"you are such an idiot":               false,
		"send nudes":                          false,
		"Lovely photos! Coffee this weekend?": true,
		"my number is 9876543210, call me":    true,
	} {
		code, res := d.kindCheck(text)
		if code != http.StatusOK || res.Kind != want {
			t.Fatalf("%q: %d kind=%v reasons=%v, want kind=%v", text, code, res.Kind, res.Reasons, want)
		}
	}
}

// incomingNotes returns spark id -> note_hidden from GET /sparks/incoming.
func (d *m1Deck) incomingNotes() map[uuid.UUID]string {
	d.t.Helper()
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/sparks/incoming", ``, d.viewer)
	var body struct {
		Data []struct {
			ID         uuid.UUID `json:"id"`
			NoteHidden string    `json:"note_hidden"`
		} `json:"data"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		d.t.Fatalf("incoming: %d %s", rec.Code, rec.Body.String())
	}
	out := map[uuid.UUID]string{}
	for _, s := range body.Data {
		out[s.ID] = s.NoteHidden
	}
	return out
}

func (d *m1Deck) sparkWithNote(from uuid.UUID, note string) uuid.UUID {
	d.t.Helper()
	body, _ := json.Marshal(map[string]string{"to_user_id": d.viewer.String(), "target_kind": "prompt", "target_ref": uuid.NewString()[:8], "note": note})
	rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", string(body), from)
	var out struct {
		Data struct {
			Spark struct {
				ID uuid.UUID `json:"id"`
			} `json:"spark"`
		} `json:"data"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		d.t.Fatalf("spark with note: %d %s", rec.Code, rec.Body.String())
	}
	return out.Data.Spark.ID
}

// Guard: the recipient's filter marks unkind notes and their hidden words;
// switching unkind off, or the flag off, marks nothing.
func TestCommentFilterMarksNotes(t *testing.T) {
	d := newM1Deck(t, m13Config(true))
	rude := d.sparkWithNote(d.candidate(), "you look stupid")
	word := d.sparkWithNote(d.candidate(), "Do you like cricket?")
	nice := d.sparkWithNote(d.candidate(), "Great answer about books")
	if rec := contractDo(d.env.r, http.MethodPut, "/v1/dating/comment-filter", `{"filter_unkind":true,"words":["cricket"]}`, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("filter: %d", rec.Code)
	}
	got := d.incomingNotes()
	if got[rude] != "unkind" || got[word] != "your_words" || got[nice] != "" {
		t.Fatalf("note_hidden = rude %q word %q nice %q", got[rude], got[word], got[nice])
	}
	if rec := contractDo(d.env.r, http.MethodPut, "/v1/dating/comment-filter", `{"filter_unkind":false,"words":[]}`, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("filter off: %d", rec.Code)
	}
	if got := d.incomingNotes(); got[rude] != "" || got[word] != "" {
		t.Fatalf("with the filter off: rude %q word %q", got[rude], got[word])
	}
	off := newM1Deck(t, m13Config(false))
	r := off.sparkWithNote(off.candidate(), "you look stupid")
	if got := off.incomingNotes(); got[r] != "" {
		t.Fatalf("flag off but the note is marked %q", got[r])
	}
}

// Guard: "bothered" is for the caller's own match, and is kept as a safety
// event.
func TestBotheredRules(t *testing.T) {
	d := newM1Deck(t, m13Config(true))
	other, stranger := d.candidate(), d.candidate()
	match := d.matchWith(other)
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/matches/"+match.String()+"/bothered", `{"bothered":true}`, stranger); rec.Code != http.StatusNotFound {
		t.Fatalf("a stranger answered: %d", rec.Code)
	}
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/matches/"+match.String()+"/bothered", `{"bothered":true}`, d.viewer); rec.Code != http.StatusCreated {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body.String())
	}
	ctx := context.Background()
	tx, err := d.env.st.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var events, rows int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM dating_safety_events WHERE user_id = $1 AND kind = 'message_bothered'`, d.viewer).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM dating_message_feedback WHERE user_id = $1 AND other_id = $2`, d.viewer, other).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if events != 1 || rows != 1 {
		t.Fatalf("safety events %d, feedback rows %d, want 1 and 1", events, rows)
	}
}
