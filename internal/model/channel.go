package model

// Channel represents a Twitch channel stream entity.
type Channel struct {
	ID           string `json:"id"`
	Login        string `json:"login"`
	DisplayName  string `json:"display_name"`
	ACLBased     bool   `json:"acl_based"`
	Online       bool   `json:"online"`
	Game         *Game  `json:"game,omitempty"`
	Viewers      int    `json:"viewers"`
	DropsEnabled bool   `json:"drops_enabled"`
	BroadcastID  string `json:"broadcast_id,omitempty"`
}

// Name returns DisplayName if non-empty, otherwise Login.
func (c Channel) Name() string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return c.Login
}
