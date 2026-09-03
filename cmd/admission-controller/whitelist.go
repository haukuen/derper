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
	seen    map[string]bool
	statErr string
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
	return &whitelist{sources: map[string]string{}, path: path, syncs: map[string]*syncStatus{}, seen: map[string]bool{}}
}

// loadIfChanged re-reads the whitelist file if it changed on disk. Only
// manual entries are affected; synced entries live in memory only.
func (w *whitelist) loadIfChanged() {
	st, err := os.Stat(w.path)
	if err != nil {
		w.noteUnreadable(err)
		return
	}
	w.noteReadable()
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

// noteUnreadable warns when the whitelist file cannot be stat'ed (missing
// file, wrong bind mount). Warnings are deduplicated so a persistent
// condition does not flood the log; the in-memory list keeps serving and
// unknown keys stay fail-closed.
func (w *whitelist) noteUnreadable(err error) {
	msg := err.Error()
	w.mu.Lock()
	prev := w.statErr
	w.statErr = msg
	w.mu.Unlock()
	if msg != prev {
		log.Printf("whitelist file %s unreadable, keeping current in-memory entries until it returns: %v", w.path, err)
	}
}

// noteReadable logs the recovery after a period of unreadability, so
// operators notice when the file (or its mount) came back.
func (w *whitelist) noteReadable() {
	w.mu.Lock()
	prev := w.statErr
	w.statErr = ""
	w.mu.Unlock()
	if prev != "" {
		log.Printf("whitelist file %s readable again", w.path)
	}
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

// allowed reports whether the key is allowlisted by any source, and
// whether this is the first admission for it since process start (used
// for an ALLOW log line; seen keys are bounded by the list size).
func (w *whitelist) allowed(key string) (bool, bool) {
	w.loadIfChanged()
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.sources[key]; !ok {
		return false, false
	}
	first := !w.seen[key]
	w.seen[key] = true
	return true, first
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
