package proxy

import (
	"testing"

	"github.com/alpkeskin/rota/core/internal/models"
)

func TestGetOrCreateTransport_CachesPerProxy(t *testing.T) {
	p := &models.Proxy{
		ID:       1,
		Address:  "127.0.0.1:8080",
		Protocol: "http",
	}

	t1, err := GetOrCreateTransport(p)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	t2, err := GetOrCreateTransport(p)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if t1 != t2 {
		t.Fatal("expected same transport instance from cache")
	}
}

func TestGetOrCreateTransport_DifferentProxies(t *testing.T) {
	p1 := &models.Proxy{
		ID:       1,
		Address:  "127.0.0.1:8081",
		Protocol: "http",
	}
	p2 := &models.Proxy{
		ID:       2,
		Address:  "127.0.0.1:8082",
		Protocol: "http",
	}

	t1, err := GetOrCreateTransport(p1)
	if err != nil {
		t.Fatalf("proxy1: %v", err)
	}

	t2, err := GetOrCreateTransport(p2)
	if err != nil {
		t.Fatalf("proxy2: %v", err)
	}

	if t1 == t2 {
		t.Fatal("expected different transport instances for different proxies")
	}
}

func TestCreateProxyTransport_HTTP(t *testing.T) {
	p := &models.Proxy{
		Address:  "127.0.0.1:3128",
		Protocol: "http",
	}
	tr, err := CreateProxyTransport(p)
	if err != nil {
		t.Fatalf("CreateProxyTransport: %v", err)
	}
	if tr.Proxy == nil {
		t.Fatal("HTTP proxy transport should have Proxy function set")
	}
}

func TestCreateProxyTransport_SOCKS5(t *testing.T) {
	p := &models.Proxy{
		Address:  "127.0.0.1:1080",
		Protocol: "socks5",
	}
	tr, err := CreateProxyTransport(p)
	if err != nil {
		t.Fatalf("CreateProxyTransport: %v", err)
	}
	if tr.Dial == nil {
		t.Fatal("SOCKS5 transport should have Dial function set")
	}
}

func TestCreateProxyTransport_UnsupportedProtocol(t *testing.T) {
	p := &models.Proxy{
		Address:  "127.0.0.1:9999",
		Protocol: "ftp",
	}
	_, err := CreateProxyTransport(p)
	if err == nil {
		t.Fatal("expected error for unsupported protocol")
	}
}

func TestCreateProxyTransport_WithAuth(t *testing.T) {
	user := "myuser"
	pass := "mypass"
	p := &models.Proxy{
		Address:  "127.0.0.1:3128",
		Protocol: "http",
		Username: &user,
		Password: &pass,
	}
	tr, err := CreateProxyTransport(p)
	if err != nil {
		t.Fatalf("CreateProxyTransport with auth: %v", err)
	}
	if tr.Proxy == nil {
		t.Fatal("should have Proxy set")
	}
}
