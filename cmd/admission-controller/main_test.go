package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShortKey(t *testing.T) {
	full := "nodekey:" + strings.Repeat("a", 64)
	if got := shortKey(full); got != "nodekey:[aaaaaaaaaaaa]" {
		t.Errorf("shortKey(full) = %q, want nodekey:[aaaaaaaaaaaa]", got)
	}
	if got := shortKey("nodekey:short"); got != "nodekey:short" {
		t.Errorf("shortKey(short) should be returned as-is, got %q", got)
	}
}

func newTestWhitelist(t *testing.T, content string) *whitelist {
	t.Helper()
	path := filepath.Join(t.TempDir(), "whitelist.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return newWhitelist(path)
}

func TestHandleAdmit(t *testing.T) {
	wl := newTestWhitelist(t, "nodekey:aaaa\n")
	handler := handleAdmit(wl)

	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body))
		handler(rr, req)
		return rr
	}

	rr := post(`{"NodePublic":"aaaa","Source":"10.0.0.1"}`)
	if rr.Code != http.StatusOK || rr.Body.String() != `{"Allow":true}` {
		t.Errorf("allowed key: got %d %q, want 200 {\"Allow\":true}", rr.Code, rr.Body.String())
	}

	rr = post(`{"NodePublic":"nodekey:ffff","Source":"10.0.0.2"}`)
	if rr.Code != http.StatusOK || rr.Body.String() != `{"Allow":false}` {
		t.Errorf("unknown key: got %d %q, want 200 {\"Allow\":false}", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	handler(rr, httptest.NewRequest(http.MethodGet, "/verify", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /verify: got %d, want 405", rr.Code)
	}

	rr = post("{not json")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed JSON: got %d, want 400", rr.Code)
	}
}

func TestNewMuxRoutes(t *testing.T) {
	wl := newTestWhitelist(t, "nodekey:aaaa\n")
	server := httptest.NewServer(newMux(wl, nil))
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /: got %d, want 404 (the catch-all admission route is gone)", resp.StatusCode)
	}

	resp, err = http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "ok" {
		t.Errorf("GET /healthz: got %d %q, want 200 ok", resp.StatusCode, body)
	}

	resp, err = http.Get(server.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /status: got %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "version: "+version) {
		t.Errorf("GET /status should report the build version %q, got %q", version, body)
	}
	if !strings.Contains(string(body), "manual: 1") {
		t.Errorf("GET /status should report the manual key count, got %q", body)
	}
}
