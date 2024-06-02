package scheme

import (
	"net/http"
	"net/url"
)

type Proxy struct {
	Scheme    string
	Host      string
	Url       *url.URL
	Transport *http.Transport
}
