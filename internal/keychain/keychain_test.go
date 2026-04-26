package keychain

import (
	"testing"

	"github.com/zalando/go-keyring"
)

// init switches the go-keyring library into its in-memory mock backend so the
// tests don't touch the real OS keychain (which on macOS prompts the user
// during CI).
func init() {
	keyring.MockInit()
}

func TestSaveLoadDelete(t *testing.T) {
	const profile, user, pw = "p1", "alice", "secret"

	if err := SavePassword(profile, user, pw); err != nil {
		t.Fatalf("SavePassword: %v", err)
	}
	got, err := LoadPassword(profile, user)
	if err != nil {
		t.Fatalf("LoadPassword: %v", err)
	}
	if got != pw {
		t.Errorf("LoadPassword = %q, want %q", got, pw)
	}

	if err := DeletePassword(profile, user); err != nil {
		t.Fatalf("DeletePassword: %v", err)
	}
	got, err = LoadPassword(profile, user)
	if err != nil {
		t.Fatalf("LoadPassword after delete: %v", err)
	}
	if got != "" {
		t.Errorf("LoadPassword after delete = %q, want empty", got)
	}
}

// TestLoadMissingReturnsEmpty captures the contract relied on by app.buildDTO:
// an unset entry must surface as ("", nil) so the UI can show an empty
// password field rather than an error toast.
func TestLoadMissingReturnsEmpty(t *testing.T) {
	got, err := LoadPassword("nonexistent-profile", "nonexistent-user")
	if err != nil {
		t.Fatalf("missing entry should return nil error, got %v", err)
	}
	if got != "" {
		t.Errorf("missing entry should return empty string, got %q", got)
	}
}

// TestDeleteMissingReturnsNil captures the contract relied on by
// DeleteProfile/RenameProfile in app.go: the call is fired unconditionally
// after the profile mutation and must not propagate "not found" as an error.
func TestDeleteMissingReturnsNil(t *testing.T) {
	if err := DeletePassword("nonexistent-profile", "nonexistent-user"); err != nil {
		t.Errorf("DeletePassword on missing entry should return nil, got %v", err)
	}
}

// TestProfileIsolation guards the invariant that two profiles can share the
// same username without their stored passwords colliding. This is what makes
// per-profile Basic-auth credentials work; if accountKey ever stops including
// the profile name, this test catches it.
func TestProfileIsolation(t *testing.T) {
	if err := SavePassword("staging", "alice", "stg-secret"); err != nil {
		t.Fatal(err)
	}
	if err := SavePassword("prod", "alice", "prd-secret"); err != nil {
		t.Fatal(err)
	}

	stg, _ := LoadPassword("staging", "alice")
	prd, _ := LoadPassword("prod", "alice")
	if stg != "stg-secret" {
		t.Errorf("staging password collision: got %q", stg)
	}
	if prd != "prd-secret" {
		t.Errorf("prod password collision: got %q", prd)
	}

	// Cleanup so other tests don't see leftovers in the mock store.
	_ = DeletePassword("staging", "alice")
	_ = DeletePassword("prod", "alice")
}

func TestAccountKey(t *testing.T) {
	cases := []struct {
		profile, user, want string
	}{
		{"prod", "alice", "prod:alice"},
		{"", "alice", "_unknown:alice"},
		{"prod", "", "prod:_default"},
		{"", "", "_unknown:_default"},
	}
	for _, c := range cases {
		if got := accountKey(c.profile, c.user); got != c.want {
			t.Errorf("accountKey(%q, %q) = %q, want %q", c.profile, c.user, got, c.want)
		}
	}
}

// TestEmptyUsernameSlot verifies that a freshly-created profile with no
// username yet still has a stable keychain slot. Without this, the slot
// collapses to whatever the underlying keyring uses for an empty user, which
// historically has been platform-dependent.
func TestEmptyUsernameSlot(t *testing.T) {
	if err := SavePassword("new-profile", "", "placeholder"); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPassword("new-profile", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "placeholder" {
		t.Errorf("empty-username slot lost data: got %q", got)
	}
	_ = DeletePassword("new-profile", "")
}
