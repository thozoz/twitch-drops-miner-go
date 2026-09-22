package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadTokenLineStopsAtEnter(t *testing.T) {
	input, err := readTokenLine(strings.NewReader("token\nignored"))
	require.NoError(t, err)
	assert.Equal(t, "token\n", input)
}

func TestReadTokenLineAcceptsEOFWithoutNewline(t *testing.T) {
	input, err := readTokenLine(strings.NewReader("token"))
	require.NoError(t, err)
	assert.Equal(t, "token", input)
}
