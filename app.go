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

	a.log.SetCallback(func(entry logging.Entry) {
		runtime.EventsEmit(ctx, "log", entry)
	})

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
	UpstreamHost   string `json:"upstream_host"`
	UpstreamPort   int    `json:"upstream_port"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	VerifyTLS      bool   `json:"verify_tls"`
	CustomCAPath   string `json:"custom_ca_path"`
	ConnectTimeout int    `json:"connect_timeout"`
	IdleTimeout    int    `json:"idle_timeout"`
	BindHost       string `json:"bind_host"`
	BindPort       int    `json:"bind_port"`
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

	if a.server != nil && a.server.IsRunning() {
		return fmt.Errorf("cannot save config while proxy is running — stop the proxy first")
	}

	newCfg := config.Config{
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

	if err := newCfg.Validate(); err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	if dto.Password != "" {
		if err := keychain.SavePassword(dto.Username, dto.Password); err != nil {
			return fmt.Errorf("save password: %w", err)
		}
	}

	if err := config.Save(newCfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	a.cfg = newCfg
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

// buildDiagTLS creates a TLS config from the current app config for diagnostics.
func (a *App) buildDiagTLS() (*tls.Config, error) {
	return upstream.BuildTLSConfig(upstream.TLSConfig{
		VerifyTLS:  a.cfg.Upstream.VerifyTLS,
		CustomCA:   a.cfg.Upstream.CustomCAPath,
		ServerName: a.cfg.Upstream.Host,
	})
}

func (a *App) logCheckResult(testName string, result upstream.CheckResult) {
	if result.OK {
		a.log.Info("%s passed: %s", testName, result.Message)
	} else {
		a.log.Error("%s failed: %s", testName, result.Message)
	}
}

func (a *App) TestUpstreamTLS() upstream.CheckResult {
	tlsCfg, err := a.buildDiagTLS()
	if err != nil {
		return upstream.CheckResult{OK: false, Message: err.Error()}
	}
	result := upstream.CheckTLS(a.ctx, a.cfg.UpstreamAddr(), a.cfg.ConnectTimeoutDuration(), tlsCfg)
	a.logCheckResult("TLS test", result)
	return result
}

func (a *App) TestProxyAuth() upstream.CheckResult {
	pw, _ := keychain.LoadPassword(a.cfg.Upstream.Username)
	tlsCfg, err := a.buildDiagTLS()
	if err != nil {
		return upstream.CheckResult{OK: false, Message: err.Error()}
	}
	result := upstream.CheckProxyAuth(a.ctx, a.cfg.UpstreamAddr(), a.cfg.ConnectTimeoutDuration(), tlsCfg, a.cfg.Upstream.Username, pw)
	a.logCheckResult("Auth test", result)
	return result
}

func (a *App) TestCONNECT(target string) upstream.CheckResult {
	if target == "" {
		target = "example.com:443"
	}
	pw, _ := keychain.LoadPassword(a.cfg.Upstream.Username)
	tlsCfg, err := a.buildDiagTLS()
	if err != nil {
		return upstream.CheckResult{OK: false, Message: err.Error()}
	}
	result := upstream.CheckCONNECT(a.ctx, a.cfg.UpstreamAddr(), a.cfg.ConnectTimeoutDuration(), tlsCfg, a.cfg.Upstream.Username, pw, target)
	a.logCheckResult("CONNECT test", result)
	return result
}

func (a *App) TestHTTPGet(targetURL string) upstream.CheckResult {
	if targetURL == "" {
		targetURL = "http://example.com/"
	}
	pw, _ := keychain.LoadPassword(a.cfg.Upstream.Username)
	tlsCfg, err := a.buildDiagTLS()
	if err != nil {
		return upstream.CheckResult{OK: false, Message: err.Error()}
	}
	result := upstream.CheckHTTP(a.ctx, a.cfg.UpstreamAddr(), a.cfg.ConnectTimeoutDuration(), tlsCfg, a.cfg.Upstream.Username, pw, targetURL)
	a.logCheckResult("HTTP test", result)
	return result
}

// --- File picker ---

func (a *App) SelectCAFile() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select CA Certificate PEM File",
		Filters: []runtime.FileFilter{
			{DisplayName: "PEM Files", Pattern: "*.pem;*.crt;*.cer"},
			{DisplayName: "All Files", Pattern: "*"},
		},
	})
}
