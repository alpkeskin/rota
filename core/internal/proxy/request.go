package proxy

import (
	"context"

	"github.com/alpkeskin/rota/core/internal/models"
)

// ProxyRequest is what authentication resolved for one client request:
// the proxy user, their pool chain and the routing options from the
// username. It is nil for requests on an open proxy or authenticated with
// the legacy single-user credentials (those use the global rotation).
type ProxyRequest struct {
	User  *models.ProxyUser
	Chain *PoolChain
	Opts  UsernameOptions
}

type proxyRequestKey struct{}

// WithProxyRequest returns a context carrying preq.
func WithProxyRequest(ctx context.Context, preq *ProxyRequest) context.Context {
	return context.WithValue(ctx, proxyRequestKey{}, preq)
}

// ProxyRequestFrom returns the request's resolved user routing, or nil.
func ProxyRequestFrom(ctx context.Context) *ProxyRequest {
	preq, _ := ctx.Value(proxyRequestKey{}).(*ProxyRequest)
	return preq
}
