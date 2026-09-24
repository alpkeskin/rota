// Package metrics defines Rota's Prometheus metrics and the /metrics handler.
//
// All metrics live on a private registry (not the global default) so tests and
// embedders don't collide with other libraries' registrations.
package metrics

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/alpkeskin/rota/core/internal/version"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "rota"

// Registry holds every Rota metric plus the Go runtime and process collectors.
var Registry = prometheus.NewRegistry()

var (
	// ProxyRequests counts client requests handled by the proxy port.
	// kind: http | connect. outcome: success | upstream_error | internal_error |
	// rejected_auth | rejected_rate_limit. A CONNECT counts as success once
	// the tunnel is established; see TunnelsClosed for how it ended.
	ProxyRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "proxy",
		Name:      "requests_total",
		Help:      "Client requests handled by the proxy, by kind and outcome.",
	}, []string{"kind", "outcome"})

	// ProxyRequestDuration measures time to an upstream response (HTTP) or to
	// an established tunnel (CONNECT); tunnel lifetime is not included.
	ProxyRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "proxy",
		Name:      "request_duration_seconds",
		Help:      "Time to upstream response (http) or tunnel establishment (connect).",
		Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"kind", "outcome"})

	// ActiveTunnels is the number of CONNECT tunnels currently open.
	ActiveTunnels = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "proxy",
		Name:      "active_tunnels",
		Help:      "CONNECT tunnels currently open.",
	})

	// TunnelsClosed counts CONNECT tunnels by how they ended: clean (both
	// directions reached EOF) or error (the copy failed — this includes
	// resets during ordinary teardown, so read it as a ratio, not an alarm).
	TunnelsClosed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "proxy",
		Name:      "tunnels_closed_total",
		Help:      "CONNECT tunnels closed, by result (clean or error).",
	}, []string{"result"})

	// APIRequests counts REST API requests by route pattern (not raw path, to
	// keep cardinality bounded), method and status code.
	APIRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "api",
		Name:      "requests_total",
		Help:      "REST API requests by route, method and status.",
	}, []string{"route", "method", "status"})

	// APIRequestDuration measures REST API latency by route pattern.
	APIRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "api",
		Name:      "request_duration_seconds",
		Help:      "REST API request latency by route and method.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"route", "method"})

	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "build_info",
		Help:      "Build information; the value is always 1.",
	}, []string{"version"})
)

func init() {
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		ProxyRequests,
		ProxyRequestDuration,
		ActiveTunnels,
		TunnelsClosed,
		APIRequests,
		APIRequestDuration,
		buildInfo,
	)
	buildInfo.WithLabelValues(version.Version).Set(1)
}

// ObserveProxyRequest records one proxy request outcome and its duration.
func ObserveProxyRequest(kind, outcome string, d time.Duration) {
	ProxyRequests.WithLabelValues(kind, outcome).Inc()
	ProxyRequestDuration.WithLabelValues(kind, outcome).Observe(d.Seconds())
}

// RegisterCounterFunc exposes an existing monotonically increasing counter
// (e.g. one kept by another package) without that package importing Prometheus.
func RegisterCounterFunc(name, help string, fn func() float64) {
	Registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      name,
		Help:      help,
	}, fn))
}

// StatusCounter returns proxy counts by status (e.g. from the database).
type StatusCounter func(ctx context.Context) (map[string]int, error)

// upstreamCollector reports the upstream proxy inventory by status at scrape
// time, so the gauge is always consistent with the database.
type upstreamCollector struct {
	count StatusCounter
	desc  *prometheus.Desc
	up    *prometheus.Desc
}

// RegisterUpstreamInventory adds a collector that queries proxy counts by
// status on every scrape. A failing query reports rota_upstream_inventory_up 0
// instead of failing the whole scrape.
func RegisterUpstreamInventory(count StatusCounter) {
	Registry.MustRegister(&upstreamCollector{
		count: count,
		desc: prometheus.NewDesc(namespace+"_upstream_proxies",
			"Upstream proxies in the inventory, by status.", []string{"status"}, nil),
		up: prometheus.NewDesc(namespace+"_upstream_inventory_up",
			"1 if the upstream inventory could be read from the database at scrape time.", nil, nil),
	})
}

func (c *upstreamCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
	ch <- c.up
}

func (c *upstreamCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	counts, err := c.count(ctx)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)
	for status, n := range counts {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(n), status)
	}
}

// Handler serves the registry in the Prometheus exposition format. When token
// is non-empty, requests must carry "Authorization: Bearer <token>".
func Handler(token string) http.Handler {
	h := promhttp.HandlerFor(Registry, promhttp.HandlerOpts{})
	if token == "" {
		return h
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		got, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="rota-metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}
