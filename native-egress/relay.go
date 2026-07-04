package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type RelayDeps struct {
	Transport    http.RoundTripper
	FP           *FPCache
	BodyTemplate *BodyTemplateCache
	SessionID    func(account string) string
	Now          func() time.Time
	Datadog      *DatadogEmitter
}

func relayHandler(d RelayDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayStart := time.Now()
		rawBody, err := io.ReadAll(r.Body)
		if err != nil || len(rawBody) == 0 {
			degrade(w, "bad_request")
			return
		}
		configDir := r.Header.Get("X-Native-Config-Dir")
		account := r.Header.Get("X-Native-Account")
		stream := r.Header.Get("X-Native-Stream") == "1"
		clientBeta := r.Header.Get("X-Native-Anthropic-Beta")
		// One session id per conversation, used for BOTH the header and metadata
		// (real CC keeps them equal and rotates per conversation).
		convKey := r.Header.Get("X-Native-Session-Key")
		sessionID := conversationSessionID(account, convKey)

		token, _, _, err := ReadToken(configDir)
		if err != nil || token == "" {
			degrade(w, "no_token")
			return
		}

		fp, ok := d.FP.Get(account, configDir, d.Now())
		if !ok {
			degrade(w, "no_fingerprint")
			return
		}

		// ALL requests get wrapped in the CC template — no passthrough.
		var cloaked []byte
		tmpl := builtinTemplate()
		if t := d.BodyTemplate.Get(); t != nil {
			tmpl = t
		}
		cloaked, err = MergeUserRequest(rawBody, tmpl, deriveUserID(account, configDir, sessionID))
		if err != nil {
			degrade(w, "merge_error")
			return
		}

		if reason := ValidateBody(cloaked); reason != "" {
			rejectBody(w, reason)
			return
		}

		// Always stream from upstream — NE assembles to JSON for non-stream clients.
		headers := BuildHeaders(fp, token, sessionID, true, clientBeta)

		upReq, err := http.NewRequestWithContext(r.Context(), "POST", "https://api.anthropic.com/v1/messages?beta=true", bytesReader(cloaked))
		if err != nil {
			degrade(w, "build_request_error")
			return
		}
		upReq.Header = headers

		logRelay(account, headers, cloaked)
		logMergeSummary(account, cloaked)

		resp, err := d.Transport.RoundTrip(upReq)
		if err != nil {
			degrade(w, "upstream_error")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			// Decompress the error body: we advertise gzip/br/zstd, so Anthropic
			// compresses error responses too. Reading resp.Body raw would forward
			// gzip bytes as application/json (garbled to the client) AND break the
			// signature check below (bytes.Contains can't match inside gzip).
			errBody, _ := io.ReadAll(io.LimitReader(decodedBody(resp), 8192))
			logUpstreamError(resp.StatusCode, errBody)

			// Auto-retry on expired thinking signature: strip thinking blocks and resend.
			if resp.StatusCode == 400 && bytes.Contains(errBody, []byte("signature")) {
				resp.Body.Close()
				stripped := stripThinkingBlocks(cloaked)
				if stripped != nil {
					logDD("thinking signature expired, retrying without thinking blocks")
					retryReq, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(stripped))
					if err == nil {
						retryReq.Header = headers
						retryReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(stripped)))
						resp2, err2 := d.Transport.RoundTrip(retryReq)
						if err2 == nil {
							defer resp2.Body.Close()
							if resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
								resp = resp2
								goto handleSuccess
							}
							errBody, _ = io.ReadAll(io.LimitReader(decodedBody(resp2), 8192))
							logUpstreamError(resp2.StatusCode, errBody)
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(resp2.StatusCode)
							w.Write(errBody)
							return
						}
					}
				}
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(errBody)
			return
		}
	handleSuccess:

		requestID := resp.Header.Get("Request-Id")
		// Track upstream TTFB: time from relay start to first upstream byte.
		upstreamTTFB := time.Since(relayStart).Milliseconds()

		if !stream {
			// Client wants non-streaming: read full SSE, assemble final Message JSON.
			// decodedBody transparently decompresses per Content-Encoding (we now
			// advertise gzip/br/zstd like real CC); the non-stream path builds its
			// own response headers so it never forwards content-encoding.
			assembled, assembleErr := assembleSSEToMessage(decodedBody(resp))
			if assembleErr != nil && assembled == nil {
				// Upstream returned 2xx then closed before any message_start (transient
				// empty stream). Retry once with the same cloaked body before failing.
				resp.Body.Close()
				logDD("sse_empty_stream, retrying once: %v", assembleErr)
				if retryReq, rerr := http.NewRequestWithContext(r.Context(), "POST", "https://api.anthropic.com/v1/messages?beta=true", bytesReader(cloaked)); rerr == nil {
					retryReq.Header = headers
					if resp2, err2 := d.Transport.RoundTrip(retryReq); err2 == nil {
						defer resp2.Body.Close()
						if resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
							assembled, assembleErr = assembleSSEToMessage(decodedBody(resp2))
							if requestID == "" {
								requestID = resp2.Header.Get("Request-Id")
							}
						}
					}
				}
			}
			if assembleErr != nil {
				logDD("sse_assemble_error: %v", assembleErr)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(502)
				if assembled != nil {
					w.Write(assembled)
				} else {
					fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":"SSE assembly failed: %s"}}`, assembleErr.Error())
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Upstream-TTFB-Ms", fmt.Sprintf("%d", upstreamTTFB))
			if requestID != "" {
				w.Header().Set("Request-Id", requestID)
			}
			input, output, cached, cacheCreation, _, _ := extractResponseMeta(assembled)
			w.Header().Set("X-Usage-Input", fmt.Sprintf("%d", input))
			w.Header().Set("X-Usage-Output", fmt.Sprintf("%d", output))
			w.Header().Set("X-Usage-Cache-Read", fmt.Sprintf("%d", cached))
			w.Header().Set("X-Usage-Cache-Creation", fmt.Sprintf("%d", cacheCreation))
			w.WriteHeader(200)
			w.Write(assembled)

			if d.Datadog != nil {
				relayDuration := time.Since(relayStart).Milliseconds()
				model := extractModel(rawBody)
				input, output, cached, _, stopReason, toolCount := extractResponseMeta(assembled)
				d.Datadog.EmitAfterRelay(d.SessionID(account), model, requestID, stopReason,
					input, output, cached, toolCount, relayDuration, len(rawBody))
			}
			return
		}

		// Streaming: forward SSE events to client.
		for k, vs := range resp.Header {
			kl := strings.ToLower(k)
			if kl == "content-encoding" || kl == "content-length" {
				continue
			}
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.Header().Set("X-Upstream-TTFB-Ms", fmt.Sprintf("%d", upstreamTTFB))
		w.WriteHeader(resp.StatusCode)
		rc := http.NewResponseController(w)
		// decodedBody decompresses per Content-Encoding; we already stripped the
		// content-encoding/content-length response headers above, so the client
		// never double-decodes.
		respReader := decodedBody(resp)
		buf := make([]byte, 16*1024)
		var respCapture bytes.Buffer
		for {
			n, rerr := respReader.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					break
				}
				_ = rc.Flush()
				if respCapture.Len() < 32*1024 {
					respCapture.Write(buf[:n])
				}
			}
			if rerr != nil {
				break
			}
		}

		if d.Datadog != nil {
			relayDuration := time.Since(relayStart).Milliseconds()
			model := extractModel(rawBody)
			input, output, cached, _, stopReason, toolCount := extractResponseMeta(respCapture.Bytes())
			d.Datadog.EmitAfterRelay(d.SessionID(account), model, requestID, stopReason,
				input, output, cached, toolCount, relayDuration, len(rawBody))
		}
	}
}

