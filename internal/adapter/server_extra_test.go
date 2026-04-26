package adapter_test

import (
	"net"
	"strings"
	"testing"

	"burp-upstream-adapter/internal/adapter"
	"burp-upstream-adapter/internal/config"
	"burp-upstream-adapter/internal/logging"
)

// TestStopBeforeStartIsNoop guards the contract that callers (specifically
// the App.onAppShutdown hook and the tray's stop button) can invoke Stop
// unconditionally without checking IsRunning. A panic or error here would
// crash shutdown sequences in the wild.
func TestStopBeforeStartIsNoop(t *testing.T) {
	srv, err := adapter.NewServer(
		config.ProfileConfig{Host: "127.0.0.1", Port: 9999, ConnectTimeout: 5, IdleTimeout: 30, VerifyTLS: false},
		config.LocalConfig{BindHost: "127.0.0.1", BindPort: 0},
		"u", "p", logging.New(10),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Stop(); err != nil {
		t.Errorf("Stop on never-started server should be nil, got %v", err)
	}
	if srv.BoundAddr() != "" {
		t.Errorf("BoundAddr on never-started server should be empty, got %q", srv.BoundAddr())
	}
	if srv.IsRunning() {
		t.Error("IsRunning on never-started server should be false")
	}
}

// TestStartListenFailureSurfacesError verifies that a Start failure due to
// the bind port being already in use is reported as a typed error rather
// than panicking. We hold a listener on a known port, then ask the adapter
// to bind to the same port.
func TestStartListenFailureSurfacesError(t *testing.T) {
	hog, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hog.Close()
	port := hog.Addr().(*net.TCPAddr).Port

	srv, err := adapter.NewServer(
		config.ProfileConfig{Host: "127.0.0.1", Port: 9999, ConnectTimeout: 5, IdleTimeout: 30, VerifyTLS: false},
		config.LocalConfig{BindHost: "127.0.0.1", BindPort: port},
		"u", "p", logging.New(10),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = srv.Start()
	if err == nil {
		_ = srv.Stop()
		t.Fatal("expected Start to fail when port is busy, got nil")
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Errorf("expected 'listen' in error, got %v", err)
	}
	if srv.IsRunning() {
		t.Error("server should not be marked running after a failed Start")
	}
}

// TestNewServerInvalidCAFails verifies the early-failure path: when a
// profile carries a malformed Custom CA PEM, NewServer should refuse to
// build rather than producing a server that silently falls back to system
// roots and quietly trusts the wrong upstream.
func TestNewServerInvalidCAFails(t *testing.T) {
	prof := config.ProfileConfig{
		Host: "127.0.0.1", Port: 9999, ConnectTimeout: 5, IdleTimeout: 30,
		VerifyTLS:   true,
		CustomCAPEM: "this is not a PEM",
	}
	if _, err := adapter.NewServer(prof, config.LocalConfig{BindHost: "127.0.0.1", BindPort: 0}, "u", "p", logging.New(10)); err == nil {
		t.Fatal("expected NewServer to fail with invalid CA PEM")
	}
}

// TestStopIdempotent guards that Stop can be called twice without panicking.
// The first Stop tears down the listener; the second should observe the
// running=false state and return nil immediately.
func TestStopIdempotent(t *testing.T) {
	srv, err := adapter.NewServer(
		config.ProfileConfig{Host: "127.0.0.1", Port: 9999, ConnectTimeout: 5, IdleTimeout: 30, VerifyTLS: false},
		config.LocalConfig{BindHost: "127.0.0.1", BindPort: 0},
		"u", "p", logging.New(10),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	if err := srv.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := srv.Stop(); err != nil {
		t.Errorf("second Stop should be nil, got %v", err)
	}
}
