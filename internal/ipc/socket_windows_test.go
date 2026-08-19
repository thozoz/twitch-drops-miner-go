//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func randomPipeAddr() string {
	return fmt.Sprintf(`\\.\pipe\tdm-test-%d-%d`, time.Now().UnixNano(), rand.Intn(100000))
}

func TestProbeRunning_AbsentSocket(t *testing.T) {
	addr := randomPipeAddr()
	running, err := ProbeRunning(addr, 100*time.Millisecond)
	require.NoError(t, err)
	assert.False(t, running)
}

func TestBind_AlreadyRunning(t *testing.T) {
	addr := randomPipeAddr()

	ln1, err := Bind(addr)
	require.NoError(t, err)
	defer func() { _ = Unbind(ln1, addr) }()

	// Accept in background so Windows named pipe client can connect/handshake
	go func() {
		conn, _ := ln1.Accept()
		if conn != nil {
			_ = conn.Close()
		}
	}()

	ln2, err := Bind(addr)
	assert.True(t, errors.Is(err, ErrAlreadyRunning), "expected ErrAlreadyRunning, got %v", err)
	assert.Nil(t, ln2)
}

func TestBind_StaleSocketReplaced(t *testing.T) {
	addr := randomPipeAddr()

	ln1, err := Bind(addr)
	require.NoError(t, err)

	// Close listener simulating process termination
	require.NoError(t, ln1.Close())

	// Re-binding should succeed
	ln2, err := Bind(addr)
	require.NoError(t, err)
	defer func() { _ = Unbind(ln2, addr) }()

	errCh := make(chan error, 1)
	go func() {
		conn, err := ln2.Accept()
		if err != nil {
			errCh <- err
			return
		}
		_ = conn.Close()
		errCh <- nil
	}()

	// Verify new listener accepts connections
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	clientConn, err := DialClient(ctx, addr, time.Second)
	require.NoError(t, err)
	require.NoError(t, clientConn.Close())

	require.NoError(t, <-errCh)
}
