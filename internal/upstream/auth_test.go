package upstream

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestBasicAuthHeader(t *testing.T) {
	header := BasicAuthHeader("user", "pass")
	if !strings.HasPrefix(header, "Basic ") {
		t.Fatalf("expected 'Basic ' prefix, got %s", header)
	}

	encoded := strings.TrimPrefix(header, "Basic ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if string(decoded) != "user:pass" {
		t.Errorf("expected 'user:pass', got '%s'", string(decoded))
	}
}

func TestBasicAuthHeaderSpecialChars(t *testing.T) {
	header := BasicAuthHeader("admin", "p@ss:w0rd!")
	encoded := strings.TrimPrefix(header, "Basic ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if string(decoded) != "admin:p@ss:w0rd!" {
		t.Errorf("unexpected decoded value: '%s'", string(decoded))
	}
}

func TestBasicAuthHeaderEmpty(t *testing.T) {
	header := BasicAuthHeader("", "")
	encoded := strings.TrimPrefix(header, "Basic ")
	decoded, _ := base64.StdEncoding.DecodeString(encoded)
	if string(decoded) != ":" {
		t.Errorf("expected ':', got '%s'", string(decoded))
	}
}
