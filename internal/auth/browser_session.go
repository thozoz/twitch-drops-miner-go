package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const browserLoginURL = "https://www.twitch.tv/drops/inventory"

// BrowserSession contains the account and integrity headers emitted by Twitch's
// own web client. All fields are captured from the same authenticated GQL request.
type BrowserSession struct {
	AccessToken     string
	ClientID        string
	ClientIntegrity string
	DeviceID        string
	ClientSessionID string
	UserAgent       string
}

// CaptureBrowserSession opens a normal Chromium window using a TDM-owned
// persistent profile and waits for Twitch's web client to send an authenticated
// GQL request. The user signs in inside that window on first use.
func CaptureBrowserSession(ctx context.Context, profileDir string, ready func()) (BrowserSession, error) {
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return BrowserSession{}, fmt.Errorf("create browser profile directory: %w", err)
	}

	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.UserDataDir(profileDir),
		chromedp.WindowSize(1200, 850),
		chromedp.Flag("headless", false),
	}
	if browserPath := findBrowserExecutable(); browserPath != "" {
		opts = append(opts, chromedp.ExecPath(browserPath))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	captured := make(chan BrowserSession, 1)
	chromedp.ListenTarget(browserCtx, func(event any) {
		req, ok := event.(*network.EventRequestWillBeSent)
		if !ok || req.Request == nil || !strings.HasPrefix(req.Request.URL, "https://gql.twitch.tv/gql") {
			return
		}
		if session, ok := browserSessionFromHeaders(req.Request.Headers); ok {
			select {
			case captured <- session:
			default:
			}
		}
	})

	if err := chromedp.Run(browserCtx, network.Enable()); err != nil {
		return BrowserSession{}, fmt.Errorf("start Chromium: %w", err)
	}
	if ready != nil {
		ready()
	}
	if err := chromedp.Run(browserCtx, chromedp.Navigate(browserLoginURL)); err != nil {
		return BrowserSession{}, fmt.Errorf("open Twitch login page: %w", err)
	}

	select {
	case session := <-captured:
		return session, nil
	case <-ctx.Done():
		return BrowserSession{}, fmt.Errorf("waiting for authenticated Twitch browser session: %w", ctx.Err())
	}
}

var captureBrowserSession = CaptureBrowserSession

func browserSessionFromHeaders(headers network.Headers) (BrowserSession, bool) {
	normalized := make(map[string]string, len(headers))
	for name, value := range headers {
		normalized[strings.ToLower(name)] = fmt.Sprint(value)
	}

	authorization := normalized["authorization"]
	if len(authorization) < len("OAuth ") || !strings.EqualFold(authorization[:len("OAuth ")], "OAuth ") {
		return BrowserSession{}, false
	}

	session := BrowserSession{
		AccessToken:     strings.TrimSpace(authorization[len("OAuth "):]),
		ClientID:        strings.TrimSpace(normalized["client-id"]),
		ClientIntegrity: strings.TrimSpace(normalized["client-integrity"]),
		DeviceID:        strings.TrimSpace(normalized["x-device-id"]),
		ClientSessionID: strings.TrimSpace(normalized["client-session-id"]),
		UserAgent:       strings.TrimSpace(normalized["user-agent"]),
	}
	if session.AccessToken == "" || session.ClientID == "" || session.ClientIntegrity == "" || session.UserAgent == "" {
		return BrowserSession{}, false
	}
	return session, true
}

func findBrowserExecutable() string {
	if configured := strings.TrimSpace(os.Getenv("TDM_BROWSER_PATH")); configured != "" {
		return configured
	}

	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = []string{
			"chrome.exe",
			"msedge.exe",
			filepath.Join(os.Getenv("PROGRAMFILES"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("PROGRAMFILES(X86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("PROGRAMFILES"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("PROGRAMFILES(X86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		candidates = []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge"}
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

var errBrowserSessionRequired = errors.New("browser-backed Twitch session required: run 'tdm auth login'")
