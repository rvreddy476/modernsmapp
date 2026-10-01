//go:build integration

package http

// Golden contract fixtures for MStore / MSeller (lane C1).
//
// Every file under testdata/contracts/<area>/<route>_<status>[_<case>].json is
// the body the REAL handler wrote for that request, run through the
// production route table (RegisterRoutes + RegisterP0Routes + the fence), the
// production service and the production store, against a database the
// production bootstrap migrated. The web and Android lanes copy these files
// byte for byte and decode them with their own DTOs, so a fixture that drifts
// from the handler is a client that decodes the wrong shape.
//
// ─── THE DATABASE ────────────────────────────────────────────────────────
//
// commerce_it_test is shared with every other integration suite and none of
// them tear down, so an aggregate read (home, categories with product_count,
// the product grid) can never be byte-stable there. This test therefore does
// what internal/piibackfill already does: it creates a scratch database on
// the SAME server COMMERCE_TEST_DSN names, runs BootstrapSchema on it, seeds a
// small world with FIXED ids and FIXED timestamps, renders every fixture, and
// drops the database again. The scratch name ends in `_test`, which is the
// only shape testdsn.Refuse admits.
//
// ─── WHAT IS PINNED, AND WHAT IS NORMALISED ──────────────────────────────
//
// Everything seeded carries a fixed id (the 00000000-0000-4000-8000-0000000c…
// block) and a fixed timestamp, so a GET renders the same bytes on every run.
// A POST that creates a row (an address, a cart line, an order, a review, a
// product) gets a fresh uuid.New() from the store and NOW() from the
// database; those cannot be pinned without changing the store, so
// `stabilise` maps them to deterministic values AFTER the handler has
// written its bytes: the n-th fresh uuid of the run becomes
// 00000000-0000-4000-8000-0000000f000n, any timestamp produced during the
// run is re-based onto 2026-09-30T10:00:00Z at the same offset, a fresh
// order number keeps its sequence but is pinned to the 2026 series, and the
// clock-derived suffix service.uniqueSlug appends to a new product's slug is
// pinned to 00000. Nothing else is touched: keys, key order, nesting and
// every business value are the handler's own.
//
//	COMMERCE_TEST_DSN=… go test -tags=integration ./internal/http/ -run TestContractFixtures -v
//	COMMERCE_TEST_DSN=… go test -tags=integration ./internal/http/ -run TestContractFixtures -update
//
// `-update` rewrites every file; review the diff before committing it.

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/commerce-service/database"
	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/payments"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/commerce-service/internal/testdsn"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var updateContracts = flag.Bool("update", false, "rewrite the golden contract fixtures from the handlers")

// ctFixedNow is the instant every run-time timestamp is re-based onto.
var ctFixedNow = time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

// ─── The contract environment ────────────────────────────────────────────

type contractEnv struct {
	t        *testing.T
	pool     *pgxpool.Pool
	r        *gin.Engine
	svc      *service.Service
	payments *ctPaymentsServer
	runStart time.Time

	// freshIDs maps every uuid the run produced (not seeded) to its pinned
	// replacement, in order of first appearance across the whole run.
	freshIDs map[string]string
	knownIDs map[string]bool
}

// ctPaymentsServer is the payments-service the real *payments.Client talks
// to: an httptest server answering the two internal routes commerce calls on
// the buyer path, with a session mode the fixtures switch between.
type ctPaymentsServer struct {
	srv     *httptest.Server
	session string // "razorpay" | "stub"
	intents map[string]map[string]any
	created int
	// keys is every idempotency key commerce sent, in order. The fixtures
	// never read it; the lane C1 behaviour tests assert a retry's fresh key.
	keys []string
}

