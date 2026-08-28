package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func findCamoufoxExecutable() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CAMOUFOX_PATH")); configured != "" {
		return validateCamoufoxExecutable(configured)
	}
	name, err := camoufoxExecutablePath()
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
		path, err := validateCamoufoxExecutable(candidate)
		if err == nil {
			return path, nil
		}
	}
	path, err := installCamoufox(name)
	if err != nil {
		return "", fmt.Errorf("auto-prepare Camoufox: %w", err)
	}
	return validateCamoufoxExecutable(path)
}

func camoufoxExecutablePath() (string, error) {
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

func validateCamoufoxExecutable(path string) (string, error) {
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
