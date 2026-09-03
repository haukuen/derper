package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SourceManual marks entries loaded from the whitelist file.
const SourceManual = "manual"

// whitelist is the set of allowed node keys: manual entries come from the
// text file (re-read whenever its mtime or size changes) and synced
// entries come from the Tailscale API (in memory only). sources maps each
// key to where it came from.
type whitelist struct {
	mu      sync.RWMutex
	sources map[string]string
	mod     time.Time
	size    int64
	path    string
	syncs   map[string]*syncStatus
}

// syncStatus tracks the health of one sync source so that a failing sync
// keeps serving its last known list instead of silently clearing it.
type syncStatus struct {
	entries   int
	lastOK    time.Time
	lastErr   string
	lastErrAt time.Time
}

func newWhitelist(path string) *whitelist {
	return &whitelist{sources: map[string]string{}, path: path, syncs: map[string]*syncStatus{}}
}

// loadIfChanged re-reads the whitelist file if it changed on disk. Only
// manual entries are affected; synced entries live in memory only.
func (w *whitelist) loadIfChanged() {
	st, err := os.Stat(w.path)
	if err != nil {
		return
	}
	w.mu.RLock()
	unchanged := st.ModTime().Equal(w.mod) && st.Size() == w.size
	w.mu.RUnlock()
	if unchanged {
		return
	}

	data, err := os.ReadFile(w.path)
	if err != nil {
		log.Printf("whitelist read failed, keeping previous contents: %v", err)
		return
	}
	keys := parseWhitelist(string(data))
	w.mu.Lock()
	// Drop manual entries that vanished from the file, keep synced ones.
	for k, src := range w.sources {
		if src == SourceManual {
			if _, ok := keys[k]; !ok {
				delete(w.sources, k)
			}
		}
	}
	for k := range keys {
		w.sources[k] = SourceManual
	}
	w.mod, w.size = st.ModTime(), st.Size()
	w.mu.Unlock()
	log.Printf("whitelist loaded: %d manual key(s)", len(keys))
}

// parseWhitelist parses the file format: one key per line, whole-line
// comments starting with # or ;, trailing comments after # or ; ignored.
func parseWhitelist(s string) map[string]bool {
	keys := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = line[:i]
		}
		key := normalizeKey(line)
		if key == "" {
			continue
		}
		keys[key] = true
	}
	return keys
}

// replaceForSource atomically swaps all entries belonging to a sync
// source. In-memory only: the file stays the manual source of truth and
// the list is rebuilt on the first sync after a restart.
func (w *whitelist) replaceForSource(source string, keys []string) {
	w.mu.Lock()
	for k, src := range w.sources {
		if src == source {
			delete(w.sources, k)
		}
	}
	for _, k := range keys {
		w.sources[k] = source
	}
	st, ok := w.syncs[source]
	if !ok {
		st = &syncStatus{}
		w.syncs[source] = st
	}
	st.entries = len(keys)
	st.lastOK = time.Now()
	st.lastErr, st.lastErrAt = "", time.Time{}
	w.mu.Unlock()
	log.Printf("sync %s: %d key(s)", source, len(keys))
}

// recordSyncError notes a failed round for a source; its previous list
// stays in force until the next success.
func (w *whitelist) recordSyncError(source string, err error) {
	w.mu.Lock()
	st, ok := w.syncs[source]
	if !ok {
		st = &syncStatus{}
		w.syncs[source] = st
	}
	st.lastErr = err.Error()
	st.lastErrAt = time.Now()
	w.mu.Unlock()
	log.Printf("sync %s failed, keeping previous list: %v", source, err)
}

// allowed reports whether the key is allowlisted by any source.
func (w *whitelist) allowed(key string) bool {
	w.loadIfChanged()
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, ok := w.sources[key]
	return ok
}

// statusLine renders the /status body: total, manual and per-source counts.
func (w *whitelist) statusLine() string {
	w.loadIfChanged()
	w.mu.RLock()
	defer w.mu.RUnlock()
	manual := 0
	for _, src := range w.sources {
		if src == SourceManual {
			manual++
		}
	}
	var b strings.Builder
	b.WriteString("total entries: ")
	b.WriteString(strconv.Itoa(len(w.sources)))
	b.WriteString(" (manual: ")
	b.WriteString(strconv.Itoa(manual))
	b.WriteString(")\n")
	for src, st := range w.syncs {
		b.WriteString("sync " + src + ": entries=" + strconv.Itoa(st.entries))
		if st.lastErr != "" {
			b.WriteString(" last_error=" + st.lastErr + " at " + st.lastErrAt.UTC().Format(time.RFC3339))
		} else if !st.lastOK.IsZero() {
			b.WriteString(" last_success=" + st.lastOK.UTC().Format(time.RFC3339))
		}
		b.WriteString("\n")
	}
	return b.String()
}
