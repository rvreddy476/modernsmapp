package service

// The video a recording became (2 Oct 2026). post-service makes the unlisted
// long video from live.stream.vod_ready and reports nothing back, so
// live-service-v2 asks for it:
//
//	GET {POST_SERVICE_URL}/v1/internal/posts/by-live-stream/<stream id>   (internal key)
//	-> 200 {"data":{"post_id":"<uuid>","visibility":"...","deleted":false}}
//	   404 no post for the stream (yet)
//
// and stores the id as live_streams.recording_post_id, once, only while it
// is still NULL (store SetRecordingPost).
//
// Who asks:
//   - the sweeper, for every import that reached 'done' and whose lookup is
//     still pending: at most postLookupBatch streams per tick, each with
//     backoff (15s doubling to 15 minutes);
//   - GetStream, for the one stream being read: one lookup bounded by
//     postLookupReadTimeout, at most once per postLookupReadGap per stream,
//     and a failure changes nothing.
//
// How it ends:
//   - a post            -> stored, lookup 'found';
//   - a deleted post    -> nothing stored (an id stored meanwhile is
//     cleared), lookup 'deleted', never asked again;
//   - 404               -> "not yet": asked again later; after
//     postLookupGiveUp (24h since the import finished) the lookup is
//     'gave_up'. Every stream is asked at least once, so streams that ended
//     long before this shipped are still backfilled;
//   - any other failure -> logged and asked again. It is not an answer, so
//     it does not end the lookup at 24h; postLookupErrorGiveUp (72h) does.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// RecordingPost is post-service's answer for one stream.
type RecordingPost struct {
	PostID     uuid.UUID
	Visibility string
	// Deleted: the post exists but was deleted; it must not be offered.
	Deleted bool
}

// ErrRecordingPostNotYet: post-service has no post for the stream (404).
var ErrRecordingPostNotYet = errors.New("recording post: none yet")

// RecordingPostSource finds the post made from a stream's recording.
// ErrRecordingPostNotYet means a definite "no post"; any other error means
// the answer is unknown.
type RecordingPostSource interface {
	RecordingPost(ctx context.Context, streamID uuid.UUID) (RecordingPost, error)
}

// Lookup cadence.
const (
	postLookupBatch       = 10               // streams per sweeper tick
	postLookupLease       = time.Minute      // a claimed lookup is not claimed again within this
	postLookupGiveUp      = 24 * time.Hour   // no post this long after the import finished
	postLookupErrorGiveUp = 72 * time.Hour   // nothing but errors this long
	postLookupMaxBackoff  = 15 * time.Minute //
	postLookupReadGap     = 10 * time.Second // detail reads ask at most this often per stream
	postLookupReadTimeout = 1500 * time.Millisecond
	postLookupHTTPTimeout = 3 * time.Second
)

// postLookupBackoff is the wait after the n-th unsettled lookup: 15s
// doubling, capped at 15 minutes.
func postLookupBackoff(attempts int) time.Duration {
	d := 15 * time.Second
	for i := 1; i < attempts && d < postLookupMaxBackoff; i++ {
		d *= 2
	}
	if d > postLookupMaxBackoff {
		d = postLookupMaxBackoff
	}
	return d
}

// HTTPRecordingPosts is RecordingPostSource over post-service.
type HTTPRecordingPosts struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

// NewHTTPRecordingPosts returns nil when baseURL is empty (not configured).
func NewHTTPRecordingPosts(baseURL, internalKey string) *HTTPRecordingPosts {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &HTTPRecordingPosts{baseURL: baseURL, internalKey: internalKey, http: &http.Client{Timeout: postLookupHTTPTimeout}}
}

