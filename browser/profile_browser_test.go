package browser

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseSingletonOwner(t *testing.T) {
	host, pid, ok := parseSingletonOwner("/tmp/e95dee47e227-433")
	if !ok {
		t.Fatal("expected singleton owner to be parsed")
	}
	if host != "e95dee47e227" || pid != 433 {
		t.Fatalf("unexpected singleton owner: host=%q pid=%d", host, pid)
	}

	if _, _, ok := parseSingletonOwner("invalid-singleton-owner"); ok {
		t.Fatal("expected malformed singleton owner to be rejected")
	}
}

func TestStaleSingletonLockIsConservativeForRegularFiles(t *testing.T) {
	profileDir := t.TempDir()
	lockPath := filepath.Join(profileDir, "SingletonLock")
	if err := os.WriteFile(lockPath, []byte("not a symlink"), 0600); err != nil {
		t.Fatal(err)
	}

	stale, reason, err := staleSingletonLock(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Fatalf("regular SingletonLock must not be treated as stale: %s", reason)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("regular SingletonLock should remain untouched: %v", err)
	}
}

func TestRemoveStaleSingletonFilesKeepsProfileData(t *testing.T) {
	profileDir := t.TempDir()
	for _, name := range singletonFileNames {
		if err := os.WriteFile(filepath.Join(profileDir, name), []byte("stale"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(profileDir, "Cookies"), []byte("session"), 0600); err != nil {
		t.Fatal(err)
	}
	storageDir := filepath.Join(profileDir, "Local Storage")
	if err := os.Mkdir(storageDir, 0700); err != nil {
		t.Fatal(err)
	}
	storageFile := filepath.Join(storageDir, "session.db")
	if err := os.WriteFile(storageFile, []byte("session data"), 0600); err != nil {
		t.Fatal(err)
	}

	removed, err := removeStaleSingletonFiles(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != len(singletonFileNames) {
		t.Fatalf("removed files=%v, want %v", removed, singletonFileNames)
	}
	for _, name := range singletonFileNames {
		if _, err := os.Lstat(filepath.Join(profileDir, name)); !os.IsNotExist(err) {
			t.Fatalf("singleton file %s still exists, err=%v", name, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(profileDir, "Cookies")); err != nil || string(data) != "session" {
		t.Fatalf("Cookies was changed or removed: data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(storageFile); err != nil || string(data) != "session data" {
		t.Fatalf("Local Storage was changed or removed: data=%q err=%v", data, err)
	}
}

func TestIsStaleProfileLockError(t *testing.T) {
	if isStaleProfileLockError(errString("generic launch failed")) {
		t.Fatal("generic file errors must not be treated as profile lock errors")
	}
	if !isStaleProfileLockError(errString("The profile appears to be in use by another Google Chrome process")) {
		t.Fatal("expected Chrome profile lock error to be recognized")
	}
}

func TestProfileLeaseSerializesSameProfile(t *testing.T) {
	profileDir := normalizedProfileDir(t.TempDir())
	first, err := acquireProfileLease(context.Background(), profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseProfileLease(first)

	secondCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	secondResult := make(chan chan struct{}, 1)
	secondErr := make(chan error, 1)
	go func() {
		lease, err := acquireProfileLease(secondCtx, profileDir)
		if err != nil {
			secondErr <- err
			return
		}
		secondResult <- lease
	}()

	select {
	case lease := <-secondResult:
		releaseProfileLease(lease)
		t.Fatal("same profile lease was acquired concurrently")
	case err := <-secondErr:
		t.Fatalf("second lease should wait until the first is released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	releaseProfileLease(first)
	select {
	case lease := <-secondResult:
		if lease == nil {
			t.Fatal("second lease is nil")
		}
		releaseProfileLease(lease)
	case err := <-secondErr:
		t.Fatalf("second lease did not acquire after release: %v", err)
	case <-time.After(time.Second):
		t.Fatal("second lease did not acquire after release")
	}
}

func TestProfileLeaseCancellationIsBounded(t *testing.T) {
	profileDir := normalizedProfileDir(t.TempDir())
	first, err := acquireProfileLease(context.Background(), profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseProfileLease(first)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = acquireProfileLease(ctx, profileDir)
	if err == nil {
		t.Fatal("expected canceled profile lease acquisition to fail")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("profile lease cancellation took too long: %s", elapsed)
	}
}

func TestProfileBrowserCloseReleasesActiveCountAndLease(t *testing.T) {
	profileDir := normalizedProfileDir(t.TempDir())
	lease, err := acquireProfileLease(context.Background(), profileDir)
	if err != nil {
		t.Fatal(err)
	}
	profileBrowserMu.Lock()
	activeProfileBrowser[profileDir] = 1
	profileBrowserMu.Unlock()

	b := &ProfileBrowser{profileDir: profileDir, profileLease: lease}
	b.Close()
	if got := activeProfileBrowserCount(profileDir); got != 0 {
		t.Fatalf("active profile browser count=%d, want 0", got)
	}

	next, err := acquireProfileLease(context.Background(), profileDir)
	if err != nil {
		t.Fatalf("profile lease was not released by Close: %v", err)
	}
	releaseProfileLease(next)
}

func TestNewProfileBrowserWithContextReturnsLaunchError(t *testing.T) {
	profileDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var browser *ProfileBrowser
	var err error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("safe profile constructor panicked: %v", recovered)
			}
		}()
		browser, err = NewProfileBrowserWithContext(ctx, true, profileDir, filepath.Join(profileDir, "missing-chrome"), "")
	}()
	if browser != nil {
		browser.Close()
		t.Fatal("expected invalid Chrome binary to fail")
	}
	if err == nil {
		t.Fatal("expected invalid Chrome binary to return an error")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
