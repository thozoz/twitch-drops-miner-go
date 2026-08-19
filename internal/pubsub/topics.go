package pubsub

import "fmt"

// Topic represents a Twitch PubSub topic string.
type Topic string

// String returns the topic as a string.
func (t Topic) String() string {
	return string(t)
}

// UserDropsTopic returns the user-drop-events topic for a given user ID.
func UserDropsTopic(userID int) Topic {
	return Topic(fmt.Sprintf("user-drop-events.%d", userID))
}

// UserNotificationsTopic returns the onsite-notifications topic for a given user ID.
func UserNotificationsTopic(userID int) Topic {
	return Topic(fmt.Sprintf("onsite-notifications.%d", userID))
}

// ChannelStreamStateTopic returns the video-playback-by-id topic for a given channel ID.
func ChannelStreamStateTopic(channelID string) Topic {
	return Topic(fmt.Sprintf("video-playback-by-id.%s", channelID))
}

// ChannelStreamUpdateTopic returns the broadcast-settings-update topic for a given channel ID.
func ChannelStreamUpdateTopic(channelID string) Topic {
	return Topic(fmt.Sprintf("broadcast-settings-update.%s", channelID))
}

// ChunkTopics splits a slice of topics into chunks with at most size topics per chunk.
func ChunkTopics(topics []Topic, size int) [][]Topic {
	if len(topics) == 0 {
		return nil
	}
	if size <= 0 {
		size = 20
	}
	var chunks [][]Topic
	for i := 0; i < len(topics); i += size {
		end := i + size
		if end > len(topics) {
			end = len(topics)
		}
		chunks = append(chunks, topics[i:end])
	}
	return chunks
}
