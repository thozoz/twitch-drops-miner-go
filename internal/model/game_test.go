package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGame_Slug(t *testing.T) {
	tests := []struct {
		name     string
		game     Game
		expected string
	}{
		{
			name:     "Explicit slug given",
			game:     NewGame("1", "Custom Game Name", "custom-slug"),
			expected: "custom-slug",
		},
		{
			name:     "Elden Ring",
			game:     NewGame("1", "Elden Ring", ""),
			expected: "elden-ring",
		},
		{
			name:     "Marvel's Spider-Man",
			game:     NewGame("2", "Marvel's Spider-Man", ""),
			expected: "marvels-spider-man",
		},
		{
			name:     "Sea of Thieves",
			game:     NewGame("3", "Sea of Thieves", ""),
			expected: "sea-of-thieves",
		},
		{
			name:     "Special characters and whitespace",
			game:     NewGame("4", "  -- Counter-Strike: Global Offensive !! -- ", ""),
			expected: "counter-strike-global-offensive",
		},
		{
			name:     "Empty name",
			game:     NewGame("5", "", ""),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.game.Slug())
		})
	}
}

func TestGame_IsSpecial(t *testing.T) {
	assert.True(t, NewGame("509663", "Special 1", "").IsSpecial())
	assert.True(t, NewGame("509672", "Special 2", "").IsSpecial())
	assert.False(t, NewGame("123456", "Regular Game", "").IsSpecial())
	assert.False(t, NewGame("", "No ID", "").IsSpecial())
}
