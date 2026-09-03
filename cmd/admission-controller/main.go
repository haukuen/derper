// Command admission-controller is a centralized allowlist service for
// derper's --verify-client-url option. derper POSTs every new client's
// node key and source IP here; we answer {"Allow":true/false} based on
// the allowlist: manual entries from a whitelist file (hot-reloaded on
// edit) plus optional per-tailnet lists pulled from the Tailscale API.
//
// The admission protocol (tailcfg.DERPAdmitClientRequest/Response) is two
// plain JSON fields, so this binary deliberately has no tailscale
// dependency: it works with any derper version and keeps the Docker
// build small and reproducible.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	listen        = flag.String("listen", envOr("ADMIT_LISTEN", ":8081"), "listen address for the admission controller HTTP API")
	whitelistPath = flag.String("whitelist", envOr("ADMIT_WHITELIST", "/etc/derper/whitelist.txt"), "path to the node key whitelist file")
)

// admitRequest mirrors tailcfg.DERPAdmitClientRequest. NodePublic arrives
// as the string form "nodekey:<64 hex chars>"; Source is the client IP.
type admitRequest struct {
	NodePublic string `json:"NodePublic"`
	Source     string `json:"Source"`
}

func main() {
	flag.Parse()
	wl := newWhitelist(*whitelistPath)
	wl.loadIfChanged()

	// Optional Tailscale device sync; nil when unconfigured.
	syncClients := startSyncLoop(wl, os.Getenv("TS_SYNC_CLIENTS"), syncInterval())

	admit := func(rw http.ResponseWriter, r *http.Request) {
		var req admitRequest
		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<10))
		if err != nil {
			http.Error(rw, "bad request", http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(rw, "bad request", http.StatusBadRequest)
			return
		}
		key := normalizeKey(req.NodePublic)
		allow := wl.allowed(key)
		if !allow {
			log.Printf("DENY nodekey=%s source=%s", shortKey(key), req.Source)
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.Write([]byte(fmt.Sprintf(`{"Allow":%t}`, allow)))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/verify", admit)
	mux.HandleFunc("/verify/", admit)
	mux.HandleFunc("/", admit)
	mux.HandleFunc("/healthz", func(rw http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(rw, "ok")
	})
	mux.HandleFunc("/status", func(rw http.ResponseWriter, r *http.Request) {
		fmt.Fprint(rw, wl.statusLine())
		if syncClients != nil {
			for id := range syncClients {
				fmt.Fprintf(rw, "sync client %s configured\n", id)
			}
		}
	})

	srv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	log.Printf("admission controller listening on %s, whitelist %s", *listen, *whitelistPath)
	log.Fatal(srv.ListenAndServe())
}

// envOr returns the environment variable's value or a fallback, so the
// Dockerfile's ENV defaults actually take effect without flags.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// normalizeKey canonicalizes a node key to the "nodekey:<hex>" form.
func normalizeKey(k string) string {
	k = strings.TrimSpace(k)
	if k != "" && !strings.HasPrefix(k, "nodekey:") {
		k = "nodekey:" + k
	}
	return k
}

// shortKey shortens a key for logs, e.g. "nodekey:[111111111111]".
func shortKey(k string) string {
	if len(k) > 8+12 {
		return k[:8] + "[" + k[8:8+12] + "]"
	}
	return k
}