// RecordingPost asks post-service (internal key only — never a user
// identity header). Only a 404 is ErrRecordingPostNotYet; a 200 without a
// usable post_id is an error, never "no post".
func (c *HTTPRecordingPosts) RecordingPost(ctx context.Context, streamID uuid.UUID) (RecordingPost, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/internal/posts/by-live-stream/"+streamID.String(), nil)
	if err != nil {
		return RecordingPost{}, err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return RecordingPost{}, fmt.Errorf("recording post: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return RecordingPost{}, ErrRecordingPostNotYet
	case resp.StatusCode != http.StatusOK:
		return RecordingPost{}, fmt.Errorf("recording post: status %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			PostID     string `json:"post_id"`
			Visibility string `json:"visibility"`
			Deleted    bool   `json:"deleted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return RecordingPost{}, fmt.Errorf("recording post: decode: %w", err)
	}
	id, err := uuid.Parse(out.Data.PostID)
	if err != nil || id == uuid.Nil {
		return RecordingPost{}, errors.New("recording post: answer has no post_id")
	}
	return RecordingPost{PostID: id, Visibility: out.Data.Visibility, Deleted: out.Data.Deleted}, nil
}

// ResolveRecordingPosts runs the due lookups once (the sweeper).
func (s *Service) ResolveRecordingPosts(ctx context.Context) {
	if s.posts == nil {
		return
	}
	due, err := s.store.ClaimDuePostLookups(ctx, postLookupBatch, postLookupLease)
	if err != nil {
		slog.Warn("live-v2 recording post: claim", "err", err)
		return
	}
	for _, l := range due {
		s.lookupRecordingPost(ctx, l, true)
	}
}

// lookupRecordingPost asks once for one claimed stream and records the
// outcome. scheduled is the sweeper's lookup: only it counts attempts,
// schedules the next one and gives up. It reports whether the stream row may
// have changed.
func (s *Service) lookupRecordingPost(ctx context.Context, l postgres.PostLookup, scheduled bool) (changed bool) {
	post, err := s.posts.RecordingPost(ctx, l.StreamID)
	switch {
	case err == nil && post.Deleted:
		slog.Info("live-v2 recording post: the post was deleted; not offered", "stream_id", l.StreamID)
		if err := s.store.StopPostLookup(ctx, l.StreamID, postgres.PostLookupDeleted); err != nil {
			slog.Warn("live-v2 recording post: record deleted", "stream_id", l.StreamID, "err", err)
		}
		return true
	case err == nil:
		stored, err := s.store.SetRecordingPost(ctx, l.StreamID, post.PostID)
		if err != nil {
			slog.Warn("live-v2 recording post: store", "stream_id", l.StreamID, "err", err)
			return false
		}
		if stored {
			slog.Info("live-v2 recording post: stored", "stream_id", l.StreamID, "post_id", post.PostID)
		}
		return true
	}
	notYet := errors.Is(err, ErrRecordingPostNotYet)
	if !notYet {
		slog.Warn("live-v2 recording post: lookup failed; will ask again", "stream_id", l.StreamID, "err", err)
	}
	if !scheduled {
		return false
	}
	limit := postLookupErrorGiveUp
	if notYet {
		limit = postLookupGiveUp
	}
	if l.Age >= limit {
		slog.Warn("live-v2 recording post: gave up", "stream_id", l.StreamID, "age", l.Age.String(), "no_post", notYet)
		if err := s.store.StopPostLookup(ctx, l.StreamID, postgres.PostLookupGaveUp); err != nil {
			slog.Warn("live-v2 recording post: record give-up", "stream_id", l.StreamID, "err", err)
		}
		return false
	}
	if err := s.store.RetryPostLookup(ctx, l.StreamID, postLookupBackoff(l.Attempts+1)); err != nil {
		slog.Warn("live-v2 recording post: record retry", "stream_id", l.StreamID, "err", err)
	}
	return false
}

// resolveRecordingPostOnRead is GetStream's one bounded lookup for a stream
// with a recording and no video id. It returns the row to serve: the fresh
// one when the lookup settled, otherwise st unchanged. Nothing here fails
// the read.
func (s *Service) resolveRecordingPostOnRead(ctx context.Context, st *postgres.LiveStream) *postgres.LiveStream {
	if s.posts == nil || st == nil || st.RecordingURL == nil || st.RecordingPostID != nil {
		return st
	}
	l, ok, err := s.store.ClaimPostLookup(ctx, st.ID, postLookupReadGap)
	if err != nil || !ok {
		return st
	}
	lctx, cancel := context.WithTimeout(ctx, postLookupReadTimeout)
	defer cancel()
	if !s.lookupRecordingPost(lctx, l, false) {
		return st
	}
	fresh, err := s.store.GetByID(ctx, st.ID)
	if err != nil {
		return st
	}
	return fresh
}
