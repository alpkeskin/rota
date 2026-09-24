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
	return BidirectionalCopyCounted(client, upstream, nil, nil)
}

// BidirectionalCopyCounted is BidirectionalCopy that reports bytes as they
// move: onUp for client→upstream, onDown for upstream→client (either may be
// nil). Counting as data flows, not at the end, lets quotas and usage see
// long-lived tunnels.
func BidirectionalCopyCounted(client, upstream net.Conn, onUp, onDown func(int64)) error {
	var wg sync.WaitGroup
	var clientErr, upstreamErr error

	wg.Add(2)

	// upstream → client
	go func() {
		defer wg.Done()
		clientErr = copyOneDirection(client, upstream, onDown)
		// When upstream closes or errors, half-close the client write side
		// so the client knows there's no more data coming.
		if tc, ok := client.(*net.TCPConn); ok {
			tc.CloseWrite() //nolint:errcheck
		}
	}()

	// client → upstream
	go func() {
		defer wg.Done()
		upstreamErr = copyOneDirection(upstream, client, onUp)
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
func copyOneDirection(dst, src net.Conn, count func(int64)) error {
	// Try platform-specific zero-copy (splice on Linux)
	ok, err := trySplice(dst, src, count)
	if ok {
		return err
	}

	// Fallback: userspace copy through a pooled buffer.
	buf := bufPool.Get().([]byte)
	defer bufPool.Put(buf)
	var w io.Writer = dst
	if count != nil {
		w = countingWriter{w: dst, count: count}
	}
	_, err = io.CopyBuffer(w, src, buf)
	return err
}

// countingWriter reports every successful write. It deliberately hides
// ReadFrom so io.CopyBuffer can't bypass it.
type countingWriter struct {
	w     io.Writer
	count func(int64)
}

func (c countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		c.count(int64(n))
	}
	return n, err
}

// bufPool reuses 32KB buffers for io.CopyBuffer to reduce GC pressure.
var bufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 32*1024)
		return buf
	},
}

// abortConn tears a tunnel connection down from another goroutine without
// blocking. A plain Close can block indefinitely while a splice(2) pump is
// parked in poll() on the socket (Close waits for the in-flight raw read),
// so the socket is first shut down — which wakes poll and makes splice
// return — and then closed in the background.
func abortConn(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		tc.CloseRead()  //nolint:errcheck // best effort
		tc.CloseWrite() //nolint:errcheck // best effort
	}
	go c.Close() //nolint:errcheck // best effort
}
