package proxy

import (
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Proxy struct {
	sync.RWMutex
	Scheme              string
	Host                string
	Url                 *url.URL
	Transport           *http.Transport
	LatestUsageStatus   string
	LatestUsageAt       string
	LatestUsageDuration string
	AvgUsageDuration    string
	UsageCount          int
}

type ProxyHistory struct {
	Scheme     string `json:"scheme"`
	Host       string `json:"host"`
	Status     string `json:"status"`
	Duration   string `json:"duration"`
	RequestUrl string `json:"request_url"`
	UsedAt     string `json:"used_at"`
}

type requestInfo struct {
	id      string
	url     string
	request *http.Request
	startAt time.Time
}

const (
	// HTTP Status Codes
	StatusProxyAuthRequired = 407
	StatusBadGateway        = 502

	msgFailedToListen            = "failed to listen"
	msgProxyServerStarted        = "rota proxy server started"
	msgRequestReceived           = "request received"
	msgAuthError                 = "authentication error"
	msgReqRotationSuccess        = "request rotation success"
	msgReqRotationError          = "request rotation error"
	msgRemovingUnhealthyProxy    = "removing unhealthy proxy"
	msgNoProxyFound              = "no proxy found"
	msgProxyAttemptsExhausted    = "proxy attempts exhausted"
	msgAllProxyAttemptsFailed    = "all proxy attempts failed"
	msgUnauthorized              = "rota proxy: unauthorized. request id: %s"
	msgBadGateway                = "rota proxy: bad gateway. request id: %s"
	msgRateLimitExceeded         = "rota proxy: rate limit exceeded. remote address: %s"
	msgFailedToCreateProxy       = "failed to create proxy"
	msgProxiesLoadedSuccessfully = "proxies loaded successfully"
	msgLoadingProxies            = "loading proxies"
	msgFailedToLoadProxies       = "failed to load proxies"
	msgUnsupportedProxyScheme    = "unsupported proxy scheme"
	msgCheckingProxies           = "checking proxies"
	msgFailedToCreateOutputFile  = "failed to create output file"
	msgFailedToCreateRequest     = "failed to create request"
	msgDeadProxy                 = "dead proxy"
	msgAliveProxy                = "alive proxy"
	msgFailedToWriteOutputFile   = "failed to write output file"
)

var hopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
	"Te", // canonicalized version of "TE"
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}
