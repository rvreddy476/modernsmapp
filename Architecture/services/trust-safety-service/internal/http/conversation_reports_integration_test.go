//go:build integration

package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Conversation reports end to end: the internal route, the real service and
// the schema through migration 016 on a *_test database:
//
//	M7_TRUST_POSTGRES_DSN=…/trust_conv_reports_it_test go test -tags integration ./internal/http/ -run ConversationReports -v
func TestConversationReportsEndToEnd(t *testing.T) {
	pool := openTrustTestDB(t)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)
	h := New(service.New(postgres.New(pool), nil))
	r := gin.New()
	h.RegisterRoutes(r)

	alice, bob, carol := uuid.New(), uuid.New(), uuid.New()
	group, dm := uuid.New(), uuid.New()
	reasonReporters := make([]uuid.UUID, 7)
	for i := range reasonReporters {
		reasonReporters[i] = uuid.New()
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM trust.reports WHERE reporter_id = ANY($1)`,
			append([]uuid.UUID{alice, bob, carol}, reasonReporters...))
	})
	internal := map[string]string{"X-Internal-Service-Key": "k"}
	post := func(path string, body map[string]any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		w := serve(r, http.MethodPost, path, string(raw), internal)
		var env struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return w.Code, env.Data
	}
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}

	// 1. A group report: 201, entity "conversation", evidence stored.
	groupBody := map[string]any{
		"reporter_id": alice.String(), "entity_type": "conversation", "entity_id": group.String(),
		"conversation_id": group.String(), "conversation_type": "group", "reason": "hate",
		"note": "slurs", "message_ids": ids, "idempotency_key": "chat-conv-report:it:" + group.String(),
	}
	code, data := post(ConversationReportPath, groupBody)
	if code != http.StatusCreated || data["created"] != true {
		t.Fatalf("group report: %d %v", code, data)
	}
	// 2. The same call again (retry): 200, nothing new.
	if code, data = post(ConversationReportPath, groupBody); code != http.StatusOK || data["created"] != false {
		t.Fatalf("retry: %d %v", code, data)
	}
	// 3. A fresh key while the first is still open: the active-duplicate rule.
	again := map[string]any{}
	for k, v := range groupBody {
		again[k] = v
	}
	again["idempotency_key"] = "chat-conv-report:it:newer:" + group.String()
	if code, data = post(ConversationReportPath, again); code != http.StatusOK || data["created"] != false {
		t.Fatalf("active duplicate: %d %v", code, data)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM trust.reports WHERE reporter_id = $1`, alice).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d rows for alice (%v), want 1", n, err)
	}

	// 4. The admin queue (GET /v1/reports, admin scope) shows it with its evidence.
	w := serve(r, http.MethodGet, "/v1/reports?limit=100", "", map[string]string{
		"X-Internal-Service-Key": "k", "X-User-Id": uuid.NewString(), "X-Scopes": "admin",
	})
	var queue struct {
		Data struct {
			Items []postgres.Report `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &queue); err != nil || w.Code != http.StatusOK {
		t.Fatalf("queue %d %s (%v)", w.Code, w.Body.String(), err)
	}
	var found *postgres.Report
	for i := range queue.Data.Items {
		if queue.Data.Items[i].ReporterID == alice {
			found = &queue.Data.Items[i]
		}
	}
	if found == nil {
		t.Fatal("conversation report missing from the admin queue")
	}
	if found.EntityType != "conversation" || found.EntityID != group || found.EntityKey != "" ||
		found.Reason != "hate_abuse" || found.Details != "slurs" || found.Status != "open" {
		t.Fatalf("queued %+v", found)
	}
	var ev service.ReportEvidence
	if err := json.Unmarshal(found.Evidence, &ev); err != nil {
		t.Fatalf("evidence %s: %v", found.Evidence, err)
	}
	if ev.ConversationID != group.String() || ev.ConversationType != "group" || len(ev.MessageIDs) != 3 ||
		ev.MessageIDs[0] != ids[0] || ev.Source != service.EvidenceSourceMessageService || ev.CapturedAt.IsZero() {
		t.Fatalf("evidence %+v", ev)
	}

	// 5. A DM report files the other person as a user report.
	dmKey := "chat-conv-report:it:" + dm.String()
	code, data = post(ConversationReportPath, map[string]any{
		"reporter_id": bob.String(), "entity_type": "user", "entity_id": carol.String(),
		"conversation_id": dm.String(), "conversation_type": "direct", "reason": "scam",
		"message_ids": []string{uuid.NewString()}, "idempotency_key": dmKey,
	})
	if code != http.StatusCreated {
		t.Fatalf("dm report: %d %v", code, data)
	}
	var et, reason, convInEvidence string
	if err := pool.QueryRow(ctx, `SELECT entity_type, reason, evidence->>'conversation_id' FROM trust.reports
		WHERE reporter_id = $1 AND entity_id = $2`, bob, carol).Scan(&et, &reason, &convInEvidence); err != nil {
		t.Fatal(err)
	}
	if et != "user" || reason != "scam_fraud" || convInEvidence != dm.String() {
		t.Fatalf("dm row (%s, %s, %s)", et, reason, convInEvidence)
	}

	// 6. One key space with the message route: message-service's fallback
	// filing with the same key is a duplicate, not a second report.
	code, data = post(MessageReportPath, map[string]any{
		"reporter_id": bob.String(), "entity_id": dm.String() + ":" + uuid.NewString(),
		"reason": "scam", "idempotency_key": dmKey,
	})
	if code != http.StatusOK || data["created"] != false {
		t.Fatalf("message-route fallback with the same key: %d %v", code, data)
	}

	// 7. Every contract reason maps to its stored category.
	for i, tc := range []struct{ in, want string }{
		{"spam", "spam"}, {"harassment", "harassment"}, {"hate", "hate_abuse"}, {"sexual", "sexual_content"},
		{"violence", "violence_threat"}, {"scam", "scam_fraud"}, {"other", "other"},
	} {
		code, _ := post(ConversationReportPath, map[string]any{
			"reporter_id": reasonReporters[i].String(), "entity_type": "conversation", "entity_id": group.String(),
			"conversation_id": group.String(), "conversation_type": "group", "reason": tc.in,
		})
		if code != http.StatusCreated {
			t.Fatalf("reason %s: %d", tc.in, code)
		}
		var got string
		if err := pool.QueryRow(ctx, `SELECT reason FROM trust.reports WHERE reporter_id = $1`, reasonReporters[i]).Scan(&got); err != nil || got != tc.want {
			t.Fatalf("reason %s stored %q (%v), want %q", tc.in, got, err, tc.want)
		}
	}
	// An unknown reason is the caller's error, never a 500.
	if code, _ := post(ConversationReportPath, map[string]any{
		"reporter_id": carol.String(), "entity_type": "conversation", "entity_id": group.String(),
		"conversation_id": group.String(), "conversation_type": "group", "reason": "bored",
	}); code != http.StatusBadRequest {
		t.Fatalf("unknown reason: %d", code)
	}

	// 8. The schema (migration 016) holds the same rules on its own.
	insert := func(entityType string, entityID uuid.UUID, key *string, evidence string) error {
		_, err := pool.Exec(ctx, `INSERT INTO trust.reports (id, reporter_id, entity_type, entity_id, entity_key, reason, details, status, evidence)
			VALUES ($1, $2, $3, $4, $5, 'spam', '', 'open', NULLIF($6, '')::jsonb)`, uuid.New(), carol, entityType, entityID, key, evidence)
		return err
	}
	someKey := "x"
	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = uuid.NewString()
	}
	tooManyJSON, _ := json.Marshal(map[string]any{"conversation_id": group.String(), "message_ids": tooMany, "source": "message-service"})
	for name, err := range map[string]error{
		"conversation with an entity_key":     insert("conversation", uuid.New(), &someKey, ""),
		"conversation evidence names another": insert("conversation", uuid.New(), nil, `{"conversation_id":"`+group.String()+`","source":"message-service"}`),
		"21 message ids":                      insert("user", uuid.New(), nil, string(tooManyJSON)),
		"message_ids not an array":            insert("user", uuid.New(), nil, `{"message_ids":"x","source":"message-service"}`),
	} {
		// A clean CHECK violation (23514), not some other error (a CHECK
		// that raised on a scalar would also "refuse" the row, by accident).
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Errorf("schema did not refuse %s with a CHECK violation: %v", name, err)
		}
	}
	// And still accepts the good shapes (a conversation with no evidence,
	// evidence with no message_ids).
	if err := insert("conversation", uuid.New(), nil, ""); err != nil {
		t.Fatalf("plain conversation row refused: %v", err)
	}
	if err := insert("post", uuid.New(), nil, `{"text":"x","source":"reporter"}`); err != nil {
		t.Fatalf("evidence without message_ids refused: %v", err)
	}
}
