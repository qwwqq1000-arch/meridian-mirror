package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// RedactAuth returns a clone of h with the authorization header masked.
// The last 4 characters of the token are preserved for debugging; the rest
// is replaced with "Bearer ***<last4>".
// SECURITY: the raw token is NEVER logged — only the last 4 chars are shown.
func RedactAuth(h http.Header) http.Header {
	out := h.Clone()
	if a := out.Get("authorization"); a != "" {
		last := a
		if len(a) > 4 {
			last = a[len(a)-4:]
		}
		out.Set("authorization", "Bearer ***"+last)
	}
	return out
}

// logRelay logs the outgoing headers (with auth redacted) and the cloaked body
// to stderr. Gated by MERIDIAN_NATIVE_DEBUG (default: enabled — set to "0"
// or "false" to silence).
func logRelay(account string, headers http.Header, body []byte) {
	v := os.Getenv("MERIDIAN_NATIVE_DEBUG")
	if v != "1" && v != "true" {
		return
	}
	safe := RedactAuth(headers)
	fmt.Fprintf(os.Stderr, "[native-egress] relay account=%s headers=%v body=%s\n",
		account, safe, body)
}

// logUpstreamError logs a non-2xx upstream response body (truncated) so native
// failures (e.g. 400 invalid_request) are diagnosable. Always prints for
// non-2xx since these are actionable.
func logUpstreamError(status int, body []byte) {
	truncated := body
	if len(truncated) > 500 {
		truncated = truncated[:500]
	}
	fmt.Fprintf(os.Stderr, "[native-egress] upstream_non2xx status=%d body=%s\n", status, truncated)
}

func logMergeSummary(account string, cloaked []byte) {
	var d map[string]any
	if json.Unmarshal(cloaked, &d) != nil {
		return
	}
	sysBlocks := 0
	cacheBlocks := 0
	if sys, ok := d["system"].([]any); ok {
		sysBlocks = len(sys)
		for _, s := range sys {
			if m, ok := s.(map[string]any); ok {
				if _, ok := m["cache_control"]; ok {
					cacheBlocks++
				}
			}
		}
	}
	toolCount := 0
	if t, ok := d["tools"].([]any); ok {
		toolCount = len(t)
	}
	msgCount := 0
	if m, ok := d["messages"].([]any); ok {
		msgCount = len(m)
	}
	model, _ := d["model"].(string)
	thinking := "none"
	if th, ok := d["thinking"].(map[string]any); ok {
		if t, ok := th["type"].(string); ok {
			thinking = t
		}
	}
	oc := "none"
	if o, ok := d["output_config"].(map[string]any); ok {
		if e, ok := o["effort"].(string); ok {
			oc = e
		}
	}
	fmt.Fprintf(os.Stderr, "[native-egress] merged account=%s model=%s sys=%d cache=%d/4 tools=%d msgs=%d thinking=%s effort=%s\n",
		account, model, sysBlocks, cacheBlocks, toolCount, msgCount, thinking, oc)
}
