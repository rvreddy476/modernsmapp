package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/httpclient"
	"github.com/atpost/shared/o11y/logging"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "dating-echo-refresher"})

	pgDSN := os.Getenv("POSTGRES_DSN")
	if pgDSN == "" {
		slog.Error("POSTGRES_DSN is required")
		os.Exit(1)
	}
	batchSize := envInt("ECHO_REFRESH_BATCH_SIZE", 100)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	poolCfg, err := pgxpool.ParseConfig(pgDSN)
	if err != nil {
		slog.Error("parse db config", "error", err)
		os.Exit(1)
	}
	poolCfg.MaxConns = 8
	dbPool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		slog.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	if err := database.BootstrapSchema(ctx, dbPool); err != nil {
		slog.Error("bootstrap schema", "error", err)
		os.Exit(1)
	}

	st := store.New(dbPool)
	r := newRefresher(st)

	targets, err := st.ListEchoRefreshTargets(ctx, batchSize)
	if err != nil {
		slog.Error("list refresh targets", "error", err)
		os.Exit(1)
	}
	slog.Info("echo-refresher run started", "batch_size", batchSize, "targets", len(targets))

	successes := 0
	for _, t := range targets {
		if err := r.refreshOne(ctx, t.UserID); err != nil {
			slog.Warn("refresh failed", "user_id", t.UserID, "error", err)
			continue
		}
		successes++
	}
	slog.Info("echo-refresher run complete", "succeeded", successes, "attempted", len(targets))
}

type refresher struct {
	store         *store.Store
	client        *http.Client
	qaBase        string
	feedBase      string
	communityBase string
	postBase      string
	qaAnswerLimit int
	feedReelLimit int
	postLimit     int
}

func newRefresher(st *store.Store) *refresher {
	return &refresher{
		store:         st,
		client:        httpclient.New(3 * time.Second),
		qaBase:        envOr("QA_SERVICE_URL", "http://qa-service:8110"),
		feedBase:      envOr("FEED_SERVICE_URL", "http://feed-service:8104"),
		communityBase: envOr("COMMUNITY_SERVICE_URL", "http://community-service:8109"),
		postBase:      envOr("POST_SERVICE_URL", "http://post-service:8103"),
		qaAnswerLimit: envInt("ECHO_QA_LIMIT", 2),
		feedReelLimit: envInt("ECHO_REEL_LIMIT", 3),
		postLimit:     envInt("ECHO_POST_LIMIT", 5),
	}
}

func (r *refresher) refreshOne(ctx context.Context, userID uuid.UUID) error {
	answers := r.fetchEnvelope(ctx, fmt.Sprintf("%s/v1/qa/profile/%s/answers?limit=%d", r.qaBase, userID, r.qaAnswerLimit), "")
	reels := r.fetchEnvelope(ctx, fmt.Sprintf("%s/v1/feed/users/%s/reels?limit=%d", r.feedBase, userID, r.feedReelLimit), "")
	communities := r.fetchEnvelope(ctx, fmt.Sprintf("%s/v1/communities/me", r.communityBase), userID.String())
	posts := r.fetchEnvelope(ctx, fmt.Sprintf("%s/v1/posts/users/%s?limit=%d", r.postBase, userID, r.postLimit), "")

	return r.store.UpsertEchoCache(ctx, userID, reels, answers, communities, posts)
}

func (r *refresher) fetchEnvelope(ctx context.Context, url, asUser string) []byte {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	if asUser != "" {
		req.Header.Set("X-User-ID", asUser)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		slog.Warn("echo fetch failed", "url", url, "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 400 {
		slog.Warn("echo fetch non-2xx", "url", url, "status", resp.StatusCode)
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		var arr []any
		if err2 := json.Unmarshal(body, &arr); err2 == nil {
			return body
		}
		return nil
	}
	if len(envelope.Data) == 0 {
		return nil
	}
	return envelope.Data
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
