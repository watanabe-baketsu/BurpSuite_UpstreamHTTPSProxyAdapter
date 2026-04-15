package upstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"time"
)

type TLSConfig struct {
	VerifyTLS  bool
	CustomCA   string // PEM file path
	ServerName string
}

func BuildTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: !cfg.VerifyTLS,
		ServerName:         cfg.ServerName,
	}
	if cfg.CustomCA != "" {
		pem, err := os.ReadFile(cfg.CustomCA)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no valid certificates in CA file: %s", cfg.CustomCA)
		}
		tlsCfg.RootCAs = pool
	}
	return tlsCfg, nil
}

func LoadCustomCA(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no valid certificates found in: %s", path)
	}
	return pool, nil
}

func DialTLS(ctx context.Context, addr string, timeout time.Duration, tlsCfg *tls.Config) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("TLS dial %s: %w", addr, err)
	}
	return conn, nil
}
