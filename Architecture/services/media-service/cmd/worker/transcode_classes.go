package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/segmentio/kafka-go"
)

// Transcode classes (2026-11-07): reels must not wait behind long videos.
//
// The worker consumed media.events one message at a time, and a long-video
// transcode may run up to processing.MaxTranscodeJobBudget (48 h) with up
// to eight attempts, so a 10-second reel queued behind it waited for days.
// Work is now split by class:
//
//   - short: a video of at most processing.ReelMaxDurationSeconds (the
//     shorts boundary, 10 min);
//   - long: anything longer, or whose length the header does not give.
//
// THE ROUTING. Every transcode request still lands on media.events: the
// relay that publishes it (media-service's outbox) does not know the
// video's length, because nothing has probed the original yet. The short
// consumer reads each request's length from the asset row (a re-run) or the
// MP4 header (processing.MP4Duration, a few small range reads), and FORWARDS
// a long one, byte for byte, to media.events.long, then commits. It never
// downloads a long original. The long consumer transcodes whatever reaches
// media.events.long.
//
// THE PROCESS. MEDIA_TRANSCODE_CLASSES names the classes one worker
// consumes: "short", "long", or both (the default, so a single dev worker
// handles everything). Each class is its own consumer loop, so one worker
// with both runs a reel beside a long transcode instead of after it. Prod
// can run a dedicated long-video deployment (MEDIA_TRANSCODE_CLASSES=long)
// beside short-only workers.
//
// THE ROLLOUT. A worker of the previous release consuming media.events
// processes every request it reads, long ones included, exactly as before;
// requests already queued there are never stranded. A request the short
// consumer cannot forward (Kafka refused the write) is transcoded where it
// is: slower for the reels behind it, never lost.

// transcodeClass is the queue a transcode request belongs on.
type transcodeClass string

const (
	classShort transcodeClass = "short"
	classLong  transcodeClass = "long"
)

const (
	// transcodeClassesEnv selects the classes this worker consumes.
	transcodeClassesEnv = "MEDIA_TRANSCODE_CLASSES"
	// intakeTopic is where every transcode request is published (the relay,
	// cmd/server) and where the short consumer reads.
	intakeTopic = "media.events"
	// longTopic holds the long requests the short consumer forwarded.
	longTopic = intakeTopic + ".long"
	// The consumer groups. The short group keeps the name the single
	// consumer always had, so its committed offsets carry over.
	shortConsumerGroup = "media-transcode-worker"
	longConsumerGroup  = "media-transcode-worker-long"

	// classifyTimeout bounds the header read that classifies one request.
	classifyTimeout = 30 * time.Second
)

var transcodeRouted = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "media_transcode_routed_total",
	Help: "Transcode requests read by the short consumer, by the class they were sorted into and what happened to them.",
}, []string{"class", "outcome"})

// parseTranscodeClasses reads MEDIA_TRANSCODE_CLASSES: a comma list of
// short and long (or "all"); empty means both. Anything else is an error,
// so a typo cannot start a worker that consumes nothing.
func parseTranscodeClasses(v string) ([]transcodeClass, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" || v == "all" {
		return []transcodeClass{classShort, classLong}, nil
	}
	var short, long bool
	for _, part := range strings.Split(v, ",") {
		switch transcodeClass(strings.TrimSpace(part)) {
		case classShort:
			short = true
		case classLong:
			long = true
		default:
			return nil, fmt.Errorf("%s=%q: %q is not short or long", transcodeClassesEnv, v, strings.TrimSpace(part))
		}
	}
	var out []transcodeClass
	if short {
		out = append(out, classShort)
	}
	if long {
		out = append(out, classLong)
	}
	return out, nil
}

// consumerFor is the topic and consumer group one class is read from.
func consumerFor(c transcodeClass) (topic, group string) {
	if c == classLong {
		return longTopic, longConsumerGroup
	}
	return intakeTopic, shortConsumerGroup
}

// topicForClass is where a request of class c is transcoded from: long
// requests are forwarded to longTopic, short ones stay on the intake.
func topicForClass(c transcodeClass) string {
	if c == classLong {
		return longTopic
	}
	return intakeTopic
}

