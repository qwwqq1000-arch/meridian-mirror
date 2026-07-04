package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const oauthTokenURL = "https://platform.claude.com/v1/oauth/token"
const oauthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

// resolveConfigDir returns configDir if non-empty, else defaults to
// $CLAUDE_CONFIG_DIR if set, else $HOME/.claude, else .claude.
func resolveConfigDir(configDir string) string {
	if configDir != "" {
		return configDir
	}
	if c := os.Getenv("CLAUDE_CONFIG_DIR"); c != "" {
		return c
	}
	if h := os.Getenv("HOME"); h != "" {
		return filepath.Join(h, ".claude")
	}
	return ".claude"
}

type credsFile struct {
	ClaudeAiOauth struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    int64  `json:"expiresAt"`
	} `json:"claudeAiOauth"`
}

func ReadToken(configDir string) (string, string, int64, error) {
	configDir = resolveConfigDir(configDir)
	b, err := os.ReadFile(filepath.Join(configDir, ".credentials.json"))
	if err != nil {
		return "", "", 0, err
	}
	var c credsFile
	if err := json.Unmarshal(b, &c); err != nil {
		return "", "", 0, err
	}
	if c.ClaudeAiOauth.AccessToken == "" {
		return "", "", 0, errors.New("no access token")
	}
	return c.ClaudeAiOauth.AccessToken, c.ClaudeAiOauth.RefreshToken, c.ClaudeAiOauth.ExpiresAt, nil
}

func RefreshToken(refresh string, post func(string, []byte) ([]byte, int, error)) (string, string, int64, error) {
	reqBody, _ := json.Marshal(map[string]string{
		"grant_type": "refresh_token", "refresh_token": refresh, "client_id": oauthClientID,
	})
	respBody, status, err := post(oauthTokenURL, reqBody)
	if err != nil {
		return "", "", 0, err
	}
	if status != 200 {
		return "", "", 0, errors.New("refresh failed")
	}
	var r struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &r); err != nil {
		return "", "", 0, err
	}
	return r.AccessToken, r.RefreshToken, time.Now().Add(time.Duration(r.ExpiresIn)*time.Second).UnixMilli(), nil
}
