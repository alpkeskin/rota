package proxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/metrics"
	"github.com/alpkeskin/rota/core/internal/tracing"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"go.opentelemetry.io/otel/trace"
)

// SOCKS5 constants (RFC 1928, RFC 1929).
const (
	socksVersion        = 0x05
	socksAuthNone       = 0x00
	socksAuthUserPass   = 0x02
	socksAuthNoAccept   = 0xFF
	socksUserPassVer    = 0x01
	socksCmdConnect     = 0x01
	socksAtypIPv4       = 0x01
	socksAtypDomain     = 0x03
	socksAtypIPv6       = 0x04
	socksRepSucceeded   = 0x00
	socksRepFailure     = 0x01
	socksRepNotAllowed  = 0x02
	socksRepHostUnreach = 0x04
	socksRepCmdNotSupp  = 0x07
	socksRepAtypNotSup  = 0x08

	// socksHandshakeTimeout bounds the negotiation, so idle or slow clients
	// can't hold connections open before authenticating.
	socksHandshakeTimeout = 30 * time.Second
)

// socksServer is an inbound SOCKS5 listener that routes CONNECT requests
// exactly like HTTP CONNECT: same authentication and username routing
// options, same per-user limits and byte accounting, same upstream
// selection. Only CONNECT is supported (no BIND or UDP ASSOCIATE).
type socksServer struct {
	auth      *UserAuthMiddleware
	rateLimit *RateLimitMiddleware
	upstream  *UpstreamProxyHandler
	logger    *logger.Logger

	mu       sync.Mutex
	listener net.Listener
	conns    map[net.Conn]struct{}
	closed   bool
	wg       sync.WaitGroup

	// ctx is cancelled by Close, ending handlers still negotiating.
	ctx    context.Context
	cancel context.CancelFunc
}

// socksCloseWait caps how long Close waits for handlers, so a client stuck
// in an upstream dial can't use up the whole shutdown deadline.
const socksCloseWait = 2 * time.Second

func newSOCKSServer(auth *UserAuthMiddleware, rl *RateLimitMiddleware, upstream *UpstreamProxyHandler, log *logger.Logger) *socksServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &socksServer{auth: auth, rateLimit: rl, upstream: upstream, logger: log, conns: make(map[net.Conn]struct{}), ctx: ctx, cancel: cancel}
}

// Serve accepts connections until Close.
func (s *socksServer) Serve(l net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		l.Close() //nolint:errcheck // closed before serving: don't leak the socket
		return net.ErrClosed
	}
	s.listener = l
	s.mu.Unlock()

	var tempDelay time.Duration
	for {
		conn, err := l.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// Anything else (EMFILE, ECONNABORTED, …) is transient for a
			// listener: back off and keep serving, like net/http does.
			if tempDelay == 0 {
				tempDelay = 5 * time.Millisecond
			} else if tempDelay *= 2; tempDelay > time.Second {
				tempDelay = time.Second
			}
			s.logger.Warn("socks5 accept failed; retrying", "error", err, "delay", tempDelay)
			time.Sleep(tempDelay)
			continue
		}
		tempDelay = 0
		if !s.track(conn, true) {
			conn.Close() //nolint:errcheck // best-effort close/write
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.track(conn, false)
			s.handle(conn)
		}()
	}
}

func (s *socksServer) track(c net.Conn, add bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		if s.closed {
			return false
		}
		s.conns[c] = struct{}{}
		return true
	}
	delete(s.conns, c)
	return true
}

// Close stops accepting and closes every open SOCKS connection. Tunnels are
// cut: unlike HTTP keep-alive, they have no request boundary to drain at.
func (s *socksServer) Close(ctx context.Context) error {
	s.cancel()
	s.mu.Lock()
	s.closed = true
	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}
	for c := range s.conns {
		abortConn(c) //nolint:errcheck // best-effort close/write
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	wait := time.NewTimer(socksCloseWait)
	defer wait.Stop()
	select {
	case <-done:
	case <-ctx.Done():
	case <-wait.C:
	}
	return err
}

