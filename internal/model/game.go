package model

import (
	"regexp"
	"strings"
)

var (
	reApostrophe = regexp.MustCompile(`'`)
	reNonAlpha   = regexp.MustCompile(`\W+`)
	reMultiDash  = regexp.MustCompile(`-{2,}`)
)

// SpecialGameIDs defines Twitch game IDs that can be earned on any channel.
var SpecialGameIDs = map[string]struct{}{
	"509663": {},
	"509672": {},
}

// Game represents a Twitch game category.
type Game struct {
	ID   string
	Name string
	slug string
}

// NewGame creates a new Game entity. If slug is empty, it will be derived from Name when Slug() is called.
func NewGame(id, name, slug string) Game {
	return Game{
		ID:   id,
		Name: name,
		slug: slug,
	}
}

// Slug returns the URL-friendly slug for the game name.
// If an explicit slug was provided at creation, it returns it; otherwise it derives
// a slug from Name (lowercase, strip apostrophes, collapse non-alphanumeric to '-', trim/collapse dashes).
func (g Game) Slug() string {
	if g.slug != "" {
		return g.slug
	}
	if g.Name == "" {
		return ""
	}
	s := strings.ToLower(g.Name)
	s = reApostrophe.ReplaceAllString(s, "")
	s = reNonAlpha.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	s = reMultiDash.ReplaceAllString(s, "-")
	return s
}

// IsSpecial reports whether this game has special campaign drop rules (can be earned anywhere).
func (g Game) IsSpecial() bool {
	_, ok := SpecialGameIDs[g.ID]
	return ok
}
