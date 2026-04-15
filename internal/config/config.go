package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const DefaultConfigFile = "adapter.config.json"

type Config struct {
	Upstream UpstreamConfig `json:"upstream"`
	Local    LocalConfig    `json:"local"`
}

type UpstreamConfig struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`
	VerifyTLS      bool   `json:"verify_tls"`
	CustomCAPath   string `json:"custom_ca_path,omitempty"`
	ConnectTimeout int    `json:"connect_timeout_sec"`
	IdleTimeout    int    `json:"idle_timeout_sec"`
}

type LocalConfig struct {
	BindHost string `json:"bind_host"`
	BindPort int    `json:"bind_port"`
}

func Default() Config {
	return Config{
		Upstream: UpstreamConfig{
			Host:           "",
			Port:           3128,
			Username:       "",
			VerifyTLS:      true,
			ConnectTimeout: 30,
			IdleTimeout:    300,
		},
		Local: LocalConfig{
			BindHost: "127.0.0.1",
			BindPort: 18080,
		},
	}
}

func (c *Config) Validate() error {
	var errs []error

	if c.Upstream.Host == "" {
		errs = append(errs, errors.New("upstream host is required"))
	}
	if c.Upstream.Port < 1 || c.Upstream.Port > 65535 {
		errs = append(errs, fmt.Errorf("upstream port must be 1-65535, got %d", c.Upstream.Port))
	}
	if c.Local.BindHost == "" {
		errs = append(errs, errors.New("local bind host is required"))
	}
	if net.ParseIP(c.Local.BindHost) == nil {
		errs = append(errs, fmt.Errorf("invalid bind host IP: %s", c.Local.BindHost))
	}
	if c.Local.BindPort < 1 || c.Local.BindPort > 65535 {
		errs = append(errs, fmt.Errorf("local bind port must be 1-65535, got %d", c.Local.BindPort))
	}
	if c.Upstream.ConnectTimeout < 1 {
		errs = append(errs, errors.New("connect timeout must be >= 1 second"))
	}
	if c.Upstream.IdleTimeout < 1 {
		errs = append(errs, errors.New("idle timeout must be >= 1 second"))
	}
	if c.Upstream.CustomCAPath != "" {
		if _, err := os.Stat(c.Upstream.CustomCAPath); err != nil {
			errs = append(errs, fmt.Errorf("custom CA file not found: %s", c.Upstream.CustomCAPath))
		}
	}
	return errors.Join(errs...)
}

func (c *Config) UpstreamAddr() string {
	return net.JoinHostPort(c.Upstream.Host, strconv.Itoa(c.Upstream.Port))
}

func (c *Config) LocalAddr() string {
	return net.JoinHostPort(c.Local.BindHost, strconv.Itoa(c.Local.BindPort))
}

func (c *Config) ConnectTimeoutDuration() time.Duration {
	return time.Duration(c.Upstream.ConnectTimeout) * time.Second
}

func (c *Config) IdleTimeoutDuration() time.Duration {
	return time.Duration(c.Upstream.IdleTimeout) * time.Second
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".burp-upstream-adapter")
	return dir, os.MkdirAll(dir, 0700)
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DefaultConfigFile), nil
}

func Load() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Default(), err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Default(), err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

func Save(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
