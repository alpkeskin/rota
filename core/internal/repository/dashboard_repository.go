package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
)

// DashboardRepository handles dashboard statistics operations
type DashboardRepository struct {
	db *database.DB
}

// NewDashboardRepository creates a new DashboardRepository
func NewDashboardRepository(db *database.DB) *DashboardRepository {
	return &DashboardRepository{db: db}
}

// GetStats retrieves overall dashboard statistics
func (r *DashboardRepository) GetStats(ctx context.Context) (*models.DashboardStats, error) {
	query := `
		WITH current_stats AS (
			SELECT
				COUNT(*) FILTER (WHERE status = 'active') as active_proxies,
				COUNT(*) as total_proxies,
				COALESCE(SUM(requests), 0) as total_requests,
				COALESCE(AVG(CASE WHEN requests > 0 THEN (successful_requests::float / requests * 100) END), 0) as avg_success_rate,
				COALESCE(AVG(avg_response_time), 0)::int as avg_response_time
			FROM proxies
		),
		yesterday_stats AS (
			SELECT
				COUNT(*) as requests_yesterday,
				COALESCE(AVG(CASE WHEN success THEN 1.0 ELSE 0.0 END) * 100, 0) as success_rate_yesterday,
				COALESCE(AVG(response_time), 0)::int as response_time_yesterday
			FROM proxy_requests
			WHERE timestamp >= NOW() - INTERVAL '2 days'
			  AND timestamp < NOW() - INTERVAL '1 day'
		),
		today_stats AS (
			SELECT
				COUNT(*) as requests_today,
				COALESCE(AVG(CASE WHEN success THEN 1.0 ELSE 0.0 END) * 100, 0) as success_rate_today,
				COALESCE(AVG(response_time), 0)::int as response_time_today
			FROM proxy_requests
			WHERE timestamp >= NOW() - INTERVAL '1 day'
		)
		SELECT
			c.active_proxies,
			c.total_proxies,
			c.total_requests,
			c.avg_success_rate,
			c.avg_response_time,
			CASE WHEN y.requests_yesterday > 0
				THEN ((t.requests_today - y.requests_yesterday)::float / y.requests_yesterday * 100)
				ELSE 0
			END as request_growth,
			(t.success_rate_today - y.success_rate_yesterday) as success_rate_growth,
			(t.response_time_today - y.response_time_yesterday) as response_time_delta
		FROM current_stats c, yesterday_stats y, today_stats t
	`

	var stats models.DashboardStats
	err := r.db.Pool.QueryRow(ctx, query).Scan(
		&stats.ActiveProxies,
		&stats.TotalProxies,
		&stats.TotalRequests,
		&stats.AvgSuccessRate,
		&stats.AvgResponseTime,
		&stats.RequestGrowth,
		&stats.SuccessRateGrowth,
		&stats.ResponseTimeDelta,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get dashboard stats: %w", err)
	}

	return &stats, nil
}

// GetResponseTimeChart retrieves response time chart data
func (r *DashboardRepository) GetResponseTimeChart(ctx context.Context, interval string) ([]models.ChartDataPoint, error) {
	// Determine time bucket based on interval
	bucketSize := "4 hours"
	lookback := "24 hours"

	switch interval {
	case "1h":
		bucketSize = "1 hour"
		lookback = "24 hours"
	case "4h":
		bucketSize = "4 hours"
		lookback = "24 hours"
	case "1d":
		bucketSize = "1 day"
		lookback = "7 days"
	}

	query := fmt.Sprintf(`
		SELECT
			time_bucket('%s', timestamp) as bucket,
			COALESCE(AVG(response_time), 0)::int as avg_response_time
		FROM proxy_requests
		WHERE timestamp >= NOW() - INTERVAL '%s'
		  AND success = true
		GROUP BY bucket
		ORDER BY bucket
	`, bucketSize, lookback)

	rows, err := r.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get response time chart: %w", err)
	}
	defer rows.Close()

	data := []models.ChartDataPoint{}
	for rows.Next() {
		var bucket time.Time
		var value int

		if err := rows.Scan(&bucket, &value); err != nil {
			return nil, fmt.Errorf("failed to scan chart data: %w", err)
		}

		data = append(data, models.ChartDataPoint{
			Time:  bucket.Format("15:04"),
			Value: value,
		})
	}

	return data, nil
}

// GetSuccessRateChart retrieves success rate chart data
func (r *DashboardRepository) GetSuccessRateChart(ctx context.Context, interval string) ([]models.SuccessRateDataPoint, error) {
	// Determine time bucket based on interval
	bucketSize := "4 hours"
	lookback := "24 hours"

	switch interval {
	case "1h":
		bucketSize = "1 hour"
		lookback = "24 hours"
	case "4h":
		bucketSize = "4 hours"
		lookback = "24 hours"
	case "1d":
		bucketSize = "1 day"
		lookback = "7 days"
	}

	query := fmt.Sprintf(`
		SELECT
			time_bucket('%s', timestamp) as bucket,
			(COUNT(*) FILTER (WHERE success = true) * 100 / GREATEST(COUNT(*), 1))::int as success_rate,
			(COUNT(*) FILTER (WHERE success = false) * 100 / GREATEST(COUNT(*), 1))::int as failure_rate
		FROM proxy_requests
		WHERE timestamp >= NOW() - INTERVAL '%s'
		GROUP BY bucket
		ORDER BY bucket
	`, bucketSize, lookback)

	rows, err := r.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get success rate chart: %w", err)
	}
	defer rows.Close()

	data := []models.SuccessRateDataPoint{}
	for rows.Next() {
		var bucket time.Time
		var success, failure int

		if err := rows.Scan(&bucket, &success, &failure); err != nil {
			return nil, fmt.Errorf("failed to scan chart data: %w", err)
		}

		data = append(data, models.SuccessRateDataPoint{
			Time:    bucket.Format("15:04"),
			Success: success,
			Failure: failure,
		})
	}

	return data, nil
}
