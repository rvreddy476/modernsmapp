package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	datingevents "github.com/atpost/dating-service/internal/events"
	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Mechanic M17 — scam alerts (DATING_SCAM_ALERT_ENABLED). Driven through the
// real report route and the admin action, with a Kafka writer that records
// (or refuses) what is published.

type scamWriter struct {
	mu   sync.Mutex
	msgs []kafka.Message
	fail bool
}

func (w *scamWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fail {
		return errors.New("kafka down")
	}
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *scamWriter) Close() error { return nil }

// alerts returns the scam alerts published so far, by recipient.
func (w *scamWriter) alerts(t *testing.T) map[uuid.UUID]datingevents.ScamAlertPayload {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[uuid.UUID]datingevents.ScamAlertPayload{}
	for _, m := range w.msgs {
		var env struct {
			EventType string          `json:"event_type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(m.Value, &env); err != nil {
			t.Fatal(err)
		}
		if env.EventType != events.EventDatingScamAlert {
			continue
		}
		var p datingevents.ScamAlertPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out[uuid.MustParse(p.RecipientID)] = p
	}
	return out
}

// matchPair makes a and b match (a sparks first).
func (d *m1Deck) matchPair(a, b uuid.UUID) {
	d.t.Helper()
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", sparkBody(b, "m17"), a); rec.Code != http.StatusCreated {
		d.t.Fatalf("spark: %d %s", rec.Code, rec.Body.String())
	}
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", sparkBody(a, "m17"), b); rec.Code != http.StatusCreated {
		d.t.Fatalf("spark back: %d %s", rec.Code, rec.Body.String())
	}
}

// reportAs files a report and returns its id.
func (d *m1Deck) reportAs(reporter, target uuid.UUID, reason string) uuid.UUID {
	d.t.Helper()
	body := `{"target_id":"` + target.String() + `","reason":"` + reason + `","details":"m17"}`
	rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/report", body, reporter)
	var out struct {
		Data struct {
			ID uuid.UUID `json:"id"`
		} `json:"data"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Data.ID == uuid.Nil {
		d.t.Fatalf("report: %d %s", rec.Code, rec.Body.String())
	}
	return out.Data.ID
}

type scamCase struct {
	d                         *m1Deck
	w                         *scamWriter
	scammer, recent, old, rep uuid.UUID
}

func newScamCase(t *testing.T, on bool) *scamCase {
	t.Helper()
	d := newM1Deck(t, service.MechanicsConfig{ScamAlert: on})
	w := &scamWriter{}
	d.env.svc.SetProducer(datingevents.NewProducerWithWriter(w))
	// The queue is shared by every test in this database: start empty.
	d.exec(`UPDATE dating_scam_alerts SET cancelled_at = now() WHERE sent_at IS NULL AND cancelled_at IS NULL`)
	c := &scamCase{d: d, w: w, scammer: d.candidate(), recent: d.candidate(), old: d.candidate(), rep: d.candidate()}
	d.matchPair(c.recent, c.scammer)
	d.matchPair(c.old, c.scammer)
	d.exec(`UPDATE dating_matches SET matched_at = now() - interval '100 days', last_message_at = NULL
        WHERE (user_a = $1 AND user_b = $2) OR (user_a = $2 AND user_b = $1)`, c.old, c.scammer)
	d.matchPair(c.rep, c.scammer)
	return c
}

func (c *scamCase) suspend(t *testing.T, reason string) {
	t.Helper()
	report := c.d.reportAs(c.rep, c.scammer, reason)
	if _, err := c.d.env.svc.ActOnReport(context.Background(), uuid.New(), report, c.scammer, "suspend"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
}

// Guard: a suspension on a scam report warns the recent match, by first
// name — not the reporter, not a match older than 90 days.
func TestScamAlertWarnsRecentMatches(t *testing.T) {
	c := newScamCase(t, true)
	c.suspend(t, "scam")
	got := c.w.alerts(t)
	if len(got) != 1 {
		t.Fatalf("alerts = %+v, want only the recent match", got)
	}
	a, ok := got[c.recent]
	if !ok || a.RemovedFirstName != "Asha" || a.MatchID == "" {
		t.Fatalf("alert to the recent match = %+v (present %v)", a, ok)
	}
	if _, warned := got[c.rep]; warned {
		t.Fatalf("the reporter was warned")
	}
	if _, warned := got[c.old]; warned {
		t.Fatalf("a match from 100 days ago was warned")
	}
	// Queueing again warns nobody twice.
	if n, err := c.d.env.st.QueueScamAlerts(context.Background(), c.scammer, c.rep); err != nil || n != 0 {
		t.Fatalf("a second queue added %d (err %v)", n, err)
	}
}

// Guard: another report reason, or the flag off, warns nobody.
func TestScamAlertOnlyForScamWithTheFlag(t *testing.T) {
	c := newScamCase(t, true)
	c.suspend(t, "harassment")
	if got := c.w.alerts(t); len(got) != 0 {
		t.Fatalf("a harassment suspension sent scam alerts: %+v", got)
	}
	off := newScamCase(t, false)
	off.suspend(t, "scam")
	if got := off.w.alerts(t); len(got) != 0 {
		t.Fatalf("the flag is off but alerts went out: %+v", got)
	}
	if n := off.queued(t); n != 0 {
		t.Fatalf("the flag is off but %d alerts were queued", n)
	}
}

// queued counts the alerts queued about the scammer.
func (c *scamCase) queued(t *testing.T) int {
	t.Helper()
	ctx := context.Background()
	tx, err := c.d.env.st.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM dating_scam_alerts WHERE subject_id = $1`, c.scammer).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Guard: an alert that could not be published stays queued and the next
// send delivers it.
func TestScamAlertRetries(t *testing.T) {
	c := newScamCase(t, true)
	c.w.fail = true
	c.suspend(t, "scam")
	if got := c.w.alerts(t); len(got) != 0 {
		t.Fatalf("published while Kafka was down: %+v", got)
	}
	c.w.fail = false
	if n, err := c.d.env.svc.SendPendingScamAlerts(context.Background()); err != nil || n != 1 {
		t.Fatalf("retry sent %d (err %v), want 1", n, err)
	}
	if got := c.w.alerts(t); len(got) != 1 {
		t.Fatalf("after the retry: %+v", got)
	}
	if n, _ := c.d.env.svc.SendPendingScamAlerts(context.Background()); n != 0 {
		t.Fatalf("a sent alert was sent again (%d)", n)
	}
}
