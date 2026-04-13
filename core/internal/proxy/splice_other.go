//go:build !linux

package proxy

import "net"

// trySplice is a no-op on non-Linux platforms.
// Returns (false, nil) so the caller falls back to io.Copy.
func trySplice(dst, src net.Conn) (bool, error) {
	return false, nil
}
