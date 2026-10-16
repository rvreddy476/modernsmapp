package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
)

/*
	Comment report -> auto-review hold (TikTok parity T-13).

	The gateway routes POST /v1/reports to trust-safety-service, so the
	comments.flagged_count hold that post-service's own report route drives
	was unreachable. trust-safety publishes ReportFiled on social.events.v1
	for every report it stores (service/moderation.go FileReportRef); this
	consumer counts each one whose entity_type is "comment" toward the same
	threshold (store/postgres/comment_report_holds.go).

	Idempotency lives in the database, not in Redis: the (comment, reporter)
	row is written in the same transaction as the count, so a redelivery —
	or a second report by the same reporter — can never count twice, even
	after the Redis receipt has expired. Transient failures retry forever
	(the hold is a safety state; parking it in a DLQ would silently drop a
	report); an event that can never apply is Permanent and is DLQed.
*/

// commentReportGroupID is this consumer's own group on the shared social
// topic, distinct from every other post-service group.
const commentReportGroupID = "post-service-comment-report-hold"

// commentReportApplier is the one service call the handler needs, an
// interface so the handler is testable with a fake.
type commentReportApplier interface {
	ApplyTrustSafetyCommentReport(ctx context.Context, reportID, commentID, reporterID uuid.UUID) (postgres.CommentReportOutcome, error)
}

// CommentReportHoldConsumer applies trust-safety comment reports.
type CommentReportHoldConsumer struct {
	svc      commentReportApplier
	consumer *sharedkafka.Consumer
}

// NewCommentReportHoldConsumer builds the consumer on topic (the social
// topic trust-safety-service publishes ReportFiled to).
func NewCommentReportHoldConsumer(svc *service.Service, brokers []string, topic string, m *metrics.KafkaConsumerMetrics) *CommentReportHoldConsumer {
	c := &CommentReportHoldConsumer{svc: svc}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:      brokers,
			GroupID:      commentReportGroupID,
			Topic:        topic,
			DLQTopic:     topic + ".dlq",
			RetryForever: true,
		},
		nil, // idempotent per (comment, reporter) in PostgreSQL; see above
		m, c.handle,
	)
	return c
}

func (c *CommentReportHoldConsumer) Start(ctx context.Context) { c.consumer.Start(ctx) }

func (c *CommentReportHoldConsumer) Close() error { return c.consumer.Close() }

func (c *CommentReportHoldConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	if env.EventType != events.ReportFiled {
		return nil
	}
	var p events.ReportFiledPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return sharedkafka.Permanent(fmt.Errorf("decode ReportFiled: %w", err))
	}
	if strings.ToLower(strings.TrimSpace(p.EntityType)) != "comment" {
		return nil // posts, users, hashtags, sounds: not a comment hold
	}
	reportID, err1 := uuid.Parse(p.ReportID)
	commentID, err2 := uuid.Parse(p.EntityID)
	reporterID, err3 := uuid.Parse(p.ReporterID)
	if err1 != nil || err2 != nil || err3 != nil || reportID == uuid.Nil || commentID == uuid.Nil || reporterID == uuid.Nil {
		return sharedkafka.Permanent(fmt.Errorf("ReportFiled %q carries an unusable id (report %q, comment %q, reporter %q)",
			env.EventID, p.ReportID, p.EntityID, p.ReporterID))
	}

	out, err := c.svc.ApplyTrustSafetyCommentReport(ctx, reportID, commentID, reporterID)
	if err != nil {
		// Returned, not logged: the offset must not pass a report that was
		// not counted.
		return fmt.Errorf("apply comment report %s: %w", reportID, err)
	}
	switch {
	case !out.Found:
		slog.InfoContext(ctx, "comment report: comment not held by post-service, nothing to count",
			"report_id", reportID, "comment_id", commentID)
	case !out.Counted:
		slog.InfoContext(ctx, "comment report: reporter already counted on this comment (redelivery or repeat report)",
			"report_id", reportID, "comment_id", commentID)
	case out.Held:
		slog.InfoContext(ctx, "comment report: auto-review hold applied",
			"report_id", reportID, "comment_id", commentID, "post_id", out.PostID)
	}
	return nil
}
