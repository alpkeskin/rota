package services

import "testing"

func TestGeoMetricsSnapshot(t *testing.T) {
	m := NewGeoMetrics(15)

	m.RecordBatchRequest()
	m.RecordBatchRequest()
	m.RecordBatchRequest()
	m.RecordIPsUpdated(7)
	m.SetQueueState(42, 5)

	snap := m.Metrics()
	if snap.BatchRequestsLastMin != 3 {
		t.Errorf("batch_requests_last_minute = %d, want 3", snap.BatchRequestsLastMin)
	}
	if snap.BatchRequestsLimit != 15 {
		t.Errorf("batch_requests_limit = %d, want 15", snap.BatchRequestsLimit)
	}
	if snap.UsagePercent1m != 20.0 {
		t.Errorf("usage_percent_1m = %v, want 20", snap.UsagePercent1m)
	}
	if snap.IPsUpdatedLast10m != 7 {
		t.Errorf("ips_updated_last_10m = %d, want 7", snap.IPsUpdatedLast10m)
	}
	if snap.QueuePending != 42 {
		t.Errorf("queue_pending = %d, want 42", snap.QueuePending)
	}
	if snap.QueuedInMemory != 5 {
		t.Errorf("queued_in_memory = %d, want 5", snap.QueuedInMemory)
	}
}

func TestGeoMetricsZeroLimit(t *testing.T) {
	m := NewGeoMetrics(0)
	m.RecordBatchRequest()

	if snap := m.Metrics(); snap.UsagePercent1m != 0 {
		t.Errorf("usage_percent_1m = %v, want 0 when limit is 0", snap.UsagePercent1m)
	}
}

func TestGeoMetricsEmpty(t *testing.T) {
	m := NewGeoMetrics(15)
	snap := m.Metrics()
	if snap.BatchRequestsLastMin != 0 || snap.IPsUpdatedLast10m != 0 ||
		snap.QueuePending != 0 || snap.QueuedInMemory != 0 || snap.UsagePercent1m != 0 {
		t.Errorf("expected all-zero snapshot, got %+v", snap)
	}
}
