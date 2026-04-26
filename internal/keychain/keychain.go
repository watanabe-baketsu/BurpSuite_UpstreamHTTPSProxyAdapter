package keychain

import "github.com/zalando/go-keyring"

const (
	serviceName = "burp-upstream-adapter"
)

// accountKey returns the keychain account identifier used for a given
// profile. Empty username is represented by a reserved "_default" token so
// that an unconfigured profile still has a stable slot.
//
// Profiles are isolated by prefixing with the profile name, so two profiles
// may share an identical upstream username without colliding.
func accountKey(profile, username string) string {
	if profile == "" {
		profile = "_unknown"
	}
	if username == "" {
		username = "_default"
	}
	return profile + ":" + username
}

// SavePassword stores the upstream password for (profile, username).
func SavePassword(profile, username, password string) error {
	return keyring.Set(serviceName, accountKey(profile, username), password)
}

// LoadPassword returns the upstream password for (profile, username). An
// unset entry returns an empty string with nil error so callers can treat
// it as "not configured yet".
func LoadPassword(profile, username string) (string, error) {
	pw, err := keyring.Get(serviceName, accountKey(profile, username))
	if err != nil {
		if err == keyring.ErrNotFound {
			return "", nil
		}
		return "", err
	}
	return pw, nil
}

// DeletePassword removes the stored credential for (profile, username).
// Returns nil if the entry does not exist so callers can call this
// unconditionally when tearing down a profile.
func DeletePassword(profile, username string) error {
	err := keyring.Delete(serviceName, accountKey(profile, username))
	if err == keyring.ErrNotFound {
		return nil
	}
	return err
}
