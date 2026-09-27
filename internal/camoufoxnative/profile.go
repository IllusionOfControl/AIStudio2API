package camoufoxnative

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// profilePrefix is the temporary Camoufox profile directory name prefix created by this service
const profilePrefix = "aistudio-camoufox-"

// profileLockName is the lock file held by the creating process inside the profile
const profileLockName = ".aistudio2api-profile.lock"

// createProfile creates a temporary profile and holds its lock file during browser lifetime
func createProfile() (string, *flock.Flock, error) {
	profile, err := os.MkdirTemp("", profilePrefix+"*")
	if err != nil {
		return "", nil, fmt.Errorf("create Camoufox profile: %w", err)
	}
	lock := flock.New(filepath.Join(profile, profileLockName))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		_ = os.RemoveAll(profile)
		return "", nil, errors.Join(fmt.Errorf("lock Camoufox profile"), err)
	}
	return profile, lock, nil
}

// removeProfile releases profile lock and removes directory, retrying within 2 seconds if files are still occupied
func removeProfile(profile string, lock *flock.Flock) error {
	if lock != nil {
		if err := lock.Unlock(); err != nil {
			return fmt.Errorf("release Camoufox profile lock: %w", err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.RemoveAll(profile)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// RemoveStaleProfiles removes temporary profiles whose creating process has exited, returning count of removed profiles
func RemoveStaleProfiles() (int, error) {
	root := os.TempDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, fmt.Errorf("read temp directory: %w", err)
	}
	removed := 0
	var removeErrors []error
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), profilePrefix) {
			continue
		}
		profile := filepath.Join(root, entry.Name())
		lockPath := filepath.Join(profile, profileLockName)
		if _, err := os.Stat(lockPath); err != nil {
			continue
		}
		lock := flock.New(lockPath)
		locked, err := lock.TryLock()
		if err != nil || !locked {
			continue
		}
		if err := removeProfile(profile, lock); err != nil {
			removeErrors = append(removeErrors, fmt.Errorf("remove stale Camoufox profile %s: %w", profile, err))
			continue
		}
		removed++
	}
	return removed, errors.Join(removeErrors...)
}
