package vars

import (
	"fmt"
	"os"
	"time"

	"github.com/alpkeskin/rota/pkg/scheme"
	"github.com/projectdiscovery/gologger"
)

var Version = "1.0.0"
var Author = "github.com/alpkeskin"

var Logo = `
            _        
           | |       
  _ __ ___ | |_ __ _ 
 | '__/ _ \| __/ _' |
 | | | (_) | || (_| |
 |_|  \___/ \__\__,_|
`
var Banner = fmt.Sprintf("%s\nv%s\n%s\n", Logo, Version, Author)

var Ac *AppConfig

type AppConfig struct {
	Port       string
	Method     string
	Auth       string
	Retries    int
	Check      bool
	Verbose    bool
	Timeout    time.Duration
	Log        *gologger.Logger
	ProxyList  []scheme.Proxy
	OutputFile *os.File
}
