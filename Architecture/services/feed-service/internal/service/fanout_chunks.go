package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Chunked fan-out (audit H3, 2026-10-30).
//
// FanoutPost used to read EVERY follower page into one map (a million
// entries for a million followers) and then write the rows; any single
// failure returned an error and the shared consumer re-ran the whole thing,
// graph pagination included, three more times. Now:
//
//   - each graph page (or channel-subscriber page) and the timeline writes
//     for it are one chunk. The chunk's writes go through a bounded worker
//     pool and the chunk finishes before the next page is read, so memory
//     is one page plus a bounded dedupe set (fanoutDedupeCap), never the
//     audience;
//   - after each chunk the leg's position is saved in Redis under the
//     event's id (WithFanoutKey). A failed chunk returns an error and the
//     consumer retries the event; the retry resumes at the failed chunk
//     instead of starting over. A later event for the same post (a
//     visibility change, a restore) has a different id and starts fresh;
//   - progress that cannot be read or saved costs only repeated work:
//     every timeline write upserts (scylla.TimelineTS), so writing a chunk
//     twice cannot duplicate a row.

const (
	// fanoutPageSize is the graph page size of every fan-out leg.
	fanoutPageSize = 100
	// fanoutWorkers bounds the concurrent timeline writes of one chunk.
	fanoutWorkers = 16
	// fanoutDedupeCap bounds the set that keeps one recipient from being
	// written twice by two legs (a follower who is also a connection or a
	// subscriber). Past it, a recipient may get the same row written twice,
	// which is an upsert of the same key and harmless.
	fanoutDedupeCap = 20000
	// fanoutProgressTTL bounds a progress record. It only has to outlive
	// the consumer's retries (seconds to minutes) and a DLQ replay.
	fanoutProgressTTL = 24 * time.Hour
)

type fanoutKeyCtx struct{}

// WithFanoutKey tags ctx with the id of the event being handled, the key
// FanoutPost stores its chunk progress under. Without one (direct calls,
// tests) no progress is kept and a retry starts from the first chunk.
func WithFanoutKey(ctx context.Context, eventID string) context.Context {
	if eventID == "" {
		return ctx
	}
	return context.WithValue(ctx, fanoutKeyCtx{}, eventID)
}

// FanoutKeyFrom returns the event id WithFanoutKey stored, or "".
func FanoutKeyFrom(ctx context.Context) string { return fanoutKeyFrom(ctx) }

func fanoutKeyFrom(ctx context.Context) string {
	v, _ := ctx.Value(fanoutKeyCtx{}).(string)
	return v
}

// FanoutProgressKey is the Redis hash holding one event's fan-out progress
// for one post: field <leg> is the next cursor, field <leg>:done is set
// once the leg has finished.
func FanoutProgressKey(postID uuid.UUID, eventID string) string {
	return fmt.Sprintf("feed:fanout:progress:%s:%s", postID, eventID)
}

// fanoutProgress is one event's saved position per leg. A zero value (no
// Redis, no event id) remembers nothing.
type fanoutProgress struct {
	s      *Service
	key    string
	fields map[string]string
}

func (s *Service) loadFanoutProgress(ctx context.Context, postID uuid.UUID) *fanoutProgress {
	p := &fanoutProgress{s: s, fields: map[string]string{}}
	eventID := fanoutKeyFrom(ctx)
	if s.rdb == nil || eventID == "" {
		return p
	}
	p.key = FanoutProgressKey(postID, eventID)
	fields, err := s.rdb.HGetAll(ctx, p.key).Result()
	if err != nil {
		// Start over: every write upserts, so this only repeats work.
		log.Printf("fanout progress %s unreadable, starting from the first chunk: %v", p.key, err)
		return p
	}
	p.fields = fields
	return p
}

func (p *fanoutProgress) cursor(leg string) string { return p.fields[leg] }
func (p *fanoutProgress) done(leg string) bool     { return p.fields[leg+":done"] == "1" }

func (p *fanoutProgress) save(ctx context.Context, leg, cursor string) {
	p.fields[leg] = cursor
	p.write(ctx, leg, cursor)
}

func (p *fanoutProgress) finish(ctx context.Context, leg string) {
	p.fields[leg+":done"] = "1"
	p.write(ctx, leg+":done", "1")
}

func (p *fanoutProgress) write(ctx context.Context, field, value string) {
	if p.key == "" {
		return
	}
	pipe := p.s.rdb.TxPipeline()
	pipe.HSet(ctx, p.key, field, value)
	pipe.Expire(ctx, p.key, fanoutProgressTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("fanout progress %s: could not record %s: %v", p.key, field, err)
	}
}

// clear drops the record once the whole fan-out succeeded; the consumer's
// dedupe receipt covers a redelivery from then on.
func (p *fanoutProgress) clear(ctx context.Context) {
	if p.key == "" {
		return
	}
	if err := p.s.rdb.Del(ctx, p.key).Err(); err != nil {
		log.Printf("fanout progress %s: could not clear: %v", p.key, err)
	}
}

// recipientDedupe remembers up to fanoutDedupeCap recipients already
// written, so one person reached by two legs gets one write.
type recipientDedupe struct {
	mu   sync.Mutex
	seen map[uuid.UUID]struct{}
}

