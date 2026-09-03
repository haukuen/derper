package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// tailscaleSync periodically pulls the device list of member tailnets via
// the Tailscale API and feeds the result into the whitelist as per-source
// entries. Credentials are OAuth clients (client id + secret) with the
// read-only "devices:core:read" scope - deliberately NOT the 90-day
// fully-permitted access tokens, which grant full control over a tailnet.
//
// Only the standard library is used, keeping with the no-tailscale-
// dependency rule of this binary.

const (
	tsTokenURL     = "https://controlplane.tailscale.com/api/v2/oauth/token"
	tsDevicesURL   = "https://controlplane.tailscale.com/api/v2/tailnet/-/devices"
	tsTokenRefresh = 5 * time.Minute // re-mint access tokens this long before expiry
)

// tsClient syncs one member tailnet, identified by its OAuth client id.
type tsClient struct {
	clientID     string
	clientSecret string

	mu          sync.Mutex
	httpClient  *http.Client
	accessToken string
	tokenExpiry time.Time
}

// tsDevice mirrors the fields of the API's device object we care about.
type tsDevice struct {
	NodeKey string `json:"nodeKey"`
}

// parseTSSyncClients parses TS_SYNC_CLIENTS: a comma-separated list of
// clientID:clientSecret pairs.
func parseTSSyncClients(v string) map[string]*tsClient {
	clients := map[string]*tsClient{}
	for _, spec := range strings.Split(v, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		i := strings.Index(spec, ":")
		if i <= 0 || i == len(spec)-1 {
			log.Printf("TS_SYNC_CLIENTS: skipping malformed entry (want clientID:clientSecret): %q", spec)
			continue
		}
		id, secret := spec[:i], spec[i+1:]
		clients[id] = &tsClient{
			clientID:     id,
			clientSecret: secret,
			httpClient:   &http.Client{Timeout: 15 * time.Second},
		}
	}
	return clients
}

// sourceName is the whitelist source label for this client, e.g.
// "ts:k1234567890ab" - short enough for logs, traceable to the member.
func (c *tsClient) sourceName() string {
	prefix := c.clientID
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	return "ts:" + prefix
}

// respErr turns a non-2xx response into an error that carries a truncated
// response body, so upstream API or proxy error messages end up in the
// logs and in /status instead of just a bare status code.
func respErr(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	resp.Body.Close()
	if s := strings.TrimSpace(string(body)); s != "" {
		return fmt.Errorf("%s: %s", resp.Status, s)
	}
	return fmt.Errorf("%s", resp.Status)
}

// token returns a valid access token, minting a fresh one via the OAuth
// client credentials flow when needed. Tokens live ~1h; we refresh a bit
// early. An error here must never clear the synced list - callers keep
// the previous round's entries.
func (c *tsClient) token() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	form := url.Values{"scope": {"devices:core:read"}}
	req, err := http.NewRequest("POST", tsTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.clientID, c.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", respErr(resp)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		resp.Body.Close()
		return "", err
	}
	resp.Body.Close()
	if tok.AccessToken == "" {
		return "", fmt.Errorf("oauth token response missing access_token")
	}
	c.accessToken = tok.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tok.ExpiresIn)*time.Second - tsTokenRefresh)
	return c.accessToken, nil
}

// devices returns every device node key in the client's tailnet,
// following cursor pagination.
func (c *tsClient) devices() ([]string, error) {
	tok, err := c.token()
	if err != nil {
		return nil, err
	}

	var keys []string
	cursor := ""
	for {
		u := tsDevicesURL + "?limit=100"
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, respErr(resp)
		}
		var page struct {
			Devices    []tsDevice `json:"devices"`
			NextCursor string     `json:"nextCursor"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, d := range page.Devices {
			if d.NodeKey == "" {
				continue
			}
			keys = append(keys, normalizeKey(d.NodeKey))
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return keys, nil
}

// runSync executes one round for every configured client; failures keep
// the previous list and are only recorded.
func runSync(wl *whitelist, clients map[string]*tsClient) {
	for id, c := range clients {
		keys, err := c.devices()
		if err != nil {
			wl.recordSyncError(c.sourceName(), err)
			continue
		}
		wl.replaceForSource(c.sourceName(), keys)
		log.Printf("synced tailnet via client %s: %d device(s)", id, len(keys))
	}
}

// startSyncLoop validates config, runs one round immediately and keeps
// polling. It returns the clients for status reporting, or nil when no
// sync is configured.
func startSyncLoop(wl *whitelist, spec string, interval time.Duration) map[string]*tsClient {
	clients := parseTSSyncClients(spec)
	if len(clients) == 0 {
		return nil
	}
	go func() {
		runSync(wl, clients)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			runSync(wl, clients)
		}
	}()
	return clients
}

// syncInterval reads TS_SYNC_INTERVAL (a Go duration) with a 5 minute
// default.
func syncInterval() time.Duration {
	if v := os.Getenv("TS_SYNC_INTERVAL"); v != "" {
		if n, err := time.ParseDuration(v); err == nil && n > 0 {
			return n
		}
		log.Printf("TS_SYNC_INTERVAL=%q is not a valid duration, using default", v)
	}
	return 5 * time.Minute
}
