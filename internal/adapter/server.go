package adapter

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"burp-upstream-adapter/internal/config"
	"burp-upstream-adapter/internal/logging"
	"burp-upstream-adapter/internal/upstream"
)

type Server struct {
	cfg       config.Config
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
}

func NewServer(cfg config.Config, username, password string, log *logging.Logger) (*Server, error) {
	tlsCfg, err := upstream.BuildTLSConfig(upstream.TLSConfig{
		VerifyTLS:  cfg.Upstream.VerifyTLS,
		CustomCA:   cfg.Upstream.CustomCAPath,
		ServerName: cfg.Upstream.Host,
	})
	if err != nil {
		return nil, fmt.Errorf("build TLS config: %w", err)
	}

	proxyURL := &url.URL{
		Scheme: "https",
		Host:   cfg.UpstreamAddr(),
		User:   url.UserPassword(username, password),
	}

	transport := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: tlsCfg,
	}

	return &Server{
		cfg:       cfg,
		username:  username,
		password:  password,
		tlsCfg:    tlsCfg,
		log:       log,
		metrics:   NewMetrics(),
		transport: transport,
	}, nil
}

func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("server already running")
	}

	addr := s.cfg.LocalAddr()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	var ctx context.Context
	ctx, s.cancel = context.WithCancel(context.Background())
	s.listener = listener

	s.server = &http.Server{
		Handler:     http.HandlerFunc(s.handleRequest),
		ReadTimeout: s.cfg.ConnectTimeoutDuration(),
		WriteTimeout: 0, // No write timeout for tunnels
		IdleTimeout:  s.cfg.IdleTimeoutDuration(),
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
