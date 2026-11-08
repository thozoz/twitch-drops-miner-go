package channel

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMasterPlaylist(t *testing.T) {
	baseURL, _ := url.Parse("https://usher.ttvnw.net/api/channel/hls/streamer.m3u8")

	t.Run("valid master playlist with multiple qualities", func(t *testing.T) {
		body := `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080
https://video-weaver.sjc01.hls.ttvnw.net/v1/playlist/1080p.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=160000,RESOLUTION=160x90
https://video-weaver.sjc01.hls.ttvnw.net/v1/playlist/160p.m3u8
`
		res, err := ParseMasterPlaylist(baseURL, body)
		require.NoError(t, err)
		assert.Equal(t, "https://video-weaver.sjc01.hls.ttvnw.net/v1/playlist/160p.m3u8", res)
	})

	t.Run("relative url in master playlist", func(t *testing.T) {
		body := `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=160000
variant/index.m3u8
`
		res, err := ParseMasterPlaylist(baseURL, body)
		require.NoError(t, err)
		assert.Equal(t, "https://usher.ttvnw.net/api/channel/hls/variant/index.m3u8", res)
	})

	t.Run("rejects json response", func(t *testing.T) {
		body := `{"error": "Channel is offline"}`
		_, err := ParseMasterPlaylist(baseURL, body)
		require.ErrorIs(t, err, ErrPlaylistJSON)
	})

	t.Run("rejects non-m3u8 text", func(t *testing.T) {
		body := `some random text`
		_, err := ParseMasterPlaylist(baseURL, body)
		require.ErrorIs(t, err, ErrMalformedPlaylist)
	})

	t.Run("rejects unsupported scheme", func(t *testing.T) {
		body := `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=160000
file:///etc/passwd
`
		_, err := ParseMasterPlaylist(baseURL, body)
		require.ErrorIs(t, err, ErrMalformedPlaylist)
	})
}

func TestParseMediaPlaylist(t *testing.T) {
	baseURL, _ := url.Parse("https://video-weaver.sjc01.hls.ttvnw.net/v1/playlist/160p.m3u8")

	t.Run("valid media playlist extracts all segments", func(t *testing.T) {
		body := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXTINF:2.000,
chunk-0.ts
#EXTINF:2.000,
chunk-1.ts
#EXTINF:2.000,
https://other-cdn.ttvnw.net/chunk-2.ts
`
		res, err := ParseMediaPlaylist(baseURL, body)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"https://video-weaver.sjc01.hls.ttvnw.net/v1/playlist/chunk-0.ts",
			"https://video-weaver.sjc01.hls.ttvnw.net/v1/playlist/chunk-1.ts",
			"https://other-cdn.ttvnw.net/chunk-2.ts",
		}, res)
	})

	t.Run("ignores tags like EXT-X-ENDLIST and headers", func(t *testing.T) {
		body := `#EXTM3U
#EXTINF:2.0,
chunk-0.ts
#EXT-X-ENDLIST
`
		res, err := ParseMediaPlaylist(baseURL, body)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"https://video-weaver.sjc01.hls.ttvnw.net/v1/playlist/chunk-0.ts",
		}, res)
	})

	t.Run("rejects json response", func(t *testing.T) {
		body := `{"error": "Forbidden"}`
		_, err := ParseMediaPlaylist(baseURL, body)
		require.ErrorIs(t, err, ErrPlaylistJSON)
	})
}
