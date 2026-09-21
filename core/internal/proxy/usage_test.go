package proxy

import (
	"context"
	"testing"
)

// TestAggregateRecords verifies the batched stat aggregation reproduces the
// sequential per-request semantics of updateProxyStats (AUD-17/AUD-38 + the
// batching perf change): weighted avg sum, consecutive-failure reset on success,
// and only trailing failures accumulating.
func TestAggregateRecords(t *testing.T) {
	rec := func(id int, success bool, rt int) RequestRecord {
		r := RequestRecord{ProxyID: id, Success: success}
		if success {
			r.ResponseTime = rt
		} else {
			r.ErrorMessage = "boom"
		}
		return r
	}

	tests := []struct {
		name           string
		batch          []RequestRecord
		id             int
		wantReq        int
		wantSucc       int
		wantSumRT      int64
		wantTrailing   int
		wantHadSuccess bool
		wantLastOK     bool
	}{
		{
			name:           "all successes",
			batch:          []RequestRecord{rec(1, true, 100), rec(1, true, 200)},
			id:             1,
			wantReq:        2,
			wantSucc:       2,
			wantSumRT:      300,
			wantTrailing:   0,
			wantHadSuccess: true,
			wantLastOK:     true,
		},
		{
			name:           "all failures accumulate",
			batch:          []RequestRecord{rec(1, false, 0), rec(1, false, 0), rec(1, false, 0)},
			id:             1,
			wantReq:        3,
			wantSucc:       0,
			wantSumRT:      0,
			wantTrailing:   3,
			wantHadSuccess: false,
			wantLastOK:     false,
		},
		{
			name:           "success mid-window resets, only trailing fail counts",
			batch:          []RequestRecord{rec(1, false, 0), rec(1, false, 0), rec(1, true, 50), rec(1, false, 0)},
			id:             1,
			wantReq:        4,
			wantSucc:       1,
			wantSumRT:      50,
			wantTrailing:   1,
			wantHadSuccess: true,
			wantLastOK:     false,
		},
		{
			name:           "ends on success",
			batch:          []RequestRecord{rec(1, false, 0), rec(1, true, 80)},
			id:             1,
			wantReq:        2,
			wantSucc:       1,
			wantSumRT:      80,
			wantTrailing:   0,
			wantHadSuccess: true,
			wantLastOK:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, aggs := aggregateRecords(tc.batch)
			a := aggs[tc.id]
			if a == nil {
				t.Fatalf("no aggregate for proxy %d", tc.id)
			}
			if a.reqDelta != tc.wantReq {
				t.Errorf("reqDelta = %d, want %d", a.reqDelta, tc.wantReq)
			}
			if a.succDelta != tc.wantSucc {
				t.Errorf("succDelta = %d, want %d", a.succDelta, tc.wantSucc)
			}
			if a.sumRT != tc.wantSumRT {
				t.Errorf("sumRT = %d, want %d", a.sumRT, tc.wantSumRT)
			}
			if a.trailingFails != tc.wantTrailing {
				t.Errorf("trailingFails = %d, want %d", a.trailingFails, tc.wantTrailing)
			}
			if a.hadSuccess != tc.wantHadSuccess {
				t.Errorf("hadSuccess = %v, want %v", a.hadSuccess, tc.wantHadSuccess)
			}
			if a.lastWasSuccess != tc.wantLastOK {
				t.Errorf("lastWasSuccess = %v, want %v", a.lastWasSuccess, tc.wantLastOK)
			}
		})
	}
}

// TestAggregateRecordsMultiProxy checks per-proxy isolation and first-seen order.
func TestAggregateRecordsMultiProxy(t *testing.T) {
	batch := []RequestRecord{
		{ProxyID: 7, Success: true, ResponseTime: 10},
		{ProxyID: 3, Success: false, ErrorMessage: "x"},
		{ProxyID: 7, Success: true, ResponseTime: 20},
	}
	order, aggs := aggregateRecords(batch)
	if len(order) != 2 || order[0] != 7 || order[1] != 3 {
		t.Fatalf("order = %v, want [7 3]", order)
	}
	if aggs[7].reqDelta != 2 || aggs[7].succDelta != 2 || aggs[7].sumRT != 30 {
		t.Errorf("proxy 7 agg wrong: %+v", aggs[7])
	}
	if aggs[3].reqDelta != 1 || aggs[3].trailingFails != 1 || aggs[3].hadSuccess {
		t.Errorf("proxy 3 agg wrong: %+v", aggs[3])
	}
}

