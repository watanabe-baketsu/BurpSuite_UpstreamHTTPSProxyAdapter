package upstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type CheckResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Latency string `json:"latency"`
}

func CheckTLS(ctx context.Context, addr string, timeout time.Duration, tlsCfg *tls.Config) CheckResult {
	start := time.Now()
	conn, err := DialTLS(ctx, addr, timeout, tlsCfg)
	elapsed := time.Since(start)
	if err != nil {
		return CheckResult{OK: false, Message: err.Error(), Latency: elapsed.String()}
	}
	conn.Close()
	return CheckResult{OK: true, Message: "TLS handshake successful", Latency: elapsed.String()}
}

func CheckProxyAuth(ctx context.Context, addr string, timeout time.Duration, tlsCfg *tls.Config, username, password string) CheckResult {
	start := time.Now()
	conn, err := DialTLS(ctx, addr, timeout, tlsCfg)
	elapsed := time.Since(start)
	if err != nil {
		return CheckResult{OK: false, Message: err.Error(), Latency: elapsed.String()}
	}
	defer conn.Close()

	// Send a CONNECT to a non-routable address (RFC 5737). The proxy will
	// check credentials before attempting to connect to the target, so:
	//   407 → credentials are wrong
	//   any other response (200, 502, 503, …) → credentials accepted
	const probeTarget = "192.0.2.1:443"
	authHeader := BasicAuthHeader(username, password)
	reqLine := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n", probeTarget, probeTarget, authHeader)
	if _, err := io.WriteString(conn, reqLine); err != nil {
		return CheckResult{OK: false, Message: fmt.Sprintf("write failed: %v", err), Latency: time.Since(start).String()}
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	elapsed = time.Since(start)
	if err != nil {
		return CheckResult{OK: false, Message: fmt.Sprintf("read response: %v", err), Latency: elapsed.String()}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusProxyAuthRequired {
		return CheckResult{OK: false, Message: "407 Proxy Authentication Required - check credentials", Latency: elapsed.String()}
	}
	// Any non-407 means the proxy accepted our credentials.
	// 200 = target reachable, 502/503 = target unreachable but auth passed.
	return CheckResult{
		OK:      true,
		Message: fmt.Sprintf("Auth OK (proxy responded %d %s)", resp.StatusCode, http.StatusText(resp.StatusCode)),
		Latency: elapsed.String(),
	}
}

func CheckCONNECT(ctx context.Context, addr string, timeout time.Duration, tlsCfg *tls.Config, username, password, targetHost string) CheckResult {
	start := time.Now()
	conn, err := DialTLS(ctx, addr, timeout, tlsCfg)
	if err != nil {
		return CheckResult{OK: false, Message: err.Error(), Latency: time.Since(start).String()}
	}
	defer conn.Close()

	authHeader := BasicAuthHeader(username, password)
	reqLine := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n", targetHost, targetHost, authHeader)
	if _, err := io.WriteString(conn, reqLine); err != nil {
		return CheckResult{OK: false, Message: fmt.Sprintf("write failed: %v", err), Latency: time.Since(start).String()}
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	elapsed := time.Since(start)
	if err != nil {
		return CheckResult{OK: false, Message: fmt.Sprintf("read response: %v", err), Latency: elapsed.String()}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return CheckResult{OK: true, Message: fmt.Sprintf("CONNECT to %s succeeded", targetHost), Latency: elapsed.String()}
	}
	return CheckResult{OK: false, Message: fmt.Sprintf("CONNECT failed: %d %s", resp.StatusCode, resp.Status), Latency: elapsed.String()}
}

func CheckHTTP(ctx context.Context, addr string, timeout time.Duration, tlsCfg *tls.Config, username, password, targetURL string) CheckResult {
	start := time.Now()
	conn, err := DialTLS(ctx, addr, timeout, tlsCfg)
	if err != nil {
		return CheckResult{OK: false, Message: err.Error(), Latency: time.Since(start).String()}
	}
	defer conn.Close()

	authHeader := BasicAuthHeader(username, password)
	reqLine := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\nConnection: close\r\n\r\n", targetURL, hostFromURL(targetURL), authHeader)
	if _, err := io.WriteString(conn, reqLine); err != nil {
		return CheckResult{OK: false, Message: fmt.Sprintf("write failed: %v", err), Latency: time.Since(start).String()}
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	elapsed := time.Since(start)
	if err != nil {
		return CheckResult{OK: false, Message: fmt.Sprintf("read response: %v", err), Latency: elapsed.String()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return CheckResult{OK: true, Message: fmt.Sprintf("HTTP %d %s", resp.StatusCode, resp.Status), Latency: elapsed.String()}
	}
	return CheckResult{OK: false, Message: fmt.Sprintf("HTTP %d %s", resp.StatusCode, resp.Status), Latency: elapsed.String()}
}

func hostFromURL(u string) string {
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "https://")
	if idx := strings.Index(u, "/"); idx >= 0 {
		u = u[:idx]
	}
	return u
}
