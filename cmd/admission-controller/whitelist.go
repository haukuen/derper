package main

import (
	"log"
	"os"
	"strings"
	"sync"
	"time"
)
// whitelist is a set of node keys loaded from a text file, reloaded
// automatically whenever the file's mtime or size changes.
type whitelist struct {
	mu   sync.RWMutex
	keys map[string]bool
	mod  time.Time
	size int64
	path string
}

func newWhitelist(path string) *whitelist {
	return &whitelist{keys: map[string]bool{}, path: path}
}


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
	w.keys, w.mod, w.size = keys, st.ModTime(), st.Size()
	w.mu.Unlock()
	log.Printf("whitelist loaded: %d key(s)", len(keys))
}

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

func (w *whitelist) allowed(key string) bool {
	w.loadIfChanged()
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.keys[key]
}

func (w *whitelist) count() int {
	w.loadIfChanged()
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.keys)
}
