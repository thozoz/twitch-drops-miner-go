package channel

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var (
	ErrMalformedPlaylist = errors.New("malformed or unsupported HLS playlist")
	ErrPlaylistJSON      = errors.New("stream playlist returned JSON error response")
)

// ParseMasterPlaylist parses an HLS master playlist body and returns the URI of the last declared variant stream.
func ParseMasterPlaylist(baseURL *url.URL, body string) (string, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "", fmt.Errorf("%w: empty body", ErrMalformedPlaylist)
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return "", ErrPlaylistJSON
	}
	if !strings.HasPrefix(trimmed, "#EXTM3U") {
		return "", fmt.Errorf("%w: missing #EXTM3U header", ErrMalformedPlaylist)
	}

	scanner := bufio.NewScanner(strings.NewReader(body))
	var lastVariantURI string
	var nextIsURI bool

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			nextIsURI = true
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if nextIsURI {
			lastVariantURI = line
			nextIsURI = false
		}
	}

	if lastVariantURI == "" {
		return "", fmt.Errorf("%w: no stream variants found in master playlist", ErrMalformedPlaylist)
	}

	return resolveURL(baseURL, lastVariantURI)
}

// ParseMediaPlaylist parses an HLS media playlist body and returns all segment URIs.
func ParseMediaPlaylist(baseURL *url.URL, body string) ([]string, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: empty body", ErrMalformedPlaylist)
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return nil, ErrPlaylistJSON
	}
	if !strings.HasPrefix(trimmed, "#EXTM3U") {
		return nil, fmt.Errorf("%w: missing #EXTM3U header", ErrMalformedPlaylist)
	}

	scanner := bufio.NewScanner(strings.NewReader(body))
	var segments []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		resolved, err := resolveURL(baseURL, line)
		if err != nil {
			return nil, err
		}
		segments = append(segments, resolved)
	}

	if len(segments) == 0 {
		return nil, fmt.Errorf("%w: no segments found in media playlist", ErrMalformedPlaylist)
	}

	return segments, nil
}

func resolveURL(baseURL *url.URL, rawURI string) (string, error) {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return "", fmt.Errorf("%w: invalid URI %q: %v", ErrMalformedPlaylist, rawURI, err)
	}

	var resolved *url.URL
	if parsed.IsAbs() {
		resolved = parsed
	} else {
		if baseURL == nil {
			return "", fmt.Errorf("%w: relative URI %q without base URL", ErrMalformedPlaylist, rawURI)
		}
		resolved = baseURL.ResolveReference(parsed)
	}

	scheme := strings.ToLower(resolved.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%w: unsupported scheme %q", ErrMalformedPlaylist, resolved.Scheme)
	}

	return resolved.String(), nil
}
