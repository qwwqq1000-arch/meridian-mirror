package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	ddLogsURL = "https://http-intake.logs.us5.datadoghq.com/api/v2/logs"
	ddAPIKey  = "pubea5604404508cdd34afb69e6f42a05bc"
)

type ddEvent map[string]interface{}

type DatadogEmitter struct {
	transport    http.RoundTripper
	bodyTemplate *BodyTemplateCache
	fp           *FPCache
	startTime    time.Time
	platform     string
	arch         string
	distroID     string
	distroVer    string
	kernel       string
	deployEnv    string
}

func NewDatadogEmitter(transport http.RoundTripper, bt *BodyTemplateCache, fp *FPCache) *DatadogEmitter {
	d := &DatadogEmitter{
		transport:    transport,
		bodyTemplate: bt,
		fp:           fp,
		startTime:    time.Now(),
		platform:     runtime.GOOS,
		arch:         runtime.GOARCH,
	}
	if d.arch == "amd64" {
		d.arch = "x64"
	}
	d.deployEnv = "unknown-" + d.platform

	if d.platform == "linux" {
		if b, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(line, "ID=") {
					d.distroID = strings.Trim(strings.TrimPrefix(line, "ID="), "\"")
				}
				if strings.HasPrefix(line, "VERSION_ID=") {
					d.distroVer = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), "\"")
				}
			}
		}
		if b, err := os.ReadFile("/proc/version"); err == nil {
			parts := strings.Fields(string(b))
			if len(parts) > 2 {
				d.kernel = parts[2]
			}
		}
	}
	return d
}

// version reads the current CLI version from FP headers (dynamic).
func (d *DatadogEmitter) version() string {
	if fp := d.fp.Peek(); fp != nil {
		if v := ExtractVersionFromUA(fp["user-agent"]); v != "" {
			return v
		}
	}
	return "2.1.187"
}

// nodeVersion reads from FP headers (dynamic).
func (d *DatadogEmitter) nodeVersion() string {
	if fp := d.fp.Peek(); fp != nil {
		if nv := fp["x-stainless-runtime-version"]; nv != "" {
			return nv
		}
	}
	return "v24.3.0"
}

// betas reads from FP headers (dynamic).
func (d *DatadogEmitter) betas(eventName string) string {
	baseBetas := ""
	if fp := d.fp.Peek(); fp != nil {
		baseBetas = fp["anthropic-beta"]
	}
	if baseBetas == "" {
		baseBetas = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14," +
			"thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05"
	}
	if eventName == "tengu_api_success" {
		return baseBetas
	}
	parts := strings.Split(baseBetas, ",")
	if len(parts) > 6 {
		parts = parts[:6]
	}
	return strings.Join(parts, ",")
}

// buildTime derived from CLI version's build timestamp.
func (d *DatadogEmitter) buildTime() time.Time {
	// Approximate: use start time minus some offset. In practice this is
	// close enough — the real CLI does (now - buildTimestamp).
	return d.startTime.Add(-1000 * time.Minute)
}

func (d *DatadogEmitter) buildAgeMinutes() int {
	return int(time.Since(d.buildTime()).Minutes())
}

func stableUserBucket(sessionID string) int {
	h := sha256.Sum256([]byte("dd-bucket:" + sessionID))
	return int(binary.BigEndian.Uint32(h[:4]) % 100)
}

func (d *DatadogEmitter) baseEvent(sessionID, model string, eventName string) ddEvent {
	bucket := stableUserBucket(sessionID)
	version := d.version()
	betas := d.betas(eventName)

	entrypoint := "sdk-cli"
	if eventName == "tengu_init" {
		entrypoint = "claude"
	}

	tags := fmt.Sprintf("event:%s,arch:%s,client_type:sdk-cli,entrypoint:%s,model:%s,platform:%s,user_bucket:%d,user_type:external,version:%s,version_base:%s",
		eventName, d.arch, entrypoint, model, d.platform, bucket, version, version)
	if eventName == "tengu_api_success" {
		tags = fmt.Sprintf("event:%s,arch:%s,client_type:sdk-cli,entrypoint:sdk-cli,model:%s,platform:%s,provider:firstParty,user_bucket:%d,user_type:external,version:%s,version_base:%s",
			eventName, d.arch, model, d.platform, bucket, version, version)
	}

	e := ddEvent{
		"ddsource":               "nodejs",
		"ddtags":                 tags,
		"service":                "claude-code",
		"hostname":               "claude-code",
		"env":                    "external",
		"model":                  model,
		"session_id":             sessionID,
		"user_type":              "external",
		"betas":                  betas,
		"entrypoint":             entrypoint,
		"is_interactive":         "false",
		"client_type":            "sdk-cli",
		"platform":               d.platform,
		"platform_raw":           d.platform,
		"arch":                   d.arch,
		"node_version":           d.nodeVersion(),
		"terminal":               "ssh-session",
		"shell":                  "bash",
		"package_managers":       "npm",
		"runtimes":               "bun,node",
		"is_running_with_bun":    true,
		"is_ci":                  false,
		"is_claubbit":            false,
		"is_claude_code_remote":  false,
		"is_local_agent_mode":    false,
		"is_conductor":           false,
		"is_github_action":       false,
		"is_claude_code_action":  false,
		"is_claude_ai_auth":      true,
		"version":                version,
		"version_base":           version,
		"build_time":             d.buildTime().Format(time.RFC3339),
		"deployment_environment": d.deployEnv,
		"swe_bench_run_id":       "",
		"swe_bench_instance_id":  "",
		"swe_bench_task_id":      "",
		"user_bucket":            bucket,
	}

	if d.platform == "linux" {
		e["linux_kernel"] = d.kernel
		e["linux_distro_version"] = d.distroVer
		e["linux_distro_id"] = d.distroID
	}

	return e
}

