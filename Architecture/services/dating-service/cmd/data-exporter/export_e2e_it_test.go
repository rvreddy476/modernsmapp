// Lane D9 end-to-end export: the owner requests an export over HTTP, the
// exporter consumes the dating.data.export.requested event exactly as it
// arrives from Kafka, the document is stored sealed, and only the owner can
// download it — with sealed fields opened and no trace of the person who
// reported them. Needs TEST_PG_DSN on a database whose name ends in _test.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/dating-service/internal/datingpii"
	datingevents "github.com/atpost/dating-service/internal/events"
	datinghttp "github.com/atpost/dating-service/internal/http"
	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

type kafkaRecorder struct {
	mu   sync.Mutex
	msgs []kafka.Message
}

func (k *kafkaRecorder) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.msgs = append(k.msgs, msgs...)
	return nil
}

func (k *kafkaRecorder) Close() error { return nil }

func (k *kafkaRecorder) ofType(t *testing.T, typ string) []kafka.Message {
	t.Helper()
	k.mu.Lock()
	defer k.mu.Unlock()
	var out []kafka.Message
	for _, m := range k.msgs {
		var env struct {
			EventType string `json:"event_type"`
		}
		if err := json.Unmarshal(m.Value, &env); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		if env.EventType == typ {
			out = append(out, m)
		}
	}
	return out
}

func do(r http.Handler, method, path, body string, user uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", user.String())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestD9_ExportCompletesEndToEndForOwnerOnly(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping lane D9 export end-to-end test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.BootstrapSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	crypto, err := datingpii.New(ctx, []datingpii.VersionedKey{{Version: 1, Key: bytes.Repeat([]byte{9}, 32)}},
		[]byte("dating-it-test-lookup-salt-0001"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	st.SetPII(crypto)
	svc := service.New(st, nil)
	rec := &kafkaRecorder{}
	svc.SetProducer(datingevents.NewProducerWithWriter(rec))
	storage, err := exportStorageFromEnv(func(string) string { return "" }, st)
	if err != nil {
		t.Fatalf("default storage: %v", err)
	}
	svc.SetExportStorageClient(storage)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	datinghttp.New(svc).RegisterRoutes(r)

	owner, target, reporter, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	religion, community, intent := "Sikh", "Jat", "casual"
	if _, err := st.UpsertProfile(ctx, owner, store.UpsertProfileParams{Intent: &intent, Religion: &religion, Community: &community}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetConsent(ctx, owner, store.ConsentTypeReligion, true, "v-test"); err != nil {
		t.Fatal(err)
	}
	lat, lng := 12.971599, 77.594566
	if _, err := st.RecordPanicIncident(ctx, store.RecordPanicParams{UserID: owner, Source: store.PanicSourcePanic, Latitude: &lat, Longitude: &lng}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateReport(ctx, owner, target, "spam", "my own words"); err != nil {
		t.Fatal(err)
	}
	const reporterWords = "reporter-private-detail-7f3a"
	if _, err := st.CreateReport(ctx, reporter, owner, "harassment", reporterWords); err != nil {
		t.Fatal(err)
	}

	// 1. The owner asks for an export.
	resp := do(r, http.MethodPost, "/v1/dating/data-export", ``, owner)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("request export: %d %s", resp.Code, resp.Body.String())
	}
	requested := rec.ofType(t, "dating.data.export.requested")
	if len(requested) != 1 {
		t.Fatalf("requested events = %d, want 1", len(requested))
	}

	// 2. The exporter consumes the event as Kafka delivers it.
	if err := processMessage(ctx, svc, requested[0]); err != nil {
		t.Fatalf("exporter: %v", err)
	}
	if len(rec.ofType(t, "dating.data.export.ready")) != 1 {
		t.Fatalf("no dating.data.export.ready event")
	}

	// 3. The export is ready, with the owner's download path, and sealed at rest.
	resp = do(r, http.MethodGet, "/v1/dating/data-export/me", ``, owner)
	var list struct {
		Data []store.DataExport `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil || len(list.Data) != 1 {
		t.Fatalf("exports list = %s err=%v", resp.Body.String(), err)
	}
	exp := list.Data[0]
	if exp.Status != "ready" || exp.DownloadURL == nil || *exp.DownloadURL != service.DataExportDownloadPath(exp.ID) ||
		exp.DownloadExpiresAt == nil || exp.DownloadExpiresAt.Before(time.Now().Add(6*24*time.Hour)) {
		t.Fatalf("export row = %+v, want ready with a 7-day download path", exp)
	}
	var plaintextAtRest bool
	if err := pool.QueryRow(ctx, `SELECT position(convert_to('Sikh', 'UTF8') in payload_sealed) > 0
        FROM dating_data_exports WHERE id = $1`, exp.ID).Scan(&plaintextAtRest); err != nil || plaintextAtRest {
		t.Fatalf("export stored in plaintext: %v err=%v", plaintextAtRest, err)
	}

	// 4. Nobody else can download it; the answer does not reveal it exists.
	for _, who := range []uuid.UUID{stranger, reporter} {
		if resp = do(r, http.MethodGet, *exp.DownloadURL, ``, who); resp.Code != http.StatusNotFound {
			t.Fatalf("download by another user: %d %s", resp.Code, resp.Body.String())
		}
	}

	// 5. The owner gets the document with sealed fields opened.
	resp = do(r, http.MethodGet, *exp.DownloadURL, ``, owner)
	if resp.Code != http.StatusOK || resp.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("owner download: %d %v", resp.Code, resp.Header())
	}
	raw := resp.Body.String()
	var doc service.UserDataExport
	if err := json.Unmarshal(resp.Body.Bytes(), &doc); err != nil {
		t.Fatalf("export is not JSON: %v", err)
	}
	if doc.UserID != owner || doc.Profile == nil || doc.Profile.Religion == nil || *doc.Profile.Religion != religion ||
		doc.Profile.Community == nil || *doc.Profile.Community != community {
		t.Fatalf("profile in export = %+v, want religion and community opened", doc.Profile)
	}
	if len(doc.PanicIncidents) != 1 || doc.PanicIncidents[0].Latitude == nil || *doc.PanicIncidents[0].Latitude != lat {
		t.Fatalf("panic incidents in export = %+v, want the exact point opened", doc.PanicIncidents)
	}
	if len(doc.ConsentLog) == 0 {
		t.Fatalf("export has no consent history")
	}
	if len(doc.ReportsFiled) != 1 || doc.ReportsFiled[0].TargetUserID != target {
		t.Fatalf("reports filed = %+v", doc.ReportsFiled)
	}
	if len(doc.ReportsAgainst) != 1 || doc.ReportsAgainst[0].Reason != "harassment" {
		t.Fatalf("reports against = %+v", doc.ReportsAgainst)
	}
	if strings.Contains(raw, reporter.String()) || strings.Contains(raw, reporterWords) {
		t.Fatalf("export reveals the reporter of a report against the owner")
	}
}
