// Package version exposes the build version. It is set at link time:
//
//	go build -ldflags "-X github.com/alpkeskin/rota/core/internal/version.Version=v2.3.0"
//
// and reported by /health and /status. Unset builds report "dev".
package version

// Version is the release tag the binary was built from, or "dev".
var Version = "dev"
