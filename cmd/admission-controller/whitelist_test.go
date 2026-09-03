package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// admit is a test helper: run a key through allowed() and keep only the
// verdict.
func admit(w *whitelist, key string) bool {
	allow, _ := w.allowed(key)
	return allow
}

func TestNormalizeKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"abc", "nodekey:abc"},
		{"nodekey:abc", "nodekey:abc"},
		{"  nodekey:abc  ", "nodekey:abc"},
	}
	for _, c := range cases {
		if got := normalizeKey(c.in); got != c.want {
			t.Errorf("normalizeKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseWhitelist(t *testing.T) {
	input := "# whole-line comment\n" +
		"; also a comment\n" +
		"nodekey:aaa  # trailing comment\n" +
		"bbb ; trailing semicolon comment\n" +
		"\n" +
		"nodekey:aaa\n" // duplicate
	keys := parseWhitelist(input)
	if len(keys) != 2 {
		t.Fatalf("parseWhitelist got %d keys, want 2: %v", len(keys), keys)
	}
	for _, k := range []string{"nodekey:aaa", "nodekey:bbb"} {
		if !keys[k] {
			t.Errorf("parseWhitelist missing %q, got %v", k, keys)
		}
	}
}

// writeWhitelist writes the file and bumps its mtime well past the last
// recorded mod time, since same-size edits within one mtime granularity
// window would otherwise look unchanged.
func writeWhitelist(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
}

func TestLoadIfChangedDropsVanishedManualKeysKeepsSynced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whitelist.txt")
	wl := newWhitelist(path)

	writeWhitelist(t, path, "nodekey:aaa\nnodekey:bbb\n")
	wl.loadIfChanged()
	if !admit(wl, "nodekey:aaa") {
		t.Fatal("aaa should be allowed after first load")
	}

	wl.replaceForSource("ts:k123", []string{"nodekey:sync1"})

	writeWhitelist(t, path, "nodekey:aaa\n")
	wl.loadIfChanged()
	if admit(wl, "nodekey:bbb") {
		t.Error("bbb vanished from the file, should be denied after reload")
	}
	if !admit(wl, "nodekey:aaa") {
		t.Error("aaa should survive the reload")
	}
	if !admit(wl, "nodekey:sync1") {
		t.Error("synced keys must not be affected by file reloads")
	}
}

func TestReplaceForSourceScopesKeysToSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whitelist.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	wl := newWhitelist(path)
	wl.loadIfChanged()

	wl.replaceForSource("ts:kaaa", []string{"nodekey:a1", "nodekey:a2"})
	wl.replaceForSource("ts:kbbb", []string{"nodekey:b1"})
	wl.replaceForSource("ts:kaaa", []string{"nodekey:a2", "nodekey:a3"})

	if !admit(wl, "nodekey:a2") || admit(wl, "nodekey:a1") || !admit(wl, "nodekey:a3") {
		t.Error("ts:kaaa replacement should swap exactly its own keys")
	}
	if !admit(wl, "nodekey:b1") {
		t.Error("ts:kbbb keys must survive another source's replacement")
	}
}

func TestRecordSyncErrorKeepsListAndSurfacesInStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whitelist.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	wl := newWhitelist(path)
	wl.loadIfChanged()

	wl.replaceForSource("ts:kaaa", []string{"nodekey:a1"})
	wl.recordSyncError("ts:kaaa", errors.New("401: credential is not valid"))

	if !admit(wl, "nodekey:a1") {
		t.Error("a failed sync round must keep the previous list")
	}
	status := wl.statusLine()
	if !strings.Contains(status, "last_error=401: credential is not valid") {
		t.Errorf("status should surface the sync error, got %q", status)
	}

	wl.replaceForSource("ts:kaaa", []string{"nodekey:a1"})
	status = wl.statusLine()
	if strings.Contains(status, "last_error") {
		t.Errorf("a successful round should clear last_error, got %q", status)
	}
}

func TestAllowedReportsFirstAdmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whitelist.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	wl := newWhitelist(path)
	wl.loadIfChanged()
	wl.replaceForSource("ts:kaaa", []string{"nodekey:a1"})

	if allow, first := wl.allowed("nodekey:a1"); !allow || !first {
		t.Errorf("first admission: got (%v, %v), want (true, true)", allow, first)
	}
	if allow, first := wl.allowed("nodekey:a1"); !allow || first {
		t.Errorf("repeat admission: got (%v, %v), want (true, false)", allow, first)
	}
	if allow, _ := wl.allowed("nodekey:ffff"); allow {
		t.Error("unknown key must be denied")
	}
}

func TestUnreadableFileIsFlaggedAndRecovers(t *testing.T) {
	dir := t.TempDir()

	wl := newWhitelist(filepath.Join(dir, "missing.txt"))
	wl.loadIfChanged()
	if wl.statErr == "" {
		t.Error("a missing whitelist file should set statErr so a warning is logged")
	}

	path := filepath.Join(dir, "whitelist.txt")
	writeWhitelist(t, path, "nodekey:aaa\n")
	wl.path = path
	wl.loadIfChanged()
	if wl.statErr != "" {
		t.Errorf("statErr should clear once the file is readable, got %q", wl.statErr)
	}
	if !admit(wl, "nodekey:aaa") {
		t.Error("key from the recovered file should be allowed")
	}
}
