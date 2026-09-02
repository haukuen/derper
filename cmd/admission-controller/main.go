// Command admission-controller is a centralized allowlist service for
// derper's --verify-client-url option. derper POSTs every new client's
// node key and source IP here; we answer {"Allow":true/false} based on a
// whitelist file that is re-read on change (edit + save = instant reload,
// no restart).
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
	"strings"
	"time"
)

var (
	listen        = flag.String("listen", ":8081", "listen address for the admission controller HTTP API")
	whitelistPath = flag.String("whitelist", "/etc/derper/whitelist.txt", "path to the node key whitelist file")
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
		fmt.Fprintf(rw, "whitelist entries: %d\n", wl.count())
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
