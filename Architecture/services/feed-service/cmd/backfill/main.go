package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// post represents a row from the Postgres posts table.
type post struct {
	ID          uuid.UUID
	AuthorID    uuid.UUID
	CreatedAt   time.Time
	ContentType string
	Visibility  string
}

// listedToFollowers mirrors the feed's listing rule (service.listableVisibility):
// only these reach anyone's home timeline but the author's.
func listedToFollowers(visibility string) bool {
	switch strings.ToLower(strings.TrimSpace(visibility)) {
	case "", "public", "followers":
		return true
	}
	return false
}

// fetchFollowers calls graph-service to get all followers for a user,
// paginating through all pages (100 per page).
func fetchFollowers(ctx context.Context, graphURL string, userID uuid.UUID) ([]uuid.UUID, error) {
	var allFollowers []uuid.UUID
	offset := 0
	limit := 100

	for {
		url := fmt.Sprintf("%s/v1/graph/followers/%s?limit=%d&offset=%d", graphURL, userID.String(), limit, offset)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("graph-service request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("graph-service returned %d: %s", resp.StatusCode, string(body))
		}

		var envelope struct {
			Data []uuid.UUID `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, fmt.Errorf("unmarshal followers: %w", err)
		}

		allFollowers = append(allFollowers, envelope.Data...)

		// If we got fewer than limit, we've fetched all pages.
		if len(envelope.Data) < limit {
			break
		}
		offset += limit
	}

	return allFollowers, nil
}

func main() {
	log.Println("=== Timeline Backfill Script ===")
	log.Println("Populating ScyllaDB home timelines for all users from existing Postgres posts.")

	// ── Config from environment variables ──────────────────────────────
	pgDSN := os.Getenv("POSTGRES_DSN")
	if pgDSN == "" {
		pgDSN = "postgres://postgres:postgres@localhost:5432/app?sslmode=disable"
	}

	scyllaHosts := os.Getenv("SCYLLA_HOSTS")
	if scyllaHosts == "" {
		scyllaHosts = "localhost"
	}

	graphURL := os.Getenv("GRAPH_SERVICE_URL")
	if graphURL == "" {
		graphURL = "http://localhost:8083"
	}

	workerCount := 10

	// ── Connect to Postgres ────────────────────────────────────────────
	ctx := context.Background()

	dbPool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		log.Fatalf("Unable to connect to Postgres: %v", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		log.Fatalf("Postgres ping failed: %v", err)
	}
	log.Println("Connected to Postgres")

	// ── Connect to ScyllaDB ───────────────────────────────────────────
	cluster := gocql.NewCluster(scyllaHosts)
	cluster.Keyspace = "social_feed"
	cluster.Consistency = gocql.Quorum
	scyllaSession, err := cluster.CreateSession()
	if err != nil {
		log.Fatalf("Unable to connect to ScyllaDB: %v", err)
	}
	defer scyllaSession.Close()
	log.Println("Connected to ScyllaDB")
	timelines := scylla.New(scyllaSession)

	// ── Query all non-deleted posts from Postgres ─────────────────────
	log.Println("Querying all non-deleted posts from Postgres...")

	// Only posts a timeline still keeps: the store writes nothing older
	// than scylla.TimelineRetention anyway.
	since := time.Now().Add(-scylla.TimelineRetention)
	rows, err := dbPool.Query(ctx,
		`SELECT id, author_id, created_at, COALESCE(content_type, ''), COALESCE(visibility, '')
		   FROM posts WHERE deleted_at IS NULL AND created_at > $1 ORDER BY created_at DESC`,
		since,
	)
	if err != nil {
		log.Fatalf("Failed to query posts: %v", err)
	}

	var posts []post
	for rows.Next() {
		var p post
		if err := rows.Scan(&p.ID, &p.AuthorID, &p.CreatedAt, &p.ContentType, &p.Visibility); err != nil {
			log.Fatalf("Failed to scan post row: %v", err)
		}
		posts = append(posts, p)
	}
	rows.Close()

	if rows.Err() != nil {
		log.Fatalf("Error iterating post rows: %v", rows.Err())
	}

	totalPosts := len(posts)
	log.Printf("Found %d posts to backfill", totalPosts)

	if totalPosts == 0 {
		log.Println("No posts to backfill. Exiting.")
		return
	}

	// ── Process posts concurrently ────────────────────────────────────
	var (
		processedCount int64
		errorCount     int64
		timelineWrites int64
	)

	startTime := time.Now()

	// Create a buffered channel to distribute posts to workers.
	postCh := make(chan post, workerCount*2)

	var wg sync.WaitGroup

	// Start worker goroutines.
	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for p := range postCh {
				// The live writers: same key (scylla.TimelineTS), so a re-run
				// or a post the consumer already fanned out upserts instead of
				// duplicating; same TTL and post index.

				// 1. The author's own timeline.
				if err := timelines.AddToAuthorTimeline(ctx, p.AuthorID, p.ID, p.CreatedAt, p.ContentType); err != nil {
					log.Printf("[worker %d] Failed to insert author timeline for post %s: %v", workerID, p.ID, err)
					atomic.AddInt64(&errorCount, 1)
				} else {
					atomic.AddInt64(&timelineWrites, 1)
				}

				if !listedToFollowers(p.Visibility) {
					atomic.AddInt64(&processedCount, 1)
					continue
				}

				// 2. Fetch followers from graph-service.
				followers, err := fetchFollowers(ctx, graphURL, p.AuthorID)
				if err != nil {
					log.Printf("[worker %d] Failed to fetch followers for author %s (post %s): %v", workerID, p.AuthorID, p.ID, err)
					atomic.AddInt64(&errorCount, 1)
					atomic.AddInt64(&processedCount, 1)
					continue
				}

				// 3. Insert into home_timeline_by_user for each follower.
				for _, followerID := range followers {
					if err := timelines.AddToHomeTimeline(ctx, followerID, p.ID, p.AuthorID, p.CreatedAt, p.ContentType); err != nil {
						log.Printf("[worker %d] Failed to insert home timeline for follower %s, post %s: %v", workerID, followerID, p.ID, err)
						atomic.AddInt64(&errorCount, 1)
					} else {
						atomic.AddInt64(&timelineWrites, 1)
					}
				}

				current := atomic.AddInt64(&processedCount, 1)

				// Log progress every 100 posts.
				if current%100 == 0 {
					elapsed := time.Since(startTime)
					rate := float64(current) / elapsed.Seconds()
					log.Printf("Progress: %d/%d posts processed (%.1f posts/sec), %d timeline writes, %d errors",
						current, totalPosts, rate, atomic.LoadInt64(&timelineWrites), atomic.LoadInt64(&errorCount))
				}
			}
		}(w)
	}

	// Feed posts to workers.
	for _, p := range posts {
		postCh <- p
	}
	close(postCh)

	// Wait for all workers to finish.
	wg.Wait()

	elapsed := time.Since(startTime)
	log.Println("=== Backfill Complete ===")
	log.Printf("Total posts processed: %d", processedCount)
	log.Printf("Total timeline writes: %d", timelineWrites)
	log.Printf("Total errors: %d", errorCount)
	log.Printf("Elapsed time: %s", elapsed)
	if processedCount > 0 {
		log.Printf("Average rate: %.1f posts/sec", float64(processedCount)/elapsed.Seconds())
	}
}
