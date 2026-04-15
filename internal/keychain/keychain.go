package keychain

import "github.com/zalando/go-keyring"

const (
	serviceName = "burp-upstream-adapter"
)

func SavePassword(username, password string) error {
	if username == "" {
		username = "_default"
	}
	return keyring.Set(serviceName, username, password)
}

func LoadPassword(username string) (string, error) {
	if username == "" {
		username = "_default"
	}
	pw, err := keyring.Get(serviceName, username)
	if err != nil {
		if err == keyring.ErrNotFound {
			return "", nil
		}
		return "", err
	}
	return pw, nil
}

func DeletePassword(username string) error {
	if username == "" {
		username = "_default"
	}
	err := keyring.Delete(serviceName, username)
	if err == keyring.ErrNotFound {
		return nil
	}
	return err
}
