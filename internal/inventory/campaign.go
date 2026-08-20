package inventory

import (
	"time"

	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

// BenefitType defines the distribution type of a drop reward.
type BenefitType string

const (
	BenefitBadge             BenefitType = "BADGE"
	BenefitEmote             BenefitType = "EMOTE"
	BenefitDirectEntitlement BenefitType = "DIRECT_ENTITLEMENT"
	BenefitUnknown           BenefitType = "UNKNOWN"
)

// IsBadgeOrEmote reports whether this benefit type is a badge or emote.
func (bt BenefitType) IsBadgeOrEmote() bool {
	return bt == BenefitBadge || bt == BenefitEmote
}

// Benefit represents a single reward item inside a drop.
type Benefit struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	ImageURL string      `json:"image_url"`
	Type     BenefitType `json:"type"`
}

// TimedDrop represents a time-based drop within a campaign.
type TimedDrop struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Benefits            []Benefit `json:"benefits"`
	StartsAt            time.Time `json:"starts_at"`
	EndsAt              time.Time `json:"ends_at"`
	ClaimID             string    `json:"claim_id,omitempty"`
	IsClaimed           bool      `json:"is_claimed"`
	PreconditionDropIDs []string  `json:"precondition_drop_ids,omitempty"`
	RequiredMinutes     int       `json:"required_minutes"`
	CurrentMinutes      int       `json:"current_minutes"`
}

// Progress returns the completion ratio of the drop between 0.0 and 1.0.
func (d TimedDrop) Progress() float64 {
	if d.CurrentMinutes <= 0 || d.RequiredMinutes <= 0 {
		return 0.0
	}
	if d.CurrentMinutes >= d.RequiredMinutes {
		return 1.0
	}
	return float64(d.CurrentMinutes) / float64(d.RequiredMinutes)
}

// RemainingMinutes returns the number of minutes left to watch, floored at 0.
func (d TimedDrop) RemainingMinutes() int {
	rem := d.RequiredMinutes - d.CurrentMinutes
	if rem < 0 {
		return 0
	}
	return rem
}

// CheckClaimedFromBenefits determines if a drop was claimed based on claimed benefit timestamps.
// The drop is considered claimed if the subset of benefit IDs found in claimedBenefits is non-empty
// and all timestamps in that subset fall within [startsAt, endsAt).
func CheckClaimedFromBenefits(benefits []Benefit, startsAt, endsAt time.Time, claimedBenefits map[string]time.Time) bool {
	var matched int
	for _, b := range benefits {
		if awardTime, ok := claimedBenefits[b.ID]; ok {
			matched++
			if awardTime.Before(startsAt) || !awardTime.Before(endsAt) {
				return false
			}
		}
	}
	return matched > 0
}

// DropsCampaign represents a Twitch Drops campaign.
type DropsCampaign struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Game            model.Game      `json:"game"`
	Linked          bool            `json:"linked"`
	LinkURL         string          `json:"link_url"`
	StartsAt        time.Time       `json:"starts_at"`
	EndsAt          time.Time       `json:"ends_at"`
	Valid           bool            `json:"valid"`
	AllowedChannels []model.Channel `json:"allowed_channels,omitempty"`
	Drops           []TimedDrop     `json:"drops"`
}

// HasBadgeOrEmote reports whether any drop in the campaign has any benefit
// whose type is a badge or emote.
func (c DropsCampaign) HasBadgeOrEmote() bool {
	for _, d := range c.Drops {
		for _, b := range d.Benefits {
			if b.Type.IsBadgeOrEmote() {
				return true
			}
		}
	}
	return false
}

// Eligible reports whether the campaign can be mined. A linked account is
// always required; a campaign whose rewards are exclusively badges/emotes is
// additionally gated behind enableBadgesEmotes, so operators must opt in
// before such campaigns enter the mining candidate pool.
func (c DropsCampaign) Eligible(enableBadgesEmotes bool) bool {
	if c.HasBadgeOrEmote() {
		return enableBadgesEmotes
	}
	return c.Linked
}

// Active reports whether the campaign is valid and currently running.
func (c DropsCampaign) Active(now time.Time) bool {
	return c.Valid && !now.Before(c.StartsAt) && now.Before(c.EndsAt)
}

// Upcoming reports whether the campaign is valid and has not started yet.
func (c DropsCampaign) Upcoming(now time.Time) bool {
	return c.Valid && now.Before(c.StartsAt)
}

