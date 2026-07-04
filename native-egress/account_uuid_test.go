package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveUserIDUsesRealAccountUUID(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".claude.json"),
		[]byte(`{"oauthAccount":{"accountUuid":"6d13f8ba-ac25-4ef1-8b62-dec3a9834661"}}`), 0600)

	uid := deriveUserID("default", dir, "sess-xyz")
	var m map[string]string
	if err := json.Unmarshal([]byte(uid), &m); err != nil {
		t.Fatal(err)
	}
	if m["account_uuid"] != "6d13f8ba-ac25-4ef1-8b62-dec3a9834661" {
		t.Fatalf("account_uuid not real: %q", m["account_uuid"])
	}
	if m["session_id"] != "sess-xyz" {
		t.Fatalf("session_id not threaded: %q", m["session_id"])
	}
	if m["device_id"] == m["account_uuid"] || strings.HasPrefix(m["device_id"], strings.ReplaceAll(m["account_uuid"], "-", "")[:8]) {
		t.Fatal("device_id must differ from account_uuid")
	}
}
