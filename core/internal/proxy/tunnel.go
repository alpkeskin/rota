package proxy

import (
	"io"
	"net"
	"sync"
)

// BidirectionalCopy copies data between client and upstream in both directions
// concurrently. It returns when either direction encounters an error or EOF.
// On Linux with raw TCP sockets, it attempts splice(2) for zero-copy transfer
// before falling back to io.Copy.
func BidirectionalCopy(client, upstream net.Conn) error {
	var wg sync.WaitGroup
	var clientErr, upstreamErr error

	wg.Add(2)

	// upstream → client
	go func() {
		defer wg.Done()
		clientErr = copyOneDirection(client, upstream)
		// When upstream closes or errors, half-close the client write side
		// so the client knows there's no more data coming.
		if tc, ok := client.(*net.TCPConn); ok {
			tc.CloseWrite() //nolint:errcheck
		}
	}()

	// client → upstream
	go func() {
		defer wg.Done()
		upstreamErr = copyOneDirection(upstream, client)
		// When client closes or errors, half-close the upstream write side.
		if tc, ok := upstream.(*net.TCPConn); ok {
			tc.CloseWrite() //nolint:errcheck
		}
	}()

	wg.Wait()

	// Return whichever error is more meaningful
	if clientErr != nil {
		return clientErr
	}
	return upstreamErr
}

// copyOneDirection copies from src to dst using the most efficient method
// available on the current platform. On Linux with raw TCP sockets it tries
// splice(2) first; otherwise it falls back to io.Copy.
func copyOneDirection(dst, src net.Conn) error {
	// Try platform-specific zero-copy (splice on Linux)
	ok, err := trySplice(dst, src)
	if ok {
		return err
	}

	// Fallback: standard io.Copy (uses sendfile or splice via Go runtime
	// when possible, otherwise userspace buffer copy)
	buf := bufPool.Get().([]byte)
	defer bufPool.Put(buf)
	_, err = io.CopyBuffer(dst, src, buf)
	return err
}

// bufPool reuses 32KB buffers for io.CopyBuffer to reduce GC pressure.
var bufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 32*1024)
		return buf
	},
}