func (s *socksServer) handle(conn net.Conn) {
	defer conn.Close() //nolint:errcheck // best-effort close/write
	startTime := time.Now()
	conn.SetDeadline(time.Now().Add(socksHandshakeTimeout)) //nolint:errcheck

	if s.rateLimit != nil {
		if host, _, err := net.SplitHostPort(conn.RemoteAddr().String()); err == nil && !s.rateLimit.Allow(host) {
			metrics.ProxyRequests.WithLabelValues("socks5", "rejected_rate_limit").Inc()
			return
		}
	}

	r := bufio.NewReader(conn)
	ctx := s.ctx

	// 1. Method negotiation.
	methods, err := readGreeting(r)
	if err != nil {
		return
	}
	username, password, hasCreds := "", "", false
	switch {
	case methods[socksAuthUserPass]:
		if _, err := conn.Write([]byte{socksVersion, socksAuthUserPass}); err != nil {
			return
		}
		username, password, err = readUserPass(r)
		if err != nil {
			return
		}
		hasCreds = true
	case methods[socksAuthNone]:
		// Only select "no auth" if this proxy lets unauthenticated clients
		// through; otherwise refuse at method selection (RFC 1928).
		if _, result, _ := s.auth.Authorize(ctx, "", "", false); result != AuthAllowed {
			conn.Write([]byte{socksVersion, socksAuthNoAccept}) //nolint:errcheck // best-effort close/write
			metrics.ProxyRequests.WithLabelValues("socks5", "rejected_auth").Inc()
			return
		}
		if _, err := conn.Write([]byte{socksVersion, socksAuthNone}); err != nil {
			return
		}
	default:
		conn.Write([]byte{socksVersion, socksAuthNoAccept}) //nolint:errcheck
		return
	}

	preq, result, authErr := s.auth.Authorize(ctx, username, password, hasCreds)
	if hasCreds {
		status := byte(0x00)
		if result != AuthAllowed {
			status = 0x01
		}
		if _, err := conn.Write([]byte{socksUserPassVer, status}); err != nil || status != 0 {
			if result == AuthBadOptions {
				s.logger.Debug("socks5: invalid routing options", "error", authErr)
			}
			metrics.ProxyRequests.WithLabelValues("socks5", "rejected_auth").Inc()
			return
		}
	} else if result != AuthAllowed {
		// Credentials are required but the client offered only "no auth";
		// RFC 1928 lets us fail the request after method selection.
		writeSocksReply(conn, socksRepNotAllowed) //nolint:errcheck
		metrics.ProxyRequests.WithLabelValues("socks5", "rejected_auth").Inc()
		return
	}

	// 2. Request.
	host, rep, err := readConnectRequest(r)
	if err != nil {
		if rep != 0 {
			writeSocksReply(conn, rep) //nolint:errcheck
		}
		return
	}

	// The upstream connect has its own timeouts; the handshake deadline must
	// not cut the reply we send after it.
	conn.SetDeadline(time.Time{}) //nolint:errcheck
	if tracing.Enabled() {
		var span trace.Span
		ctx, span = tracing.StartProxy(ctx, "proxy SOCKS5", proxySpanAttrs("socks5", host, preq)...)
		defer span.End()
	}
	upstreamConn, proxyID, lease, err := s.upstream.OpenTunnel(ctx, host, preq)
	if err != nil {
		tracing.Fail(ctx, err)
		reply := byte(socksRepHostUnreach)
		switch {
		case errors.Is(err, ErrQuotaExceeded), errors.Is(err, ErrTooManyConnections), errors.Is(err, ErrRateLimited):
			reply = socksRepNotAllowed
		case isClientError(err):
			reply = socksRepFailure
		}
		s.logger.Warn("socks5: tunnel failed", "host", host, "error", err)
		metrics.ObserveProxyRequest("socks5", "upstream_error", time.Since(startTime))
		writeSocksReply(conn, reply) //nolint:errcheck
		return
	}
	if err := writeSocksReply(conn, socksRepSucceeded); err != nil {
		lease.Release()
		upstreamConn.Close() //nolint:errcheck // best-effort close/write
		return
	}

	// Bytes the client pipelined after the request are already buffered.
	if n := r.Buffered(); n > 0 {
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err == nil {
			w, _ := upstreamConn.Write(buf)
			countUp(lease, int64(w))
		}
	}
	s.upstream.ServeTunnel(conn, upstreamConn, lease, "socks5", host, proxyID, startTime)
}

// readGreeting reads the client's version/methods message.
func readGreeting(r *bufio.Reader) (map[byte]bool, error) {
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	if hdr[0] != socksVersion {
		return nil, fmt.Errorf("socks: unsupported version %d", hdr[0])
	}
	if hdr[1] == 0 {
		return nil, errors.New("socks: no authentication methods offered")
	}
	list := make([]byte, hdr[1])
	if _, err := io.ReadFull(r, list); err != nil {
		return nil, err
	}
	methods := make(map[byte]bool, len(list))
	for _, m := range list {
		methods[m] = true
	}
	return methods, nil
}

// readUserPass reads an RFC 1929 username/password sub-negotiation.
func readUserPass(r *bufio.Reader) (string, string, error) {
	ver, err := r.ReadByte()
	if err != nil {
		return "", "", err
	}
	if ver != socksUserPassVer {
		return "", "", fmt.Errorf("socks: unsupported auth version %d", ver)
	}
	readField := func() (string, error) {
		n, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		return string(b), nil
	}
	user, err := readField()
	if err != nil {
		return "", "", err
	}
	pass, err := readField()
	if err != nil {
		return "", "", err
	}
	return user, pass, nil
}

// readConnectRequest reads a request and returns the target as host:port.
// On failure rep is the reply code to send (0 when the connection is broken).
func readConnectRequest(r *bufio.Reader) (string, byte, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return "", 0, err
	}
	if hdr[0] != socksVersion {
		return "", socksRepFailure, fmt.Errorf("socks: bad request version %d", hdr[0])
	}
	var host string
	switch hdr[3] {
	case socksAtypIPv4:
		b := make([]byte, 4)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case socksAtypIPv6:
		b := make([]byte, 16)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case socksAtypDomain:
		n, err := r.ReadByte()
		if err != nil {
			return "", 0, err
		}
		if n == 0 {
			return "", socksRepFailure, errors.New("socks: empty domain name")
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	default:
		return "", socksRepAtypNotSup, fmt.Errorf("socks: unsupported address type %d", hdr[3])
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(r, pb); err != nil {
		return "", 0, err
	}
	if hdr[1] != socksCmdConnect {
		return "", socksRepCmdNotSupp, fmt.Errorf("socks: unsupported command %d", hdr[1])
	}
	port := binary.BigEndian.Uint16(pb)
	if port == 0 {
		return "", socksRepFailure, errors.New("socks: port 0")
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), 0, nil
}

// writeSocksReply sends a reply with an unspecified bind address: the
// upstream's address is another proxy's and would mislead the client.
func writeSocksReply(w io.Writer, rep byte) error {
	_, err := w.Write([]byte{socksVersion, rep, 0x00, socksAtypIPv4, 0, 0, 0, 0, 0, 0})
	return err
}
