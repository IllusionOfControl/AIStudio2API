package browser

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const downloadTimeout = 30 * time.Minute

// InstallCamoufox downloads and extracts the Camoufox release archive.
func InstallCamoufox(executableName string) (string, error) {
	return InstallCamoufoxContext(context.Background(), executableName)
}

// InstallCamoufoxContext downloads and extracts the Camoufox release archive using the given context.
func InstallCamoufoxContext(ctx context.Context, executableName string) (string, error) {
	asset, err := CamoufoxAssetName()
	if err != nil {
		return "", err
	}
	root, err := CamoufoxInstallRoot()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create Camoufox directory: %w", err)
	}

	downloadURL := fmt.Sprintf("https://github.com/daijro/camoufox/releases/download/v%s/%s", Release(), asset)
	archivePath, err := downloadArchive(ctx, downloadURL, root)
	if err != nil {
		return "", err
	}
	defer os.Remove(archivePath)

	if err := ExtractCamoufoxArchive(archivePath, root); err != nil {
		return "", err
	}

	executable := filepath.Join(root, executableName)
	if err := ensureExecutable(executable); err != nil {
		return "", err
	}

	slog.Info("Camoufox ready", "path", executable)
	return executable, nil
}

// Install is an alias for InstallCamoufox.
func Install(executableName string) (string, error) {
	return InstallCamoufox(executableName)
}

// CamoufoxInstallRoot returns the absolute path to the local Camoufox installation directory.
func CamoufoxInstallRoot() (string, error) {
	root, err := filepath.Abs(filepath.Join("runtime", "camoufox"))
	if err != nil {
		return "", fmt.Errorf("locate Camoufox directory: %w", err)
	}
	return root, nil
}

// InstallRoot is an alias for CamoufoxInstallRoot.
func InstallRoot() (string, error) {
	return CamoufoxInstallRoot()
}

// CamoufoxAssetName returns the release asset archive filename for the current OS and architecture.
func CamoufoxAssetName() (string, error) {
	platform := map[string]string{"windows": "win", "linux": "lin", "darwin": "mac"}[runtime.GOOS]
	architecture := map[string]string{"amd64": "x86_64", "386": "i686", "arm64": "arm64"}[runtime.GOARCH]
	if platform == "" || architecture == "" || runtime.GOOS == "darwin" && runtime.GOARCH == "386" {
		return "", fmt.Errorf("Camoufox has no release package for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("camoufox-%s-%s.%s.zip", Release(), platform, architecture), nil
}

// AssetName is an alias for CamoufoxAssetName.
func AssetName() (string, error) {
	return CamoufoxAssetName()
}

// ExtractCamoufoxArchive unpacks the zip archive into the destination directory safely.
func ExtractCamoufoxArchive(archivePath string, destination string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open Camoufox archive: %w", err)
	}
	defer archive.Close()

	for _, entry := range archive.File {
		if err := extractZipEntry(entry, destination); err != nil {
			return fmt.Errorf("extract Camoufox %s: %w", entry.Name, err)
		}
	}
	return nil
}

// ExtractArchive is an alias for ExtractCamoufoxArchive.
func ExtractArchive(archivePath string, destination string) error {
	return ExtractCamoufoxArchive(archivePath, destination)
}

func downloadArchive(ctx context.Context, url string, destinationDir string) (string, error) {
	slog.Info("Downloading Camoufox", "version", Release(), "platform", runtime.GOOS+"/"+runtime.GOARCH)

	archive, err := os.CreateTemp(filepath.Dir(destinationDir), "camoufox-*.zip")
	if err != nil {
		return "", fmt.Errorf("create Camoufox download file: %w", err)
	}
	archivePath := archive.Name()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		_ = archive.Close()
		_ = os.Remove(archivePath)
		return "", fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "AIStudio2API")

	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		_ = archive.Close()
		_ = os.Remove(archivePath)
		return "", fmt.Errorf("download Camoufox: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_ = archive.Close()
		_ = os.Remove(archivePath)
		return "", fmt.Errorf("download Camoufox: HTTP %d", resp.StatusCode)
	}

	if resp.ContentLength > 0 {
		slog.Info("Camoufox download started", "size_mib", resp.ContentLength/(1024*1024))
	}

	_, copyErr := io.Copy(archive, resp.Body)
	closeErr := archive.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(archivePath)
		return "", fmt.Errorf("save Camoufox: %w", err)
	}

	return archivePath, nil
}

func extractZipEntry(entry *zip.File, destination string) error {
	target := filepath.Join(destination, filepath.FromSlash(entry.Name))
	relative, err := filepath.Rel(destination, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("archive contains invalid path %q", entry.Name)
	}

	if entry.FileInfo().IsDir() {
		return os.MkdirAll(target, entry.Mode())
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	source, err := entry.Open()
	if err != nil {
		return err
	}
	defer source.Close()

	targetFile, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, entry.Mode())
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(targetFile, source)
	closeErr := targetFile.Close()
	return errors.Join(copyErr, closeErr)
}

func ensureExecutable(executable string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := os.Chmod(executable, 0o755); err != nil {
		return fmt.Errorf("set Camoufox executable permissions: %w", err)
	}
	return nil
}
