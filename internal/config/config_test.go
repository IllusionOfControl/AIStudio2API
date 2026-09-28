package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAutoStartDefault(t *testing.T) {
	cfg := Default()
	if cfg.AutoStart {
		t.Fatalf("expected AutoStart default to be false, got true")
	}
}

func TestAutoStartFromEnvFile(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	content := "AUTO_START=true\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp env file: %v", err)
	}

	cfg, err := Load(envPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.AutoStart {
		t.Fatalf("expected AutoStart to be true from AUTO_START=true")
	}
}

func TestWAAAutoStartAlias(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	content := "WAA_AUTO_START=true\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp env file: %v", err)
	}

	cfg, err := Load(envPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.AutoStart {
		t.Fatalf("expected AutoStart to be true from WAA_AUTO_START=true")
	}
}

func TestAutoStartFromOSLookup(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envPath, []byte("AUTO_START=false\n"), 0o600); err != nil {
		t.Fatalf("failed to write temp env file: %v", err)
	}

	t.Setenv("AUTO_START", "true")
	cfg, err := Load(envPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.AutoStart {
		t.Fatalf("expected AutoStart to be true when overridden by OS env")
	}
}
