package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRelease(t *testing.T) {
	if Release() != DefaultCamoufoxRelease {
		t.Errorf("expected default release %s, got %s", DefaultCamoufoxRelease, Release())
	}

	t.Setenv("CAMOUFOX_VERSION", "custom-version-1.0")
	if Release() != "custom-version-1.0" {
		t.Errorf("expected overridden release custom-version-1.0, got %s", Release())
	}
}

func TestCamoufoxAssetName(t *testing.T) {
	name, err := CamoufoxAssetName()
	if runtime.GOOS == "darwin" && runtime.GOARCH == "386" {
		if err == nil {
			t.Fatalf("expected error for darwin/386, got nil")
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error for %s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
	}
	if !strings.HasPrefix(name, "camoufox-") || !strings.HasSuffix(name, ".zip") {
		t.Errorf("unexpected asset name format: %s", name)
	}
	if !strings.Contains(name, Release()) {
		t.Errorf("asset name %s does not contain release version %s", name, Release())
	}
}

func TestCamoufoxExecutablePath(t *testing.T) {
	path, err := CamoufoxExecutablePath()
	if err != nil {
		t.Fatalf("unexpected error for %s: %v", runtime.GOOS, err)
	}
	switch runtime.GOOS {
	case "windows":
		if path != "camoufox.exe" {
			t.Errorf("expected camoufox.exe on windows, got %s", path)
		}
	case "linux":
		if path != "camoufox-bin" {
			t.Errorf("expected camoufox-bin on linux, got %s", path)
		}
	case "darwin":
		if !strings.HasSuffix(path, "camoufox") {
			t.Errorf("expected macos camoufox path, got %s", path)
		}
	}
}

func TestValidateCamoufoxExecutable(t *testing.T) {
	tempDir := t.TempDir()
	// Test on directory (should fail)
	_, err := ValidateCamoufoxExecutable(tempDir)
	if err == nil {
		t.Errorf("expected error when validating directory, got nil")
	}

	// Test on non-existent file (should fail)
	_, err = ValidateCamoufoxExecutable(filepath.Join(tempDir, "nonexistent"))
	if err == nil {
		t.Errorf("expected error when validating non-existent file, got nil")
	}

	// Test on existing file (should succeed)
	filePath := filepath.Join(tempDir, "dummy.exe")
	if err := os.WriteFile(filePath, []byte("test"), 0o755); err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}
	validated, err := ValidateCamoufoxExecutable(filePath)
	if err != nil {
		t.Fatalf("unexpected error validating file: %v", err)
	}
	if validated == "" {
		t.Errorf("expected non-empty validated path")
	}
}
