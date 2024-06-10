package vars

import (
	"fmt"
	"os"

	"github.com/alpkeskin/rota/pkg/request"
	"github.com/alpkeskin/rota/pkg/scheme"
	"github.com/projectdiscovery/gologger"
)

var Version = "1.0.0"
var Author = "github.com/alpkeskin"
var Logo = `                                  
            *            
          *****          
     ***************     
     ***************     
    ***  *******  ***    
  *********   *********  
    ***  *******  ***    
     ***************     
     ***************     
          *****          
            *                          
`
var Banner = fmt.Sprintf("%s\nVersion: %s\nAuthor: %s\n", Logo, Version, Author)

var Ac *AppConfig

type AppConfig struct {
	Port       string
	Log        *gologger.Logger
	Req        *request.Request
	ProxyList  []scheme.Proxy
	Method     string
	Auth       string
	Check      bool
	OutputFile *os.File
	Timeout    int
}
