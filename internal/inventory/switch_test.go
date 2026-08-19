package inventory

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tdm/internal/model"
)

func TestGetPriority(t *testing.T) {
	gameA := model.NewGame("101", "Game A", "")
	gameB := model.NewGame("102", "Game B", "")
	gameOther := model.NewGame("999", "Other Game", "")

	wanted := []model.Game{gameA, gameB}

	// 1. Channel with Game == nil -> math.MaxInt (no panic)
	chanNilGame := model.Channel{ID: "c1", Online: true, Game: nil}
	assert.Equal(t, math.MaxInt, GetPriority(chanNilGame, wanted))

	// 2. Channel offline -> math.MaxInt
	chanOffline := model.Channel{ID: "c2", Online: false, Game: &gameA}
	assert.Equal(t, math.MaxInt, GetPriority(chanOffline, wanted))

	// 3. Channel with unlisted game -> math.MaxInt
	chanOther := model.Channel{ID: "c3", Online: true, Game: &gameOther}
	assert.Equal(t, math.MaxInt, GetPriority(chanOther, wanted))

	// 4. Channel with wanted games -> returns index
	chanA := model.Channel{ID: "c4", Online: true, Game: &gameA}
	assert.Equal(t, 0, GetPriority(chanA, wanted))

	chanB := model.Channel{ID: "c5", Online: true, Game: &gameB}
	assert.Equal(t, 1, GetPriority(chanB, wanted))
}

func TestShouldSwitch(t *testing.T) {
	gameA := model.NewGame("101", "Game A", "")
	gameB := model.NewGame("102", "Game B", "")
	wanted := []model.Game{gameA, gameB}

	chanA := model.Channel{ID: "a", Online: true, Game: &gameA, ACLBased: false}
	chanB := model.Channel{ID: "b", Online: true, Game: &gameB, ACLBased: false}
	chanA_ACL := model.Channel{ID: "a_acl", Online: true, Game: &gameA, ACLBased: true}

	// Candidate higher priority (GameA vs GameB) -> switch
	assert.True(t, ShouldSwitch(chanA, chanB, wanted))
	// Candidate lower priority -> no switch
	assert.False(t, ShouldSwitch(chanB, chanA, wanted))

	// Equal priority, candidate ACL and current non-ACL -> switch
	assert.True(t, ShouldSwitch(chanA_ACL, chanA, wanted))
	// Equal priority, candidate non-ACL and current ACL -> no switch
	assert.False(t, ShouldSwitch(chanA, chanA_ACL, wanted))
}

func TestOfflineGrace(t *testing.T) {
	grace := NewOfflineGrace()
	assert.Equal(t, 120*time.Second, grace.Window)

	base := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	assert.False(t, grace.Elapsed(base, base.Add(119*time.Second)))
	assert.True(t, grace.Elapsed(base, base.Add(120*time.Second)))
	assert.True(t, grace.Elapsed(base, base.Add(121*time.Second)))
}

func TestDecide(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	grace := NewOfflineGrace()

	gameA := model.NewGame("101", "Game A", "")
	gameB := model.NewGame("102", "Game B", "")
	wanted := []model.Game{gameA, gameB}

	candA := model.Channel{ID: "cand_a", Login: "streamer_a", Online: true, Game: &gameA}
	candB := model.Channel{ID: "cand_b", Login: "streamer_b", Online: true, Game: &gameB}

	// Case 1: current == nil -> switches to first candidate
	dec1 := Decide(nil, nil, []model.Channel{candA, candB}, wanted, grace, now)
	assert.True(t, dec1.Switch)
	require.NotNil(t, dec1.Target)
	assert.Equal(t, "streamer_a", dec1.Target.Login)
	assert.Equal(t, "no channel currently watched", dec1.Reason)

	// Case 2: current is offline within grace window (e.g. 30s ago) with higher-priority candidate available
	offlineSince30s := now.Add(-30 * time.Second)
	currentB_offline := model.Channel{ID: "curr_b", Login: "curr_b", Online: false, Game: &gameB}
	dec2 := Decide(&currentB_offline, &offlineSince30s, []model.Channel{candA}, wanted, grace, now)
	assert.False(t, dec2.Switch, "Must not switch while within offline grace window")
	assert.Equal(t, "within offline grace window", dec2.Reason)

	// Case 3: current offline past grace window (e.g. 130s ago) -> switches to candidate
	offlineSince130s := now.Add(-130 * time.Second)
	dec3 := Decide(&currentB_offline, &offlineSince130s, []model.Channel{candA}, wanted, grace, now)
	assert.True(t, dec3.Switch, "Must switch after offline grace window elapses")
	require.NotNil(t, dec3.Target)
	assert.Equal(t, "streamer_a", dec3.Target.Login)

	// Case 4: current is online and a higher priority candidate is available -> switch
	currentB_online := model.Channel{ID: "curr_b", Login: "curr_b", Online: true, Game: &gameB}
	dec4 := Decide(&currentB_online, nil, []model.Channel{candA}, wanted, grace, now)
	assert.True(t, dec4.Switch)
	require.NotNil(t, dec4.Target)
	assert.Equal(t, "streamer_a", dec4.Target.Login)

	// Case 5: current is online with highest priority -> no switch
	currentA_online := model.Channel{ID: "curr_a", Login: "curr_a", Online: true, Game: &gameA}
	dec5 := Decide(&currentA_online, nil, []model.Channel{candB}, wanted, grace, now)
	assert.False(t, dec5.Switch)
	assert.Nil(t, dec5.Target)
	assert.Equal(t, "no better candidate", dec5.Reason)
}
