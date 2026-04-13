package proxy

import (
	"bytes"
	"crypto/sha256"
	"io"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"
)

func TestBidirectionalCopy_Basic(t *testing.T) {
	// Create two pairs of connected TCP sockets to simulate client ↔ upstream.
	clientConn, proxyClientSide := tcpPipe(t)
	upstreamConn, proxyUpstreamSide := tcpPipe(t)

	defer clientConn.Close()
	defer upstreamConn.Close()

	// Start bidirectional copy (proxy sits between proxyClientSide and proxyUpstreamSide).
	done := make(chan error, 1)
	go func() {
		done <- BidirectionalCopy(proxyClientSide, proxyUpstreamSide)
	}()

	// Client sends data → should arrive at upstream.
	clientMsg := []byte("hello from client")
	if _, err := clientConn.Write(clientMsg); err != nil {
		t.Fatalf("client write: %v", err)
	}

	buf := make([]byte, 256)
	n, err := upstreamConn.Read(buf)
	if err != nil {
		t.Fatalf("upstream read: %v", err)
	}
	if !bytes.Equal(buf[:n], clientMsg) {
		t.Fatalf("upstream got %q, want %q", buf[:n], clientMsg)
	}

	// Upstream sends data → should arrive at client.
	upstreamMsg := []byte("hello from upstream")
	if _, err := upstreamConn.Write(upstreamMsg); err != nil {
		t.Fatalf("upstream write: %v", err)
	}

	n, err = clientConn.Read(buf)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(buf[:n], upstreamMsg) {
		t.Fatalf("client got %q, want %q", buf[:n], upstreamMsg)
	}

	// Close both ends and wait for BidirectionalCopy to finish.
	clientConn.Close()
	upstreamConn.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("BidirectionalCopy did not finish in time")
	}
}

func TestBidirectionalCopy_LargePayload(t *testing.T) {
	clientConn, proxyClientSide := tcpPipe(t)
	upstreamConn, proxyUpstreamSide := tcpPipe(t)

	defer clientConn.Close()
	defer upstreamConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- BidirectionalCopy(proxyClientSide, proxyUpstreamSide)
	}()

	// Generate 10MB of random data.
	const payloadSize = 10 * 1024 * 1024
	payload := make([]byte, payloadSize)
	rand.New(rand.NewSource(42)).Read(payload)
	expectedHash := sha256.Sum256(payload)

	// Write in background.
	var writeErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, writeErr = clientConn.Write(payload)
		clientConn.(*net.TCPConn).CloseWrite() //nolint:errcheck
	}()

	// Read all on the upstream side.
	received, err := io.ReadAll(upstreamConn)
	if err != nil {
		t.Fatalf("upstream read: %v", err)
	}

	wg.Wait()
	if writeErr != nil {
		t.Fatalf("client write: %v", writeErr)
	}

	if len(received) != payloadSize {
		t.Fatalf("received %d bytes, want %d", len(received), payloadSize)
	}

	gotHash := sha256.Sum256(received)
	if gotHash != expectedHash {
		t.Fatal("SHA256 mismatch: data corruption during transfer")
	}

	upstreamConn.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("BidirectionalCopy did not finish in time")
	}
}

func TestBidirectionalCopy_HalfClose(t *testing.T) {
	clientConn, proxyClientSide := tcpPipe(t)
	upstreamConn, proxyUpstreamSide := tcpPipe(t)

	defer clientConn.Close()
	defer upstreamConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- BidirectionalCopy(proxyClientSide, proxyUpstreamSide)
	}()

	// Client sends data then closes write side.
	msg := []byte("one-way message")
	clientConn.Write(msg)                       //nolint:errcheck
	clientConn.(*net.TCPConn).CloseWrite()      //nolint:errcheck

	// Upstream should receive the message and then get EOF.
	buf := make([]byte, 256)
	n, err := upstreamConn.Read(buf)
	if err != nil {
		t.Fatalf("upstream read: %v", err)
	}
	if !bytes.Equal(buf[:n], msg) {
		t.Fatalf("got %q, want %q", buf[:n], msg)
	}

	// Upstream should see EOF on next read (half-close propagation).
	upstreamConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = upstreamConn.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF after half-close, got: %v", err)
	}

	// Close upstream too, BidirectionalCopy should finish.
	upstreamConn.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("BidirectionalCopy did not finish after both sides closed")
	}
}

// tcpPipe creates a connected pair of TCP sockets using a loopback listener.
// Returns (external side, internal side). The internal side is what the proxy
// would operate on.
func tcpPipe(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var serverConn net.Conn
	var serverErr error
	accepted := make(chan struct{})
	go func() {
		serverConn, serverErr = ln.Accept()
		close(accepted)
	}()

	clientConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		ln.Close()
		t.Fatalf("dial: %v", err)
	}

	<-accepted
	ln.Close()
	if serverErr != nil {
		clientConn.Close()
		t.Fatalf("accept: %v", serverErr)
	}

	return clientConn, serverConn
}
