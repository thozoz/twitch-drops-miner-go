package model

import (
	"time"
)

// AuthData represents the persisted authentication state for a Twitch session.
type AuthData struct {
	AccessToken  RedactedString `json:"access_token"`
	RefreshToken RedactedString `json:"refresh_token"`
	UserID       int            `json:"user_id"`
	Login        string         `json:"login"`
	DeviceID     string         `json:"device_id"`
	UserAgent    string         `json:"user_agent"`
	ObtainedAt   time.Time      `json:"obtained_at"`
}
