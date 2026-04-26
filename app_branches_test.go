package main

// app_branches_test.go: targeted coverage of the small error/branch paths
// that the main app_test.go didn't already exercise. Each test corresponds
// to a single guard or short-circuit in app.go that can hide a real bug if
// it ever silently changes (e.g. a validation gate getting reordered).

import (
	"os"
	"path/filepath"
	"testing"

	"burp-upstream-adapter/internal/config"
	"burp-upstream-adapter/internal/keychain"
)

// makeConfigDirUnwritable points HOME at a path that already exists as a
// regular file, so config.Save's MkdirAll(~/.burp-upstream-adapter) will
// fail. Used to exercise the otherwise-unreachable "save failed → unlock
// & return" branches in SwitchProfile / CreateProfile / etc.
func makeConfigDirUnwritable(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	// Create a file *named* like the dir we'd want to mkdir, so MkdirAll fails.
	if err := os.WriteFile(filepath.Join(tmp, ".burp-upstream-adapter"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	setHomeDir(t, tmp)
}

// TestAttachAppAndWindowAreSetters guards the trivial setters: if either is
// ever expanded into something with side effects (logging, locking, …) the
// rest of the app's startup sequence would change shape; the test pins the
// current "store the pointer" contract.
func TestAttachAppAndWindowAreSetters(t *testing.T) {
	a := newTestApp(t)
	a.attachApp(nil)    // accepts nil; emitStatus and Quit guard against it
	a.attachWindow(nil) // accepts nil; applyWindowTitle guards against it
	if a.app != nil || a.window != nil {
		t.Error("attach* should set the field to the provided value (nil here)")
	}
}

func TestDuplicateProfileRejectsInvalidName(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.DuplicateProfile(config.DefaultProfileName, ""); err == nil {
		t.Error("expected validation error for empty destination name")
	}
}

func TestDuplicateProfileRejectsExistingDestination(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Profiles["staging"] = config.DefaultProfile()
	a.mu.Unlock()
	if _, err := a.DuplicateProfile(config.DefaultProfileName, "staging"); err == nil {
		t.Error("expected error when destination name already exists")
	}
}

func TestRenameProfileRejectsInvalidNewName(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.RenameProfile(config.DefaultProfileName, "with space"); err == nil {
		t.Error("expected validation error for invalid new name")
	}
}

func TestRenameProfileRejectsMissingOld(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.RenameProfile("ghost", "alive"); err == nil {
		t.Error("expected error when source profile does not exist")
	}
}

func TestDeleteProfileRejectsMissing(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Profiles["other"] = config.DefaultProfile()
	a.mu.Unlock()
	if _, err := a.DeleteProfile("ghost"); err == nil {
		t.Error("expected error deleting a non-existent profile")
	}
}

// TestRenameMigratesEvenWhenInactive verifies the rename path when the old
// name is NOT the active profile — a separate branch from the active-rename
// case already covered by TestRenameProfileMigratesKeychain.
func TestRenameMigratesEvenWhenInactive(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	staging := config.DefaultProfile()
	staging.Username = "alice"
	a.cfg.Profiles["staging"] = staging
	a.mu.Unlock()

	if err := keychain.SavePassword("staging", "alice", "stg"); err != nil {
		t.Fatal(err)
	}

	if _, err := a.RenameProfile("staging", "renamed-staging"); err != nil {
		t.Fatalf("RenameProfile inactive: %v", err)
	}
	// Active should still be the original "default", not changed by the rename.
	if a.activeProfileName() != config.DefaultProfileName {
		t.Errorf("active should not change when renaming an inactive profile, got %q", a.activeProfileName())
	}
	pw, _ := keychain.LoadPassword("renamed-staging", "alice")
	if pw != "stg" {
		t.Errorf("password didn't migrate, got %q", pw)
	}
	t.Cleanup(func() { _ = keychain.DeletePassword("renamed-staging", "alice") })
}

// TestSaveConfigPasswordSetWhenSupplied covers the branch where the user
// types a brand-new password (rather than leaving it blank for migration).
func TestSaveConfigPasswordSetWhenSupplied(t *testing.T) {
	a := newTestApp(t)
	dto := ConfigDTO{
		ActiveProfile:  config.DefaultProfileName,
		UpstreamHost:   "proxy.example.com",
		UpstreamPort:   3128,
		Username:       "alice",
		Password:       "fresh-pw",
		VerifyTLS:      true,
		ConnectTimeout: 10,
		IdleTimeout:    60,
		BindHost:       "127.0.0.1",
		BindPort:       18080,
	}
	if err := a.SaveConfig(dto); err != nil {
		t.Fatal(err)
	}
	pw, _ := keychain.LoadPassword(config.DefaultProfileName, "alice")
	if pw != "fresh-pw" {
		t.Errorf("expected fresh password to be persisted, got %q", pw)
	}
	t.Cleanup(func() { _ = keychain.DeletePassword(config.DefaultProfileName, "alice") })
}

// TestEmitStatusSafeWithoutApp guards the path that fires from observers
// during tray + frontend integration: emitStatus must not nil-deref when
// a.app hasn't been wired up (e.g. the headless test setup).
func TestEmitStatusSafeWithoutApp(t *testing.T) {
	a := newTestApp(t)
	// Should be a no-op except for invoking observers.
	called := false
	a.onStatusChange(func(running bool) { called = running })
	a.emitStatus(true)
	if !called {
		t.Error("observer should still fire even when a.app is nil")
	}
}

// TestActiveProfileNameLockedAccess is a tiny sanity check that the helper
// returns whatever ActiveProfile is set to — it's the only synchronisation
// boundary the tray uses to read the live name.
func TestActiveProfileNameLockedAccess(t *testing.T) {
	a := newTestApp(t)
	if got := a.activeProfileName(); got != config.DefaultProfileName {
		t.Errorf("activeProfileName=%q, want %q", got, config.DefaultProfileName)
	}
}

// TestDuplicateAndRenameRejectedWhileRunning covers the
// requireProxyStoppedLocked branch on the two profile-mutation methods
// not exercised by TestProfileMutationRejectedWhileRunning.
func TestDuplicateAndRenameRejectedWhileRunning(t *testing.T) {
	a := newTestApp(t)
	startEphemeralProxy(t, a)
	defer func() { _ = a.StopProxy() }()

	if _, err := a.DuplicateProfile(config.DefaultProfileName, "copy"); err == nil {
		t.Error("expected error duplicating while proxy is running")
	}
	if _, err := a.RenameProfile(config.DefaultProfileName, "renamed"); err == nil {
		t.Error("expected error renaming while proxy is running")
	}
}

// TestSwitchProfileSurfacesSaveError exercises the "config.Save failed"
// branch on SwitchProfile by making the config dir unwriteable. Without
// surfacing this error the in-memory cfg.ActiveProfile would diverge from
// the on-disk state — a particularly nasty bug because the next Load
// would silently revert.
func TestSwitchProfileSurfacesSaveError(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.cfg.Profiles["other"] = config.DefaultProfile()
	a.mu.Unlock()
	makeConfigDirUnwritable(t)

	if _, err := a.SwitchProfile("other"); err == nil {
		t.Error("expected SwitchProfile to surface config.Save failure")
	}
}

// TestCreateProfileSurfacesSaveError covers the same Save-error branch on
// CreateProfile.
func TestCreateProfileSurfacesSaveError(t *testing.T) {
	a := newTestApp(t)
	makeConfigDirUnwritable(t)

	if _, err := a.CreateProfile("nope"); err == nil {
		t.Error("expected CreateProfile to surface config.Save failure")
	}
}

// TestSaveConfigSurfacesSaveError covers the same path inside SaveConfig.
func TestSaveConfigSurfacesSaveError(t *testing.T) {
	a := newTestApp(t)
	dto := a.GetConfig()
	dto.UpstreamHost = "proxy.example.com"
	dto.UpstreamPort = 3128
	dto.ConnectTimeout = 10
	dto.IdleTimeout = 60
	dto.BindHost = "127.0.0.1"
	dto.BindPort = 18080
	makeConfigDirUnwritable(t)

	if err := a.SaveConfig(dto); err == nil {
		t.Error("expected SaveConfig to surface config.Save failure")
	}
}

// TestSaveConfigAllowedWhileRunning pins the behaviour that SaveConfig is
// NOT gated on the proxy being stopped — only profile mutations are. The
// frontend's settings panel updates DTO fields like timeouts and tray
// preferences while the proxy is running and expects the save to succeed.
// If a future refactor adds requireProxyStoppedLocked here it would silently
// break those flows; this test catches it.
func TestSaveConfigAllowedWhileRunning(t *testing.T) {
	a := newTestApp(t)
	startEphemeralProxy(t, a)
	defer func() { _ = a.StopProxy() }()

	dto := a.GetConfig()
	dto.IdleTimeout = 600 // benign change that doesn't affect the listener
	if err := a.SaveConfig(dto); err != nil {
		t.Errorf("SaveConfig should succeed while running, got %v", err)
	}
}