func newCtPaymentsServer() *ctPaymentsServer {
	p := &ctPaymentsServer{session: "razorpay", intents: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/payments/internal/intents", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ReferenceID string `json:"reference_id"`
			PayerID     string `json:"payer_id"`
			PayeeID     string `json:"payee_id"`
			AmountMinor int64  `json:"amount_minor"`
			Currency    string `json:"currency"`
			Method      string `json:"method"`
			IdemKey     string `json:"idempotency_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		p.keys = append(p.keys, in.IdemKey)
		// One intent per order, deterministic from the order id so a retry
		// collapses exactly as payments-service's idempotency key does. The
		// provider order handle counts up, so it is the same on every run
		// whatever fresh order id it was opened for.
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("contract-intent:"+in.ReferenceID)).String()
		p.created++
		providerRef := fmt.Sprintf("order_Contract%04d", p.created)
		intent := map[string]any{
			"id": id, "status": "pending", "amount_minor": in.AmountMinor, "currency": in.Currency,
			"method": in.Method, "provider_ref": providerRef,
			"reference_type": "order", "reference_id": in.ReferenceID,
			"payer_id": in.PayerID, "payee_id": in.PayeeID,
		}
		switch p.session {
		case "stub":
			// payments-service's stub mode names itself (WithStubSession):
			// no publishable key exists, so key_id is empty.
			intent["client_session"] = map[string]string{
				"provider": "stub", "order_id": providerRef, "key_id": "",
				"merchant_display_name": "Momentum Store",
			}
		default:
			intent["client_session"] = map[string]string{
				"provider": "razorpay", "order_id": providerRef,
				"key_id": "rzp_test_ContractKey01", "merchant_display_name": "Momentum Store",
			}
		}
		p.intents[id] = intent
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": intent})
	})
	mux.HandleFunc("/v1/payments/internal/intents/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/v1/payments/internal/intents/")
		id, verify := strings.CutSuffix(rest, "/verify")
		intent, ok := p.intents[id]
		if !ok {
			http.Error(w, `{"error":{"code":"NOT_FOUND"}}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if verify {
			// The ADVISORY verdict: genuine, echoing the intent's parties so
			// commerce can bind it to the order and the payer.
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"verified": true, "advisory": true, "status": "pending",
				"amount_minor": intent["amount_minor"], "payer_id": intent["payer_id"], "payee_id": intent["payee_id"],
				"reference_type": "order", "reference_id": intent["reference_id"], "application_id": "mstore",
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": intent})
	})
	p.srv = httptest.NewServer(mux)
	return p
}

// ctCourier is a deterministic courier: fixed rate, fixed AWB per order.
type ctCourier struct{}

func (ctCourier) Name() string { return "stub" }
func (ctCourier) CreateShipment(_ context.Context, req courier.ShipmentRequest) (*courier.ShipmentResponse, error) {
	awb := "CT" + strings.ToUpper(req.OrderID[len(req.OrderID)-6:])
	return &courier.ShipmentResponse{
		CourierOrderID: "stub-" + awb, AWBNumber: awb, CourierName: "stub",
		LabelURL:     "https://example.test/labels/" + awb + ".pdf",
		TrackingURL:  "https://example.test/track/" + awb,
		EstimatedETA: ctFixedNow.Add(72 * time.Hour),
	}, nil
}
func (ctCourier) CancelShipment(context.Context, string) error { return nil }
func (ctCourier) ParseWebhook(context.Context, []byte) ([]courier.TrackingUpdate, error) {
	return nil, nil
}
func (ctCourier) VerifyWebhook(map[string]string, []byte) error { return nil }
func (ctCourier) CheckServiceability(_ context.Context, req courier.ServiceabilityRequest) (*courier.ServiceabilityResult, error) {
	if req.DropPincode == "999999" {
		return &courier.ServiceabilityResult{Serviceable: false, Courier: "stub", Reason: "pincode not serviceable"}, nil
	}
	return &courier.ServiceabilityResult{
		Serviceable: true, Courier: "stub", EstimatedDays: 3,
		EstimatedETA: ctFixedNow.AddDate(0, 0, 3), ShippingChargeMinor: 4900,
	}, nil
}

