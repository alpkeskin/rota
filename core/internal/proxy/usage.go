package proxy

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/jackc/pgx/v5"
)

const (
	// usageQueueSize bounds the in-flight record buffer; on overflow records are
	// dropped (stats are best-effort) rather than blocking the request path.
	usageQueueSize = 8192
	// usageMaxBatch flushes early once this many records accumulate.
	usageMaxBatch = 500
	// usageFlushInterval is the periodic flush cadence.
	usageFlushInterval = time.Second
)

// UsageTracker tracks proxy usage and updates statistics.
//
// On the request hot path, per-request synchronous DB writes (one INSERT + one
// UPDATE each) are the dominant scaling cost. When a batch writer is started
// (StartBatchWriter), RecordRequest instead enqueues onto a buffered channel and
// a single worker coalesces records: bulk-inserting proxy_requests via CopyFrom
// and folding per-proxy stat deltas into one UPDATE per proxy per flush window.
// This turns O(requests) round-trips into O(distinct proxies / second).
type UsageTracker struct {
	repo *repository.ProxyRepository

	recordCh chan RequestRecord
	stopCh   chan struct{}
	wg       sync.WaitGroup
	started  atomic.Bool
	closed   atomic.Bool
	dropped  atomic.Int64
	logger   *logger.Logger
}

// NewUsageTracker creates a new usage tracker. It writes synchronously until
// StartBatchWriter is called.
func NewUsageTracker(repo *repository.ProxyRepository) *UsageTracker {
	return &UsageTracker{
		repo: repo,
	}
}

// StartBatchWriter enables asynchronous batched stat writes. Idempotent.
func (t *UsageTracker) StartBatchWriter(log *logger.Logger) {
	if !t.started.CompareAndSwap(false, true) {
		return
	}
	t.logger = log
	t.recordCh = make(chan RequestRecord, usageQueueSize)
	t.stopCh = make(chan struct{})
	t.wg.Add(1)
	go t.batchWriter()
}

// Stop drains and flushes any buffered records and stops the batch writer.
func (t *UsageTracker) Stop() {
	if !t.started.Load() || !t.closed.CompareAndSwap(false, true) {
		return
	}
	close(t.stopCh)
	t.wg.Wait()
	if d := t.dropped.Load(); d > 0 && t.logger != nil {
		t.logger.Warn("usage tracker dropped records under load", "dropped", d)
	}
}

// RequestRecord represents a single proxy request
type RequestRecord struct {
	ProxyID      int
	ProxyAddress string
	RequestedURL string
	Method       string
	Success      bool
	ResponseTime int // milliseconds
	StatusCode   int
	ErrorMessage string
	Timestamp    time.Time
}

// RecordRequest records a proxy request. With the batch writer running it is a
// non-blocking enqueue (dropping on overflow); otherwise it writes synchronously.
func (t *UsageTracker) RecordRequest(ctx context.Context, record RequestRecord) error {
	if t.started.Load() && !t.closed.Load() {
		select {
		case t.recordCh <- record:
		default:
			t.dropped.Add(1)
		}
		return nil
	}
	// Synchronous fallback (e.g. before StartBatchWriter, or in tests).
	if err := t.insertProxyRequest(ctx, record); err != nil {
		return fmt.Errorf("failed to insert proxy request: %w", err)
	}
	if err := t.updateProxyStats(ctx, record); err != nil {
		return fmt.Errorf("failed to update proxy stats: %w", err)
	}
	return nil
}

