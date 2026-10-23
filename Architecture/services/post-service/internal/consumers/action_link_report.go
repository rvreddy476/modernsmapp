package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
)

/*
	scam_link reports -> Action button under review (pinned contract,
	23 Oct 2026).

	The mechanism that already exists between the two services: trust-safety
	publishes ReportFiled on social.events.v1 for every report it stores
	(service/moderation.go FileReportRef), and post-service already consumes
	it for the comment auto-hold (comment_report_hold.go). This consumer, on
	its own group, takes the reports whose reason is scam_link and whose
	entity_type is post or user (a profile), and counts each reporter once
	against the target's live action link; at 3 distinct reporters within 7
	days an active link becomes under_review — hidden from viewers, /l/<id>
	stops resolving — until a moderator restores or blocks it
	(/v1/posts/internal/admin/action-links).

	Idempotency lives in PostgreSQL (action_link_reports, one row per link
	and reporter), so a redelivery never counts twice. Transient failures
	retry forever; an event that can never apply is Permanent (DLQ).
*/

const actionLinkReportGroupID = "post-service-action-link-report"

// ReasonScamLink is trust-safety's report reason for a fake job or scam
// link.
const ReasonScamLink = "scam_link"

type scamLinkApplier interface {
	ApplyScamLinkReport(ctx context.Context, reportID, reporterID uuid.UUID, targetType string, targetID uuid.UUID, reportedAt time.Time) (postgres.ActionLinkReportOutcome, error)
}

// ActionLinkReportConsumer applies scam_link reports.
type ActionLinkReportConsumer struct {
	svc      scamLinkApplier
	consumer *sharedkafka.Consumer
}

// NewActionLinkReportConsumer builds the consumer on topic (the social topic
// trust-safety publishes ReportFiled to).
func NewActionLinkReportConsumer(svc *service.Service, brokers []string, topic string, m *metrics.KafkaConsumerMetrics) *ActionLinkReportConsumer {
	c := &ActionLinkReportConsumer{svc: svc}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:      brokers,
			GroupID:      actionLinkReportGroupID,
			Topic:        topic,
			DLQTopic:     topic + ".dlq",
			RetryForever: true,
		},
		nil, // idempotent per (link, reporter) in PostgreSQL
		m, c.handle,
	)
	return c
}

func (c *ActionLinkReportConsumer) Start(ctx context.Context) { c.consumer.Start(ctx) }

func (c *ActionLinkReportConsumer) Close() error { return c.consumer.Close() }

func (c *ActionLinkReportConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	if env.EventType != events.ReportFiled {
		return nil
	}
	var p events.ReportFiledPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return sharedkafka.Permanent(fmt.Errorf("decode ReportFiled: %w", err))
	}
	if strings.ToLower(strings.TrimSpace(p.Reason)) != ReasonScamLink {
		return nil
	}
	targetType := strings.ToLower(strings.TrimSpace(p.EntityType))
	if targetType != "post" && targetType != "user" {
		return nil
	}
	reportID, err1 := uuid.Parse(p.ReportID)
	targetID, err2 := uuid.Parse(p.EntityID)
	reporterID, err3 := uuid.Parse(p.ReporterID)
	if err1 != nil || err2 != nil || err3 != nil || reportID == uuid.Nil || targetID == uuid.Nil || reporterID == uuid.Nil {
		return sharedkafka.Permanent(fmt.Errorf("ReportFiled %q carries an unusable id (report %q, %s %q, reporter %q)",
			env.EventID, p.ReportID, targetType, p.EntityID, p.ReporterID))
	}
	out, err := c.svc.ApplyScamLinkReport(ctx, reportID, reporterID, targetType, targetID, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("apply scam_link report %s: %w", reportID, err)
	}
	switch {
	case !out.Found:
		slog.InfoContext(ctx, "scam_link report: target has no action link", "report_id", reportID, "target_type", targetType, "target_id", targetID)
	case out.Held:
		slog.InfoContext(ctx, "scam_link report: action link put under review",
			"report_id", reportID, "link_id", out.LinkID, "reporters", out.Reporters)
	}
	return nil
}
