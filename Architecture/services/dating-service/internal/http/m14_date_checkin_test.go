package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	datingevents "github.com/atpost/dating-service/internal/events"
	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Mechanic M14 — after-date check-ins (DATING_DATE_CHECKIN_ENABLED): golden
// fixtures and the guards behind them. Same harness as m1_deck_test.go.

var m14Fixtures = []string{
	"date_checkins_get_200",
	"date_feedback_post_201",
	"date_feedback_post_201_unsafe",
	"date_feedback_post_400_invalid",
	"date_feedback_post_404_not_enabled",
}

func m14Config(on bool) service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, DateCheckin: on}
}

// planMeet schedules a safe meet between the viewer and other, then moves it
// hoursAgo into the past, and returns its id.
func (d *m1Deck) planMeet(other uuid.UUID, hoursAgo int) uuid.UUID {
	d.t.Helper()
	when := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"with_user_id":"` + other.String() + `","when":"` + when + `","latitude":17.385,"longitude":78.4867,"venue":"Cafe"}`
	rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/meet", body, d.viewer)
	var out struct {
		Data struct {
			ID uuid.UUID `json:"meet_id"`
		} `json:"data"`
	}
	if rec.Code/100 != 2 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Data.ID == uuid.Nil {
		d.t.Fatalf("schedule meet: %d %s", rec.Code, rec.Body.String())
	}
	d.exec(`UPDATE dating_meets SET scheduled_at = now() - make_interval(hours => $2) WHERE id = $1`, out.Data.ID, hoursAgo)
	return out.Data.ID
}

func (d *m1Deck) dateFeedback(user, match uuid.UUID, body string) *httptest.ResponseRecorder {
	d.t.Helper()
	return contractDo(d.env.r, http.MethodPost, "/v1/dating/matches/"+match.String()+"/date-feedback", body, user)
}

// checkinsAsked returns the dating.date_checkin.due events published, by
// recipient.
func checkinsAsked(t *testing.T, w *scamWriter) map[uuid.UUID]datingevents.DateCheckinDuePayload {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[uuid.UUID]datingevents.DateCheckinDuePayload{}
	for _, m := range w.msgs {
		var env struct {
			EventType string          `json:"event_type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(m.Value, &env); err != nil {
			t.Fatal(err)
		}
		if env.EventType != events.EventDatingDateCheckinDue {
			continue
		}
		var p datingevents.DateCheckinDuePayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out[uuid.MustParse(p.RecipientID)] = p
	}
	return out
}

func newM14Deck(t *testing.T, on bool) (*m1Deck, *scamWriter) {
	t.Helper()
	d := newM1Deck(t, m14Config(on))
	w := &scamWriter{}
	d.env.svc.SetProducer(datingevents.NewProducerWithWriter(w))
	// Earlier tests leave due meets behind: mark them asked so this test's
	// sweep sees only its own.
	d.exec(`UPDATE dating_meets SET date_checkin_asked_at = now() WHERE date_checkin_asked_at IS NULL`)
	return d, w
}

