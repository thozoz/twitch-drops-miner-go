package daemon

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRingBuffer_WriteAndSubscribe(t *testing.T) {
	ring := NewRingBuffer(5)

	ch, cancel := ring.Subscribe()

	_, err := ring.Write([]byte("line 1\nline 2\r\nline 3\n"))
	require.NoError(t, err)

	// Verify subscriber receives lines
	received := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		select {
		case l := <-ch:
			received = append(received, l)
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timed out waiting for subscriber line %d", i+1)
		}
	}
	assert.Equal(t, []string{"line 1", "line 2", "line 3"}, received)

	// Check Lines querying
	assert.Equal(t, []string{"line 2", "line 3"}, ring.Lines(2))
	assert.Equal(t, []string{"line 1", "line 2", "line 3"}, ring.Lines(10))
	assert.Equal(t, []string{"line 1", "line 2", "line 3"}, ring.Lines(0))

	// Overflow the buffer (capacity 5)
	for i := 4; i <= 8; i++ {
		_, err = ring.Write([]byte(fmt.Sprintf("line %d\n", i)))
		require.NoError(t, err)
	}

	// Buffer holds 5 most recent: lines 4, 5, 6, 7, 8
	allLines := ring.Lines(0)
	assert.Equal(t, []string{"line 4", "line 5", "line 6", "line 7", "line 8"}, allLines)

	// Cancel subscription
	cancel()

	// Drain remaining buffered items until closed
	for {
		_, ok := <-ch
		if !ok {
			break
		}
	}
}
