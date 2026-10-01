package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// TestAdminConfig verifies credential loading, password masking, and management restart flags.
func TestAdminConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "ADMIN_AUTH_ENABLED=true\nADMIN_USERNAME=operator\nADMIN_PASSWORD=test-password\nBUILD_NATIVE_NONSTREAM=true\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admin := &runtimeAdmin{configPath: path, requests: newRequestRegistry(ctx)}
	value, err := admin.RuntimeConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "test-password") || !value.AdminPasswordSet || !value.BuildNativeNonstream {
		t.Fatalf("config=%s", raw)
	}
	manager := &runtimeManager{activeManagement: cfg}
	differentCfg := cfg
	differentCfg.AdminUsername = "different-operator"
	differentValue := runtimeConfigDTO(differentCfg)
	decorated := manager.decorateRuntimeConfig(differentValue, cfg)
	if !decorated.ManagementRestartRequired || decorated.AdminPassword != nil {
		t.Fatalf("restart=%+v", decorated)
	}
}
