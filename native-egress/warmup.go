package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

func warmupLog(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[native-egress] "+format+"\n", args...)
}

var warmupKick = make(chan struct{}, 1)

// TriggerWarmup wakes the warmup loop to re-capture immediately. The proxy fires
// POST /warmup right after an account is imported so capture happens at once.
func TriggerWarmup() {
	select {
	case warmupKick <- struct{}{}:
	default:
	}
}

// warmupLoop runs warmupTemplate until it FIRST succeeds (live fingerprint
// captured), retrying every 30s — so an account imported after startup is still
// picked up (fixes "fingerprint capture failed" when the account arrives late).
// After the first success it stops auto-retrying and only re-captures on an
// explicit kick (POST /warmup). No periodic refresh: repeatedly running the CLI
// re-touches the account's OAuth token every cycle for no benefit.
func warmupLoop(claudePath, configDir string, fpCache *FPCache, btCache *BodyTemplateCache) {
	for {
		if warmupTemplate(claudePath, configDir, fpCache, btCache) {
			warmupLog("warmup: SUCCESS — live fingerprint captured from real CLI")
			break
		}
		warmupLog("warmup: not ready (account not imported yet?) — retry in 30s (POST /warmup to retry now)")
		select {
		case <-warmupKick:
			warmupLog("warmup: kick received, retrying now")
		case <-time.After(30 * time.Second):
		}
	}
	// Captured. Only re-capture on an explicit kick (e.g. account re-import).
	// No timer — a settled account is never poked again on its own.
	for range warmupKick {
		if warmupTemplate(claudePath, configDir, fpCache, btCache) {
			warmupLog("warmup: re-capture SUCCESS")
		} else {
			warmupLog("warmup: re-capture failed — keeping previous template")
		}
	}
}

// warmupTemplate runs `claude -p "hi"` once to learn the live fingerprint AND
// body template from a genuine CC request. Returns true once the fingerprint is
// captured (body is best-effort — falls back to the builtin template). Failures
// are non-fatal (builtin fallbacks remain) and the loop retries.
func warmupTemplate(claudePath, configDir string, fpCache *FPCache, btCache *BodyTemplateCache) bool {
	fp, bodyData := captureAll(claudePath, configDir)
	if fp == nil {
		warmupLog("warmup: fingerprint capture failed (CC not logged in?)")
		return false
	}
	fpCache.mu.Lock()
	fpCache.entries["default"] = fpEntry{fp: fp, capturedAt: time.Now()}
	fpCache.mu.Unlock()

	fpVersion := ExtractVersionFromUA(fp["user-agent"])
	warmupLog("warmup: fingerprint learned (CC %s)", fpVersion)

	if len(bodyData) == 0 {
		warmupLog("warmup: body capture empty — using builtin body template")
		return true
	}
	btCache.LearnFromCC(bodyData, fpVersion, fp["anthropic-beta"], fp["x-stainless-runtime-version"])
	warmupLog("warmup: body template learned (%d bytes)", len(bodyData))
	return true
}

// captureAll runs `claude -p hi` with ANTHROPIC_BASE_URL pointed at a local
// server so we capture BOTH the request headers (→ fingerprint) and the full
// body (→ template) from one genuine request. No real API call is made.
func captureAll(claudePath, configDir string) (Fingerprint, []byte) {
	var mu sync.Mutex
	var capturedBody []byte
	var capturedFP Fingerprint

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		if len(body) > len(capturedBody) {
			capturedBody = body
			fp := Fingerprint{}
			for k, vals := range r.Header {
				kl := strings.ToLower(k)
				if excluded[kl] || len(vals) == 0 {
					continue
				}
				fp[kl] = vals[0]
			}
			if ua := fp["user-agent"]; ua != "" && strings.HasPrefix(ua, "claude-cli/") {
				capturedFP = fp
			}
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"msg_warmup","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-sonnet-4-6","stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}`))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		warmupLog("warmup: listen error: %v", err)
		return nil, nil
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	addr := ln.Addr().String()
	cmd := exec.Command(claudePath, "-p", "hi")
	cmd.Env = append(append([]string{}, osEnviron()...),
		"ANTHROPIC_BASE_URL=http://"+addr,
		"CLAUDE_CONFIG_DIR="+resolveConfigDir(configDir),
	)
	cmd.CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	return capturedFP, capturedBody
}
