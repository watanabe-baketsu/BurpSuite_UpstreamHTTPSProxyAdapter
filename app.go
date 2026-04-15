package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"burp-upstream-adapter/internal/adapter"
	"burp-upstream-adapter/internal/config"
	"burp-upstream-adapter/internal/keychain"
	"burp-upstream-adapter/internal/logging"
	"burp-upstream-adapter/internal/upstream"
)

type App struct {
	ctx    context.Context
	log    *logging.Logger
	server *adapter.Server
	cfg    config.Config
	mu     sync.Mutex
}

func NewApp() *App {
	return &App{
		log: logging.New(1000),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Push log entries to frontend
	a.log.SetCallback(func(entry logging.Entry) {
		runtime.EventsEmit(ctx, "log", entry)
	})

	// Load saved config
	cfg, err := config.Load()
	if err != nil {
		a.log.Warn("Failed to load config, using defaults: %v", err)
		cfg = config.Default()
	}
	a.cfg = cfg
	a.log.Info("Config loaded")
}

func (a *App) shutdown(_ context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server != nil && a.server.IsRunning() {
		_ = a.server.Stop()
	}
}

// --- Config methods ---

type ConfigDTO struct {
	UpstreamHost     string `json:"upstream_host"`
	UpstreamPort     int    `json:"upstream_port"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	VerifyTLS        bool   `json:"verify_tls"`
	CustomCAPath     string `json:"custom_ca_path"`
	ConnectTimeout   int    `json:"connect_timeout"`
	IdleTimeout      int    `json:"idle_timeout"`
	BindHost         string `json:"bind_host"`
	BindPort         int    `json:"bind_port"`
}

func (a *App) GetConfig() ConfigDTO {
	pw, _ := keychain.LoadPassword(a.cfg.Upstream.Username)
	return ConfigDTO{
		UpstreamHost:   a.cfg.Upstream.Host,
		UpstreamPort:   a.cfg.Upstream.Port,
		Username:       a.cfg.Upstream.Username,
		Password:       pw,
		VerifyTLS:      a.cfg.Upstream.VerifyTLS,
		CustomCAPath:   a.cfg.Upstream.CustomCAPath,
		ConnectTimeout: a.cfg.Upstream.ConnectTimeout,
		IdleTimeout:    a.cfg.Upstream.IdleTimeout,
		BindHost:       a.cfg.Local.BindHost,
		BindPort:       a.cfg.Local.BindPort,
	}
}

func (a *App) SaveConfig(dto ConfigDTO) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.cfg = config.Config{
		Upstream: config.UpstreamConfig{
			Host:           dto.UpstreamHost,
			Port:           dto.UpstreamPort,
			Username:       dto.Username,
			VerifyTLS:      dto.VerifyTLS,
			CustomCAPath:   dto.CustomCAPath,
			ConnectTimeout: dto.ConnectTimeout,
			IdleTimeout:    dto.IdleTimeout,
		},
		Local: config.LocalConfig{
			BindHost: dto.BindHost,
			BindPort: dto.BindPort,
		},
	}

	if err := a.cfg.Validate(); err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	// Save password to keychain (never to JSON)
	if dto.Password != "" {
		if err := keychain.SavePassword(dto.Username, dto.Password); err != nil {
			return fmt.Errorf("save password: %w", err)
		}
	}

	if err := config.Save(a.cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	a.log.Info("Config saved")
	return nil
}

// --- Proxy control ---

func (a *App) StartProxy() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.server != nil && a.server.IsRunning() {
		return fmt.Errorf("proxy is already running")
	}

	if err := a.cfg.Validate(); err != nil {
		return fmt.Errorf("config invalid: %w", err)
	}

	pw, _ := keychain.LoadPassword(a.cfg.Upstream.Username)

	srv, err := adapter.NewServer(a.cfg, a.cfg.Upstream.Username, pw, a.log)
	if err != nil {
		return err
	}

	if err := srv.Start(); err != nil {
		return err
	}

	a.server = srv
	runtime.EventsEmit(a.ctx, "status", "running")
	return nil
}

func (a *App) StopProxy() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.server == nil || !a.server.IsRunning() {
		return nil
	}

	if err := a.server.Stop(); err != nil {
		return err
	}

	runtime.EventsEmit(a.ctx, "status", "stopped")
	return nil
}

func (a *App) GetStatus() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server != nil && a.server.IsRunning() {
		return "running"
	}
	return "stopped"
}

func (a *App) GetMetrics() adapter.MetricsSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server != nil {
		return a.server.GetMetrics()
	}
	return adapter.MetricsSnapshot{}
}

func (a *App) GetLogs() []logging.Entry {
	return a.log.Entries()
}

func (a *App) ClearLogs() {
	a.log.Clear()
}

// --- Diagnostics ---

func (a *App) TestUpstreamTLS() upstream.CheckResult {
	cfg := a.cfg
	tlsCfg := &tls.Config{
		InsecureSkipVerify: !cfg.Upstream.VerifyTLS,
		ServerName:         cfg.Upstream.Host,
	}
	if cfg.Upstream.CustomCAPath != "" {
		pool, err := upstream.LoadCustomCA(cfg.Upstream.CustomCAPath)
		if err != nil {
			return upstream.CheckResult{OK: false, Message: err.Error()}
		}
		tlsCfg.RootCAs = pool
	}
	result := upstream.CheckTLS(a.ctx, cfg.UpstreamAddr(), cfg.ConnectTimeoutDuration(), tlsCfg)
	if result.OK {
		a.log.Info("TLS test passed: %s", result.Message)
	} else {
		a.log.Error("TLS test failed: %s", result.Message)
	}
	return result
}

func (a *App) TestProxyAuth() upstream.CheckResult {
	cfg := a.cfg
	pw, _ := keychain.LoadPassword(cfg.Upstream.Username)
	tlsCfg := &tls.Config{
		InsecureSkipVerify: !cfg.Upstream.VerifyTLS,
		ServerName:         cfg.Upstream.Host,
	}
	if cfg.Upstream.CustomCAPath != "" {
		pool, err := upstream.LoadCustomCA(cfg.Upstream.CustomCAPath)
		if err != nil {
			return upstream.CheckResult{OK: false, Message: err.Error()}
		}
		tlsCfg.RootCAs = pool
	}
	result := upstream.CheckProxyAuth(a.ctx, cfg.UpstreamAddr(), cfg.ConnectTimeoutDuration(), tlsCfg, cfg.Upstream.Username, pw)
	if result.OK {
		a.log.Info("Auth test passed: %s", result.Message)
	} else {
		a.log.Error("Auth test failed: %s", result.Message)
	}
	return result
}

func (a *App) TestCONNECT(target string) upstream.CheckResult {
	if target == "" {
		target = "example.com:443"
	}
	cfg := a.cfg
	pw, _ := keychain.LoadPassword(cfg.Upstream.Username)
	tlsCfg := &tls.Config{
		InsecureSkipVerify: !cfg.Upstream.VerifyTLS,
		ServerName:         cfg.Upstream.Host,
	}
	if cfg.Upstream.CustomCAPath != "" {
		pool, err := upstream.LoadCustomCA(cfg.Upstream.CustomCAPath)
		if err != nil {
			return upstream.CheckResult{OK: false, Message: err.Error()}
		}
		tlsCfg.RootCAs = pool
	}
	result := upstream.CheckCONNECT(a.ctx, cfg.UpstreamAddr(), cfg.ConnectTimeoutDuration(), tlsCfg, cfg.Upstream.Username, pw, target)
	if result.OK {
		a.log.Info("CONNECT test passed: %s", result.Message)
	} else {
		a.log.Error("CONNECT test failed: %s", result.Message)
	}
	return result
}

func (a *App) TestHTTPGet(targetURL string) upstream.CheckResult {
	if targetURL == "" {
		targetURL = "http://example.com/"
	}
	cfg := a.cfg
	pw, _ := keychain.LoadPassword(cfg.Upstream.Username)
	tlsCfg := &tls.Config{
		InsecureSkipVerify: !cfg.Upstream.VerifyTLS,
		ServerName:         cfg.Upstream.Host,
	}
	if cfg.Upstream.CustomCAPath != "" {
		pool, err := upstream.LoadCustomCA(cfg.Upstream.CustomCAPath)
		if err != nil {
			return upstream.CheckResult{OK: false, Message: err.Error()}
		}
		tlsCfg.RootCAs = pool
	}
	result := upstream.CheckHTTP(a.ctx, cfg.UpstreamAddr(), cfg.ConnectTimeoutDuration(), tlsCfg, cfg.Upstream.Username, pw, targetURL)
	if result.OK {
		a.log.Info("HTTP test passed: %s", result.Message)
	} else {
		a.log.Error("HTTP test failed: %s", result.Message)
	}
	return result
}

// --- File picker ---

func (a *App) SelectCAFile() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select CA Certificate PEM File",
		Filters: []runtime.FileFilter{
			{DisplayName: "PEM Files", Pattern: "*.pem;*.crt;*.cer"},
			{DisplayName: "All Files", Pattern: "*"},
		},
	})
	if err != nil {
		return "", err
	}
	return path, nil
}
