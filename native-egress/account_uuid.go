package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// readAccountUUID returns the real Anthropic account UUID from .claude.json's
// oauthAccount.accountUuid, or "" if absent. The proxy's account-info feature
// populates this field on onboarding.
func readAccountUUID(configDir string) string {
	b, err := os.ReadFile(filepath.Join(resolveConfigDir(configDir), ".claude.json"))
	if err != nil {
		return ""
	}
	var d struct {
		OauthAccount struct {
			AccountUUID string `json:"accountUuid"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(b, &d) != nil {
		return ""
	}
	return d.OauthAccount.AccountUUID
}
