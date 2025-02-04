<div align="center" style="margin-bottom: 20px;">
  <img src="static/rota.png" alt="rota" width="150px">
  <h1 align="center">
  Rota - Open Source Proxy Rotator
  </h1>
</div>

<p align="center">
<a href="https://opensource.org/licenses/Apache-2.0"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg"></a>
<a href="https://golang.org"><img src="https://img.shields.io/badge/made%20with-Go-brightgreen"></a>
<a href="https://goreportcard.com/badge/github.com/alpkeskin/rota"><img src="https://goreportcard.com/badge/github.com/alpkeskin/rota"></a>
<a href="https://github.com/alpkeskin/rota/releases"><img src="https://img.shields.io/github/release/alpkeskin/rota"></a>
<a href="#"><img src="https://img.shields.io/badge/platform-osx%2Flinux%2Fwindows-green"></a>
</p>

<p align="center">
  <a href="#key-highlights">Key Highlights</a> •
  <a href="#installation">Installation</a> •
  <a href="#configuration">Configuration</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#api">API</a> •
  <a href="#contributing">Contributing</a> •
  <a href="#what-is-next">What's Next</a>
</p>

**Rota** is a lightning-fast, self-hosted proxy rotation powerhouse that revolutionizes how you manage and rotate proxies. Built with performance at its core, this robust tool handles thousands of requests per second while seamlessly rotating IPs to maintain your anonymity. Whether you're conducting intensive web scraping operations, performing security research, or need reliable proxy management, Rota delivers enterprise-grade proxy rotation capabilities in an open-source package.

# Key Highlights
- 🚀 Self-hosted solution with complete control over your proxy infrastructure
- ⚡ Blazing-fast performance optimized for high-throughput operations
- 🔄 Advanced proxy rotation with intelligent IP management (random, roundrobin least_conn, time_based)
- 🤖 Automatic proxy pool management (real-time file monitoring and automatic removal of unhealthy proxies)
- 🌍 Supports HTTP, SOCKS v4(A) & v5 Protocols
- ✅ Built-in proxy checker to maintain a healthy proxy pool
- 🔒 Rate limiting to prevent abuse
- 🌐 Perfect companion for web scraping and data collection projects
- 🔍 Cross-platform compatibility (Windows, Linux, Mac, Raspberry Pi)
- 🔗 Easy integration with upstream proxies (e.g., *Burp Suite*) and proxy chains (e.g., *OWASP ZAP*)

# Installation

```sh
go install -v github.com/alpkeskin/rota/cmd/rota@latest
```

## Docker

```sh
docker pull ghcr.io/alpkeskin/rota:latest
```

### Docker Run

```sh
docker run \           
  --name rota-proxy \
  -p 8080:8080 \
  -p 8081:8081 \
  -v "$(pwd)/config.yml:/etc/rota/config.yml" \
  -v "$(pwd)/proxies.txt:/etc/rota/proxies.txt" \
  rota:latest --config /etc/rota/config.yml
```
note: If API is not enabled, dont use `-p 8081:8081`

# Configuration

Example configuration file can be found in [config.yml](config.yml)

* `proxy_file`: Path to the proxy file
* `file_watch`: Watch for file changes and reload proxies
* `proxy`: Proxy configurations
  - `port`: Proxy server port
  - `authentication`: Authentication configurations
    - `enabled`: Enable basic authentication
    - `username`: Username
    - `password`: Password
  - `rotation`: Rotation configurations
    - `method`: Rotation method (random, roundrobin, least_conn, time_based)
    - `time_based`: Time-based rotation configurations (only if method is time_based)
      - `interval`: Interval in seconds
    - `remove_unhealthy`: Remove unhealthy proxies from rotation
    - `fallback`: Recommended for continuous operation in case of proxy failures
    - `fallback_max_retries`: Number of retries for fallback. If this is reached, the response will be returned "bad gateway"
    - `timeout`: Timeout for proxy requests
    - `retries`: Number of retries to get a healthy proxy
  - `rate_limit`: Rate limiting configurations
    - `enabled`: Enable rate limiting
    - `interval`: Interval in seconds
    - `max_requests`: Maximum number of requests per interval
* `api`: API configurations
  - `enabled`: Enable API endpoints
  - `port`: API server port
* `healthcheck`: Healthcheck configurations
  - `output`: Output method (file, stdout)
  - `file`: Path to the healthcheck file
  - `timeout`: Timeout for healthcheck requests
  - `workers`: Number of workers to check proxies
  - `url`: URL to check proxies
  - `status`: Status code to check proxies
  - `headers`: Headers to check proxies
* `logging`: Logging configurations
  - `stdout`: Log to stdout
  - `file`: Path to the log file
  - `level`: Log level (debug, info, warn, error, fatal)

### Proxies file pattern

Proxies file should be in the following format:
```
scheme://ip:port or scheme://username:password@ip:port

Examples:
socks5://192.111.137.37:18762
http://192.111.137.37:9911
https://192.111.137.37:9911
socks5://admin:admin@192.111.137.37:18762
http://admin:admin@192.111.137.37:8080
https://admin:admin@192.111.137.37:8081
```

# Quick Start

```sh
rota --config config.yml
```
Default config file path is `config.yml`. **So you can use `rota` without any arguments.** That's it! 🎉

### Proxy Checker
```sh
rota --config config.yml --check
```

## API

For now, API is enabled by default. You can disabled it by setting `api.enabled` to `false` in your config file.

Endpoints:
- `/healthz`: Health check endpoint to monitor the service status
- `/proxies`: Returns a list of all currently available proxies in the pool, including their status and performance metrics
- `/metrics`: Provides detailed system metrics
- `/history`: Shows a chronological log of proxy rotations, requests, and any errors encountered during operation

# Contributing

Contributions are welcome! Please feel free to submit a PR. If you have any questions, please feel free to open an issue or contact me on [LinkedIn](https://www.linkedin.com/in/alpkeskin/).
**Please ensure your pull requests are meaningful and add value to the project. Pull requests that do not contribute significant improvements or fixes will not be accepted.**


##
> Thanks for your interest in Rota. I hope you enjoy using it.
>
> [LinkedIn](https://www.linkedin.com/in/alpkeskin)
> [Twitter](https://x.com/alpkeskindev)
> [GitHub](https://github.com/alpkeskin)
