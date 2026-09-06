package events

// The product indexing path, end to end through the consumer.
//
// ─── WHAT THESE TESTS ARE ACTUALLY ABOUT ────────────────────────────────
//
// Not "does a publish index and an unpublish delete" — that part is
// trivial. The point of this design is that the EVENT TYPE DOES NOT DECIDE.
// Both events trigger the same read-back, and the document commerce returns
// is what settles it.
//
// So the tests that matter are the ones where the event and the truth
// disagree:
//
//	TestAStalePublishDoesNotResurrectARejectedListing
//	TestAnOutOfOrderPairSettlesOnTheCatalogue
//
// Under an implementation that switched on the event type, both of those
// leave a listing a moderator refused sitting in the search index with
// nothing in the system that would ever take it out.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/search-service/internal/commerceclient"
	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// ─── A fake OpenSearch that records what the consumer did ───────────────

type indexOp struct {
	Method string
	Index  string
	DocID  string
	Body   map[string]any
}

type fakeSearch struct {
	mu  sync.Mutex
	ops []indexOp
	srv *httptest.Server
}

func newFakeSearch(t *testing.T) (*search.Store, *fakeSearch) {
	t.Helper()
	f := &fakeSearch{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Index bootstrap: claim every index already exists so New() does
		// no creation work, and answer the alias probe with "no alias yet".
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 3 && parts[1] == "_doc" {
			op := indexOp{Method: r.Method, Index: parts[0], DocID: parts[2]}
			if r.Method == http.MethodPut || r.Method == http.MethodPost {
				_ = json.NewDecoder(r.Body).Decode(&op.Body)
			}
			f.mu.Lock()
			f.ops = append(f.ops, op)
			f.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"acknowledged":true,"result":"updated"}`)
	}))
	t.Cleanup(f.srv.Close)

	store, err := search.New(f.srv.URL)
	if err != nil {
		t.Fatalf("fake opensearch store: %v", err)
	}
	f.reset()
	return store, f
}

func (f *fakeSearch) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = nil
}

// productOps returns only the operations against the products alias.
func (f *fakeSearch) productOps() []indexOp {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []indexOp{}
	for _, op := range f.ops {
		if op.Index == search.IndexProducts {
			out = append(out, op)
		}
	}
	return out
}

// ─── A fake commerce whose answer the test controls ─────────────────────

type fakeCommerce struct {
	mu    sync.Mutex
	doc   *commerceclient.SearchDoc
	gone  bool
	fail  bool
	calls int
	srv   *httptest.Server
}

func newFakeCommerce(t *testing.T) (*commerceclient.Client, *fakeCommerce) {
	t.Helper()
	f := &fakeCommerce{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case f.fail:
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"code":"BOOM"}}`)
		case f.gone:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"code":"NOT_FOUND"}}`)
		default:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": f.doc})
		}
	}))
	t.Cleanup(f.srv.Close)
	return commerceclient.New(f.srv.URL, "test-key"), f
}

func (f *fakeCommerce) say(doc commerceclient.SearchDoc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doc, f.gone, f.fail = &doc, false, false
}

func (f *fakeCommerce) sayGone() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doc, f.gone, f.fail = nil, true, false
}

func (f *fakeCommerce) sayBroken() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doc, f.gone, f.fail = nil, false, true
}

func (f *fakeCommerce) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

const testProductID = "11111111-1111-1111-1111-111111111111"

func liveDoc() commerceclient.SearchDoc {
	return commerceclient.SearchDoc{
		ProductID:      testProductID,
		SellerID:       "22222222-2222-2222-2222-222222222222",
		Visible:        true,
		Status:         "active",
		ApprovalStatus: "approved",
		Title:          "Swami and Friends",
		MinPriceMinor:  129900,
		Currency:       "INR",
		CategoryID:     "cccccccc-cccc-cccc-cccc-cccccccccc03",
		CategoryName:   "Physics",
		CategoryPath: []commerceclient.Category{
			{ID: "cccccccc-cccc-cccc-cccc-cccccccccc01", Name: "Books", Slug: "books"},
			{ID: "cccccccc-cccc-cccc-cccc-cccccccccc03", Name: "Physics", Slug: "physics"},
		},
		Attributes: map[string]any{"binding": []any{"paperback"}},
	}
}

func rejectedDoc() commerceclient.SearchDoc {
	d := liveDoc()
	d.Visible = false
	d.ApprovalStatus = "rejected"
	return d
}

func newProductConsumer(t *testing.T) (*Consumer, *fakeSearch, *fakeCommerce) {
	t.Helper()
	store, fs := newFakeSearch(t)
	client, fc := newFakeCommerce(t)
	return (&Consumer{store: store}).WithCommerceClient(client), fs, fc
}

func envelopeFor(eventType, productID string) events.EventEnvelope {
	payload, _ := json.Marshal(events.ProductVisibilityPayload{ProductID: productID})
	return events.EventEnvelope{EventType: eventType, Payload: payload}
}

// deliver runs one message through the same switch the Kafka loop uses, so
// these tests exercise the real dispatch and not a hand-called helper.
func deliver(t *testing.T, c *Consumer, eventType string) error {
	t.Helper()
	raw, err := json.Marshal(envelopeFor(eventType, testProductID))
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return c.processMessage(context.Background(), kafka.Message{Value: raw})
}

// ─── The tests ──────────────────────────────────────────────────────────

func TestAPublishIndexesTheReadBackDocument(t *testing.T) {
	c, fs, fc := newProductConsumer(t)
	fc.say(liveDoc())

	if err := deliver(t, c, events.ProductPublished); err != nil {
		t.Fatalf("publish: %v", err)
	}

	ops := fs.productOps()
	if len(ops) != 1 || ops[0].Method != http.MethodPut {
		t.Fatalf("ops = %+v, want one index write against the products alias", ops)
	}
	if ops[0].DocID != testProductID {
		t.Fatalf("document id = %q, want the product id", ops[0].DocID)
	}
	// The document is the READ-BACK, not the payload — the payload carried
	// no title, no price and no category at all.
	if ops[0].Body["title"] != "Swami and Friends" {
		t.Fatalf("indexed title = %v; the document must come from the read-back", ops[0].Body["title"])
	}
	if ops[0].Body["min_price_minor"] != float64(129900) {
		t.Fatalf("indexed min_price_minor = %v, want 129900", ops[0].Body["min_price_minor"])
	}
	ids, _ := ops[0].Body["category_ids"].([]any)
	if len(ids) != 2 {
		t.Fatalf("category_ids = %v, want the ancestor chain", ops[0].Body["category_ids"])
	}
}

func TestAnUnpublishDeletesTheDocument(t *testing.T) {
	c, fs, fc := newProductConsumer(t)
	fc.say(rejectedDoc())

	if err := deliver(t, c, events.ProductUnpublished); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	ops := fs.productOps()
	if len(ops) != 1 || ops[0].Method != http.MethodDelete {
		t.Fatalf("ops = %+v, want one delete", ops)
	}
}

// A REPLAY. The same message arriving twice must leave the index in the
// same state as one arrival — this is what lets the DLQ replayer re-apply
// anything without an operator reasoning about what it will do.
func TestAReplayIsIdempotent(t *testing.T) {
	c, fs, fc := newProductConsumer(t)
	fc.say(liveDoc())

	for i := 0; i < 3; i++ {
		if err := deliver(t, c, events.ProductPublished); err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
	}
	ops := fs.productOps()
	if len(ops) != 3 {
		t.Fatalf("got %d ops for 3 deliveries", len(ops))
	}
	for i, op := range ops {
		if op.Method != http.MethodPut || op.DocID != testProductID {
			t.Fatalf("op %d = %+v, want an upsert of the same document", i, op)
		}
		if fmt.Sprint(op.Body) != fmt.Sprint(ops[0].Body) {
			t.Fatalf("delivery %d produced a different document; a replay must converge, not drift", i)
		}
	}
}

// A STALE PUBLISH. The listing was approved, then rejected. A `published`
// event replayed from the DLQ afterwards must NOT put it back.
//
// This is the test that fails under "switch on the event type", and it
// fails by leaving a rejected listing publicly searchable forever.
func TestAStalePublishDoesNotResurrectARejectedListing(t *testing.T) {
	c, fs, fc := newProductConsumer(t)

	fc.say(liveDoc())
	if err := deliver(t, c, events.ProductPublished); err != nil {
		t.Fatalf("initial publish: %v", err)
	}
	// The catalogue moves on: a moderator rejects the listing.
	fc.say(rejectedDoc())
	fs.reset()

	// The OLD publish event is replayed.
	if err := deliver(t, c, events.ProductPublished); err != nil {
		t.Fatalf("replayed publish: %v", err)
	}

	ops := fs.productOps()
	if len(ops) != 1 || ops[0].Method != http.MethodDelete {
		t.Fatalf("ops = %+v.\n"+
			"A replayed `published` for a listing the catalogue has since rejected must DELETE "+
			"the document. Trusting the event type here leaves a rejected listing in search with "+
			"nothing that would ever remove it.", ops)
	}
}

// OUT OF ORDER. These events are not keyed by product, so an approve and a
// reject can arrive backwards. Whichever order they arrive in, the index
// must end up agreeing with the catalogue.
func TestAnOutOfOrderPairSettlesOnTheCatalogue(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order []string
	}{
		{"in order", []string{events.ProductPublished, events.ProductUnpublished}},
		{"backwards", []string{events.ProductUnpublished, events.ProductPublished}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, fs, fc := newProductConsumer(t)
			// The catalogue's final word: the listing is NOT visible.
			fc.say(rejectedDoc())

			for _, et := range tc.order {
				if err := deliver(t, c, et); err != nil {
					t.Fatalf("%s: %v", et, err)
				}
			}
			ops := fs.productOps()
			if len(ops) != 2 {
				t.Fatalf("got %d ops, want one per event", len(ops))
			}
			for i, op := range ops {
				if op.Method != http.MethodDelete {
					t.Fatalf("op %d = %+v.\n"+
						"Both events must resolve to what the catalogue says NOW. Arrival order "+
						"decides nothing.", i, op)
				}
			}
		})
	}
}

// A product commerce no longer has is deleted, and that is a FACT, not a
// failure — so it must not retry or dead-letter.
func TestAGoneProductIsDeletedNotRetried(t *testing.T) {
	c, fs, fc := newProductConsumer(t)
	fc.sayGone()

	if err := deliver(t, c, events.ProductPublished); err != nil {
		t.Fatalf("a 404 from commerce must not be an error: %v", err)
	}
	ops := fs.productOps()
	if len(ops) != 1 || ops[0].Method != http.MethodDelete {
		t.Fatalf("ops = %+v, want one delete", ops)
	}
}

// A commerce OUTAGE must be an error. Rendering "I could not reach
// commerce" as "this listing is not visible" would empty the product index
// during an outage — the one failure mode a read-back design has to refuse.
func TestACommerceOutageIsAnErrorNotAnEmptyIndex(t *testing.T) {
	c, fs, fc := newProductConsumer(t)
	fc.sayBroken()

	err := deliver(t, c, events.ProductPublished)
	if err == nil {
		t.Fatal("a failed read-back returned nil; the message would be committed and the listing " +
			"would silently never be indexed")
	}
	if ops := fs.productOps(); len(ops) != 0 {
		t.Fatalf("ops = %+v; nothing may be written to the index on an unresolved read-back", ops)
	}
}

// No commerce client wired is the same class of problem: refuse, do not
// quietly drop the event.
func TestAnUnwiredConsumerRefusesRatherThanDropping(t *testing.T) {
	store, fs := newFakeSearch(t)
	c := &Consumer{store: store}

	if err := deliver(t, c, events.ProductPublished); err == nil {
		t.Fatal("an unwired consumer returned nil; the offset would advance and the event would " +
			"be lost")
	}
	if ops := fs.productOps(); len(ops) != 0 {
		t.Fatalf("ops = %+v, want none", ops)
	}
}

// The legacy event that never fired. Kept only so a DLQ payload from before
// this step decodes — and when it does, it goes through the SAME read-back
// rather than indexing the stale title and price it carries.
func TestALegacyProductListedGoesThroughTheReadBack(t *testing.T) {
	c, fs, fc := newProductConsumer(t)
	fc.say(liveDoc())

	payload, _ := json.Marshal(events.ProductListedPayload{
		ProductID: testProductID,
		Title:     "A TITLE FROM 2025",
		Price:     1.00,
	})
	raw, _ := json.Marshal(events.EventEnvelope{
		EventType: events.ProductListed, Payload: payload,
	})
	if err := c.processMessage(context.Background(), kafka.Message{Value: raw}); err != nil {
		t.Fatalf("legacy replay: %v", err)
	}

	ops := fs.productOps()
	if len(ops) != 1 {
		t.Fatalf("ops = %+v, want one", ops)
	}
	if ops[0].Body["title"] == "A TITLE FROM 2025" {
		t.Fatal("the legacy payload's title reached the index; the whole reason for the read-back " +
			"is that a payload's copy of the catalogue goes stale")
	}
	if fc.callCount() == 0 {
		t.Fatal("commerce was never consulted")
	}
}
