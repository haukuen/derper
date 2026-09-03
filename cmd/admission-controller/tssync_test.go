package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseTSSyncClients(t *testing.T) {
	clients := parseTSSyncClients(" k123:secret1 , badformat , :nosecret , k234: , k123:secret2 , ")
	if len(clients) != 1 {
		t.Fatalf("got %d clients, want 1 (malformed entries skipped, duplicate ids overwritten): %v", len(clients), clients)
	}
	if c := clients["k123"]; c == nil || c.clientSecret != "secret2" {
		t.Errorf("duplicate client id should keep the last secret, got %+v", c)
	}
	if clients := parseTSSyncClients(""); len(clients) != 0 {
		t.Errorf("empty spec should yield no clients, got %v", clients)
	}
}

func TestSourceName(t *testing.T) {
	if got := (&tsClient{clientID: "k123"}).sourceName(); got != "ts:k123" {
		t.Errorf("sourceName = %q, want ts:k123", got)
	}
	if got := (&tsClient{clientID: "k1234567890123456789"}).sourceName(); got != "ts:k123456789012345" {
		t.Errorf("sourceName should truncate to a 16-char prefix, got %q", got)
	}
}

func TestSyncInterval(t *testing.T) {
	t.Setenv("TS_SYNC_INTERVAL", "30s")
	if got := syncInterval(); got != 30*time.Second {
		t.Errorf("syncInterval = %v, want 30s", got)
	}
	t.Setenv("TS_SYNC_INTERVAL", "garbage")
	if got := syncInterval(); got != 5*time.Minute {
		t.Errorf("invalid duration should fall back to 5m, got %v", got)
	}
	t.Setenv("TS_SYNC_INTERVAL", "")
	if got := syncInterval(); got != 5*time.Minute {
		t.Errorf("unset variable should fall back to 5m, got %v", got)
	}
}

// withTestAPI points the package-level Tailscale API URLs at an httptest
// server and restores the originals when the test finishes.
func withTestAPI(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	oldToken, oldDevices := tsTokenURL, tsDevicesURL
	t.Cleanup(func() { tsTokenURL, tsDevicesURL = oldToken, oldDevices })
	srv := httptest.NewServer(handler)
	tsTokenURL = srv.URL + "/token"
	tsDevicesURL = srv.URL + "/devices"
	t.Cleanup(srv.Close)
	return srv
}

func TestDevicesFollowsPaginationAndSkipsEmptyKeys(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "k123" || pass != "secret" {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		if got := r.FormValue("scope"); got != "devices:core:read" {
			http.Error(w, "wrong scope: "+got, http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	})
	mux.HandleFunc("/devices", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			http.Error(w, "bad auth header", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("cursor") == "" {
			w.Write([]byte(`{"devices":[{"nodeKey":"aaaa"}],"nextCursor":"page2"}`))
			return
		}
		w.Write([]byte(`{"devices":[{"nodeKey":"bbbb"},{"nodeKey":""}],"nextCursor":""}`))
	})
	srv := withTestAPI(t, mux)

	c := &tsClient{clientID: "k123", clientSecret: "secret", httpClient: srv.Client()}
	keys, err := c.devices()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"nodekey:aaaa", "nodekey:bbbb"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("devices = %v, want %v", keys, want)
	}
}

func TestDevicesReportsStatusAndBodyOnUpstreamError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	})
	// Non-JSON error body, as an intermediary proxy would produce.
	mux.HandleFunc("/devices", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("forbidden by policy"))
	})
	srv := withTestAPI(t, mux)

	c := &tsClient{clientID: "k123", clientSecret: "secret", httpClient: srv.Client()}
	_, err := c.devices()
	if err == nil {
		t.Fatal("expected an error for the 403 response")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "forbidden by policy") {
		t.Errorf("error should carry both status and response body, got %v", err)
	}
}

func TestTokenReportsUpstreamError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_client"}`))
	})
	srv := withTestAPI(t, mux)

	c := &tsClient{clientID: "k123", clientSecret: "wrong", httpClient: srv.Client()}
	if _, err := c.token(); err == nil || !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("token error should carry the upstream message, got %v", err)
	}
}
