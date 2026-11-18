// dlq-replay lists a dead-letter topic and re-publishes chosen messages to
// the topic they came from (condition 6 / reels-5, 18 Nov 2026). Runbook:
// docs/runbooks/dlq-replay.md. The rules live in internal/dlqreplay.
//
//	dlq-replay -dlq media.events.dlq                      list every message
//	dlq-replay -dlq media.events.dlq -all                 dry run: what -all would replay
//	dlq-replay -dlq media.events.dlq -offsets 0/12,1/7 -write
//	dlq-replay -dlq media.events.dlq -all -group post-service-media-transcode -write
//
// Nothing is written without -write. Production (ENV=prod or
// APP_ENV=production) is refused unless -confirm-production names the
// dead-letter topic again. A message already replayed (a marker on the
// dead-letter topic says so) is never sent twice.
//
// Environment, the same as the server: KAFKA_BROKERS and the KAFKA_TLS_* /
// KAFKA_SASL_* settings (shared/transport), REDIS_ADDR and its REDIS_*
// settings (needed with -write: the dead-lettering group's dedupe receipt
// is cleared before the re-publish), ENV / APP_ENV.
//
// Exit status: 0 done, 1 a read or write failed (re-run is safe), 2 misuse
// or refused.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/atpost/post-service/internal/dlqreplay"
	"github.com/atpost/shared/transport"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

type options struct {
	dlq, offsets, group, eventType, confirm string
	all, write                              bool
}

func parseFlags(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("dlq-replay", flag.ContinueOnError)
	fs.StringVar(&o.dlq, "dlq", "", "dead-letter topic, e.g. media.events.dlq (required)")
	fs.BoolVar(&o.all, "all", false, "replay every message not replayed yet (after -group / -event-type)")
	fs.StringVar(&o.offsets, "offsets", "", "replay these messages: partition/offset,...")
	fs.StringVar(&o.group, "group", "", "only messages this consumer group dead-lettered")
	fs.StringVar(&o.eventType, "event-type", "", "only messages of this event type")
	fs.BoolVar(&o.write, "write", false, "write (default: dry run)")
	fs.StringVar(&o.confirm, "confirm-production", "", "in production, the dead-letter topic's name again")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.dlq == "" {
		return o, errors.New("-dlq is required")
	}
	if _, err := dlqreplay.SourceOf(o.dlq); err != nil {
		return o, err
	}
	if o.write && !o.all && o.offsets == "" {
		return o, dlqreplay.ErrNoSelection
	}
	return o, nil
}

func run(args []string) int {
	o, err := parseFlags(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dlq-replay:", err)
		return 2
	}
	env := os.Getenv("ENV")
	if env == "" {
		env = os.Getenv("APP_ENV")
	}
	if err := dlqreplay.GuardEnvironment(env, o.dlq, o.confirm); err != nil {
		fmt.Fprintln(os.Stderr, "dlq-replay:", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	brokers := strings.Split(envOr("KAFKA_BROKERS", "kafka:9092"), ",")
	dialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dlq-replay: kafka settings:", err)
		return 2
	}

	msgs, err := readTopic(ctx, brokers, dialer, o.dlq)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dlq-replay: read", o.dlq+":", err)
		return 1
	}
	entries := dlqreplay.Load(msgs)
	src, _ := dlqreplay.SourceOf(o.dlq)
	fmt.Printf("%s: %d dead-lettered message(s), source topic %s\n", o.dlq, len(entries), src)
	for _, e := range entries {
		state := "pending"
		if e.Replayed {
			state = "replayed"
		}
		fmt.Printf("  %-8s %-9s %s group=%s type=%s event=%s error=%q\n",
			e.Ref(), state, e.Time.UTC().Format(time.RFC3339), e.Group, e.EventType, e.EventID, truncate(e.Error, 120))
	}
	if !o.all && o.offsets == "" {
		return 0 // list only
	}

	refs, err := dlqreplay.ParseRefs(o.offsets)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dlq-replay:", err)
		return 2
	}
	chosen, skipped, err := dlqreplay.Select(entries, dlqreplay.Selection{All: o.all, Refs: refs, Group: o.group, EventType: o.eventType})
	if err != nil {
		fmt.Fprintln(os.Stderr, "dlq-replay:", err)
		return 2
	}
	for _, e := range skipped {
		fmt.Printf("skip %s: already replayed\n", e.Ref())
	}

	var w dlqreplay.Writer
	var rc dlqreplay.Receipts
	if o.write {
		writer := &kafka.Writer{
			Addr:         kafka.TCP(brokers...),
			Balancer:     &kafka.Hash{}, // the same partition as the producers that key by aggregate
			RequiredAcks: kafka.RequireAll,
			Transport:    &kafka.Transport{TLS: dialer.TLS, SASL: dialer.SASLMechanism},
		}
		defer writer.Close()
		w = writer
		if addr := os.Getenv("REDIS_ADDR"); addr != "" {
			rdb, err := transport.NewRedisClientFromEnv(addr)
			if err != nil {
				fmt.Fprintln(os.Stderr, "dlq-replay: redis settings:", err)
				return 2
			}
			defer rdb.Close()
			rc = redisReceipts{rdb}
		}
	}
	rep, err := dlqreplay.Replay(ctx, o.dlq, chosen, w, rc, o.write)
	verb := "would replay"
	if o.write {
		verb = "replayed"
	}
	for _, r := range rep.Replayed {
		fmt.Printf("%s %s -> %s\n", verb, r, src)
	}
	for _, r := range rep.Refused {
		fmt.Printf("refused %s\n", r)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "dlq-replay:", err)
		return 1
	}
	if !o.write && len(rep.Replayed) > 0 {
		fmt.Println("dry run: nothing written; add -write to send")
	}
	return 0
}

type redisReceipts struct{ rdb *redis.Client }

func (r redisReceipts) Clear(ctx context.Context, key string) error {
	return r.rdb.Del(ctx, key).Err()
}

// readTopic reads every message on every partition of topic, from the
// earliest retained offset to the high-water mark at the time of the call.
func readTopic(ctx context.Context, brokers []string, dialer *kafka.Dialer, topic string) ([]kafka.Message, error) {
	conn, err := dialer.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		return nil, err
	}
	parts, err := conn.ReadPartitions(topic)
	_ = conn.Close()
	if err != nil {
		return nil, err
	}
	var out []kafka.Message
	for _, p := range parts {
		lc, err := dialer.DialLeader(ctx, "tcp", brokers[0], topic, p.ID)
		if err != nil {
			return nil, err
		}
		first, last, err := lc.ReadOffsets()
		_ = lc.Close()
		if err != nil {
			return nil, err
		}
		if last <= first {
			continue
		}
		r := kafka.NewReader(kafka.ReaderConfig{Brokers: brokers, Topic: topic, Partition: p.ID, Dialer: dialer, MaxBytes: 10 << 20})
		if err := r.SetOffset(first); err != nil {
			_ = r.Close()
			return nil, err
		}
		for {
			rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			m, err := r.ReadMessage(rctx)
			cancel()
			if err != nil {
				_ = r.Close()
				return nil, fmt.Errorf("partition %d: %w", p.ID, err)
			}
			out = append(out, m)
			if m.Offset >= last-1 {
				break
			}
		}
		_ = r.Close()
	}
	return out, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
