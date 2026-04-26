package adapter

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"burp-upstream-adapter/internal/config"
	"burp-upstream-adapter/internal/logging"
	"burp-upstream-adapter/internal/upstream"
)

type Server struct {
	profile   config.ProfileConfig
	local     config.LocalConfig
	username  string
	password  string
	tlsCfg    *tls.Config
	log       *logging.Logger
	metrics   *Metrics
	transport *http.Transport

	cancel   context.CancelFunc
	listener net.Listener
	server   *http.Server
	mu       sync.Mutex
	running  bool

	// tunnelsMu guards tunnels. Hijacked CONNECT connections live outside
	// http.Server.Shutdown's tracking (Go's docs explicitly note Shutdown
	// "does not attempt to close nor wait for hijacked connections"), so we
	// keep our own set and close it during Stop. Without this, every active
	// Burp tunnel survives a Stop() — relay goroutines stay parked in
	// io.Copy, FDs leak, and the upstream proxy keeps half-open sockets.
	tunnelsMu sync.Mutex
	tunnels   map[*tunnel]struct{}
}

// tunnel holds the two endpoints of a single hijacked CONNECT relay so the
// server can force-close both halves on shutdown.
type tunnel struct {
	client   net.Conn
	upstream net.Conn
}

// NewServer builds a proxy server from a single profile plus the shared local
// listener settings. The caller is responsible for supplying the password
// already resolved from the keychain.
func NewServer(profile config.ProfileConfig, local config.LocalConfig, username, password string, log *logging.Logger) (*Server, error) {
	tlsCfg, err := upstream.BuildTLSConfig(upstream.TLSConfig{
		VerifyTLS:   profile.VerifyTLS,
		CustomCAPEM: []byte(profile.CustomCAPEM),
		ServerName:  profile.Host,
	})
	if err != nil {
		return nil, fmt.Errorf("build TLS config: %w", err)
	}

	proxyURL := &url.URL{
		Scheme: "https",
		Host:   profile.UpstreamAddr(),
		User:   url.UserPassword(username, password),
	}

	transport := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: tlsCfg,
	}

	return &Server{
		profile:   profile,
		local:     local,
		username:  username,
		password:  password,
		tlsCfg:    tlsCfg,
		log:       log,
		metrics:   NewMetrics(),
		transport: transport,
		tunnels:   make(map[*tunnel]struct{}),
	}, nil
}

// registerTunnel records a live hijacked CONNECT pair so Stop can force-close
// it. Returns a release function the caller defers to drop the entry once
// the relay exits naturally.
//
// If the server has already started shutting down (closeAllTunnels has run),
// the new tunnel is closed immediately and a no-op release is returned. This
// avoids a TOCTOU leak where a CONNECT goroutine that just finished
// hijacker.Hijack() — and is therefore no longer tracked by http.Server.Shutdown
// — could register itself just after closeAllTunnels swept the map.
func (s *Server) registerTunnel(t *tunnel) (release func()) {
	s.tunnelsMu.Lock()
	if s.tunnels == nil {
		s.tunnelsMu.Unlock()
		if t.client != nil {
			_ = t.client.Close()
		}
		if t.upstream != nil {
			_ = t.upstream.Close()
		}
		return func() {}
	}
	s.tunnels[t] = struct{}{}
	s.tunnelsMu.Unlock()
	return func() {
		s.tunnelsMu.Lock()
		delete(s.tunnels, t)
		s.tunnelsMu.Unlock()
	}
}

// closeAllTunnels force-closes every registered hijacked tunnel and locks
// the registry against further additions. Closing the underlying conns
// makes the io.Copy calls in relay() return, letting the relay goroutines
// drain and freeing the upstream socket / FD.
func (s *Server) closeAllTunnels() {
	s.tunnelsMu.Lock()
	tunnels := make([]*tunnel, 0, len(s.tunnels))
	for t := range s.tunnels {
		tunnels = append(tunnels, t)
	}
	// nil signals "shutting down" to registerTunnel — any CONNECT that
	// hijacks after this point will close itself instead of leaking.
	s.tunnels = nil
	s.tunnelsMu.Unlock()

	for _, t := range tunnels {
		if t.client != nil {
			_ = t.client.Close()
		}
		if t.upstream != nil {
			_ = t.upstream.Close()
		}
	}
}

// localAddr returns the listener bind address.
func (s *Server) localAddr() string {
	return net.JoinHostPort(s.local.BindHost, strconv.Itoa(s.local.BindPort))
}

func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("server already running")
	}

	addr := s.localAddr()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	var ctx context.Context
	ctx, s.cancel = context.WithCancel(context.Background())
	s.listener = listener

	s.server = &http.Server{
		Handler:      http.HandlerFunc(s.handleRequest),
		ReadTimeout:  s.profile.ConnectTimeoutDuration(),
		WriteTimeout: 0, // No write timeout for tunnels
		IdleTimeout:  s.profile.IdleTimeoutDuration(),
		BaseContext:  func(_ net.Listener) context.Context { return ctx },
	}

	s.running = true
	s.log.Info("Proxy server started on %s", addr)

	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.log.Error("server error: %v", err)
			s.metrics.SetError(err.Error())
		}
	}()

	return nil
}

func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	s.log.Info("Stopping proxy server...")
	s.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		s.log.Warn("shutdown error: %v", err)
		s.server.Close()
	}

	// http.Server.Shutdown does not wait for or close hijacked CONNECT
	// tunnels — we have to. Without this, an active Burp browsing session
	// keeps every relay goroutine alive (and its upstream socket open)
	// after Stop returns, which is the visible "the app won't fully quit"
	// behaviour.
	s.closeAllTunnels()

	s.transport.CloseIdleConnections()
	s.running = false
	s.log.Info("Proxy server stopped")
	return nil
}

func (s *Server) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Server) GetMetrics() MetricsSnapshot {
	return s.metrics.Snapshot()
}

func (s *Server) BoundAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleCONNECT(w, r)
	} else {
		s.handleHTTP(w, r)
	}
}
