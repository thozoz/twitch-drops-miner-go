package inventory

import (
	"math"
	"sort"
	"time"
)

// SelectCampaign picks the best campaign to mine from candidates based on:
// 1. Unconditionally excluding games present in the exclude list.
// 2. Requiring the campaign to be Eligible() and CanEarnWithin(now, now + 1*time.Hour).
// 3. Ordering by priority list index ascending (unlisted games sort at math.MaxInt),
//    with EndsAt ascending as tiebreaker (and sole ordering if priority is empty).
// Returns the best campaign, or nil if no candidate qualifies.
func SelectCampaign(candidates []DropsCampaign, priority, exclude []string, now time.Time) *DropsCampaign {
	excludeMap := make(map[string]struct{}, len(exclude))
	for _, ex := range exclude {
		excludeMap[ex] = struct{}{}
	}

	var filtered []DropsCampaign
	for _, c := range candidates {
		// Exclusion check: case-sensitive match on Game.Name
		if _, excluded := excludeMap[c.Game.Name]; excluded {
			continue
		}

		// Eligibility and earnability within 1 hour
		if !c.Eligible() || !c.CanEarnWithin(now, now.Add(1*time.Hour)) {
			continue
		}

		filtered = append(filtered, c)
	}

	if len(filtered) == 0 {
		return nil
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		pi := priorityIndex(filtered[i].Game.Name, priority)
		pj := priorityIndex(filtered[j].Game.Name, priority)

		if pi != pj {
			return pi < pj
		}

		return filtered[i].EndsAt.Before(filtered[j].EndsAt)
	})

	res := filtered[0]
	return &res
}

func priorityIndex(gameName string, priority []string) int {
	for i, p := range priority {
		if p == gameName {
			return i
		}
	}
	return math.MaxInt
}
