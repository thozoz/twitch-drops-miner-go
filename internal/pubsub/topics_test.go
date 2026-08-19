package pubsub

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopics(t *testing.T) {
	assert.Equal(t, Topic("user-drop-events.12345"), UserDropsTopic(12345))
	assert.Equal(t, Topic("onsite-notifications.12345"), UserNotificationsTopic(12345))
	assert.Equal(t, Topic("video-playback-by-id.67890"), ChannelStreamStateTopic("67890"))
	assert.Equal(t, Topic("broadcast-settings-update.67890"), ChannelStreamUpdateTopic("67890"))

	// Ensure String() method works as expected
	assert.Equal(t, "user-drop-events.12345", UserDropsTopic(12345).String())
}

func TestChunkTopics(t *testing.T) {
	t.Run("45 topics in chunks of 20", func(t *testing.T) {
		var topics []Topic
		for i := 1; i <= 45; i++ {
			topics = append(topics, Topic(fmt.Sprintf("topic.%d", i)))
		}

		chunks := ChunkTopics(topics, 20)
		require.Len(t, chunks, 3)
		assert.Len(t, chunks[0], 20)
		assert.Len(t, chunks[1], 20)
		assert.Len(t, chunks[2], 5)

		var flattened []Topic
		for _, chunk := range chunks {
			flattened = append(flattened, chunk...)
		}
		assert.ElementsMatch(t, topics, flattened)
	})

	t.Run("empty slice", func(t *testing.T) {
		chunks := ChunkTopics(nil, 20)
		assert.Empty(t, chunks)

		chunks2 := ChunkTopics([]Topic{}, 20)
		assert.Empty(t, chunks2)
	})

	t.Run("exact multiple of chunk size", func(t *testing.T) {
		var topics []Topic
		for i := 1; i <= 40; i++ {
			topics = append(topics, Topic(fmt.Sprintf("topic.%d", i)))
		}
		chunks := ChunkTopics(topics, 20)
		require.Len(t, chunks, 2)
		assert.Len(t, chunks[0], 20)
		assert.Len(t, chunks[1], 20)
	})

	t.Run("fewer than chunk size", func(t *testing.T) {
		topics := []Topic{"topic.1", "topic.2", "topic.3"}
		chunks := ChunkTopics(topics, 20)
		require.Len(t, chunks, 1)
		assert.Len(t, chunks[0], 3)
		assert.Equal(t, topics, chunks[0])
	})

	t.Run("zero or negative chunk size defaults to 20", func(t *testing.T) {
		topics := []Topic{"topic.1", "topic.2"}
		chunks := ChunkTopics(topics, 0)
		require.Len(t, chunks, 1)
		assert.Len(t, chunks[0], 2)
	})
}
