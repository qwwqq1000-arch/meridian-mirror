package main

import (
	_ "embed"
	"encoding/json"
	"sync"
)

//go:embed builtin_template.json
var builtinTemplateJSON []byte

var (
	builtinOnce sync.Once
	builtinTmpl *BodyTemplate
)

// builtinTemplate returns a BodyTemplate parsed from the embedded JSON captured
// from a real Claude Code CLI request. Used as an immediate fallback when no
// live CC request has been seen yet (same pattern as builtinFP for headers).
// Updated by re-capturing when the CC CLI version changes.
func builtinTemplate() *BodyTemplate {
	builtinOnce.Do(func() {
		var raw map[string]any
		if err := json.Unmarshal(builtinTemplateJSON, &raw); err != nil {
			logDD("failed to parse builtin template: %v", err)
			return
		}
		tmpl := &BodyTemplate{Stream: true}
		if sys, ok := raw["system"].([]any); ok {
			tmpl.System = sys
		}
		if tools, ok := raw["tools"].([]any); ok {
			tmpl.Tools = tools
		}
		if cm := raw["context_management"]; cm != nil {
			tmpl.ContextManagement = cm
		}
		if oc := raw["output_config"]; oc != nil {
			tmpl.OutputConfig = oc
		}
		if diag := raw["diagnostics"]; diag != nil {
			tmpl.Diagnostics = diag
		}
		if th := raw["thinking"]; th != nil {
			tmpl.Thinking = th
		}
		if mt, ok := raw["max_tokens"].(float64); ok && mt > 0 {
			tmpl.MaxTokens = int(mt)
		}
		builtinTmpl = tmpl
		logDD("builtin template loaded: system=%d blocks, tools=%d", len(tmpl.System), len(tmpl.Tools))
	})
	return builtinTmpl
}
