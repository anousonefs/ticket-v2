package monitoring

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
)

var (
	queueLength = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "queue_length_total",
			Help: "Current queue length per event",
		},
		[]string{"event_id", "queue_type"},
	)

	processingUsers = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "processing_users_total",
			Help: "Current number of users in processing per event",
		},
		[]string{"event_id"},
	)

	queueOperations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "queue_operations_total",
			Help: "Total queue operations",
		},
		[]string{"operation", "event_id", "status"},
	)

	goroutineCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_goroutines_total",
			Help: "Current number of active goroutines",
		},
	)

	seatLockDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "seat_lock_duration_seconds",
			Help:    "Duration of seat locks",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10),
		},
		[]string{"event_id"},
	)
)

type Monitor struct {
	redis *redis.Client
}

func NewMonitor(redisClient *redis.Client) *Monitor {
	monitor := &Monitor{redis: redisClient}

	// Start metrics collection
	go monitor.collectMetrics()

	return monitor
}

func (m *Monitor) collectMetrics() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		ctx := context.Background()

		// Collect queue metrics
		m.collectQueueMetrics(ctx)

		// Collect goroutine metrics
		m.collectGoroutineMetrics()
	}
}

func (m *Monitor) collectQueueMetrics(ctx context.Context) {
	// Get all waiting queues
	waitingKeys, _ := m.redis.Keys(ctx, "queue:waiting:*").Result()
	for _, key := range waitingKeys {
		eventID := key[len("queue:waiting:"):]
		length, _ := m.redis.LLen(ctx, key).Result()
		queueLength.WithLabelValues(eventID, "waiting").Set(float64(length))
	}

	// Get all processing queues
	processingKeys, _ := m.redis.Keys(ctx, "queue:processing:*").Result()
	for _, key := range processingKeys {
		eventID := key[len("queue:processing:"):]
		length, _ := m.redis.SCard(ctx, key).Result()
		processingUsers.WithLabelValues(eventID).Set(float64(length))
	}
}

func (m *Monitor) collectGoroutineMetrics() {
	// This would require runtime inspection or manual tracking
	// For now, we'll track it manually in the service
}

// Track queue operations
func (m *Monitor) TrackQueueOperation(operation, eventID, status string) {
	queueOperations.WithLabelValues(operation, eventID, status).Inc()
}

// Track seat lock duration
func (m *Monitor) TrackSeatLock(eventID string, duration time.Duration) {
	seatLockDuration.WithLabelValues(eventID).Observe(duration.Seconds())
}
