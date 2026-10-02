package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M5 — first move (DATING_FIRST_MOVE_ENABLED): golden fixtures and
// the guards behind them. Same harness as m1_deck_test.go.

var m5Fixtures = []string{
	"first_move_get_200",
	"first_move_put_200",
	"first_move_put_400_too_many_questions",
	"first_move_get_404_not_enabled",
	"match_get_200_first_move_waiting",
	"match_get_200_first_move_yours",
	"match_opening_answer_post_201",
	"match_opening_answer_409_not_pending",
	"match_extend_post_200_free",
	"match_extend_429_limit_reached",
}

// chatRecorder is a message client that records the conversations created
// (with their first movers) and the opening answers posted.
type chatRecorder struct {
	mu        sync.Mutex
	creates   []service.CreateConversationRequest
	answers   []string
	answerErr error
}

func (c *chatRecorder) CreateConversation(_ context.Context, req service.CreateConversationRequest) (*service.CreateConversationResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.creates = append(c.creates, req)
	return &service.CreateConversationResponse{ConversationID: uuid.New().String()}, nil
}

func (c *chatRecorder) SendOpeningAnswer(_ context.Context, _, _ uuid.UUID, text, _ string) (*service.CreateConversationResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.answerErr != nil {
		return nil, c.answerErr
	}
	c.answers = append(c.answers, text)
	return &service.CreateConversationResponse{}, nil
}

func (c *chatRecorder) lastMovers() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.creates) == 0 {
		return nil
	}
	return c.creates[len(c.creates)-1].FirstMoverIDs
}

func m5Config(on bool) service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, FirstMove: on}
}

func newM5Deck(t *testing.T, on bool) (*m1Deck, *chatRecorder) {
	t.Helper()
	d := newM1Deck(t, m5Config(on))
	chat := &chatRecorder{}
	d.env.svc.SetMessageClient(chat)
	return d, chat
}

func putFirstMove(r http.Handler, user uuid.UUID, body string) *httptest.ResponseRecorder {
	return contractDo(r, http.MethodPut, "/v1/dating/first-move", body, user)
}

// matchWith makes the viewer and other match (other sparks first) and
// returns the match id.
func (d *m1Deck) matchWith(other uuid.UUID) uuid.UUID {
	d.t.Helper()
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", sparkBody(d.viewer, "m5"), other); rec.Code != http.StatusCreated {
		d.t.Fatalf("other sparks: %d %s", rec.Code, rec.Body.String())
	}
	rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", sparkBody(other, "m5"), d.viewer)
	var body struct {
		Data struct {
			MatchID string `json:"match_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	id, err := uuid.Parse(body.Data.MatchID)
	if rec.Code != http.StatusCreated || err != nil {
		d.t.Fatalf("viewer sparks back: %d %s", rec.Code, rec.Body.String())
	}
	return id
}

type m5Match struct {
	ExpiresAt time.Time `json:"expires_at"`
	FirstMove *struct {
		YouMoveFirst     bool `json:"you_move_first"`
		CanExtend        bool `json:"can_extend"`
		OpeningQuestions []struct {
			ID   uuid.UUID `json:"id"`
			Text string    `json:"text"`
		} `json:"opening_questions"`
	} `json:"first_move"`
}

func getM5Match(t *testing.T, r http.Handler, matchID, user uuid.UUID) m5Match {
	t.Helper()
	rec := contractDo(r, http.MethodGet, "/v1/dating/matches/"+matchID.String(), ``, user)
	if rec.Code != http.StatusOK {
		t.Fatalf("get match: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data m5Match `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func answerBody(questionID uuid.UUID, answer string) string {
	b, _ := json.Marshal(map[string]string{"question_id": questionID.String(), "answer": answer})
	return string(b)
}

func TestM5FirstMoveContracts(t *testing.T) {
	d, _ := newM5Deck(t, true)
	// The match stores the pair in id order (user_a < user_b); keep the
	// viewer first so the fixture does not depend on random ids.
	other := d.candidate()
	for other.String() < d.viewer.String() {
		other = d.candidate()
	}
	labels := map[uuid.UUID]string{d.viewer: "<viewer>", other: "<other>"}

	assertContract(t, putFirstMove(d.env.r, other, `{"enabled":true,"questions":["What does your perfect Sunday look like?","Tea or coffee, and why?"]}`),
		http.StatusOK, "first_move_put_200", labels)
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/first-move", ``, other), http.StatusOK, "first_move_get_200", labels)
	assertContract(t, putFirstMove(d.env.r, other, `{"questions":["a","b","c","d"]}`),
		http.StatusBadRequest, "first_move_put_400_too_many_questions", labels)

	match := d.matchWith(other)
	labels[match] = "<match>"
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/matches/"+match.String(), ``, d.viewer),
		http.StatusOK, "match_get_200_first_move_waiting", labels)
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/matches/"+match.String(), ``, other),
		http.StatusOK, "match_get_200_first_move_yours", labels)

	assertContract(t, contractDo(d.env.r, http.MethodPost, "/v1/dating/matches/"+match.String()+"/extend", ``, d.viewer),
		http.StatusOK, "match_extend_post_200_free", labels)
	assertContract(t, contractDo(d.env.r, http.MethodPost, "/v1/dating/matches/"+match.String()+"/extend", ``, d.viewer),
		http.StatusTooManyRequests, "match_extend_429_limit_reached", labels)

	q := getM5Match(t, d.env.r, match, d.viewer).FirstMove.OpeningQuestions[0].ID
	assertContract(t, contractDo(d.env.r, http.MethodPost, "/v1/dating/matches/"+match.String()+"/opening-answer", answerBody(q, "A long walk, then dosa."), d.viewer),
		http.StatusCreated, "match_opening_answer_post_201", labels)
	assertContract(t, contractDo(d.env.r, http.MethodPost, "/v1/dating/matches/"+match.String()+"/opening-answer", answerBody(q, "x"), other),
		http.StatusConflict, "match_opening_answer_409_not_pending", labels)

	off, _ := newM5Deck(t, false)
	assertContract(t, contractDo(off.env.r, http.MethodGet, "/v1/dating/first-move", ``, off.viewer),
		http.StatusNotFound, "first_move_get_404_not_enabled", map[uuid.UUID]string{off.viewer: "<viewer>"})
}

