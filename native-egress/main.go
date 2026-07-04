package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// sessionStore provides stable per-account session IDs (generated once, reused).
var sessionStore = struct {
	mu  sync.Mutex
	ids map[string]string
}{ids: map[string]string{}}

func stableSessionID(account string) string {
	sessionStore.mu.Lock()
	defer sessionStore.mu.Unlock()
	if id, ok := sessionStore.ids[account]; ok {
		return id
	}
	id := uuid.NewString()
	sessionStore.ids[account] = id
	return id
}

func newServer() http.Handler {
	claudePath := os.Getenv("CLAUDE_PATH")
	if claudePath == "" {
		claudePath = "claude"
	}
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = home + "/.claude"
	}

	fpCache := NewFPCache(10*time.Minute, defaultCapture(claudePath, configDir))
	bodyTmpl := NewBodyTemplateCache(10 * time.Minute)
	transport := NewUTLSTransport()

	go warmupLoop(claudePath, configDir, fpCache, bodyTmpl)

	deps := RelayDeps{
		Transport:    transport,
		FP:           fpCache,
		BodyTemplate: bodyTmpl,
		SessionID:    stableSessionID,
		Now:          time.Now,
		Datadog:      NewDatadogEmitter(transport, bodyTmpl, fpCache),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	// POST /warmup: re-run capture now (fired by the proxy right after an account
	// is imported so the live fingerprint is captured without waiting for retry).
	mux.HandleFunc("/warmup", func(w http.ResponseWriter, _ *http.Request) {
		TriggerWarmup()
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/relay", relayHandler(deps))
	return mux
}

// resolveAddr determines the listen address. Priority:
//  1. NATIVE_EGRESS_ADDR env var (full addr override)
//  2. --port flag (binds 127.0.0.1:<port>)
//  3. default: 127.0.0.1:0 (random port, only useful in tests)
func resolveAddr(port int, envAddr string) string {
	if envAddr != "" {
		return envAddr
	}
	if port != 0 {
		return fmt.Sprintf("127.0.0.1:%d", port)
	}
	return "127.0.0.1:0"
}

func main() {
	port := flag.Int("port", 0, "port to listen on (overridden by NATIVE_EGRESS_ADDR)")
	flag.Parse()

	addr := resolveAddr(*port, os.Getenv("NATIVE_EGRESS_ADDR"))
	fmt.Fprintf(os.Stderr, "native-egress listening on %s\n", addr)
	if err := http.ListenAndServe(addr, newServer()); err != nil {
		fmt.Fprintln(os.Stderr, "native-egress exited:", err)
		os.Exit(1)
	}
}
