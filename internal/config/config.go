package config

import (
	"github.com/alpkeskin/rota/pkg/request"
	"github.com/alpkeskin/rota/pkg/scheme"
	"github.com/projectdiscovery/gologger"
)

var Ac *AppConfig

type AppConfig struct {
	Port      string
	Log       *gologger.Logger
	Req       *request.Request
	ProxyList []scheme.Proxy
	Method    string
	Auth      string
	Check     bool
	Output    string
	Timeout   int
}