func TestM5ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m5Fixtures {
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
		if strings.Contains(string(raw), "first_mover_ids") {
			t.Fatalf("%s: exposes the raw first mover list", name)
		}
	}
}

// Guard: the opted-in person is sent to chat as the first mover, and the
// match gets the 24-hour window.
func TestFirstMoveChatGetsTheOptedInPerson(t *testing.T) {
	d, chat := newM5Deck(t, true)
	other := d.candidate()
	if rec := putFirstMove(d.env.r, other, `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("opt in: %d %s", rec.Code, rec.Body.String())
	}
	match := d.matchWith(other)
	if got := chat.lastMovers(); len(got) != 1 || got[0] != other.String() {
		t.Fatalf("chat first movers = %v, want [%s]", got, other)
	}
	m := getM5Match(t, d.env.r, match, d.viewer)
	if left := time.Until(m.ExpiresAt); left < 23*time.Hour || left > 25*time.Hour {
		t.Fatalf("first-move match expires in %s, want about 24h", left)
	}
	if m.FirstMove == nil || m.FirstMove.YouMoveFirst {
		t.Fatalf("waiting viewer's first_move = %+v", m.FirstMove)
	}
}

// Guard: nobody opted in, or the flag off, means no rule and seven days.
func TestFirstMoveNoOptInOrFlagOffChangesNothing(t *testing.T) {
	for _, on := range []bool{true, false} {
		d, chat := newM5Deck(t, on)
		other := d.candidate()
		if !on {
			// Opted in before the flag went off: the match still has no rule.
			if err := d.env.st.SetFirstMoveSettings(context.Background(), other, ptrBool(true), nil); err != nil {
				t.Fatal(err)
			}
		}
		match := d.matchWith(other)
		if got := chat.lastMovers(); len(got) != 0 {
			t.Fatalf("flag=%v: chat got first movers %v", on, got)
		}
		m := getM5Match(t, d.env.r, match, d.viewer)
		if m.FirstMove != nil {
			t.Fatalf("flag=%v: a match without a rule carries first_move", on)
		}
		if left := time.Until(m.ExpiresAt); left < 6*24*time.Hour {
			t.Fatalf("flag=%v: expires in %s, want the seven-day window", on, left)
		}
	}
}

// Guard: only the waiting person may answer, only a first mover's live
// question, and the answer passes moderation.
func TestOpeningAnswerRules(t *testing.T) {
	d, chat := newM5Deck(t, true)
	other := d.candidate()
	if rec := putFirstMove(d.env.r, other, `{"enabled":true,"questions":["Tea or coffee?"]}`); rec.Code != http.StatusOK {
		t.Fatalf("mover setup: %d %s", rec.Code, rec.Body.String())
	}
	// The viewer has a question too, but is not a first mover.
	if rec := putFirstMove(d.env.r, d.viewer, `{"questions":["Mountains or sea?"]}`); rec.Code != http.StatusOK {
		t.Fatalf("viewer setup: %d %s", rec.Code, rec.Body.String())
	}
	match := d.matchWith(other)
	path := "/v1/dating/matches/" + match.String() + "/opening-answer"
	moverQ := getM5Match(t, d.env.r, match, d.viewer).FirstMove.OpeningQuestions[0].ID
	viewerQs, _ := d.env.st.LiveOpeningQuestions(context.Background(), []uuid.UUID{d.viewer})

	d.wantRefusal(contractDo(d.env.r, http.MethodPost, path, answerBody(viewerQs[d.viewer][0].ID, "Sea"), d.viewer), http.StatusNotFound, "OPENING_QUESTION_UNKNOWN")
	d.wantRefusal(contractDo(d.env.r, http.MethodPost, path, answerBody(moverQ, "Call me on 9876543210"), d.viewer), http.StatusBadRequest, "OPENING_ANSWER_REFUSED")
	d.wantRefusal(contractDo(d.env.r, http.MethodPost, path, answerBody(moverQ, "   "), d.viewer), http.StatusBadRequest, "OPENING_ANSWER_INVALID")
	d.wantRefusal(contractDo(d.env.r, http.MethodPost, path, answerBody(moverQ, "Coffee"), other), http.StatusConflict, "FIRST_MOVE_NOT_PENDING")
	if len(chat.answers) != 0 {
		t.Fatalf("a refused answer reached chat: %v", chat.answers)
	}
	if rec := contractDo(d.env.r, http.MethodPost, path, answerBody(moverQ, "Coffee, always."), d.viewer); rec.Code != http.StatusCreated {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body.String())
	}
	if len(chat.answers) != 1 || !strings.Contains(chat.answers[0], "Tea or coffee?") || !strings.Contains(chat.answers[0], "Coffee, always.") {
		t.Fatalf("chat got %v, want the question and the answer", chat.answers)
	}
	// A replaced (archived) question can no longer be answered.
	if rec := putFirstMove(d.env.r, other, `{"questions":["Something new?"]}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	d.wantRefusal(contractDo(d.env.r, http.MethodPost, path, answerBody(moverQ, "Again"), d.viewer), http.StatusNotFound, "OPENING_QUESTION_UNKNOWN")
}

// Guard: the free extend is the waiting person's, once per rolling 24
// hours; the first mover gets the premium path.
func TestFreeExtendIsTheWaitingPersonsOnceADay(t *testing.T) {
	d, _ := newM5Deck(t, true)
	other := d.candidate()
	if rec := putFirstMove(d.env.r, other, `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	match := d.matchWith(other)
	path := "/v1/dating/matches/" + match.String() + "/extend"
	before := getM5Match(t, d.env.r, match, d.viewer).ExpiresAt
	if rec := contractDo(d.env.r, http.MethodPost, path, ``, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("free extend: %d %s", rec.Code, rec.Body.String())
	}
	after := getM5Match(t, d.env.r, match, d.viewer)
	if gain := after.ExpiresAt.Sub(before); gain < 23*time.Hour || gain > 25*time.Hour {
		t.Fatalf("free extend added %s, want 24h", gain)
	}
	if after.FirstMove.CanExtend {
		t.Fatalf("can_extend still true after the free extend")
	}
	d.wantRefusal(contractDo(d.env.r, http.MethodPost, path, ``, d.viewer), http.StatusTooManyRequests, "EXTEND_LIMIT_REACHED")
	d.wantRefusal(contractDo(d.env.r, http.MethodPost, path, ``, other), http.StatusForbidden, "FORBIDDEN")
}

// Guard: opening questions are validated.
func TestOpeningQuestionValidation(t *testing.T) {
	d, _ := newM5Deck(t, true)
	for body, code := range map[string]string{
		`{"questions":["a","b","c","d"]}`:                    "OPENING_QUESTIONS_TOO_MANY",
		`{"questions":["   "]}`:                              "OPENING_QUESTION_INVALID",
		`{"questions":["` + strings.Repeat("x", 141) + `"]}`: "OPENING_QUESTION_INVALID",
		`{"questions":["Find me at https://x.example"]}`:     "OPENING_QUESTION_REFUSED",
	} {
		d.wantRefusal(putFirstMove(d.env.r, d.viewer, body), http.StatusBadRequest, code)
	}
}

// Guard: a block ends the opening answer like everything else.
func TestOpeningAnswerAfterABlockIsNotFound(t *testing.T) {
	d, _ := newM5Deck(t, true)
	other := d.candidate()
	if rec := putFirstMove(d.env.r, other, `{"enabled":true,"questions":["Tea or coffee?"]}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	match := d.matchWith(other)
	q := getM5Match(t, d.env.r, match, d.viewer).FirstMove.OpeningQuestions[0].ID
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/block", `{"target_user_id":"`+d.viewer.String()+`"}`, other); rec.Code != http.StatusOK {
		t.Fatalf("block: %d %s", rec.Code, rec.Body.String())
	}
	d.wantRefusal(contractDo(d.env.r, http.MethodPost, "/v1/dating/matches/"+match.String()+"/opening-answer", answerBody(q, "Coffee"), d.viewer), http.StatusNotFound, "NOT_FOUND")
}

func ptrBool(b bool) *bool { return &b }
