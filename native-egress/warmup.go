package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func warmupLog(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[native-egress] "+format+"\n", args...)
}

// warmupPreloadJS intercepts globalThis.fetch inside the CC CLI process and
// writes the first large POST /v1/messages body (>10 KB = the main sonnet
// request, not the small haiku routing request) to a temp file, then restores
// the original fetch.  Loaded via NODE_OPTIONS=--require.
const warmupPreloadJS = `const _of=globalThis.fetch,_fs=require("fs");
globalThis.fetch=async function(u,o){
if(typeof u==="string"&&u.includes("/v1/messages")&&o&&typeof o.body==="string"&&o.body.length>10000){
try{_fs.writeFileSync(process.env._NE_BODY_PATH||"/tmp/ne_warmup_body.json",o.body)}catch(e){}
globalThis.fetch=_of}
return _of.apply(this,arguments)};
`

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
	start := time.Now()
	tmpDir := os.TempDir()
	preloadPath := filepath.Join(tmpDir, "ne_warmup_preload.cjs")
	bodyPath := filepath.Join(tmpDir, "ne_warmup_body.json")

	os.Remove(bodyPath)
	if err := os.WriteFile(preloadPath, []byte(warmupPreloadJS), 0644); err != nil {
		warmupLog("warmup: write preload: %v", err)
		return false
	}
	defer os.Remove(preloadPath)
	defer os.Remove(bodyPath)

	nodeOpts := "--require " + preloadPath
	if existing := os.Getenv("NODE_OPTIONS"); existing != "" {
		nodeOpts = existing + " " + nodeOpts
	}

	cmd := exec.Command(claudePath, "-p", "hi")
	cmd.Env = append(append([]string{}, osEnviron()...),
		"ANTHROPIC_LOG=debug",
		"CLAUDE_CONFIG_DIR="+resolveConfigDir(configDir),
		"NODE_OPTIONS="+nodeOpts,
		"_NE_BODY_PATH="+bodyPath,
	)

	warmupLog("warmup: running %s -p hi ...", claudePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		warmupLog("warmup: claude exited with error: %v (output: %d bytes)", err, len(out))
	}

	fp, ok := ParseFingerprint(string(out))
	if !ok {
		warmupLog("warmup: fingerprint parse failed (CC not logged in?)")
		return false
	}

	fpCache.mu.Lock()
	fpCache.entries["default"] = fpEntry{fp: fp, capturedAt: time.Now()}
	fpCache.mu.Unlock()

	fpVersion := ExtractVersionFromUA(fp["user-agent"])
	fpBetas := fp["anthropic-beta"]
	fpNodeVer := fp["x-stainless-runtime-version"]
	warmupLog("warmup: fingerprint learned (CC %s, node %s)", fpVersion, fpNodeVer)

	bodyData, err := os.ReadFile(bodyPath)
	if err != nil || len(bodyData) == 0 {
		warmupLog("warmup: body dump not found (CC binary may not support NODE_OPTIONS) — using builtin body template")
		return true // fingerprint captured; body falls back to builtin (same as the proven-safe baseline)
	}

	btCache.LearnFromCC(bodyData, fpVersion, fpBetas, fpNodeVer)
	warmupLog("warmup: body template learned (%d bytes, %d tools) in %s",
		len(bodyData), countTemplateTools(bodyData), time.Since(start).Round(time.Millisecond))
	return true
}

func countTemplateTools(body []byte) int {
	var parsed struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		return len(parsed.Tools)
	}
	return -1
}
