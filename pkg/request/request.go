package request

import (
	"crypto/tls"
	"fmt"
	"math/rand"
	"net/http"

	"github.com/alpkeskin/rota/internal/vars"
	"github.com/alpkeskin/rota/pkg/scheme"
	"h12.io/socks"
)

type Request struct {
	Client     *http.Client
	ProxyList  []scheme.Proxy
	HopHeaders []string
	Method     string
	Cursor     int
}

func New(method string, proxyList []scheme.Proxy) *Request {
	return &Request{
		Client:    &http.Client{},
		ProxyList: proxyList,
		HopHeaders: []string{
			"Connection",
			"Keep-Alive",
			"Proxy-Authenticate",
			"Proxy-Authorization",
			"Proxy-Connection",
			"Te", // canonicalized version of "TE"
			"Trailers",
			"Transfer-Encoding",
			"Upgrade",
		},
		Method: method,
		Cursor: -1,
	}
}

// Transport to auto-switch transport between HTTP/S or SOCKS v4(A) & v5 proxies.
// Depending on the protocol scheme, returning value of http.Transport with Dialer or Proxy.
func (r *Request) Transport(proxy scheme.Proxy) (tr *http.Transport, err error) {
	switch proxy.Scheme {
	case "socks4", "socks4a", "socks5":
		tr = &http.Transport{
			Dial: socks.Dial(proxy.Host),
		}
	case "http", "https":
		tr = &http.Transport{
			Proxy: http.ProxyURL(proxy.Url),
		}
	default:
		return nil, fmt.Errorf("unsupported proxy protocol scheme: %s", proxy.Scheme)
	}

	tr.DisableKeepAlives = true
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	return tr, nil
}

// Modify prepares the request to be sent via a proxy.
func (r *Request) Modify(req *http.Request) (*http.Client, *http.Request, string) {
	proxy := r.ChooseProxy()
	r.Client = &http.Client{
		Transport: proxy.Transport,
		Timeout:   vars.Ac.Timeout,
	}

	req.RequestURI = ""

	r.removeHopHeaders(req)

	return r.Client, req, proxy.Host
}

// ChooseProxy selects a proxy based on the configured method.
func (r *Request) ChooseProxy() scheme.Proxy {
	switch r.Method {
	case "random":
		return r.RandomProxy()
	case "sequent":
		return r.SequentProxy()
	default:
		return scheme.Proxy{}
	}
}

// RandomProxy selects a proxy randomly.
func (r *Request) RandomProxy() scheme.Proxy {
	r.Cursor = rand.Intn(len(r.ProxyList))
	return r.ProxyList[r.Cursor]
}

// SequentProxy selects the next proxy in sequence.
func (r *Request) SequentProxy() scheme.Proxy {
	r.Cursor++
	if r.Cursor >= len(r.ProxyList) {
		r.Cursor = 0
	}

	return r.ProxyList[r.Cursor]
}

// removeHopHeaders removes hop-by-hop headers from the request.
func (r *Request) removeHopHeaders(req *http.Request) {
	for _, h := range r.HopHeaders {
		req.Header.Del(h)
	}
}
