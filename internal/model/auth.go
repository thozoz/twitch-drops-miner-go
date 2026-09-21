package model

import (
	"time"
)

// AuthData represents the persisted authentication state for a Twitch session.
type AuthData struct {
	AccessToken   RedactedString `json:"access_token"`
	RefreshToken  RedactedString `json:"refresh_token"`
	AuthClientID  string         `json:"auth_client_id,omitempty"`
	UserID        int            `json:"user_id"`
	Login         string         `json:"login"`
	DeviceID      string         `json:"device_id"`
	AuthUserAgent string         `json:"user_agent"` // Legacy key retained for existing auth files.
	ObtainedAt    time.Time      `json:"obtained_at"`
}
