package camoufoxnative

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// accountCacheDirectoryName is the subdirectory name under the account directory storing Camoufox HTTP disk cache
const accountCacheDirectoryName = "camoufox-cache"

// accountCacheCapacityKB is the maximum capacity for a single account HTTP disk cache
const accountCacheCapacityKB = 262144

// lockAccountCache exclusively locks the account HTTP disk cache directory; returns empty directory if occupied by another runtime of the same account
func lockAccountCache(storageStatePath string) (string, *flock.Flock, error) {
	directory := filepath.Join(filepath.Dir(storageStatePath), accountCacheDirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", nil, fmt.Errorf("create account browser cache directory: %w", err)
	}
	lock := flock.New(filepath.Join(directory, ".lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return "", nil, fmt.Errorf("lock account browser cache: %w", err)
	}
	if !locked {
		return "", nil, nil
	}
	return directory, lock, nil
}

// releaseAccountCache releases the account HTTP disk cache directory after the browser process exits
func releaseAccountCache(lock *flock.Flock) error {
	if lock == nil {
		return nil
	}
	if err := lock.Unlock(); err != nil {
		return fmt.Errorf("release account browser cache: %w", err)
	}
	return nil
}

// cachePreferences returns Firefox preferences placing HTTP disk cache into the designated directory
func cachePreferences(directory string) map[string]any {
	return map[string]any{
		"browser.cache.disk.parent_directory":   directory,
		"browser.cache.disk.smart_size.enabled": false,
		"browser.cache.disk.capacity":           accountCacheCapacityKB,
	}
}
