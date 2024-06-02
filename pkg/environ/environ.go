package environ

import (
	"bufio"
	"flag"
	"fmt"
	"net/url"
	"os"

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
	flag.StringVar(&proxy, "p", "", "")

	file := ""
	flag.StringVar(&file, "file", "", "")
	flag.StringVar(&file, "f", "", "")

	method := ""
	flag.StringVar(&method, "method", "random", "")
	flag.StringVar(&method, "m", "random", "")

	verbose := false
	flag.BoolVar(&verbose, "verbose", false, "")
	flag.BoolVar(&verbose, "v", false, "")

	flag.Parse()

	if method != "random" && method != "sequent" {
		log.Fatal().Msg("method must be random or sequent")
	}

	if proxy == "" && file == "" {
		log.Fatal().Msg("single proxy or proxy file must be provided")
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
		Verbose:   verbose,
	}

	config.Ac.Log.Info().Msg("config initialized")
}
