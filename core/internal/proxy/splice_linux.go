//go:build linux

package proxy

import (
	"net"
	"syscall"
)

// trySplice attempts zero-copy transfer using Linux splice(2) syscall.
// Returns (true, err) if splice was used, (false, nil) if caller should
// fall back to io.Copy (e.g. non-TCP connections).
func trySplice(dst, src net.Conn) (bool, error) {
	// Both connections must expose raw file descriptors via syscall.Conn.
	srcSC, ok := src.(syscall.Conn)
	if !ok {
		return false, nil
	}
	dstSC, ok := dst.(syscall.Conn)
	if !ok {
		return false, nil
	}

	srcRC, err := srcSC.SyscallConn()
	if err != nil {
		return false, nil
	}
	dstRC, err := dstSC.SyscallConn()
	if err != nil {
		return false, nil
	}

	// Create a kernel pipe as the intermediate buffer for splice.
	var pipeFDs [2]int
	if err := syscall.Pipe2(pipeFDs[:], syscall.O_NONBLOCK|syscall.O_CLOEXEC); err != nil {
		return false, nil // pipe creation failed, fall back
	}
	pipeR := pipeFDs[0]
	pipeW := pipeFDs[1]
	defer syscall.Close(pipeR)
	defer syscall.Close(pipeW)

	// Increase pipe buffer to 64KB for better throughput.
	fcntl(pipeR, syscall.F_SETPIPE_SZ, 65536)

	var spliceErr error

	// The outer Control call gives us the src fd.
	srcRC.Read(func(srcFD uintptr) bool {
		// The inner Control call gives us the dst fd.
		dstRC.Write(func(dstFD uintptr) bool {
			spliceErr = splicePump(int(srcFD), int(dstFD), pipeR, pipeW)
			return true
		})
		return true
	})

	if spliceErr != nil {
		return true, spliceErr
	}
	return true, nil
}

// splicePump moves data: src → pipeW → pipeR → dst using splice(2).
// Runs until src returns EOF (n==0) or an error occurs.
func splicePump(srcFD, dstFD, pipeR, pipeW int) error {
	for {
		// Move data from src socket into the pipe write end.
		n, err := syscall.Splice(srcFD, nil, pipeW, nil, 65536, syscall.SPLICE_F_MOVE|syscall.SPLICE_F_NONBLOCK)
		if err != nil {
			if err == syscall.EAGAIN {
				// Source not ready yet — use poll to wait.
				if pollErr := pollFD(srcFD, false); pollErr != nil {
					return pollErr
				}
				continue
			}
			if n == 0 {
				return nil // EOF
			}
			return err
		}
		if n == 0 {
			return nil // EOF — src closed
		}

		// Drain the pipe into the dst socket.
		for written := int64(0); written < n; {
			w, err := syscall.Splice(pipeR, nil, dstFD, nil, int(n-written), syscall.SPLICE_F_MOVE|syscall.SPLICE_F_NONBLOCK)
			if err != nil {
				if err == syscall.EAGAIN {
					if pollErr := pollFD(dstFD, true); pollErr != nil {
						return pollErr
					}
					continue
				}
				return err
			}
			written += w
		}
	}
}

// pollFD waits for a file descriptor to become ready for reading or writing.
func pollFD(fd int, write bool) error {
	events := int16(syscall.POLLIN)
	if write {
		events = syscall.POLLOUT
	}
	fds := []syscall.PollFd{{Fd: int32(fd), Events: events}}
	for {
		n, err := syscall.Poll(fds, 60000) // 60s timeout
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return err
		}
		if n == 0 {
			return syscall.ETIMEDOUT
		}
		return nil
	}
}

// fcntl is a thin wrapper for fcntl(2).
func fcntl(fd int, cmd int, arg int) {
	syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(cmd), uintptr(arg)) //nolint:errcheck
}
