package upstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"
)

// TLSConfig describes how to build an upstream TLS configuration.
//
// CustomCAPEM holds inline PEM bytes. The adapter no longer references a
// file path at dial time: the PEM content is embedded in the saved config.
type TLSConfig struct {
	VerifyTLS   bool
	CustomCAPEM []byte
	ServerName  string
}

func BuildTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: !cfg.VerifyTLS,
		ServerName:         cfg.ServerName,
	}
	if len(cfg.CustomCAPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(cfg.CustomCAPEM) {
			return nil, fmt.Errorf("no valid certificates in custom CA PEM")
		}
		tlsCfg.RootCAs = pool
	}
	return tlsCfg, nil
}

func DialTLS(ctx context.Context, addr string, timeout time.Duration, tlsCfg *tls.Config) (net.Conn, error) {
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: timeout},
		Config:    tlsCfg,
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("TLS dial %s: %w", addr, err)
	}
	return conn, nil
}