func (d *DatadogEmitter) processMetrics(uptimeSec float64) ddEvent {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return ddEvent{
		"uptime":            uptimeSec,
		"rss":               m.Sys,
		"heapTotal":         m.HeapSys,
		"heapUsed":          m.HeapAlloc,
		"external":          m.StackSys,
		"arrayBuffers":      870,
		"constrainedMemory": m.Sys,
		"cpuUsage":          ddEvent{"user": 200000 + rand.Intn(100000), "system": 40000 + rand.Intn(20000)},
	}
}

// featureNames returns the feature list — from real CLI capture or fallback.
var defaultFeatureNames = []string{
	"plugin_load_all", "plugin_load_workflows", "plugin_load_commands",
	"plugin_load_hooks", "context_git_detect", "skill_load_commands_dir",
	"plugin_load_skills", "cmd_load", "plugin_load_agents",
	"startup_resolve_model", "lsp_config_load", "lsp_init",
	"lsp_diagnostics_register", "output_style_load", "plugin_load_output_styles",
	"memory_load_prompt", "context_claude_md_load", "mcp_claudeai_fetch_configs",
	"cmd_load", "provider_route", "policy_limits_load",
	"remote_managed_settings_pull", "api_bootstrap_fetch", "mcp_registry_fetch",
	"api_request", "hook_stop_handler", "turn",
	"lsp_shutdown", "swarm_session_cleanup",
}

