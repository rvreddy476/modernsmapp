package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// KafkaConsumerMetrics tracks Kafka consumer health.
type KafkaConsumerMetrics struct {
	MessagesProcessed *prometheus.CounterVec
	ProcessingErrors  *prometheus.CounterVec
	ProcessDuration   *prometheus.HistogramVec
	DedupHits         *prometheus.CounterVec
	DLQMessages       *prometheus.CounterVec
	ConsumerLag       *prometheus.GaugeVec
	// RetryAge is how long the event now being retried has been failing, in
	// seconds since its first failed attempt; 0 when nothing is being
	// retried. A RetryForever consumer shows an outage here (the partition
	// waits behind the event) and not on the dead-letter counter.
	RetryAge *prometheus.GaugeVec
}

// NewKafkaConsumerMetrics creates Kafka consumer metrics for the given service.
func NewKafkaConsumerMetrics(serviceName string) *KafkaConsumerMetrics {
	ns := "atpost"
	sub := sanitize(serviceName)

	return &KafkaConsumerMetrics{
		MessagesProcessed: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: ns, Subsystem: sub,
				Name: "kafka_messages_processed_total",
				Help: "Total Kafka messages successfully processed.",
			},
			[]string{"topic", "consumer_group", "event_type"},
		),
		ProcessingErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: ns, Subsystem: sub,
				Name: "kafka_messages_errors_total",
				Help: "Total Kafka message processing errors.",
			},
			[]string{"topic", "consumer_group", "event_type", "error_class"},
		),
		ProcessDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: ns, Subsystem: sub,
				Name:    "kafka_message_process_duration_seconds",
				Help:    "Time taken to process a Kafka message.",
				Buckets: []float64{.001, .005, .01, .05, .1, .5, 1, 5},
			},
			[]string{"topic", "consumer_group", "event_type"},
		),
		DedupHits: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: ns, Subsystem: sub,
				Name: "kafka_dedup_hits_total",
				Help: "Total duplicate messages skipped.",
			},
			[]string{"topic", "consumer_group"},
		),
		DLQMessages: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: ns, Subsystem: sub,
				Name: "kafka_dlq_messages_total",
				Help: "Messages sent to dead-letter queue.",
			},
			[]string{"topic", "consumer_group", "event_type"},
		),
		ConsumerLag: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: ns, Subsystem: sub,
				Name: "kafka_consumer_lag",
				Help: "Current consumer lag (messages behind).",
			},
			[]string{"topic", "partition", "consumer_group"},
		),
		RetryAge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: ns, Subsystem: sub,
				Name: "kafka_message_retry_age_seconds",
				Help: "Seconds since the first failed attempt of the event being retried; 0 when none.",
			},
			[]string{"topic", "consumer_group"},
		),
	}
}
