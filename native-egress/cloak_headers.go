package main

import (
	"net/http"
	"strings"
)

// unionAnthropicBeta merges two comma-separated anthropic-beta lists, preserving
// order and dropping duplicates. The captured fingerprint carries the betas a
// simple `claude -p hi` sends; a real per-request body (e.g. structured output)
// adds request-specific betas like structured-outputs-2025-12-15. Overwriting
// the client's list with the capture would drop those and Anthropic 400s, so we
// union them.
func unionAnthropicBeta(lists ...string) string {
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	for _, list := range lists {
		for _, p := range strings.Split(list, ",") {
			p = strings.TrimSpace(p)
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return strings.Join(out, ",")
}

func BuildHeaders(fp Fingerprint, token, sessionID, clientRequestID string, stream bool, clientBeta string) http.Header {
	h := http.Header{}
	for k, v := range fp {
		h.Set(k, v)
	}
	// Union: capture baseline + the client's request-specific betas.
	// No forced injection — real CC only sends betas the SDK explicitly needs
	// for that request. Client-side (X-Native-Anthropic-Beta) carries anything
	// the request body actually requires (e.g. structured-outputs).
	if beta := unionAnthropicBeta(h.Get("anthropic-beta"), clientBeta); beta != "" {
		h.Set("anthropic-beta", beta)
	}
	h.Set("authorization", "Bearer "+token)
	h.Set("content-type", "application/json")
	h.Set("x-stainless-retry-count", "0")
	h.Set("x-claude-code-session-id", sessionID)
	h.Set("x-client-request-id", clientRequestID)
	// Always request SSE from upstream — NE assembles to JSON for non-stream
	// clients. This gives fast TTFB and matches real CC's always-stream behavior.
	h.Set("accept", "application/json")
	return h
}
