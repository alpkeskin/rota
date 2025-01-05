package config

import (
	"os"
	"testing"
)

func TestNewConfigManager(t *testing.T) {
	testConfig := `
proxy_file: "proxies.txt"
file_watch: true
proxy:
  port: 8080
  authentication:
    enabled: true
    username: "test_user"
    password: "test_pass"
  rotation:
    method: "roundrobin"
    remove_unhealthy: true
    fallback: true
    fallback_max_retries: 3
    timeout: 5
    retries: 3
api:
  enabled: true
  port: 3000
healthcheck:
  output:
    method: "file"
    file: "health.txt"
  timeout: 5
  workers: 10
  url: "https://example.com"
  status: 200
  headers: ["User-Agent: Test"]
logging:
  stdout: true
  file: "proxy.log"
  level: "info"
`
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(testConfig)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "Valid config file",
			path:    tmpfile.Name(),
			wantErr: false,
		},
		{
			name:    "Non-existent file",
			path:    "nonexistent.yaml",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm, err := NewConfigManager(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewConfigManager() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if cm.Config.ProxyFile != "proxies.txt" {
					t.Errorf("ProxyFile = %v, expected = %v", cm.Config.ProxyFile, "proxies.txt")
				}
				if cm.Config.Proxy.Port != 8080 {
					t.Errorf("Proxy.Port = %v, expected = %v", cm.Config.Proxy.Port, 8080)
				}
				if cm.Config.Api.Port != 3000 {
					t.Errorf("API.Port = %v, expected = %v", cm.Config.Api.Port, 3000)
				}
				if cm.Config.Healthcheck.Workers != 10 {
					t.Errorf("Healthcheck.Workers = %v, expected = %v", cm.Config.Healthcheck.Workers, 10)
				}
				if cm.Config.Logging.Level != "info" {
					t.Errorf("Logging.Level = %v, expected = %v", cm.Config.Logging.Level, "info")
				}
			}
		})
	}
}
