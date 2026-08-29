package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// FindCamoufoxExecutable locates an existing Camoufox installation or installs it automatically.
func FindCamoufoxExecutable() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CAMOUFOX_PATH")); configured != "" {
		return ValidateCamoufoxExecutable(configured)
	}
	name, err := CamoufoxExecutablePath()
	if err != nil {
		return "", err
	}
	candidates := []string{filepath.Join("runtime", "camoufox", name)}
	if executable, executableErr := os.Executable(); executableErr == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "runtime", "camoufox", name))
	}
	if runtime.GOOS == "windows" {
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			candidates = append(candidates, filepath.Join(localAppData, "camoufox", "camoufox", "Cache", name))
		}
	}
	for _, candidate := range candidates {
		path, err := ValidateCamoufoxExecutable(candidate)
		if err == nil {
			return path, nil
		}
	}
	path, err := InstallCamoufox(name)
	if err != nil {
		return "", fmt.Errorf("auto-prepare Camoufox: %w", err)
	}
	return ValidateCamoufoxExecutable(path)
}

// FindExecutable is an alias for FindCamoufoxExecutable.
func FindExecutable() (string, error) {
	return FindCamoufoxExecutable()
}

// CamoufoxExecutablePath returns the platform-specific Camoufox binary name or path.
func CamoufoxExecutablePath() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return "camoufox.exe", nil
	case "linux":
		return "camoufox-bin", nil
	case "darwin":
		return filepath.Join("Camoufox.app", "Contents", "MacOS", "camoufox"), nil
	default:
		return "", fmt.Errorf("Camoufox does not support %s", runtime.GOOS)
	}
}

// ExecutablePath is an alias for CamoufoxExecutablePath.
func ExecutablePath() (string, error) {
	return CamoufoxExecutablePath()
}

// ValidateCamoufoxExecutable verifies that the given path exists and is not a directory.
func ValidateCamoufoxExecutable(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve Camoufox path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("Camoufox path is a directory")
	}
	return absolute, nil
}

// ValidateExecutable is an alias for ValidateCamoufoxExecutable.
func ValidateExecutable(path string) (string, error) {
	return ValidateCamoufoxExecutable(path)
}