func bodyHasClaudeIdentity(raw []byte) bool {
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return false
	}
	return hasClaudeIdentity(body["system"])
}

func degrade(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(502)
	fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":"native-egress: %s"}}`, reason)
}

// rejectBody returns a 400 in Anthropic error format without hitting the API.
func rejectBody(w http.ResponseWriter, message string) {
	logDD("pre-validate reject: %s", message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(400)
	resp := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "invalid_request_error",
			"message": message,
		},
	}
	json.NewEncoder(w).Encode(resp)
}

func bytesReader(b []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(b))
}

func extractModel(body []byte) string {
	var m struct{ Model string `json:"model"` }
	if json.Unmarshal(body, &m) == nil && m.Model != "" {
		return m.Model
	}
	return "claude-sonnet-4-6"
}

func extractResponseMeta(respData []byte) (input, output, cached, cacheCreation int, stopReason string, toolCount int) {
	var msg struct {
		Usage struct {
			InputTokens               int `json:"input_tokens"`
			OutputTokens              int `json:"output_tokens"`
			CacheReadInputTokens      int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens  int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
		} `json:"content"`
	}
	if json.Unmarshal(respData, &msg) == nil && (msg.Usage.InputTokens > 0 || msg.Usage.CacheReadInputTokens > 0) {
		for _, c := range msg.Content {
			if c.Type == "tool_use" {
				toolCount++
			}
		}
		return msg.Usage.InputTokens, msg.Usage.OutputTokens, msg.Usage.CacheReadInputTokens, msg.Usage.CacheCreationInputTokens, msg.StopReason, toolCount
	}
	for _, line := range bytes.Split(respData, []byte("\n")) {
		line = bytes.TrimPrefix(line, []byte("data: "))
		if json.Unmarshal(line, &msg) == nil {
			if msg.Usage.InputTokens > 0 || msg.Usage.CacheReadInputTokens > 0 {
				input = msg.Usage.InputTokens
				output = msg.Usage.OutputTokens
				cached = msg.Usage.CacheReadInputTokens
				cacheCreation = msg.Usage.CacheCreationInputTokens
			}
			if msg.StopReason != "" {
				stopReason = msg.StopReason
			}
			for _, c := range msg.Content {
				if c.Type == "tool_use" {
					toolCount++
				}
			}
		}
	}
	return
}
