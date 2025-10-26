package models

// DashboardStats represents dashboard statistics
type DashboardStats struct {
	ActiveProxies      int     `json:"active_proxies"`
	TotalProxies       int     `json:"total_proxies"`
	TotalRequests      int64   `json:"total_requests"`
	AvgSuccessRate     float64 `json:"avg_success_rate"`
	AvgResponseTime    int     `json:"avg_response_time"`
	RequestGrowth      float64 `json:"request_growth"`
	SuccessRateGrowth  float64 `json:"success_rate_growth"`
	ResponseTimeDelta  int     `json:"response_time_delta"`
}

// ChartDataPoint represents a single data point in a chart
type ChartDataPoint struct {
	Time  string `json:"time"`
	Value int    `json:"value"`
}

// SuccessRateDataPoint represents a data point for success rate chart
type SuccessRateDataPoint struct {
	Time    string `json:"time"`
	Success int    `json:"success"`
	Failure int    `json:"failure"`
}

// ResponseTimeChartData represents response time chart data
type ResponseTimeChartData struct {
	Data []ChartDataPoint `json:"data"`
}

// SuccessRateChartData represents success rate chart data
type SuccessRateChartData struct {
	Data []SuccessRateDataPoint `json:"data"`
}

// ProxyStatusSimple represents simplified proxy status for dashboard
type ProxyStatusSimple struct {
	ID          string  `json:"id"`
	Address     string  `json:"address"`
	Status      string  `json:"status"`
	Requests    int64   `json:"requests"`
	SuccessRate float64 `json:"success_rate"`
}

// ProxyStatusList represents a list of simplified proxy statuses
type ProxyStatusList struct {
	Proxies []ProxyStatusSimple `json:"proxies"`
}
