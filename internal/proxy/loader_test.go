package proxy

import (
	"os"
	"testing"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestProxyLoader_CreateProxy(t *testing.T) {
	tests := []struct {
		name     string
		proxyURL string
		wantErr  bool
	}{
		{
			name:     "valid http proxy",
			proxyURL: "http://127.0.0.1:8080",
			wantErr:  false,
		},
		{
			name:     "valid https proxy",
			proxyURL: "https://127.0.0.1:8080",
			wantErr:  false,
		},
		{
			name:     "valid socks5 proxy",
			proxyURL: "socks5://127.0.0.1:1080",
			wantErr:  false,
		},
		{
			name:     "invalid proxy scheme",
			proxyURL: "ftp://127.0.0.1:21",
			wantErr:  true,
		},
		{
			name:     "invalid proxy URL",
			proxyURL: "not-a-url",
			wantErr:  true,
		},
	}

	cfg := &config.Config{}
	pl := NewProxyLoader(cfg, NewProxyServer(cfg))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy, err := pl.CreateProxy(tt.proxyURL)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, proxy)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, proxy)
				assert.NotNil(t, proxy.Transport)
			}
		})
	}
}

func TestProxyLoader_Load(t *testing.T) {
	tempFile, err := os.CreateTemp("", "proxies-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempFile.Name())

	proxyList := "http://127.0.0.1:8080\nhttps://127.0.0.1:8081\nsocks5://127.0.0.1:1080"
	if err := os.WriteFile(tempFile.Name(), []byte(proxyList), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		ProxyFile: tempFile.Name(),
	}
	ps := NewProxyServer(cfg)
	pl := NewProxyLoader(cfg, ps)

	err = pl.Load()
	assert.NoError(t, err)
	assert.Len(t, ps.Proxies, 3)

	err = pl.Reload()
	assert.NoError(t, err)
	assert.Len(t, ps.Proxies, 3)
}

func TestProxyLoader_LoadError(t *testing.T) {
	cfg := &config.Config{
		ProxyFile: "non-existent-file.txt",
	}
	pl := NewProxyLoader(cfg, NewProxyServer(cfg))

	err := pl.Load()
	assert.Error(t, err)
}
