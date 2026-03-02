package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// DBPoolMetrics exports pgxpool stats as Prometheus gauges.
type DBPoolMetrics struct {
	AcquireCount  prometheus.Gauge
	AcquiredConns prometheus.Gauge
	IdleConns     prometheus.Gauge
	TotalConns    prometheus.Gauge
	MaxConns      prometheus.Gauge
}

// NewDBPoolMetrics creates DB pool gauges for the given service and pool name.
func NewDBPoolMetrics(serviceName, poolName string) *DBPoolMetrics {
	ns := "atpost"
	sub := sanitize(serviceName)
	labels := prometheus.Labels{"pool": poolName}

	return &DBPoolMetrics{
		AcquireCount: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: ns, Subsystem: sub,
			Name:        "db_pool_acquire_count_total",
			Help:        "Cumulative count of successful acquires.",
			ConstLabels: labels,
		}),
		AcquiredConns: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: ns, Subsystem: sub,
			Name:        "db_pool_acquired_conns",
			Help:        "Number of currently acquired connections.",
			ConstLabels: labels,
		}),
		IdleConns: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: ns, Subsystem: sub,
			Name:        "db_pool_idle_conns",
			Help:        "Number of currently idle connections.",
			ConstLabels: labels,
		}),
		TotalConns: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: ns, Subsystem: sub,
			Name:        "db_pool_total_conns",
			Help:        "Total number of connections in the pool.",
			ConstLabels: labels,
		}),
		MaxConns: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: ns, Subsystem: sub,
			Name:        "db_pool_max_conns",
			Help:        "Maximum number of connections allowed.",
			ConstLabels: labels,
		}),
	}
}

// PgxPoolStat mirrors the fields from pgxpool.Stat we care about.
// Uses an interface-free approach so shared doesn't import pgx.
type PgxPoolStat struct {
	AcquireCount  int64
	AcquiredConns int32
	IdleConns     int32
	TotalConns    int32
	MaxConns      int32
}

// Update sets all gauge values from a pool stat snapshot.
func (m *DBPoolMetrics) Update(s PgxPoolStat) {
	m.AcquireCount.Set(float64(s.AcquireCount))
	m.AcquiredConns.Set(float64(s.AcquiredConns))
	m.IdleConns.Set(float64(s.IdleConns))
	m.TotalConns.Set(float64(s.TotalConns))
	m.MaxConns.Set(float64(s.MaxConns))
}