// batchWriter consumes records, flushing on size or the flush interval.
func (t *UsageTracker) batchWriter() {
	defer t.wg.Done()

	ticker := time.NewTicker(usageFlushInterval)
	defer ticker.Stop()

	batch := make([]RequestRecord, 0, usageMaxBatch)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		t.flush(ctx, batch)
		cancel()
		batch = batch[:0]
	}

	for {
		select {
		case rec := <-t.recordCh:
			batch = append(batch, rec)
			if len(batch) >= usageMaxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-t.stopCh:
			// Drain anything still buffered, then final flush.
			for {
				select {
				case rec := <-t.recordCh:
					batch = append(batch, rec)
					if len(batch) >= usageMaxBatch {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// proxyAgg accumulates a flush window's deltas for one proxy, preserving the
// sequential consecutive-failure semantics of updateProxyStats.
type proxyAgg struct {
	reqDelta       int
	succDelta      int
	sumRT          int64 // sum of successful response times (ms)
	trailingFails  int   // consecutive failures at the tail of the window
	hadSuccess     bool  // any success in the window (resets prior fail count)
	lastWasSuccess bool
	lastError      string
	lastTS         time.Time
}

// aggregateRecords folds a flush window into per-proxy deltas, preserving the
// arrival order of first appearance and the sequential consecutive-failure
// semantics. Pure/deterministic so it can be unit-tested without a DB.
func aggregateRecords(batch []RequestRecord) (order []int, aggs map[int]*proxyAgg) {
	aggs = make(map[int]*proxyAgg, len(batch))
	order = make([]int, 0, len(batch))
	for i := range batch {
		r := &batch[i]
		a, ok := aggs[r.ProxyID]
		if !ok {
			a = &proxyAgg{}
			aggs[r.ProxyID] = a
			order = append(order, r.ProxyID)
		}
		a.reqDelta++
		if r.Success {
			a.succDelta++
			a.sumRT += int64(r.ResponseTime)
			a.trailingFails = 0
			a.hadSuccess = true
			a.lastWasSuccess = true
			a.lastError = ""
		} else {
			a.trailingFails++
			a.lastWasSuccess = false
			a.lastError = r.ErrorMessage
		}
		a.lastTS = r.Timestamp
	}
	return order, aggs
}

// updateStatsBatchSQL folds a flush window's per-proxy deltas into a single row
// update. It reproduces updateProxyStats/AUD-17/AUD-38 semantics:
//   - avg weighted by successful_requests (batched sum == sequential result),
//   - a success anywhere in the window resets the consecutive-failure count,
//   - only trailing failures accumulate, marking 'failed' at >= 3.
const updateStatsBatchSQL = `
	UPDATE proxies SET
		requests = requests + $2,
		successful_requests = successful_requests + $3,
		avg_response_time = CASE
			WHEN $3 > 0 THEN ((avg_response_time * successful_requests + $4) / (successful_requests + $3))::INTEGER
			ELSE avg_response_time
		END,
		failed_requests = CASE
			WHEN $5 THEN 0
			WHEN $6 THEN $7
			ELSE failed_requests + $7
		END,
		last_check = $9,
		last_error = CASE WHEN $5 THEN NULL ELSE $8 END,
		status = CASE
			WHEN $5 THEN 'active'
			WHEN $6 THEN (CASE WHEN $7 >= 3 THEN 'failed' ELSE status END)
			ELSE (CASE WHEN failed_requests + $7 >= 3 THEN 'failed' ELSE status END)
		END,
		updated_at = NOW()
	WHERE id = $1
`

// flush bulk-inserts the request rows and applies coalesced per-proxy stat updates.
func (t *UsageTracker) flush(ctx context.Context, batch []RequestRecord) {
	pool := t.repo.GetDB().Pool

	// 1. Bulk insert request rows via CopyFrom (one round-trip).
	rows := make([][]any, 0, len(batch))
	for _, r := range batch {
		var statusCode *int
		if r.StatusCode > 0 {
			sc := r.StatusCode
			statusCode = &sc
		}
		var errMsg *string
		if r.ErrorMessage != "" {
			e := r.ErrorMessage
			errMsg = &e
		}
		rows = append(rows, []any{
			r.ProxyID, r.ProxyAddress, r.Method, r.RequestedURL,
			statusCode, r.Success, r.ResponseTime, errMsg, r.Timestamp,
		})
	}
	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"proxy_requests"},
		[]string{"proxy_id", "proxy_address", "method", "url", "status_code", "success", "response_time", "error", "timestamp"},
		pgx.CopyFromRows(rows)); err != nil {
		t.logErr("failed to bulk-insert proxy requests", err)
	}

	// 2. Aggregate per proxy in arrival order.
	order, aggs := aggregateRecords(batch)

	// 3. One UPDATE per proxy, pipelined in a single batch (one round-trip).
	b := &pgx.Batch{}
	for _, id := range order {
		a := aggs[id]
		var lastErr *string
		if !a.lastWasSuccess && a.lastError != "" {
			classified := FormatLastError(fmt.Errorf("%s", a.lastError)); lastErr = &classified
		}
		b.Queue(updateStatsBatchSQL, id, a.reqDelta, a.succDelta, a.sumRT,
			a.lastWasSuccess, a.hadSuccess, a.trailingFails, lastErr, a.lastTS)
	}
	br := pool.SendBatch(ctx, b)
	for range order {
		if _, err := br.Exec(); err != nil {
			t.logErr("failed to update proxy stats", err)
		}
	}
	br.Close()
}

func (t *UsageTracker) logErr(msg string, err error) {
	if t.logger != nil {
		t.logger.Error(msg, "error", err)
	} else {
		fmt.Fprintf(os.Stderr, "usage tracker: %s: %v\n", msg, err)
	}
}

// insertProxyRequest inserts a record into the proxy_requests hypertable
func (t *UsageTracker) insertProxyRequest(ctx context.Context, record RequestRecord) error {
	query := `
		INSERT INTO proxy_requests (
			proxy_id, proxy_address, method, url, status_code, success, response_time, error, timestamp
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	var errorMsg *string
	if record.ErrorMessage != "" {
		classified := FormatLastError(fmt.Errorf("%s", record.ErrorMessage))
		errorMsg = &classified
	}

	var statusCode *int
	if record.StatusCode > 0 {
		statusCode = &record.StatusCode
	}

	_, err := t.repo.GetDB().Pool.Exec(
		ctx,
		query,
		record.ProxyID,
		record.ProxyAddress,
		record.Method,
		record.RequestedURL,
		statusCode,
		record.Success,
		record.ResponseTime,
		errorMsg,
		record.Timestamp,
	)

	return err
}

// updateProxyStats updates proxy statistics in the proxies table (synchronous path)
func (t *UsageTracker) updateProxyStats(ctx context.Context, record RequestRecord) error {
	// Use a single query to update all statistics atomically
	// Note: We calculate avg_response_time correctly by using current requests value before increment
	query := `
		UPDATE proxies
		SET
			requests = requests + 1,
			successful_requests = CASE
				WHEN $2 THEN successful_requests + 1
				ELSE successful_requests
			END,
			failed_requests = CASE
				WHEN $2 THEN 0  -- Reset consecutive failures on success
				ELSE failed_requests + 1
			END,
			-- Only fold response time into the average for successful requests,
			-- weighted by the number of successful requests, so failures (which
			-- record ResponseTime=0) don't drag the average toward 0 (AUD-38).
			avg_response_time = CASE
				WHEN $2 THEN (
					CASE
						WHEN successful_requests = 0 THEN $3
						ELSE ((avg_response_time * successful_requests) + $3) / (successful_requests + 1)
					END
				)::INTEGER
				ELSE avg_response_time
			END,
			last_check = $4,
			last_error = CASE
				WHEN $2 THEN NULL  -- Clear error on success
				ELSE $5
			END,
			status = CASE
				WHEN $2 THEN 'active'  -- Success = active
				ELSE CASE
					WHEN (failed_requests + 1) >= 3 THEN 'failed'  -- 3 consecutive failures = failed
					ELSE status
				END
			END,
			updated_at = NOW()
		WHERE id = $1
	`

	var errorMsg *string
	if record.ErrorMessage != "" {
		classified := FormatLastError(fmt.Errorf("%s", record.ErrorMessage))
		errorMsg = &classified
	}

	_, err := t.repo.GetDB().Pool.Exec(
		ctx,
		query,
		record.ProxyID,
		record.Success,
		record.ResponseTime,
		record.Timestamp,
		errorMsg,
	)

	return err
}

// UpdateProxyStatus updates only the status of a proxy
func (t *UsageTracker) UpdateProxyStatus(ctx context.Context, proxyID int, status string) error {
	query := `
		UPDATE proxies
		SET status = $1, updated_at = NOW()
		WHERE id = $2
	`

	_, err := t.repo.GetDB().Pool.Exec(ctx, query, status, proxyID)
	return err
}

// RecordHealthCheck records a health check result
func (t *UsageTracker) RecordHealthCheck(ctx context.Context, proxyID int, success bool, responseTime int, errorMsg string) error {
	now := time.Now()

	// Give health checks self-consistent consecutive-failure accounting (AUD-17):
	// a failure increments failed_requests and only flips the proxy to 'failed'
	// once the threshold is reached; a success resets the counter and reactivates.
	query := `
		UPDATE proxies
		SET
			last_check = $1,
			last_error = CASE WHEN $2 THEN NULL ELSE $3 END,
			failed_requests = CASE WHEN $2 THEN 0 ELSE failed_requests + 1 END,
			status = CASE
				WHEN $2 THEN 'active'
				WHEN (failed_requests + 1) >= 3 THEN 'failed'
				ELSE status
			END,
			updated_at = NOW()
		WHERE id = $4
	`

	var lastError *string
	if errorMsg != "" {
		lastError = &errorMsg
	}

	_, err := t.repo.GetDB().Pool.Exec(ctx, query, now, success, lastError, proxyID)
	return err
}

// GetRecentRequests retrieves recent requests for a proxy
func (t *UsageTracker) GetRecentRequests(ctx context.Context, proxyID int, limit int) ([]RequestRecord, error) {
	// The proxy_requests table stores success (bool) and error — not status /
	// error_message, which don't exist (AUD-18).
	query := `
		SELECT
			proxy_id, method, url, COALESCE(status_code, 0), success, response_time,
			COALESCE(error, '') AS error, timestamp
		FROM proxy_requests
		WHERE proxy_id = $1
		ORDER BY timestamp DESC
		LIMIT $2
	`

	rows, err := t.repo.GetDB().Pool.Query(ctx, query, proxyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]RequestRecord, 0, limit)
	for rows.Next() {
		var record RequestRecord

		err := rows.Scan(
			&record.ProxyID,
			&record.Method,
			&record.RequestedURL,
			&record.StatusCode,
			&record.Success,
			&record.ResponseTime,
			&record.ErrorMessage,
			&record.Timestamp,
		)
		if err != nil {
			return nil, err
		}

		records = append(records, record)
	}

	return records, nil
}
