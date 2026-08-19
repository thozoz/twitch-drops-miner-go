package inventory

import (
	"math"
	"time"

	"tdm/internal/model"
)

// GetPriority returns a priority number for a given channel based on wantedGames.
// 0 has the highest priority. Higher numbers -> lower priority.
// math.MaxInt signifies lowest possible priority (offline, nil Game, or game not in wantedGames).
func GetPriority(channel model.Channel, wantedGames []model.Game) int {
	if !channel.Online || channel.Game == nil {
		return math.MaxInt
	}

	for i, g := range wantedGames {
		if g.ID == channel.Game.ID || (g.Name != "" && g.Name == channel.Game.Name) {
			return i
		}
	}
	return math.MaxInt
}

// ShouldSwitch determines if candidate should replace current channel based on:
// 1. Candidate priority is strictly higher than current priority.
// 2. Or priorities are equal, and candidate is ACL-based while current is not.
func ShouldSwitch(candidate, current model.Channel, wantedGames []model.Game) bool {
	candidatePrio := GetPriority(candidate, wantedGames)
	currentPrio := GetPriority(current, wantedGames)

	if candidatePrio < currentPrio {
		return true
	}

	if candidatePrio == currentPrio && candidate.ACLBased && !current.ACLBased {
		return true
	}

	return false
}

// OfflineGrace manages the grace window before switching away from an offline stream.
type OfflineGrace struct {
	Window time.Duration
}

// NewOfflineGrace constructs an OfflineGrace with the standard 120s window.
func NewOfflineGrace() OfflineGrace {
	return OfflineGrace{
		Window: 120 * time.Second,
	}
}

// Elapsed reports whether the grace window has elapsed since offlineSince.
func (g OfflineGrace) Elapsed(offlineSince, now time.Time) bool {
	return now.Sub(offlineSince) >= g.Window
}

// SwitchDecision describes whether to switch channels and the rationale.
type SwitchDecision struct {
	Switch bool           `json:"switch"`
	Target *model.Channel `json:"target,omitempty"`
	Reason string         `json:"reason"`
}

// Decide computes the complete SEL-05 channel switch decision:
// 1. If current is nil: switch to candidates[0] if available.
// 2. If current is offline (currentOfflineSince != nil) and within grace window: do not switch.
// 3. Otherwise: scan candidates for the first qualifying switch candidate.
func Decide(
	current *model.Channel,
	currentOfflineSince *time.Time,
	candidates []model.Channel,
	wantedGames []model.Game,
	grace OfflineGrace,
	now time.Time,
) SwitchDecision {
	if current == nil {
		if len(candidates) > 0 {
			target := candidates[0]
			return SwitchDecision{
				Switch: true,
				Target: &target,
				Reason: "no channel currently watched",
			}
		}
		return SwitchDecision{
			Switch: false,
			Target: nil,
			Reason: "no candidate channels available",
		}
	}

	if currentOfflineSince != nil && !grace.Elapsed(*currentOfflineSince, now) {
		return SwitchDecision{
			Switch: false,
			Target: nil,
			Reason: "within offline grace window",
		}
	}

	for _, cand := range candidates {
		if ShouldSwitch(cand, *current, wantedGames) {
			target := cand
			return SwitchDecision{
				Switch: true,
				Target: &target,
				Reason: "better candidate available",
			}
		}
	}

	return SwitchDecision{
		Switch: false,
		Target: nil,
		Reason: "no better candidate",
	}
}
