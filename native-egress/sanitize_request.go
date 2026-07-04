package main

// sanitizeToolChoice normalizes a string tool_choice ("auto"/"any"/"none"/name)
// into the object form the API expects.
func sanitizeToolChoice(body map[string]any) {
	tc, ok := body["tool_choice"].(string)
	if !ok {
		return
	}
	switch tc {
	case "auto", "any", "none":
		body["tool_choice"] = map[string]any{"type": tc}
	default:
		body["tool_choice"] = map[string]any{"type": "tool", "name": tc}
	}
}

// stripEmptyImageBlocks removes image blocks with empty source data that the API
// rejects.
func stripEmptyImageBlocks(msgs any) {
	arr, _ := msgs.([]any)
	for _, m := range arr {
		mm, _ := m.(map[string]any)
		if mm == nil {
			continue
		}
		// Never touch assistant turns (preserve thinking-block integrity).
		if mm["role"] == "assistant" {
			continue
		}
		content, _ := mm["content"].([]any)
		if content == nil {
			continue
		}
		filtered := content[:0]
		for _, c := range content {
			block, _ := c.(map[string]any)
			if block != nil && block["type"] == "image" {
				src, _ := block["source"].(map[string]any)
				if src == nil || src["data"] == "" {
					continue
				}
			}
			filtered = append(filtered, c)
		}
		mm["content"] = filtered
	}
}