// newCtMediaServer is the media-service the real *media.Client talks to:
// every id in the …0000000dXXXX block is a ready, moderation-passed image
// uploaded by the seller, and the batch resolver hands back fixed renditions.
func newCtMediaServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/media/batch", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IDs []string `json:"ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		data := map[string]any{}
		for _, id := range in.IDs {
			if !strings.HasPrefix(id, "00000000-0000-4000-8000-0000000d") {
				continue
			}
			data[id] = map[string]any{
				"media_id": id,
				"variants": map[string]string{
					"original":    "https://media.example.test/" + id + "/original.jpg",
					"thumb_150":   "https://media.example.test/" + id + "/thumb_150.jpg",
					"small_480":   "https://media.example.test/" + id + "/small_480.jpg",
					"medium_1080": "https://media.example.test/" + id + "/medium_1080.jpg",
				},
				"blurhash": "LEHV6nWB2yk8pyo0adR*.7kCMdnj",
				"width":    1080, "height": 1080,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	mux.HandleFunc("/v1/media/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/v1/media/")
		if !strings.HasPrefix(id, "00000000-0000-4000-8000-0000000d") {
			http.Error(w, `{"error":{"code":"NOT_FOUND"}}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"id": id, "uploader_id": ctSellerUser.String(), "file_type": "image",
			"processing_status": "ready", "moderation_status": "passed",
		}})
	})
	return httptest.NewServer(mux)
}

// ctBlob answers a fixed signed URL for the invoice route.
type ctBlob struct{}

func (ctBlob) Upload(context.Context, string, []byte, string) error { return nil }
func (ctBlob) PresignedGetURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://media.example.test/" + key + "?X-Amz-Signature=contract", nil
}

// ctScratchPool creates the scratch database beside COMMERCE_TEST_DSN's and
// bootstraps it with the production migrations. Dropped on cleanup.
func ctScratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("COMMERCE_TEST_DSN")
	if dsn == "" {
		t.Skip("COMMERCE_TEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	scratch := "commerce_contract_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "") + "_test"
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+scratch); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	admin.Close()

	scratchDSN := testdsn.SwapDatabase(dsn, scratch)
	testdsn.Refuse(scratchDSN) // belt and braces: the name we just built must pass the guard
	pool, err := pgxpool.New(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("connect to scratch: %v", err)
	}
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrapping the scratch schema: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if cleanup, err := pgxpool.New(ctx, dsn); err == nil {
			_, _ = cleanup.Exec(ctx, `DROP DATABASE IF EXISTS `+scratch+` WITH (FORCE)`)
			cleanup.Close()
		}
	})
	return pool
}

func newContractEnv(t *testing.T) *contractEnv {
	t.Helper()
	// pgx hands timestamptz back in the process's local zone. The fixtures
	// must not depend on the machine that rendered them.
	time.Local = time.UTC
	gin.SetMode(gin.TestMode)

	pool := ctScratchPool(t)
	cipher, err := pii.New(devKeyProvider{}, []byte("contract-salt-16b"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	pay := newCtPaymentsServer()
	t.Cleanup(pay.srv.Close)
	payClient, err := payments.NewInternalKeyClient(pay.srv.URL, "mstore", "contract-internal-key", true)
	if err != nil {
		t.Fatalf("payments client: %v", err)
	}
	mediaSrv := newCtMediaServer()
	t.Cleanup(mediaSrv.Close)
	svc := service.New(postgres.New(pool), nil, "").
		WithCourier(ctCourier{}).
		WithPII(cipher).
		WithBlob(ctBlob{}).
		WithPayments(payClient).
		WithMedia(media.New(mediaSrv.URL, "contract-internal-key")).
		WithAllowStubGateway(true).
		WithProductAutoApprove(true).
		// The delivery date is "today" + dispatch + transit: pinned, or
		// every delivery_estimate / quote fixture would move each day.
		WithClock(func() time.Time { return ctFixedNow })

	r := gin.New()
	r.Use(FenceMiddlewareWithStubSettlement(true))
	h := New(svc).WithInternalKey(integrationInternalKey).WithStubSettlement(true)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)

	e := &contractEnv{
		t: t, pool: pool, r: r, svc: svc, payments: pay, runStart: time.Now().UTC(),
		freshIDs: map[string]string{}, knownIDs: map[string]bool{},
	}
	e.seed()
	return e
}

// ─── Requests ────────────────────────────────────────────────────────────

type ctReq struct {
	method  string
	path    string
	user    uuid.UUID
	body    any
	headers map[string]string
}

func (e *contractEnv) do(rq ctReq) *httptest.ResponseRecorder {
	e.t.Helper()
	var rdr *bytes.Reader
	if rq.body != nil {
		b, err := json.Marshal(rq.body)
		if err != nil {
			e.t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(rq.method, rq.path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if rq.user != uuid.Nil {
		req.Header.Set("X-User-Id", rq.user.String())
	}
	for k, v := range rq.headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func (e *contractEnv) get(path string, user uuid.UUID) *httptest.ResponseRecorder {
	return e.do(ctReq{method: http.MethodGet, path: path, user: user})
}

func (e *contractEnv) post(path string, user uuid.UUID, body any) *httptest.ResponseRecorder {
	return e.do(ctReq{method: http.MethodPost, path: path, user: user, body: body})
}

func (e *contractEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("sql: %v\n%s", err, sql)
	}
}

// ─── Stabilising the bytes ───────────────────────────────────────────────

var (
	ctUUIDRe      = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	ctOrderNumRe  = regexp.MustCompile(`ORD-\d{4}-(\d{6})`)
	ctInvoiceNoRe = regexp.MustCompile(`INV-[0-9]{2}-[0-9]{2}-(\d+)`)
	// A product slug the service made unique with a clock-derived five-digit
	// suffix (service.uniqueSlug). Seeded slugs carry no such suffix.
	ctSlugRe = regexp.MustCompile(`"slug":"([a-z0-9-]+)-\d{5}"`)
)

var (
	// A JSON string holding an RFC3339 timestamp.
	ctTimestampRe = regexp.MustCompile(`"(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2}))"`)
	// A bare JSON number in unix-seconds range (created_at_epoch and kin).
	// Paise never reach 1e9 in these fixtures.
	ctEpochRe = regexp.MustCompile(`([:,\[])(1[5-9]\d{8}|[2-3]\d{9})([,}\]])`)
)

// stabilise pins the values a handler cannot help producing fresh: ids the
// store generated during the run, timestamps the database stamped during
// the run, and the year in a generated order number. Seeded ids and seeded
// timestamps pass through untouched, so a GET on seeded rows is the
// handler's exact bytes.
//
// It rewrites the TEXT, never the tree: decoding into a map and re-encoding
// would sort every object's keys, and the handler's key order is part of
// what the fixture pins.
func (e *contractEnv) stabilise(body []byte) []byte {
	s := string(body)
	s = ctTimestampRe.ReplaceAllStringFunc(s, func(quoted string) string {
		raw := quoted[1 : len(quoted)-1]
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || !e.producedDuringRun(t) {
			return quoted
		}
		return `"` + e.rebase(t).Format(time.RFC3339Nano) + `"`
	})
	s = ctEpochRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := ctEpochRe.FindStringSubmatch(m)
		var secs int64
		fmt.Sscanf(sub[2], "%d", &secs)
		t := time.Unix(secs, 0).UTC()
		if !e.producedDuringRun(t) {
			return m
		}
		return fmt.Sprintf("%s%d%s", sub[1], e.rebase(t).Unix(), sub[3])
	})
	s = ctUUIDRe.ReplaceAllStringFunc(s, func(id string) string {
		if e.knownIDs[id] {
			return id
		}
		if pinned, ok := e.freshIDs[id]; ok {
			return pinned
		}
		pinned := fmt.Sprintf("00000000-0000-4000-8000-0000000f%04x", len(e.freshIDs)+1)
		e.freshIDs[id] = pinned
		return pinned
	})
	s = ctOrderNumRe.ReplaceAllString(s, "ORD-2026-$1")
	s = ctInvoiceNoRe.ReplaceAllString(s, "INV-26-27-$1")
	s = ctSlugRe.ReplaceAllString(s, `"slug":"$1-00000"`)
	return []byte(s)
}

// producedDuringRun: anything between two minutes before the run started and
// a day after it was stamped by this run (quote expiry, reservation TTL,
// courier ETA all sit inside that window). Seeded values are in September
// 2026 or earlier by construction.
func (e *contractEnv) producedDuringRun(t time.Time) bool {
	return t.After(e.runStart.Add(-2*time.Minute)) && t.Before(e.runStart.Add(24*time.Hour+time.Hour))
}

// rebase keeps the offset from the run start, rounded to five minutes so the
// seconds a run takes never move a value, and puts it on the fixed instant.
func (e *contractEnv) rebase(t time.Time) time.Time {
	d := t.Sub(e.runStart).Round(5 * time.Minute)
	return ctFixedNow.Add(d)
}

// ─── The fixture list and the comparison ─────────────────────────────────

// ctFixture is one golden file: its name states the status the handler must
// answer with (ctStatusFromName), and the test refuses to write a fixture
// whose file name lies about it.
type ctFixture struct {
	name string // area/route_status[_case]
	run  func(e *contractEnv) *httptest.ResponseRecorder
}

func TestContractFixtures(t *testing.T) {
	e := newContractEnv(t)
	fixtures := e.fixtures()

	// The two halves of the inventory. contracts_inventory_test.go checks the
	// files against the names in THIS source; this checks the names are
	// unique and that every registered fixture rendered.
	seen := map[string]bool{}
	for _, f := range fixtures {
		if seen[f.name] {
			t.Fatalf("fixture %q is registered twice", f.name)
		}
		seen[f.name] = true
	}

	for _, f := range fixtures {
		f := f
		// Not t.Run: the fixtures are ORDERED (a cart line must exist before
		// the quote, the quote before the checkout) and a subtest filter
		// would break the chain. Each failure names its fixture.
		w := f.run(e)
		want := ctStatusFromName(f.name)
		if w.Code != want {
			t.Errorf("%s: handler answered %d, the fixture name says %d; body: %s", f.name, w.Code, want, w.Body.String())
			continue
		}
		got := e.render(w.Body.Bytes())
		path := filepath.Join(contractsDir, f.name+".json")
		if *updateContracts {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		wantBytes, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v (run with -update to write it)", f.name, err)
			continue
		}
		if !bytes.Equal(wantBytes, got) {
			t.Errorf("%s: fixture differs from the handler's output.\n--- fixture\n%s\n--- handler\n%s", f.name, wantBytes, got)
		}
	}
}

// render pretty-prints the stabilised body: two-space indent, trailing
// newline, keys in the handler's order. An empty body (204) is an empty file.
func (e *contractEnv) render(body []byte) []byte {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	stable := e.stabilise(body)
	var out bytes.Buffer
	if err := json.Indent(&out, stable, "", "  "); err != nil {
		e.t.Fatalf("response is not JSON: %v\n%s", err, body)
	}
	out.WriteByte('\n')
	return out.Bytes()
}

// sortedFixtureNames is what contracts_inventory_test.go compares the
// directory against, via the source scan.
func sortedFixtureNames(fixtures []ctFixture) []string {
	names := make([]string, 0, len(fixtures))
	for _, f := range fixtures {
		names = append(names, f.name)
	}
	sort.Strings(names)
	return names
}
