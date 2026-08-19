//go:build !windows

package ipc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeRunning_AbsentSocket(t *testing.T) {
	tempDir := t.TempDir()
	addr := filepath.Join(tempDir, "nonexistent.sock")

	running, err := ProbeRunning(addr, 100*time.Millisecond)
	require.NoError(t, err)
	assert.False(t, running)
}

func TestBind_AlreadyRunning(t *testing.T) {
	tempDir := t.TempDir()
	addr := filepath.Join(tempDir, "test.sock")

	ln1, err := Bind(addr)
	require.NoError(t, err)
	defer func() { _ = Unbind(ln1, addr) }()

	ln2, err := Bind(addr)
	assert.True(t, errors.Is(err, ErrAlreadyRunning), "expected ErrAlreadyRunning, got %v", err)
	assert.Nil(t, ln2)
}

func TestBind_StaleSocketReplaced(t *testing.T) {
	tempDir := t.TempDir()
	addr := filepath.Join(tempDir, "stale.sock")

	ln1, err := Bind(addr)
	require.NoError(t, err)

	// Close listener WITHOUT removing the file (simulating SIGKILL leaving stale socket file)
	require.NoError(t, ln1.Close())
	_, err = os.Stat(addr)
	require.NoError(t, err, "socket file should still exist")

	// Second Bind should detect stale socket, remove it, and bind successfully
	ln2, err := Bind(addr)
	require.NoError(t, err)
	defer func() { _ = Unbind(ln2, addr) }()

	// Verify file mode 0600
	info, err := os.Stat(addr)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

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
