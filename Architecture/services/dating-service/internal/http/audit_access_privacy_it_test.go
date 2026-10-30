// Audit 2026-10-30, access & privacy lane — HTTP flows on the test database
// (CI only; skipped without TEST_PG_DSN): H1 (actions keyed by a candidate id
// need a served candidate; Nebula shows only visible people) and L4 (hidden
// spark notes are never sent, the recipient reads one on request).
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// pairInDeck makes candidate the one person in viewer's deck: a gender only
// candidate holds, which viewer's preference asks for.
func pairInDeck(t *testing.T, st *store.Store, viewer, candidate uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	gender := "h1-" + uuid.NewString()[:8]
	if _, err := st.UpsertProfile(ctx, candidate, store.UpsertProfileParams{Gender: &gender}); err != nil {
		t.Fatalf("pair: candidate gender: %v", err)
	}
	if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{InterestedInGender: &gender}); err != nil {
		t.Fatalf("pair: viewer preference: %v", err)
	}
}

// dealTo deals viewer's deck (GET /pulse/today, the client's own fetch) and
// fails unless candidate was dealt: audit H1 lets a pass, stash or person
// card act only on a card the viewer was dealt.
func dealTo(t *testing.T, r http.Handler, viewer, candidate uuid.UUID) {
	t.Helper()
	if !deckIDs(t, r, viewer)[candidate] {
		t.Fatalf("candidate %s was not dealt to the viewer", candidate)
	}
}

func nebulaIDs(t *testing.T, d *m1Deck) map[uuid.UUID]bool {
	t.Helper()
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/pulse/nebula", ``, d.viewer)
	var body m1DeckBody
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("nebula: %d %s", rec.Code, rec.Body.String())
	}
	out := map[uuid.UUID]bool{}
	for _, c := range body.Data {
		out[c.CandidateID] = true
	}
	return out
}

// H1: passing (or stashing) a user id the caller was never served is the
// one 404 CANDIDATE_UNAVAILABLE, nothing is recorded, and the victim never
// reaches Nebula; a served candidate who later pauses leaves Nebula too.
func TestH1HTTP_PassNeedsAServedCandidateAndNebulaHidesPaused(t *testing.T) {
	d := newM1Deck(t, m1Config(50, 50))
	ctx := context.Background()
	victim := uuid.New()
	mustSeedActiveProfile(t, d.env.st, victim) // active, but not in the viewer's deck
	for _, id := range []uuid.UUID{victim, uuid.New()} {
		d.wantRefusal(contractDo(d.env.r, http.MethodPost, "/v1/dating/pulse/"+id.String()+"/pass", ``, d.viewer),
			http.StatusNotFound, "CANDIDATE_UNAVAILABLE")
		d.wantRefusal(contractDo(d.env.r, http.MethodPost, "/v1/dating/stash", `{"candidate_id":"`+id.String()+`"}`, d.viewer),
			http.StatusNotFound, "CANDIDATE_UNAVAILABLE")
	}
	if standing, err := d.env.st.HasStandingPass(ctx, d.viewer, victim); err != nil || standing {
		t.Fatalf("refused pass was recorded (standing=%v err=%v)", standing, err)
	}
	if nebulaIDs(t, d)[victim] {
		t.Fatalf("the never-served victim is in Nebula")
	}

	// A served candidate: the pass lands, a retry answers the same, Nebula
	// shows them — until they pause.
	served := d.candidate()
	d.dealt(served)
	d.pass(served)
	d.pass(served)
	if !nebulaIDs(t, d)[served] {
		t.Fatalf("passed candidate missing from Nebula")
	}
	if _, err := d.env.st.TransitionProfileStatus(ctx, served, store.ProfileEventPause, store.ProfileActorUser); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if nebulaIDs(t, d)[served] {
		t.Fatalf("a paused profile is still readable through Nebula")
	}
}

// L4: a note the recipient's filter hides is not in the incoming list; the
// recipient — and only the recipient — reads it with GET /sparks/:id/note.
func TestL4HTTP_HiddenNoteNeverSentRevealOnRequest(t *testing.T) {
	d := newM1Deck(t, m13Config(true))
	sender := d.candidate()
	spark := d.sparkWithNote(sender, "you look stupid")
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/sparks/incoming", ``, d.viewer)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"note_hidden":"unkind"`) {
		t.Fatalf("incoming: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "stupid") {
		t.Fatalf("incoming list carries the hidden note text: %s", rec.Body.String())
	}
	liked := contractDo(d.env.r, http.MethodGet, "/v1/dating/liked-you", ``, d.viewer)
	if strings.Contains(liked.Body.String(), "stupid") {
		t.Fatalf("liked-you carries the hidden note text: %s", liked.Body.String())
	}
	path := "/v1/dating/sparks/" + spark.String() + "/note"
	reveal := contractDo(d.env.r, http.MethodGet, path, ``, d.viewer)
	if reveal.Code != http.StatusOK || !strings.Contains(reveal.Body.String(), `"note":"you look stupid"`) {
		t.Fatalf("reveal: %d %s", reveal.Code, reveal.Body.String())
	}
	for _, who := range []uuid.UUID{sender, uuid.New()} {
		if w := contractDo(d.env.r, http.MethodGet, path, ``, who); w.Code != http.StatusNotFound {
			t.Fatalf("reveal by a non-recipient: %d %s, want 404", w.Code, w.Body.String())
		}
	}
}

// H1 x L3 regression: the served-card check must not draw a fresh random
// deck. With one card of the allowance left and two equally ranked
// candidates, the card served is a coin flip; with no cached batch (this rig
// has no Redis) the pass check used to recompute the deck, cut it to that one
// card and refuse the served card half the time (CI: TestM1DeckContracts
// "pass: status 404 CANDIDATE_UNAVAILABLE"). Eight viewers make the old
// behaviour fail with probability 1 - 2^-8; the fix is deterministic.
func TestH1HTTP_ServedCardIsPassableWithoutACachedDeck(t *testing.T) {
	for i := 0; i < 8; i++ {
		d := newM1Deck(t, m1Config(1, 1))
		d.candidate()
		d.candidate()
		ids, _ := d.deck()
		if len(ids) != 1 {
			t.Fatalf("round %d: deck = %v, want exactly the one card the allowance leaves", i, ids)
		}
		for served := range ids {
			d.pass(served) // fails the round on anything but 200
		}
	}
}
