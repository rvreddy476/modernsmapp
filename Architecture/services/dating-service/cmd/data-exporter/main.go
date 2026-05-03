// data-exporter — Sprint 5 DPDP §15.8 worker.
//
// Consumes dating.data.export.requested from the dating-events Kafka topic.
// For each event:
//
//   1. Builds the JSON payload (service.BuildExportPayload). Aadhaar number
//      is NEVER included; counterparty data in matches/sparks/vouches is
//      reduced to user_ids.
//   2. Writes the blob to media-service storage (or a local sink in dev).
//   3. Stamps the download URL + 7-day expiry on dating_data_exports.
//   4. Emits dating.data.export.ready and notifies the user.
//
// Run modes:
//   - Daemon (default): block on Kafka consumer.
//   - One-shot: DATA_EXPORTER_ONCE=true exits after one event (or 60s idle).
//
// CRITICAL RULES #6: every persistence step is wrapped + logged. A failed
// export marks the row as 'failed' so it does not block subsequent attempts.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/atpost/dating-service/database"
	datingevents "github.com/atpost/dating-service/internal/events"
	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/transport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

func main() {
	logging.Init(logging.Config{ServiceName: "dating-data-exporter"})

	pgDSN := os.Getenv("POSTGRES_DSN")
	if pgDSN == "" {
		slog.Error("POSTGRES_DSN required")
		os.Exit(1)
	}
	kafkaBrokers := strings.Split(envOr("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := envOr("KAFKA_TOPIC", "dating-events")
	groupID := envOr("KAFKA_GROUP_ID", "dating-data-exporter")
	once := os.Getenv("DATA_EXPORTER_ONCE") == "true"

	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(pgDSN)
	if err != nil {
		slog.Error("parse db config", "error", err)
		os.Exit(1)
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		slog.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := database.BootstrapSchema(ctx, pool); err != nil {
		slog.Error("bootstrap schema", "error", err)
		os.Exit(1)
	}

	st := store.New(pool)
	svc := service.New(st, nil)

	dialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("kafka dialer", "error", err)
		os.Exit(1)
	}

	producer := datingevents.NewProducerWithDialer(kafkaBrokers, kafkaTopic, dialer)
	defer func() { _ = producer.Close() }()
	svc.SetDataExportPublisher(producer)
	svc.SetExportStorageClient(newHTTPMediaExportStorage())
	svc.SetNotificationClient(newHTTPNotificationClient())

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  kafkaBrokers,
		GroupID:  groupID,
		Topic:    kafkaTopic,
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	defer func() { _ = reader.Close() }()

	idle := time.Duration(0)
	const idleTimeout = 60 * time.Second

	slog.Info("data-exporter listening", "topic", kafkaTopic, "group", groupID, "once", once)
	for {
		readCtx := ctx
		if once {
			c, cancel := context.WithTimeout(ctx, idleTimeout-idle)
			defer cancel()
			readCtx = c
		}
		m, err := reader.ReadMessage(readCtx)
		if err != nil {
			if once {
				slog.Info("idle timeout reached; exiting once-mode")
				return
			}
			slog.Warn("kafka read error", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}
		idle = 0
		if err := processMessage(ctx, svc, m); err != nil {
			slog.Warn("export message failed", "error", err)
		}
		if once {
			return
		}
	}
}

func processMessage(ctx context.Context, svc *service.Service, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return fmt.Errorf("decode envelope: %w", err)
	}
	if envelope.EventType != events.EventDatingDataExportRequested {
		return nil
	}
	var payload struct {
		ExportID string `json:"export_id"`
		UserID   string `json:"user_id"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	exportID, err := uuid.Parse(payload.ExportID)
	if err != nil {
		return fmt.Errorf("parse export_id: %w", err)
	}
	if err := svc.FulfillExport(ctx, exportID); err != nil {
		return fmt.Errorf("fulfill: %w", err)
	}
	slog.Info("data export fulfilled", "export_id", exportID, "user_id", payload.UserID)
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// --- Production wiring -----------------------------------------------------

// httpMediaExportStorage uploads JSON to media-service and asks for a signed
// URL valid for 7 days. The exact endpoint shape depends on media-service's
// blob API; we POST to `/v1/internal/exports` with the bytes and get back
// `{download_url, expires_at}`.
type httpMediaExportStorage struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

func newHTTPMediaExportStorage() *httpMediaExportStorage {
	base := os.Getenv("MEDIA_SERVICE_URL")
	if base == "" {
		base = "http://media-service:8093"
	}
	return &httpMediaExportStorage{
		baseURL:     strings.TrimRight(base, "/"),
		internalKey: os.Getenv("INTERNAL_SERVICE_KEY"),
		client:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (h *httpMediaExportStorage) WriteExport(ctx context.Context, exportID uuid.UUID, payload []byte) (string, time.Time, error) {
	url := h.baseURL + "/v1/internal/exports/" + exportID.String()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", h.internalKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("media-service upload: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", time.Time{}, fmt.Errorf("media-service status %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		DownloadURL string    `json:"download_url"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		// Tolerant fallback: if the service returns an envelope, dig.
		var env struct {
			Data struct {
				DownloadURL string    `json:"download_url"`
				ExpiresAt   time.Time `json:"expires_at"`
			} `json:"data"`
		}
		if err2 := json.Unmarshal(body, &env); err2 == nil && env.Data.DownloadURL != "" {
			return env.Data.DownloadURL, env.Data.ExpiresAt, nil
		}
		return "", time.Time{}, fmt.Errorf("media-service decode: %w", err)
	}
	if out.DownloadURL == "" {
		return "", time.Time{}, fmt.Errorf("media-service: empty download_url")
	}
	if out.ExpiresAt.IsZero() {
		out.ExpiresAt = time.Now().Add(7 * 24 * time.Hour)
	}
	return out.DownloadURL, out.ExpiresAt, nil
}

// httpNotificationClient pings notification-service so the user gets the
// "Your data export is ready" push/email.
type httpNotificationClient struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

func newHTTPNotificationClient() *httpNotificationClient {
	base := os.Getenv("NOTIFICATION_SERVICE_URL")
	if base == "" {
		base = "http://notification-service:8095"
	}
	return &httpNotificationClient{
		baseURL:     strings.TrimRight(base, "/"),
		internalKey: os.Getenv("INTERNAL_SERVICE_KEY"),
		client:      &http.Client{Timeout: 5 * time.Second},
	}
}

func (h *httpNotificationClient) NotifyDataExportReady(ctx context.Context, userID uuid.UUID, url string, expiresAt time.Time) error {
	body := map[string]any{
		"user_id":            userID.String(),
		"kind":               "dating.data_export_ready",
		"download_url":       url,
		"expires_at":         expiresAt.UTC().Format(time.RFC3339),
		"title":              "Your Pulse data export is ready",
		"body":               "Download it within 7 days from your account settings.",
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/v1/notifications/internal/dispatch", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", h.internalKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("notification-service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("notification-service status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