func TestM14DateCheckinContracts(t *testing.T) {
	d, _ := newM14Deck(t, true)
	other := d.candidate()
	match := d.matchWith(other)
	meet := d.planMeet(other, 4)
	if _, err := d.env.svc.SendDueDateCheckins(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	labels := map[uuid.UUID]string{d.viewer: "<viewer>", other: "<other>", match: "<match>", meet: "<meet>"}
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/date-checkins", ``, d.viewer), http.StatusOK, "date_checkins_get_200", labels)
	assertContract(t, d.dateFeedback(d.viewer, match, `{"met":"yes","again":"yes","felt_safe":true}`), http.StatusCreated, "date_feedback_post_201", labels)
	assertContract(t, d.dateFeedback(other, match, `{"met":"yes","again":"no","felt_safe":false}`), http.StatusCreated, "date_feedback_post_201_unsafe", labels)
	assertContract(t, d.dateFeedback(d.viewer, match, `{"met":"no","again":"yes"}`), http.StatusBadRequest, "date_feedback_post_400_invalid", labels)

	off, _ := newM14Deck(t, false)
	assertContract(t, off.dateFeedback(off.viewer, uuid.New(), `{"met":"yes"}`),
		http.StatusNotFound, "date_feedback_post_404_not_enabled", map[uuid.UUID]string{off.viewer: "<viewer>"})
}

func TestM14ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m14Fixtures {
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

// Guard: a meet is asked about once, both people, only once it is 3 hours
// past and not yet a week old.
func TestDateCheckinAsksBothOnceWhenDue(t *testing.T) {
	d, w := newM14Deck(t, true)
	due, soon, stale := d.candidate(), d.candidate(), d.candidate()
	d.matchWith(due)
	d.matchWith(soon)
	d.matchWith(stale)
	d.planMeet(due, 4)
	d.planMeet(soon, 1)
	d.planMeet(stale, 24*8)
	if _, err := d.env.svc.SendDueDateCheckins(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	got := checkinsAsked(t, w)
	if len(got) != 2 || got[d.viewer].WithFirstName != "Asha" || got[due].MatchID == "" {
		t.Fatalf("asked = %+v, want the viewer and the due date's partner", got)
	}
	if _, asked := got[soon]; asked {
		t.Fatalf("a date one hour past was asked about")
	}
	if _, asked := got[stale]; asked {
		t.Fatalf("a date eight days past was asked about")
	}
	w.msgs = nil
	if _, err := d.env.svc.SendDueDateCheckins(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if again := checkinsAsked(t, w); len(again) != 0 {
		t.Fatalf("asked twice: %+v", again)
	}
}

// Guard: an answer closes the ask; someone else's match is not found; a
// "not safe" answer is kept as a safety event.
func TestDateFeedbackRules(t *testing.T) {
	d, _ := newM14Deck(t, true)
	other, stranger := d.candidate(), d.candidate()
	match := d.matchWith(other)
	d.planMeet(other, 4)
	if _, err := d.env.svc.SendDueDateCheckins(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	pending := func(user uuid.UUID) int {
		rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/date-checkins", ``, user)
		var body struct {
			Data []json.RawMessage `json:"data"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			t.Fatalf("checkins: %d %s", rec.Code, rec.Body.String())
		}
		return len(body.Data)
	}
	if pending(d.viewer) != 1 || pending(other) != 1 {
		t.Fatalf("pending = %d / %d, want 1 / 1", pending(d.viewer), pending(other))
	}
	if rec := d.dateFeedback(d.viewer, match, `{"met":"yes","felt_safe":false}`); rec.Code != http.StatusCreated {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body.String())
	}
	if pending(d.viewer) != 0 || pending(other) != 1 {
		t.Fatalf("after answering: %d / %d, want 0 / 1", pending(d.viewer), pending(other))
	}
	if rec := d.dateFeedback(stranger, match, `{"met":"yes"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("a stranger answered about someone else's match: %d", rec.Code)
	}
	var n int
	ctx := context.Background()
	tx, err := d.env.st.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM dating_safety_events WHERE user_id = $1 AND kind = 'date_felt_unsafe'`, d.viewer).Scan(&n); err != nil || n != 1 {
		t.Fatalf("safety events = %d (err %v), want 1", n, err)
	}
	// Five answers per match is the most.
	for i := 0; i < 4; i++ {
		if rec := d.dateFeedback(d.viewer, match, `{"met":"yes"}`); rec.Code != http.StatusCreated {
			t.Fatalf("answer %d: %d", i+2, rec.Code)
		}
	}
	d.wantRefusal(d.dateFeedback(d.viewer, match, `{"met":"yes"}`), http.StatusTooManyRequests, "DATE_FEEDBACK_LIMIT")
}