// TestRecordManualTestResultSuccess verifies a successful manual test applies
// 'active' immediately and clears the failure state.
func TestRecordManualTestResultSuccess(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id := insertTestProxy(t, db.Pool, "10.0.0.1:8080", "failed")
	// Pre-existing failure state that a manual success must clear.
	if _, err := db.Pool.Exec(ctx,
		`UPDATE proxies SET failed_requests = 2, last_error = 'old' WHERE id = $1`, id); err != nil {
		t.Fatalf("seed failure state: %v", err)
	}

	if err := db.Tracker.RecordManualTestResult(ctx, id, true, ""); err != nil {
		t.Fatalf("RecordManualTestResult(success): %v", err)
	}

	row := readProxyRow(t, db.Pool, id)
	if row.Status != "active" {
		t.Errorf("status = %q, want active", row.Status)
	}
	if row.LastError != nil {
		t.Errorf("last_error = %q, want nil", *row.LastError)
	}
	if row.FailedRequests != 0 {
		t.Errorf("failed_requests = %d, want 0", row.FailedRequests)
	}
}

// TestRecordManualTestResultFailure verifies a failed manual test applies
// 'failed' immediately, stores the error, and increments the failure counter.
func TestRecordManualTestResultFailure(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id := insertTestProxy(t, db.Pool, "10.0.0.2:8080", "active")

	if err := db.Tracker.RecordManualTestResult(ctx, id, false, "Connection refused - proxy may be offline"); err != nil {
		t.Fatalf("RecordManualTestResult(failure): %v", err)
	}

	row := readProxyRow(t, db.Pool, id)
	if row.Status != "failed" {
		t.Errorf("status = %q, want failed", row.Status)
	}
	if row.LastError == nil || *row.LastError != "Connection refused - proxy may be offline" {
		t.Errorf("last_error = %v, want the failure message", row.LastError)
	}
	if row.FailedRequests != 1 {
		t.Errorf("failed_requests = %d, want 1", row.FailedRequests)
	}
}

// TestRecordHealthCheckConsecutiveFailures is a regression guard for the
// periodic path: the 3-consecutive-failure threshold and the success reset
// must be untouched by the manual-check change.
func TestRecordHealthCheckConsecutiveFailures(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id := insertTestProxy(t, db.Pool, "10.0.0.3:8080", "active")

	check := func(success bool) {
		t.Helper()
		if err := db.Tracker.RecordHealthCheck(ctx, id, success, 10, "boom"); err != nil {
			t.Fatalf("RecordHealthCheck: %v", err)
		}
	}

	// 1st failure: counter increments, status unchanged.
	check(false)
	row := readProxyRow(t, db.Pool, id)
	if row.Status != "active" || row.FailedRequests != 1 {
		t.Fatalf("after 1 failure: status = %q, failed_requests = %d; want active/1", row.Status, row.FailedRequests)
	}

	// 2nd failure: still below threshold.
	check(false)
	row = readProxyRow(t, db.Pool, id)
	if row.Status != "active" || row.FailedRequests != 2 {
		t.Fatalf("after 2 failures: status = %q, failed_requests = %d; want active/2", row.Status, row.FailedRequests)
	}

	// 3rd consecutive failure: flips to failed.
	check(false)
	row = readProxyRow(t, db.Pool, id)
	if row.Status != "failed" || row.FailedRequests != 3 {
		t.Fatalf("after 3 failures: status = %q, failed_requests = %d; want failed/3", row.Status, row.FailedRequests)
	}

	// Success: reactivates and resets the counter.
	check(true)
	row = readProxyRow(t, db.Pool, id)
	if row.Status != "active" || row.FailedRequests != 0 || row.LastError != nil {
		t.Fatalf("after success: status = %q, failed_requests = %d, last_error = %v; want active/0/nil",
			row.Status, row.FailedRequests, row.LastError)
	}
}
