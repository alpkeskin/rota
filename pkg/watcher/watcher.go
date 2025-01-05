package watcher

import (
	"fmt"
	"log/slog"

	"github.com/fsnotify/fsnotify"
)

const (
	msgProxyReloadedSuccessfully = "proxies reloaded successfully"
	msgFailedToReloadProxies     = "failed to reload proxies"
	msgProxyFileChanged          = "proxy file changed"
	msgFailedToWatchProxyFile    = "failed to watch proxy file"
	msgFailedToAddFileToWatcher  = "failed to add file to watcher"
	msgFailedToCreateWatcher     = "failed to create watcher"
	msgFailedToCloseWatcher      = "failed to close watcher"
	msgWatchingProxyFile         = "watching proxy file"
)

type ProxyLoader interface {
	Reload() error
}

type FileWatcher struct {
	proxyLoader ProxyLoader
	watcher     *fsnotify.Watcher
}

func NewFileWatcher(proxyLoader ProxyLoader) (*FileWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", msgFailedToCreateWatcher, err)
	}

	return &FileWatcher{
		proxyLoader: proxyLoader,
		watcher:     watcher,
	}, nil
}

func (fw *FileWatcher) Watch(filePath string) error {
	if err := fw.watcher.Add(filePath); err != nil {
		return fmt.Errorf("%s: %w", msgFailedToAddFileToWatcher, err)
	}

	slog.Info(msgWatchingProxyFile)

	go func() {
		for {
			select {
			case event, ok := <-fw.watcher.Events:
				if !ok {
					return
				}

				if event.Has(fsnotify.Write) {
					slog.Info(msgProxyFileChanged, "file", event.Name)
					if err := fw.proxyLoader.Reload(); err != nil {
						slog.Error(msgFailedToReloadProxies, "error", err)
					}

					slog.Info(msgProxyReloadedSuccessfully)
				}
			case err, ok := <-fw.watcher.Errors:
				if !ok {
					return
				}
				slog.Error(msgFailedToWatchProxyFile, "error", err)
			}
		}
	}()

	return nil
}

func (fw *FileWatcher) Close() error {
	if err := fw.watcher.Close(); err != nil {
		return fmt.Errorf("%s: %w", msgFailedToCloseWatcher, err)
	}
	return nil
}
