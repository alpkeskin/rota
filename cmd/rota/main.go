package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/alpkeskin/rota/internal/api"
	"github.com/alpkeskin/rota/internal/config"
	"github.com/alpkeskin/rota/internal/logging"
	"github.com/alpkeskin/rota/internal/proxy"
	"github.com/alpkeskin/rota/pkg/watcher"
)

const (
	msgConfigPathRequired     = "config file path is required"
	msgFailedToLoadConfig     = "failed to load config"
	msgConfigLoadedSuccess    = "config loaded successfully"
	msgFailedToCreateLogger   = "failed to create logger"
	msgFailedToCreateWatcher  = "failed to create watcher"
	msgFailedToWatchProxyFile = "failed to watch proxy file"
	msgFailedToLoadProxies    = "failed to load proxies"
	msgWatchingProxyFile      = "watching proxy file"
	msgMissingProxyFile       = "missing proxy file"
	msgFailedToListen         = "failed to listen"
	msgReceivedSignal         = "received signal, shutting down..."
)

func main() {
	cfgManager, err := setupConfig()
	if err != nil {
		panic(err)
	}

	logger, err := logging.NewLogger(cfgManager.Config)
	if err != nil {
		panic(err)
	}
	logger.Setup()

	cfg := cfgManager.Config

	proxyServer := proxy.NewProxyServer(cfg)
	proxyLoader := proxy.NewProxyLoader(cfg, proxyServer)
	err = proxyLoader.Load()
	if err != nil {
		slog.Error(msgFailedToLoadProxies, "error", err)
		os.Exit(1)
	}

	if cfgManager.Check {
		proxyChecker := proxy.NewProxyChecker(cfg, proxyServer)
		proxyChecker.Check()
		return
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	go runFileWatcher(cfg, proxyLoader, done)
	go runApi(cfg)
	go proxyServer.Listen()

	<-done
	slog.Info(msgReceivedSignal)
}

func setupConfig() (*config.ConfigManager, error) {
	configPath := flag.String("config", "config.yml", "config file path")
	check := flag.Bool("check", false, "check proxies")
	flag.Parse()

	configManager, err := config.NewConfigManager(*configPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", msgFailedToLoadConfig, err)
	}

	configManager.Check = *check
	return configManager, nil
}

func runFileWatcher(cfg *config.Config, proxyLoader *proxy.ProxyLoader, done chan os.Signal) {
	if !cfg.FileWatch {
		return
	}

	fileWatcher, err := watcher.NewFileWatcher(proxyLoader)
	if err != nil {
		slog.Error(msgFailedToCreateWatcher, "error", err)
		done <- syscall.SIGTERM
		return
	}

	if err := fileWatcher.Watch(cfg.ProxyFile); err != nil {
		slog.Error(msgFailedToWatchProxyFile, "error", err)
		done <- syscall.SIGTERM
	}
}

func runApi(cfg *config.Config) {
	if !cfg.Api.Enabled {
		return
	}

	api := api.NewApi(cfg)
	api.Serve()
}
