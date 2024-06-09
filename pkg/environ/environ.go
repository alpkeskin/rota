package environ

import (
	"bufio"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/alpkeskin/rota/pkg/request"
	"github.com/alpkeskin/rota/pkg/scheme"

	"github.com/projectdiscovery/gologger"
	"github.com/projectdiscovery/gologger/levels"
)

func Init() {
	log := gologger.DefaultLogger
	log.SetMaxLevel(levels.LevelDebug)

	port := ""
	flag.StringVar(&port, "port", "8080", "")

	proxy := ""
	flag.StringVar(&proxy, "proxy", "", "")

	file := ""
	flag.StringVar(&file, "file", "", "")

	method := ""
	flag.StringVar(&method, "method", "random", "")

	auth := ""
	flag.StringVar(&auth, "auth", "", "")

	flag.Parse()

	if method != "random" && method != "sequent" {
		log.Fatal().Msg("method must be random or sequent")
	}

	if proxy == "" && file == "" {
		log.Fatal().Msg("single proxy or proxy file must be provided")
	}

	// parse auth
	if auth != "" {
		authSplit := strings.Split(auth, ":")
		if len(authSplit) != 2 {
			log.Fatal().Msg("auth must be in the format user:pass")
		}
	}

	proxyList := []scheme.Proxy{}
	req := request.New(method)
	if proxy != "" {
		url, err := url.Parse(proxy)
		if err != nil {
			log.Fatal().Msg(err.Error())
		}

		p := scheme.Proxy{
			Scheme: url.Scheme,
			Host:   proxy,
		}

		tr, err := req.Transport(p)
		if err != nil {
			log.Fatal().Msg(err.Error())
		}

		p.Transport = tr
		proxyList = append(proxyList, p)
	} else {
		file, err := os.Open(file)
		if err != nil {
			log.Fatal().Msg(err.Error())
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			proxy := scanner.Text()
			url, err := url.Parse(proxy)
			if err != nil {
				log.Fatal().Msg(err.Error())
			}

			p := scheme.Proxy{
				Scheme: url.Scheme,
				Host:   proxy,
				Url:    url,
			}

			tr, err := req.Transport(p)
			if err != nil {
				msg := fmt.Sprintf("%s . Passing proxy...", err.Error())
				log.Error().Msg(msg)
				continue
			}

			p.Transport = tr
			proxyList = append(proxyList, p)
		}

		if err := scanner.Err(); err != nil {
			log.Fatal().Msg(err.Error())
		}
	}

	req.ProxyList = proxyList

	config.Ac = &config.AppConfig{
		Port:      port,
		Log:       log,
		Req:       req,
		ProxyList: proxyList,
		Method:    method,
		Auth:      auth,
	}

	config.Ac.Log.Info().Msg("config initialized")
}
