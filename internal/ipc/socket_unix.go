//go:build !windows

package ipc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrAlreadyRunning is returned when a daemon instance is already bound and running.
var ErrAlreadyRunning = errors.New("tdm daemon is already running")

// ProbeRunning checks if a daemon is actively listening on the specified Unix domain socket.
func ProbeRunning(addr string, timeout time.Duration) (bool, error) {
	conn, err := net.DialTimeout("unix", addr, timeout)
	if err == nil {
		_ = conn.Close()
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) || os.IsNotExist(err) {
		return false, nil
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			if errors.Is(sysErr.Err, syscall.ECONNREFUSED) || errors.Is(sysErr.Err, syscall.ENOENT) {
				return false, nil
			}
		}
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) || errors.Is(opErr.Err, syscall.ENOENT) {
			return false, nil
		}
	}

	// Any generic connection error on non-existent or refused socket is not running
	return false, nil
}

// Bind creates a Unix domain socket listener at addr mode 0600, cleaning stale sockets if safe.
func Bind(addr string) (net.Listener, error) {
	running, err := ProbeRunning(addr, 200*time.Millisecond)
	if err != nil {
		return nil, err
	}
	if running {
		return nil, ErrAlreadyRunning
	}

	if err := os.MkdirAll(filepath.Dir(addr), 0700); err != nil {
		return nil, err
	}

	// Remove stale socket file if left over from a previous crash/kill
	if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	ln, err := net.Listen("unix", addr)
	if err != nil {
		return nil, err
	}

	if err := os.Chmod(addr, 0600); err != nil {
		_ = ln.Close()
		_ = os.Remove(addr)
		return nil, err
	}

	return ln, nil
}

// DialClient connects to a Unix domain socket daemon with a timeout.
func DialClient(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout}
	return d.DialContext(ctx, "unix", addr)
}

// Unbind closes the listener and removes the socket file.
func Unbind(ln net.Listener, addr string) error {
	var closeErr error
	if ln != nil {
		closeErr = ln.Close()
	}
	removeErr := os.Remove(addr)
	if removeErr != nil && os.IsNotExist(removeErr) {
		removeErr = nil
	}
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}
