package main

import (
	"crypto/sha256"
	"encoding/json"
	"regexp"
	"testing"
)

var uuidV4Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var sha256HexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestUUIDv4FromBytesAlwaysValid(t *testing.T) {
	for i := 0; i < 64; i++ {
		h := sha256.Sum256([]byte{byte(i)})
		if u := uuidV4FromBytes(h[:]); !uuidV4Re.MatchString(u) {
			t.Fatalf("uuidV4FromBytes produced non-UUIDv4: %q", u)
		}
	}
}

func TestSessionIDIsUUIDv4AndRotates(t *testing.T) {
	a := conversationSessionID("acct1", "convA")
	b := conversationSessionID("acct1", "convB")
	if !uuidV4Re.MatchString(a) {
		t.Fatalf("session_id is not a valid UUIDv4: %q", a)
	}
	if a == b {
		t.Fatalf("session_id must rotate across conversations")
	}
	if a != conversationSessionID("acct1", "convA") {
		t.Fatalf("session_id must be stable within one conversation")
	}
}

func TestDeriveUserIDMatchesRealFormats(t *testing.T) {
	sid := conversationSessionID("acct1", "conv")
	out := deriveUserID("acct1", "/nonexistent-config-dir", sid)
	var uid struct {
		DeviceID    string `json:"device_id"`
		AccountUUID string `json:"account_uuid"`
		SessionID   string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(out), &uid); err != nil {
		t.Fatalf("user_id is not valid JSON: %v", err)
	}
	// device_id: real CC uses a 64-char sha256 hex (NOT a UUID)
	if !sha256HexRe.MatchString(uid.DeviceID) {
		t.Fatalf("device_id is not 64-char sha256 hex: %q", uid.DeviceID)
	}
	// account_uuid fallback and session_id: real CC uses valid UUIDv4
	if !uuidV4Re.MatchString(uid.AccountUUID) {
		t.Fatalf("account_uuid fallback is not a valid UUIDv4: %q", uid.AccountUUID)
	}
	if uid.SessionID != sid {
		t.Fatalf("session_id not threaded through: %q != %q", uid.SessionID, sid)
	}
}
