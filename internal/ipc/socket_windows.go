//go:build windows

package ipc

import (
	"context"
	"errors"
	"net"
	"os"
	"time"

	"github.com/Microsoft/go-winio"
)

// ErrAlreadyRunning is returned when a daemon instance is already bound and running.
var ErrAlreadyRunning = errors.New("tdm daemon is already running")

// ProbeRunning checks if a daemon is actively listening at the specified pipe address.
func ProbeRunning(addr string, timeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := winio.DialPipeContext(ctx, addr)
	if err == nil {
		_ = conn.Close()
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false, nil
	}

	// On Windows, named pipe non-existence or unavailable pipe is reported as path/file error
	if os.IsNotExist(err) {
		return false, nil
	}

	// Any other error (e.g. access denied or pipe not available) treated as not running
	return false, nil
}

// Bind creates a named pipe listener on Windows, rejecting with ErrAlreadyRunning if already active.
func Bind(addr string) (net.Listener, error) {
	running, err := ProbeRunning(addr, 200*time.Millisecond)
	if err != nil {
		return nil, err
	}
	if running {
		return nil, ErrAlreadyRunning
	}

	cfg := &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;OW)",
	}
	return winio.ListenPipe(addr, cfg)
}

// DialClient connects to a named pipe daemon with a timeout.
func DialClient(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return winio.DialPipeContext(ctx, addr)
}

// Unbind closes the listener on Windows.
func Unbind(ln net.Listener, addr string) error {
	if ln != nil {
		return ln.Close()
	}
	return nil
}
