<h1 align="center">
  <img src="static/rota.svg" alt="rota" width="150px">
  <br>
</h1>

<p align="center">
<a href="https://opensource.org/licenses/Apache-2.0"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg"></a>
<a href="https://golang.org"><img src="https://img.shields.io/badge/made%20with-Go-brightgreen"></a>
<a href="https://goreportcard.com/badge/github.com/alpkeskin/rota"><img src="https://goreportcard.com/badge/github.com/alpkeskin/rota"></a>
<a href="https://github.com/alpkeskin/rota/releases"><img src="https://img.shields.io/github/release/alpkeskin/rota"></a>
<a href="#"><img src="https://img.shields.io/badge/platform-osx%2Flinux%2Fwindows-green"></a>
</p>

<p align="center">
  <a href="#features">Features</a> •
  <a href="#installation">Installation</a> •
  <a href="#usage">Usage</a>
</p>

**Rota** is an incredibly fast proxy rotating tool that allows users to manage and rotate proxy IPs with ease. This open-source application is designed to handle high volumes of requests efficiently, providing seamless IP rotation and proxy checking capabilities. By consolidating multiple services, Rota empowers security researchers and developers to maintain anonymity and enhance their data scraping and web access activities with minimal effort.



# Features

🌐 Proxy IP Rotator
- **🚀 IP Rotation**: Rotates your IP address for every specific request.
- **✅ Proxy Checker**: Check if your proxy IP is still alive.
- **🌍 Supports All HTTP/S Methods**: All HTTP and HTTPS methods are supported.
- **🔄 HTTP, SOCKS v4(A) & v5 Protocols**: Compatible with all major proxy protocols.


🛠️ Ease of Use
- **📂 User-Friendly**: Simply run it against your proxy file and choose the desired action.
- **💻 Cross-Platform**: Works seamlessly on Windows, Linux, Mac, or even Raspberry Pi.
- **🔗 Easy Integration**: Easily integrates with upstream proxies (e.g., *Burp Suite*) and proxy chains (e.g., *OWASP ZAP*).


# Installation

```sh
go install -v github.com/alpkeskin/rota/cmd/rota@latest
```

## Docker

```sh
docker pull ghcr.io/alpkeskin/rota:latest
```

# Usage
```sh
rota -h
```
This will display help for the tool. Here are all the flags it supports.

```
  -auth string
    	Authentication credentials in the format user:pass
  -check
    	Enable check mode
  -file string
    	File containing proxy URLs
  -method string
    	Method to use (random or sequent) (default "random")
  -output string
    	Output file path
  -port string
    	Port to use (default "8080")
  -proxy string
    	Proxy URL
  -retries int
    	Number of retries (default 3)
  -timeout int
    	Request timeout in seconds (default 5)
  -verbose
    	Enable verbose mode
```

## Basics

Basic Start:
```sh
rota --file proxies.txt
```

Start with spesific port:
```sh
rota --file live.txt --port 4444
```

Start with Authorization:
```sh
rota --file live.txt --auth user:pass
```

Proxy List Checking:
```sh
rota --file proxies.txt --check
```

Output flag for live proxies (txt)
```sh
rota --file proxies.txt --check --output live.txt
```

### Deep Dive

```sh
rota --file live.txt --port 1234 --retries 5 --timeout 10 --method sequent --auth user:pass --verbose
```