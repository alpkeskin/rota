package environ

import (
	"bufio"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/alpkeskin/rota/internal/vars"
	"github.com/alpkeskin/rota/pkg/request"
	"github.com/alpkeskin/rota/pkg/scheme"

	"github.com/projectdiscovery/gologger"
	"github.com/projectdiscovery/gologger/levels"
)

type config struct {
	port       string
	proxy      string
	file       string
	method     string
	auth       string
	outputPath string
	timeout    int
	retries    int
	check      bool
	verbose    bool
}

func Init() {
	// Print banner
	fmt.Println(vars.Banner)

	// Initialize logger
	log := gologger.DefaultLogger
	log.SetMaxLevel(levels.LevelDebug)

	// Parse command-line flags
	config := parseFlags()

	// Validate method
	if err := validateMethod(config.method); err != nil {
		log.Fatal().Msg(err.Error())
	}

	// Validate proxy settings
	if err := validateProxy(config.proxy, config.file); err != nil {
		log.Fatal().Msg(err.Error())
	}

	// Validate auth format
	if err := validateAuth(config.auth); err != nil {
		log.Fatal().Msg(err.Error())
	}

	// Validate retries
	if err := validateRetries(config.retries); err != nil {
		log.Fatal().Msg(err.Error())
	}

	// Open output file if specified
	outputFile, err := createOutputFile(config.outputPath)
	if err != nil {
		log.Fatal().Msg(err.Error())
	}

	// Initialize proxy list
	proxyList, err := initializeProxyList(config.proxy, config.file, config.method, []scheme.Proxy{})
	if err != nil {
		log.Fatal().Msg(err.Error())
	}

	// Set timeout
	timeout := time.Duration(config.timeout) * time.Second

	// Create AppConfig and assign it to vars.Ac
	vars.Ac = &vars.AppConfig{
		Port:       config.port,
		Log:        log,
		ProxyList:  proxyList,
		Method:     config.method,
		Auth:       config.auth,
		Check:      config.check,
		Verbose:    config.verbose,
		OutputFile: outputFile,
		Timeout:    timeout,
		Retries:    config.retries,
	}

	log.Info().Msg("setup completed")
}

// parseFlags parses and returns the command-line flags.
func parseFlags() *config {
	cfg := &config{}

	flag.StringVar(&cfg.port, "port", "8080", "Port to use")
	flag.StringVar(&cfg.proxy, "proxy", "", "Proxy URL")
	flag.StringVar(&cfg.file, "file", "", "File containing proxy URLs")
	flag.StringVar(&cfg.method, "method", "random", "Method to use (random or sequent)")
	flag.StringVar(&cfg.auth, "auth", "", "Authentication credentials in the format user:pass")
	flag.StringVar(&cfg.outputPath, "output", "", "Output file path")
	flag.IntVar(&cfg.timeout, "timeout", 5, "Request timeout in seconds")
	flag.IntVar(&cfg.retries, "retries", 3, "Number of retries")
	flag.BoolVar(&cfg.check, "check", false, "Enable check mode")
	flag.BoolVar(&cfg.verbose, "verbose", false, "Enable verbose mode")

	// Print usage message when -h or --help flag is provided
	flag.Usage = func() {
		flag.PrintDefaults()
	}

	flag.Parse()
	return cfg
}

// validateMethod checks if the provided method is valid.
func validateMethod(method string) error {
	if method != "random" && method != "sequent" {
		return fmt.Errorf("method must be random or sequent")
	}
	return nil
}

// validateProxy checks if the proxy settings are valid.
func validateProxy(proxy, file string) error {
	if proxy == "" && file == "" {
		return fmt.Errorf("single proxy or proxy file must be provided")
	}
	return nil
}

// validateAuth checks if the auth format is valid.
func validateAuth(auth string) error {
	if auth != "" {
		authSplit := strings.Split(auth, ":")
		if len(authSplit) != 2 {
			return fmt.Errorf("auth must be in the format user:pass")
		}
	}
	return nil
}

func validateRetries(retries int) error {
	if retries < 1 {
		return fmt.Errorf("retries must be greater than 0")
	}
	return nil
}

// createOutputFile creates and returns the output file if specified.
func createOutputFile(outputPath string) (*os.File, error) {
	if outputPath == "" {
		return nil, nil
	}
	return os.Create(outputPath)
}

// initializeProxyList initializes and returns the proxy list.
func initializeProxyList(proxy, file, method string, proxyList []scheme.Proxy) ([]scheme.Proxy, error) {
	req := request.New(method, proxyList)

	if proxy != "" {
		p, err := createProxy(proxy, req)
		if err != nil {
			return nil, err
		}
		proxyList = append(proxyList, p)
	} else {
		fileProxies, err := loadProxiesFromFile(file, req)
		if err != nil {
			return nil, err
		}
		proxyList = append(proxyList, fileProxies...)
	}

	return proxyList, nil
}

// createProxy creates a Proxy object from a URL string.
func createProxy(proxyURL string, req *request.Request) (scheme.Proxy, error) {
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return scheme.Proxy{}, err
	}

	p := scheme.Proxy{
		Scheme: parsedURL.Scheme,
		Host:   proxyURL,
		Url:    parsedURL,
	}

	tr, err := req.Transport(p)
	if err != nil {
		return scheme.Proxy{}, err
	}
	p.Transport = tr

	return p, nil
}

// loadProxiesFromFile loads proxies from a file and returns a list of Proxy objects.
func loadProxiesFromFile(filePath string, req *request.Request) ([]scheme.Proxy, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var proxyList []scheme.Proxy
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		proxyURL := scanner.Text()
		p, err := createProxy(proxyURL, req)
		if err != nil {
			fmt.Printf("%s. Skipping proxy...\n", err.Error())
			continue
		}
		proxyList = append(proxyList, p)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return proxyList, nil
}
