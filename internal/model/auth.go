package model

import (
	"time"
)

// AuthData represents the persisted authentication state for a Twitch session.
type AuthData struct {
	AccessToken       RedactedString `json:"access_token"`
	RefreshToken      RedactedString `json:"refresh_token"`
	ClientIntegrity   RedactedString `json:"client_integrity,omitempty"`
	AuthClientID      string         `json:"auth_client_id,omitempty"`
	UserID            int            `json:"user_id"`
	Login             string         `json:"login"`
	DeviceID          string         `json:"device_id"`
	ClientSessionID   string         `json:"client_session_id,omitempty"`
	AuthUserAgent     string         `json:"user_agent"` // JSON key intentionally unchanged for existing auth files.
	ObtainedAt        time.Time      `json:"obtained_at"`
	IntegrityCaptured time.Time      `json:"integrity_captured_at,omitempty"`
}
