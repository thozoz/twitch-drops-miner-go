package auth

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
)

// Twitch client constants for the ANDROID_APP client type.
// Ported from DevilXD/TwitchDropsMiner constants.py:213-246.
const (
	AndroidClientID  = "kd1unb4b3q4t58fwlpcbzcbnm76a8fp"
	AndroidClientURL = "https://www.twitch.tv"
)

// AndroidUserAgents contains the exact Dalvik User-Agent pool for the ANDROID_APP client type.
// Transcribed verbatim from constants.py:217-245.
var AndroidUserAgents = []string{
	"Dalvik/2.1.0 (Linux; U; Android 16; SM-S911B Build/TP1A.220624.014) tv.twitch.android.app/25.3.0/2503006",
	"Dalvik/2.1.0 (Linux; U; Android 16; SM-S938B Build/BP2A.250605.031) tv.twitch.android.app/25.3.0/2503006",
	"Dalvik/2.1.0 (Linux; Android 16; SM-X716N Build/UP1A.231005.007) tv.twitch.android.app/25.3.0/2503006",
	"Dalvik/2.1.0 (Linux; U; Android 15; SM-G990B Build/AP3A.240905.015.A2) tv.twitch.android.app/25.3.0/2503006",
	"Dalvik/2.1.0 (Linux; U; Android 15; SM-G970F Build/AP3A.241105.008) tv.twitch.android.app/25.3.0/2503006",
	"Dalvik/2.1.0 (Linux; U; Android 15; SM-A566E Build/AP3A.240905.015.A2) tv.twitch.android.app/25.3.0/2503006",
	"Dalvik/2.1.0 (Linux; U; Android 14; SM-X306B Build/UP1A.231005.007) tv.twitch.android.app/25.3.0/2503006",
}

// PickUserAgent selects one random User-Agent string from the AndroidUserAgents pool.
func PickUserAgent() string {
	return AndroidUserAgents[rand.IntN(len(AndroidUserAgents))]
}

// NewDeviceID generates a 32-character lowercase hex nonce using crypto/rand (16 bytes).
// Per D-03, this is generated once on first login and persisted.
func NewDeviceID() string {
	b := make([]byte, 16)
	if _, err := cryptorand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// NewSessionID generates a 16-character lowercase hex nonce using crypto/rand (8 bytes).
// Per D-05, this is regenerated on every process start and never persisted.
func NewSessionID() string {
	b := make([]byte, 8)
	if _, err := cryptorand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}
