package state

import "time"

// RuntimeState represents the persistent session state saved to state.json.
type RuntimeState struct {
	ActiveCampaignID     string    `json:"active_campaign_id"`
	ActiveDropID         string    `json:"active_drop_id"`
	WatchingChannelID    string    `json:"watching_channel_id"`
	WatchingChannelLogin string    `json:"watching_channel_login"`
	CurrentMinutes       int       `json:"current_minutes"`
	LastSyncAt           time.Time `json:"last_sync_at"`
}
