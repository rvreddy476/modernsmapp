package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"strconv"
	"strings"
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
	// graphListPageSize is the page size of a whole-list read (graphIDList:
	// a viewer's following list on the request path). graph-service's
	// internal lists allow up to 500, a fifth of the round trips of 100.
	graphListPageSize = 500
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
// for one post: field <leg> is the leg's position (a graph leg:
// encodeLegPosition; the subscriber leg: post-service's cursor), field
// <leg>:done is set once the leg has finished.
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
	return pushChunkWith(ids, func(id uuid.UUID) error {
		return s.timelines.AddToHomeTimeline(ctx, id, postID, authorID, createdAt, contentType)
	})
}

// pushChunkWith is pushChunk's worker pool around any per-recipient write;
// the Echo notes fan-out (notes.go) writes notes_timeline rows through it.
func pushChunkWith(ids []uuid.UUID, writeOne func(id uuid.UUID) error) error {
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
				if err := writeOne(id); err != nil {
					log.Printf("timeline write failed for %s: %v", id, err)
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

// legPositionPrefix marks a saved graph-leg position in the cursor form:
// "k1:<recipients read so far>:<graph cursor>". Before 16 Nov 2026 the
// position was a bare offset ("300"); graph-service no longer pages these
// lists by offset, so a position in any other form restarts its leg from
// the first page. That is safe: every timeline write upserts.
const legPositionPrefix = "k1:"

func encodeLegPosition(seen int, cursor string) string {
	return legPositionPrefix + strconv.Itoa(seen) + ":" + cursor
}

// decodeLegPosition reads a saved position. ok is false for anything that
// is not the cursor form (an old offset, a damaged value); the caller then
// starts the leg over.
func decodeLegPosition(raw string) (cursor string, seen int, ok bool) {
	if raw == "" {
		return "", 0, true
	}
	rest, found := strings.CutPrefix(raw, legPositionPrefix)
	if !found {
		return "", 0, false
	}
	count, cursor, found := strings.Cut(rest, ":")
	if !found || cursor == "" {
		return "", 0, false
	}
	seen, err := strconv.Atoi(count)
	if err != nil || seen < 0 {
		return "", 0, false
	}
	return cursor, seen, true
}

// graphLeg runs one cursor-paged graph leg (followers or connections) chunk
// by chunk from its saved position. pageFilter may narrow a page (friends
// audience) or stop the leg (celeb threshold) before the page is written;
// its first argument is how many recipients the leg has read so far, this
// page included.
//
// The list is paged by graph-service's keyset cursor, not by offset: there
// is no depth cap (the offset lists refuse anything past 10,000), and a
// follow or unfollow while the walk is under way neither repeats nor skips
// a follower who was there throughout.
func (s *Service) graphLeg(ctx context.Context, prog *fanoutProgress, leg, kind string, authorID uuid.UUID,
	pageFilter func(seen int, ids []uuid.UUID) ([]uuid.UUID, error), write func(ids []uuid.UUID) error) error {
	if prog.done(leg) {
		return nil
	}
	cursor, seen, ok := decodeLegPosition(prog.cursor(leg))
	if !ok {
		log.Printf("fanout %s leg for %s: saved position %q is not a cursor (saved before cursor paging?); restarting the leg",
			leg, authorID, prog.cursor(leg))
	}
	// resumed: this page is being read from a SAVED cursor. If graph-service
	// refuses that cursor the leg starts over once, rather than the event
	// being dead-lettered over a position record.
	resumed := cursor != ""
	for {
		ids, next, err := s.graphIDPage(ctx, kind, authorID, cursor, fanoutPageSize)
		if err != nil {
			if resumed && UpstreamRejected(err) {
				log.Printf("fanout %s leg for %s: saved cursor %q refused (%v); restarting the leg", leg, authorID, cursor, err)
				cursor, seen, resumed = "", 0, false
				continue
			}
			return fmt.Errorf("fetch %s after cursor %q: %w", kind, cursor, err)
		}
		resumed = false
		chunk := ids
		if pageFilter != nil {
			if chunk, err = pageFilter(seen+len(ids), ids); err != nil {
				return err
			}
		}
		if err := write(chunk); err != nil {
			return fmt.Errorf("%s chunk after cursor %q: %w", kind, cursor, err)
		}
		if next == "" {
			prog.finish(ctx, leg)
			return nil
		}
		seen += len(ids)
		cursor = next
		prog.save(ctx, leg, encodeLegPosition(seen, cursor))
	}
}

// graphIDPage reads one page of a graph id list from graph-service's
// internal keyset route (GET /v1/internal/graph/<kind>/<id>?limit=&cursor=;
// contract: testdata/contracts/graph_internal_lists). kind is "followers",
// "following" or "connections"; cursor "" is the first page; the returned
// next is "" on the last page. A non-200, a transport error, a malformed
// body and a cursor that does not advance are all errors, never an empty
// page.
func (s *Service) graphIDPage(ctx context.Context, kind string, userID uuid.UUID, cursor string, limit int) (ids []uuid.UUID, next string, err error) {
	url := fmt.Sprintf("%s/v1/internal/graph/%s/%s?limit=%d", s.graphURL, kind, userID.String(), limit)
	if cursor != "" {
		url += "&cursor=" + neturl.QueryEscape(cursor)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	// The internal-service key: graph-service gates every route on it when
	// configured.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := s.graphFor(ctx).Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("graph-service request failed: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, "", fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", &UpstreamStatusError{Service: "graph-service", Status: resp.StatusCode, Body: string(body)}
	}
	return decodeGraphIDPage(kind, cursor, body)
}

// decodeGraphIDPage reads a 200 body of the internal id-list contract. Both
// members must be present: a body without `ids` (another route's shape, a
// proxy's error page) decoded leniently would be "nobody follows this
// author", the one answer a fan-out must never invent.
func decodeGraphIDPage(kind, cursor string, body []byte) ([]uuid.UUID, string, error) {
	var envelope struct {
		Data *struct {
			IDs        *[]uuid.UUID `json:"ids"`
			NextCursor *string      `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, "", fmt.Errorf("unmarshal %s: %w", kind, err)
	}
	if envelope.Data == nil || envelope.Data.IDs == nil || envelope.Data.NextCursor == nil {
		return nil, "", fmt.Errorf("unmarshal %s: response has no data.ids / data.next_cursor", kind)
	}
	next := *envelope.Data.NextCursor
	if next != "" && next == cursor {
		return nil, "", fmt.Errorf("%s page after cursor %q named the same cursor as its next page", kind, cursor)
	}
	return *envelope.Data.IDs, next, nil
}

// graphIDList reads a whole graph id list, page by page. For lists the
// caller needs whole (the viewer's following list on the request path, the
// author's following set for a friends audience); a fan-out audience is
// streamed by graphLeg instead.
func (s *Service) graphIDList(ctx context.Context, kind string, userID uuid.UUID) ([]uuid.UUID, error) {
	var all []uuid.UUID
	cursor := ""
	for {
		ids, next, err := s.graphIDPage(ctx, kind, userID, cursor, graphListPageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, ids...)
		if next == "" {
			return all, nil
		}
		cursor = next
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
