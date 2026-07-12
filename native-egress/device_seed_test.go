package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeviceSeedStableAndPersisted(t *testing.T) {
	old := deviceSeedPath
	deviceSeedPath = filepath.Join(t.TempDir(), "sub", ".device_seed")
	defer func() { deviceSeedPath = old }()

	s1 := loadOrCreateDeviceSeed()
	if len(s1) != 32 { // 16 random bytes, hex-encoded
		t.Fatalf("expected 32-char hex seed, got %q (len %d)", s1, len(s1))
	}
	if _, err := os.Stat(deviceSeedPath); err != nil {
		t.Fatalf("seed was not persisted to volume: %v", err)
	}
	// Stable across calls: the second call must read back the same persisted value.
	if s2 := loadOrCreateDeviceSeed(); s1 != s2 {
		t.Fatalf("seed not stable across calls: %q vs %q", s1, s2)
	}
}

func TestDeviceSeedUniquePerMachine(t *testing.T) {
	mk := func() string {
		old := deviceSeedPath
		deviceSeedPath = filepath.Join(t.TempDir(), ".device_seed")
		defer func() { deviceSeedPath = old }()
		return loadOrCreateDeviceSeed()
	}
	if a, b := mk(), mk(); a == b {
		t.Fatalf("two machines got the same device seed (cluster tell): %q", a)
	}
}
