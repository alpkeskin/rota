package services

import (
	"sync"
	"time"
)

// GeoIPMetricsSnapshot is the geo section of the system metrics API.
type GeoIPMetricsSnapshot struct {
	Provider             string  `json:"provider"`
	QueuePending         int     `json:"queue_pending"`
	QueuedInMemory       int     `json:"queued_in_memory"`
	BatchRequestsLastMin int     `json:"batch_requests_last_minute"`
	BatchRequestsLimit   int     `json:"batch_requests_limit"`
	UsagePercent1m       float64 `json:"usage_percent_1m"`
	IPsUpdatedLast10m    int     `json:"ips_updated_last_10m"`
}

// GeoMetrics keeps thread-safe rolling counters for geo enrichment: batch
// requests in the last minute, updated IPs in the last 10 minutes, and the
// cached queue depth / DB backlog reported by the worker.
type GeoMetrics struct {
	mu             sync.Mutex
	queuePending   int
	queuedInMemory int
	batchLimit     int
	batchTimes     []time.Time
	updatedTimes   []time.Time
}

// NewGeoMetrics creates a GeoMetrics with the configured batch limit.
func NewGeoMetrics(batchLimit int) *GeoMetrics {
	return &GeoMetrics{batchLimit: batchLimit}
}

// RecordBatchRequest counts one sent batch request in the rolling 60s window.
func (m *GeoMetrics) RecordBatchRequest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.batchTimes = append(m.batchTimes, now)
	m.batchTimes = trimBefore(m.batchTimes, now.Add(-time.Minute))
}

// RecordIPsUpdated counts n successfully persisted IPs in the rolling 10m
// window.
func (m *GeoMetrics) RecordIPsUpdated(n int) {
	if n <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for i := 0; i < n; i++ {
		m.updatedTimes = append(m.updatedTimes, now)
	}
	m.updatedTimes = trimBefore(m.updatedTimes, now.Add(-10*time.Minute))
}

// SetQueueState caches the in-memory queue depth and the DB backlog.
func (m *GeoMetrics) SetQueueState(queuePending, queuedInMemory int) {
	m.mu.Lock()
	m.queuePending = queuePending
	m.queuedInMemory = queuedInMemory
	m.mu.Unlock()
}

// Metrics returns a consistent snapshot for the API.
func (m *GeoMetrics) Metrics() *GeoIPMetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	batch1m := countSince(m.batchTimes, now.Add(-time.Minute))
	updated10m := countSince(m.updatedTimes, now.Add(-10*time.Minute))

	usage := 0.0
	if m.batchLimit > 0 {
		usage = float64(batch1m) / float64(m.batchLimit) * 100
	}

	return &GeoIPMetricsSnapshot{
		QueuePending:         m.queuePending,
		QueuedInMemory:       m.queuedInMemory,
		BatchRequestsLastMin: batch1m,
		BatchRequestsLimit:   m.batchLimit,
		UsagePercent1m:       usage,
		IPsUpdatedLast10m:    updated10m,
	}
}

// trimBefore drops entries older than cutoff, returning the surviving tail.
func trimBefore(times []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(times) && times[i].Before(cutoff) {
		i++
	}
	if i == 0 {
		return times
	}
	if i == len(times) {
		return nil
	}
	out := make([]time.Time, len(times)-i)
	copy(out, times[i:])
	return out
}

// countSince counts entries at or after since.
func countSince(times []time.Time, since time.Time) int {
	n := 0
	for _, t := range times {
		if !t.Before(since) {
			n++
		}
	}
	return n
}