// classForDuration sorts a video by its length in seconds; 0 or less is
// unknown, and an unknown length is long: only a video proven short may
// hold the reel queue.
func classForDuration(seconds float64) transcodeClass {
	if seconds > 0 && seconds <= processing.ReelMaxDurationSeconds {
		return classShort
	}
	return classLong
}

// originalReader is what classification reads of the original.
type originalReader interface {
	StatObject(ctx context.Context, objectKey string) (blob.ObjectInfo, error)
	ReadObjectRange(ctx context.Context, objectKey string, start, end int64) ([]byte, error)
}

// messageWriter writes to the long topic (a *kafka.Writer).
type messageWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
}

// longRouter is the short consumer's sorting step.
type longRouter struct {
	store   mediaReader
	blobs   originalReader
	forward messageWriter
}

// classify decides the class of the request for mediaID. A request that
// cannot be transcoded at all (the asset is gone, or not a video) is short:
// the local handler records it at once. Any other doubt is long.
func (r *longRouter) classify(ctx context.Context, mediaID uuid.UUID) (transcodeClass, string) {
	m, err := r.store.GetMedia(ctx, mediaID)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, postgres.ErrMediaNotFound) || (err == nil && m == nil) {
		return classShort, "asset gone"
	}
	if err != nil {
		return classLong, "asset not read"
	}
	if m.FileType != "video" {
		return classShort, "not a video"
	}
	if ms := m.DurationMsValue(); ms > 0 {
		return classForDuration(float64(ms) / 1000), "row duration"
	}
	ctx, cancel := context.WithTimeout(ctx, classifyTimeout)
	defer cancel()
	info, err := r.blobs.StatObject(ctx, m.StorageKey)
	if err != nil {
		return classLong, "original not found"
	}
	seconds, err := processing.MP4Duration(func(off, n int64) ([]byte, error) {
		return r.blobs.ReadObjectRange(ctx, m.StorageKey, off, off+n-1)
	}, info.Size)
	if err != nil {
		return classLong, "length unknown"
	}
	return classForDuration(seconds), "header duration"
}

// forwardIfLong forwards m to longTopic when it is a long transcode request
// and reports whether it did; false means the caller transcodes m here.
// Forwarded is durable (the write is acknowledged by every in-sync
// replica), so the caller may commit m.
func (r *longRouter) forwardIfLong(ctx context.Context, m kafka.Message) bool {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil || envelope.EventType != events.MediaTranscodeRequested {
		return false // not a request, or unreadable: the local handler decides
	}
	var payload postgres.TranscodeRequestPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return false
	}
	mediaID, err := uuid.Parse(payload.MediaAssetID)
	if err != nil {
		return false
	}
	class, reason := r.classify(ctx, mediaID)
	if class != classLong {
		transcodeRouted.WithLabelValues(string(class), "local").Inc()
		return false
	}
	if err := r.forward.WriteMessages(ctx, kafka.Message{Key: m.Key, Value: m.Value, Headers: m.Headers}); err != nil {
		if ctx.Err() == nil {
			log.Printf("Transcode for media %s is long (%s) but could not be forwarded to %s; transcoding it here: %v",
				mediaID, reason, longTopic, err)
			transcodeRouted.WithLabelValues(string(class), "forward_failed").Inc()
		}
		return false
	}
	log.Printf("Transcode for media %s forwarded to %s (%s)", mediaID, topicForClass(class), reason)
	transcodeRouted.WithLabelValues(string(class), "forwarded").Inc()
	return true
}

// consume is one class's consumer loop: fetch, handle until durable, then
// commit (the offset is committed only after the durable effect; see main).
func consume(ctx context.Context, name string, reader *kafka.Reader, tracker *busyTracker, handle func(kafka.Message) bool) {
	log.Printf("Transcode consumer %q started", name)
	for {
		m, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Printf("Fetch error (%s): %v\n", name, err)
			continue
		}

		tracker.begin()
		durable := handle(m)
		tracker.end(time.Now())
		if !durable {
			// Shutting down mid-message. The offset is deliberately left
			// uncommitted so the next owner of this partition redelivers it.
			log.Printf("Context cancelled before commit (%s); stopping without committing", name)
			break
		}
		if err := reader.CommitMessages(ctx, m); err != nil {
			// The effect is durable and idempotent, so the redelivery this
			// causes is absorbed by the inbox.
			log.Printf("Commit error (%s; message will be redelivered): %v", name, err)
		}
	}
}