func newRecipientDedupe() *recipientDedupe {
	return &recipientDedupe{seen: make(map[uuid.UUID]struct{})}
}

// first reports whether id has not been seen yet, and remembers it while
// the set has room.
func (d *recipientDedupe) first(id uuid.UUID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[id]; ok {
		return false
	}
	if len(d.seen) < fanoutDedupeCap {
		d.seen[id] = struct{}{}
	}
	return true
}

// pushChunk writes one chunk's rows through a bounded worker pool and
// waits for all of them. Any failed write fails the chunk, so its progress
// is not saved and the retry rewrites it.
func (s *Service) pushChunk(ctx context.Context, ids []uuid.UUID, postID, authorID uuid.UUID, createdAt time.Time, contentType string) error {
	if len(ids) == 0 {
		return nil
	}
	ch := make(chan uuid.UUID)
	var wg sync.WaitGroup
	var failed atomic.Int64
	workers := fanoutWorkers
	if len(ids) < workers {
		workers = len(ids)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ch {
				if err := s.timelines.AddToHomeTimeline(ctx, id, postID, authorID, createdAt, contentType); err != nil {
					log.Printf("Failed to push to timeline for user %s: %v", id, err)
					failed.Add(1)
					fanoutWrites.WithLabelValues("failed").Inc()
					continue
				}
				fanoutWrites.WithLabelValues("ok").Inc()
			}
		}()
	}
	for _, id := range ids {
		ch <- id
	}
	close(ch)
	wg.Wait()
	if n := failed.Load(); n > 0 {
		return fmt.Errorf("%d timeline write(s) failed", n)
	}
	return nil
}

// errCelebThresholdReached stops the follower leg once the author turned
// out to be at the celeb threshold.
var errCelebThresholdReached = errors.New("celeb threshold reached")

// graphLeg runs one offset-paged graph leg (followers or connections) chunk
// by chunk from its saved position. pageFilter may narrow a page (friends
// audience) or stop the leg (celeb threshold) before the page is written.
func (s *Service) graphLeg(ctx context.Context, prog *fanoutProgress, leg, kind string, authorID uuid.UUID,
	pageFilter func(seen int, ids []uuid.UUID) ([]uuid.UUID, error), write func(ids []uuid.UUID) error) error {
	if prog.done(leg) {
		return nil
	}
	offset, _ := strconv.Atoi(prog.cursor(leg))
	for {
		ids, err := s.graphIDPage(ctx, kind, authorID, offset, fanoutPageSize)
		if err != nil {
			return fmt.Errorf("fetch %s at offset %d: %w", kind, offset, err)
		}
		chunk := ids
		if pageFilter != nil {
			if chunk, err = pageFilter(offset+len(ids), ids); err != nil {
				return err
			}
		}
		if err := write(chunk); err != nil {
			return fmt.Errorf("%s chunk at offset %d: %w", kind, offset, err)
		}
		if len(ids) < fanoutPageSize {
			prog.finish(ctx, leg)
			return nil
		}
		offset += fanoutPageSize
		prog.save(ctx, leg, strconv.Itoa(offset))
	}
}

// graphIDPage reads one page of a graph id list: kind is "followers",
// "following" or "connections". A non-200, a transport error and a
// malformed body are all errors, never an empty page.
func (s *Service) graphIDPage(ctx context.Context, kind string, userID uuid.UUID, offset, limit int) ([]uuid.UUID, error) {
	url := fmt.Sprintf("%s/v1/graph/%s/%s?limit=%d&offset=%d", s.graphURL, kind, userID.String(), limit, offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	// Forward the internal-service key so graph-service's CG2 gate
	// accepts the request when configured.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := s.graphFor(ctx).Do(req)
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
		return nil, fmt.Errorf("unmarshal %s: %w", kind, err)
	}
	return envelope.Data, nil
}

// graphIDList reads a whole graph id list, page by page. For lists the
// caller needs whole (the viewer's following list on the request path, the
// author's following set for a friends audience); a fan-out audience is
// streamed by graphLeg instead.
func (s *Service) graphIDList(ctx context.Context, kind string, userID uuid.UUID) ([]uuid.UUID, error) {
	var all []uuid.UUID
	offset := 0
	for {
		ids, err := s.graphIDPage(ctx, kind, userID, offset, fanoutPageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, ids...)
		if len(ids) < fanoutPageSize {
			return all, nil
		}
		offset += fanoutPageSize
	}
}

// followerCount asks graph-service for the author's follower count, so a
// celeb is recognised before a single row is pushed. Best effort: ok is
// false on any failure, and the follower leg's running count decides.
func (s *Service) followerCount(ctx context.Context, authorID uuid.UUID) (int, bool) {
	url := fmt.Sprintf("%s/v1/graph/counts/%s", s.graphURL, authorID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false
	}
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := s.graphFor(ctx).Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	var envelope struct {
		Data struct {
			FollowerCount *int64 `json:"follower_count"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&envelope); err != nil || envelope.Data.FollowerCount == nil {
		return 0, false
	}
	return int(*envelope.Data.FollowerCount), true
}