// Expired reports whether the campaign is invalid or has already ended.
func (c DropsCampaign) Expired(now time.Time) bool {
	return !c.Valid || !now.Before(c.EndsAt)
}

// preconditionsChain returns the set of all drop IDs that serve as preconditions for uncompleted drops.
func (c DropsCampaign) preconditionsChain() map[string]struct{} {
	chain := make(map[string]struct{})
	for _, d := range c.Drops {
		if !d.IsClaimed {
			for _, pid := range d.PreconditionDropIDs {
				chain[pid] = struct{}{}
			}
		}
	}
	return chain
}

func (c DropsCampaign) dropPreconditionsMet(d TimedDrop) bool {
	if len(d.PreconditionDropIDs) == 0 {
		return true
	}
	claimedMap := make(map[string]bool, len(c.Drops))
	for _, drop := range c.Drops {
		if drop.IsClaimed {
			claimedMap[drop.ID] = true
		}
	}
	for _, pid := range d.PreconditionDropIDs {
		if !claimedMap[pid] {
			return false
		}
	}
	return true
}

func (c DropsCampaign) dropBaseEarnConditions(d TimedDrop, pChain map[string]struct{}) bool {
	if !c.dropPreconditionsMet(d) || d.IsClaimed || d.RequiredMinutes <= 0 {
		return false
	}
	if len(d.Benefits) > 0 {
		return true
	}
	_, inChain := pChain[d.ID]
	return inChain
}

func (c DropsCampaign) dropBaseCanEarn(d TimedDrop, now time.Time, pChain map[string]struct{}) bool {
	return c.dropBaseEarnConditions(d, pChain) && !now.Before(d.StartsAt) && now.Before(d.EndsAt)
}

func (c DropsCampaign) dropCanEarnWithin(d TimedDrop, now, stamp time.Time, pChain map[string]struct{}) bool {
	return c.dropBaseEarnConditions(d, pChain) && d.EndsAt.After(now) && d.StartsAt.Before(stamp)
}

// campaignLevelCanEarn checks whether the campaign is eligible, active, and matches channel ACL/game constraints.
func (c DropsCampaign) campaignLevelCanEarn(now time.Time, channel *model.Channel) bool {
	// Linkage-only gate: the badge/emote decision is made once at selection
	// time (SplitEligible/SelectCampaign) and must not be re-derived here,
	// since this path also runs from the watch session, which has no access
	// to config.
	if !c.Linked || !c.Active(now) {
		return false
	}

	if channel != nil {
		// If ACL is specified, channel must be in AllowedChannels
		if len(c.AllowedChannels) > 0 {
			found := false
			for _, allowed := range c.AllowedChannels {
				if allowed.ID == channel.ID || allowed.Login == channel.Login {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}

		// Game match check: channel.Game must match campaign game, or game must be special
		if !c.Game.IsSpecial() {
			if channel.Game == nil || channel.Game.ID != c.Game.ID {
				return false
			}
		}
	}

	return true
}

// CanEarn reports whether this campaign can be earned now on the given channel (or any channel if channel is nil).
func (c DropsCampaign) CanEarn(now time.Time, channel *model.Channel) bool {
	if !c.campaignLevelCanEarn(now, channel) {
		return false
	}

	pChain := c.preconditionsChain()
	for _, d := range c.Drops {
		if c.dropBaseCanEarn(d, now, pChain) {
			return true
		}
	}
	return false
}

// FirstEarnableDrop returns the first drop in slice order that is currently earnable,
// honoring precondition unlock order and channel/ACL/game constraints.
// Returns (nil, false) if no drop is currently earnable.
func (c DropsCampaign) FirstEarnableDrop(now time.Time, channel *model.Channel) (*TimedDrop, bool) {
	if !c.campaignLevelCanEarn(now, channel) {
		return nil, false
	}

	pChain := c.preconditionsChain()
	for i := range c.Drops {
		if c.dropBaseCanEarn(c.Drops[i], now, pChain) {
			return &c.Drops[i], true
		}
	}
	return nil, false
}

// CanEarnWithin reports whether this campaign has any drops that can be earned before stamp.
func (c DropsCampaign) CanEarnWithin(now, stamp time.Time) bool {
	// Linkage-only gate: see campaignLevelCanEarn's comment above.
	if !c.Linked || !c.Valid || !c.EndsAt.After(now) || !c.StartsAt.Before(stamp) {
		return false
	}

	pChain := c.preconditionsChain()
	for _, d := range c.Drops {
		if c.dropCanEarnWithin(d, now, stamp, pChain) {
			return true
		}
	}
	return false
}