func (d *DatadogEmitter) EmitAfterRelay(sessionID, model, requestID, stopReason string,
	inputTokens, outputTokens, cachedTokens, toolUseCount int,
	durationMs int64, inputCharLen int) {
	go func() {
		uptimeSec := time.Since(d.startTime).Seconds()
		features := defaultFeatureNames

		events := make([]ddEvent, 0, 37)

		// 1. tengu_init
		init := d.baseEvent(sessionID, model, "tengu_init")
		init["message"] = "tengu_init"
		init["has_initial_prompt"] = true
		init["has_stdin"] = true
		init["verbose"] = false
		init["debug"] = false
		init["debug_to_stderr"] = false
		init["print"] = true
		init["output_format"] = "text"
		init["input_format"] = "text"
		init["num_allowed_tools"] = 0
		init["num_disallowed_tools"] = 0
		init["mcp_client_count"] = 0
		init["worktree"] = false
		init["github_action_inputs_present"] = false
		init["dangerously_skip_permissions_passed"] = false
		init["permission_mode"] = "default"
		init["mode_is_bypass"] = false
		init["in_protected_namespace"] = false
		init["api_key_source"] = "none"
		init["allow_dangerously_skip_permissions_passed"] = false
		init["thinking_type"] = "adaptive"
		init["renderer_entry_path"] = "gb_on"
		init["auto_updates_channel"] = "latest"
		init["process_metrics"] = d.processMetrics(uptimeSec - float64(durationMs)/1000.0 - 0.1)
		events = append(events, init)

		// 2. tengu_started
		started := d.baseEvent(sessionID, model, "tengu_started")
		started["message"] = "tengu_started"
		started["process_metrics"] = d.processMetrics(uptimeSec - float64(durationMs)/1000.0)
		events = append(events, started)

		// 3. feature_ok events
		for i, name := range features {
			fe := d.baseEvent(sessionID, model, "tengu_feature_ok")
			fe["message"] = "tengu_feature_ok"
			fe["feature_name"] = name
			progress := float64(i) / float64(len(features))
			fe["process_metrics"] = d.processMetrics(uptimeSec - float64(durationMs)/1000.0*(1.0-progress))
			events = append(events, fe)
		}

		// 4. tengu_sdk_ttft
		ttftMs := durationMs - int64(100+rand.Intn(200))
		if ttftMs < 100 {
			ttftMs = 100
		}
		ttft := d.baseEvent(sessionID, model, "tengu_sdk_ttft")
		ttft["message"] = "tengu_sdk_ttft"
		ttft["ttft_ms"] = ttftMs
		ttft["process_metrics"] = d.processMetrics(uptimeSec)
		events = append(events, ttft)

		// 5. tengu_api_success
		if stopReason == "" {
			stopReason = "end_turn"
		}
		cost := float64(cachedTokens)*0.0000003 + float64(maxInt(0, inputTokens-cachedTokens))*0.000003 + float64(outputTokens)*0.000015
		apiOk := d.baseEvent(sessionID, model, "tengu_api_success")
		apiOk["message"] = "tengu_api_success"
		apiOk["message_count"] = 1
		apiOk["message_tokens"] = 0
		apiOk["input_tokens"] = inputTokens
		apiOk["output_tokens"] = outputTokens
		apiOk["cached_input_tokens"] = cachedTokens
		apiOk["uncached_input_tokens"] = maxInt(0, inputTokens-cachedTokens)
		apiOk["duration_ms"] = durationMs
		apiOk["duration_ms_including_retries"] = durationMs + int64(rand.Intn(10))
		apiOk["attempt"] = 1
		apiOk["ttft_ms"] = ttftMs
		apiOk["build_age_mins"] = d.buildAgeMinutes()
		apiOk["provider"] = "firstParty"
		apiOk["request_id"] = requestID
		apiOk["stop_reason"] = stopReason
		apiOk["cost_u_s_d"] = cost
		apiOk["did_fall_back_to_non_streaming"] = false
		apiOk["is_non_interactive_session"] = true
		apiOk["print"] = true
		apiOk["is_t_t_y"] = false
		apiOk["query_source"] = "sdk"
		apiOk["query_chain_id"] = sessionID
		apiOk["query_depth"] = 0
		apiOk["permission_mode"] = "default"
		apiOk["global_cache_strategy"] = "system_prompt"
		apiOk["text_content_length"] = outputTokens * 4
		apiOk["image_block_count"] = 0
		apiOk["image_total_pixels"] = 0
		apiOk["image_total_bytes"] = 0
		apiOk["document_block_count"] = 0
		apiOk["document_total_bytes"] = 0
		apiOk["input_text_char_length"] = inputCharLen
		apiOk["estimated_input_tokens"] = inputTokens
		apiOk["fast_mode"] = false
		apiOk["process_metrics"] = d.processMetrics(uptimeSec)
		events = append(events, apiOk)

		// 6. tengu_sdk_result
		result := d.baseEvent(sessionID, model, "tengu_sdk_result")
		result["message"] = "tengu_sdk_result"
		result["subtype"] = "success"
		result["is_error"] = false
		result["num_turns"] = 1
		result["duration_ms"] = durationMs + 50 + int64(rand.Intn(30))
		result["duration_api_ms"] = durationMs + int64(rand.Intn(10))
		result["saw_retry"] = false
		result["saw_compact"] = false
		result["tool_use_count"] = toolUseCount
		result["mcp_tool_calls"] = 0
		result["toolsearch_calls"] = 0
		result["builtin_tool_calls"] = toolUseCount
		result["process_metrics"] = d.processMetrics(uptimeSec)
		events = append(events, result)

		// 7. tengu_timer events
		for _, timer := range []struct {
			name string
			ms   int
		}{
			{"plugins_init", 5 + rand.Intn(10)},
			{"mcp_prewait", rand.Intn(5)},
		} {
			te := d.baseEvent(sessionID, model, "tengu_timer")
			te["message"] = "tengu_timer"
			te["event"] = timer.name
			te["duration_ms"] = timer.ms
			te["headless"] = true
			te["process_metrics"] = d.processMetrics(uptimeSec)
			events = append(events, te)
		}

		// 8. tengu_headless_mcp_prewait
		mcp := d.baseEvent(sessionID, model, "tengu_headless_mcp_prewait")
		mcp["message"] = "tengu_headless_mcp_prewait"
		mcp["local_only"] = false
		mcp["will_defer_mcp"] = true
		mcp["pending_before"] = 0
		mcp["pending_waited_before"] = 0
		mcp["tools_before"] = 0
		mcp["waited_ms"] = 0
		mcp["pending_after"] = 0
		mcp["pending_waited_after"] = 0
		mcp["tools_after"] = 0
		mcp["mcp_non_blocking"] = true
		mcp["process_metrics"] = d.processMetrics(uptimeSec)
		events = append(events, mcp)

		d.send(events)
	}()
}

func (d *DatadogEmitter) send(events []ddEvent) {
	body, err := json.Marshal(events)
	if err != nil {
		logDD("datadog marshal error: %v", err)
		return
	}

	req, err := http.NewRequest("POST", ddLogsURL, bytes.NewReader(body))
	if err != nil {
		logDD("datadog request error: %v", err)
		return
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "axios/1.15.2")
	req.Header.Set("dd-api-key", ddAPIKey)

	resp, err := d.transport.RoundTrip(req)
	if err != nil {
		logDD("datadog send error: %v", err)
		return
	}
	resp.Body.Close()
	logDD("datadog sent %d events, status=%d", len(events), resp.StatusCode)
}

func logDD(format string, args ...interface{}) {
	v := os.Getenv("MERIDIAN_NATIVE_DEBUG")
	if v == "1" || v == "true" {
		fmt.Fprintf(os.Stderr, "[native-egress] "+format+"\n", args...)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
