package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadToken(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"acc","refreshToken":"ref","expiresAt":123}}`), 0600)
	acc, ref, exp, err := ReadToken(dir)
	if err != nil || acc != "acc" || ref != "ref" || exp != 123 {
		t.Fatalf("got %q %q %d %v", acc, ref, exp, err)
	}
}

func TestReadTokenWithEmptyConfigDirDefaultsToHome(t *testing.T) {
	// Create a temporary home directory with .claude/.credentials.json
	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	if err := os.MkdirAll(claudeDir, 0700); err != nil {
		t.Fatalf("failed to create .claude dir: %v", err)
	}
	credsPath := filepath.Join(claudeDir, ".credentials.json")
	if err := os.WriteFile(credsPath,
		[]byte(`{"claudeAiOauth":{"accessToken":"home_token","refreshToken":"home_ref","expiresAt":999}}`), 0600); err != nil {
		t.Fatalf("failed to write credentials: %v", err)
	}

	// Set HOME to our temp dir and call ReadToken with empty configDir
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	os.Setenv("CLAUDE_CONFIG_DIR", "")
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Setenv("CLAUDE_CONFIG_DIR", "")
	}()

	acc, ref, exp, err := ReadToken("")
	if err != nil || acc != "home_token" || ref != "home_ref" || exp != 999 {
		t.Fatalf("with empty configDir, got %q %q %d %v; want home_token home_ref 999 nil", acc, ref, exp, err)
	}
}

func TestReadTokenWithNonEmptyConfigDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"explicit_token","refreshToken":"explicit_ref","expiresAt":555}}`), 0600); err != nil {
		t.Fatalf("failed to write credentials: %v", err)
	}

	// Even with HOME set, explicit configDir should be used
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", "/should/not/use/this")
	defer os.Setenv("HOME", oldHome)

	acc, ref, exp, err := ReadToken(dir)
	if err != nil || acc != "explicit_token" || ref != "explicit_ref" || exp != 555 {
		t.Fatalf("with explicit configDir, got %q %q %d %v; want explicit_token explicit_ref 555 nil", acc, ref, exp, err)
	}
}

func TestRefreshToken(t *testing.T) {
	post := func(url string, body []byte) ([]byte, int, error) {
		return []byte(`{"access_token":"newacc","refresh_token":"newref","expires_in":3600}`), 200, nil
	}
	acc, ref, _, err := RefreshToken("ref", post)
	if err != nil || acc != "newacc" || ref != "newref" {
		t.Fatalf("got %q %q %v", acc, ref, err)
	}
}
