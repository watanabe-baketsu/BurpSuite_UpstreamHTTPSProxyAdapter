package upstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type CheckResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Latency string `json:"latency"`
}

// setDeadline sets a read/write deadline on the connection so that
// all subsequent I/O (write request, read response, drain body)
// is bounded by the given timeout.
func setDeadline(conn net.Conn, timeout time.Duration) {
	conn.SetDeadline(time.Now().Add(timeout))
}

// drainAndClose reads up to 64 KB of the response body then closes it.
// This prevents blocking on large error pages while still allowing
// http.ReadResponse to function correctly.
func drainAndClose(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		io.CopyN(io.Discard, resp.Body, 64*1024)
		resp.Body.Close()
	}
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
	if err != nil {
		return CheckResult{OK: false, Message: err.Error(), Latency: time.Since(start).String()}
	}
	defer conn.Close()
	setDeadline(conn, timeout)

	// Send a CONNECT to a well-known reachable host to verify auth.
	// Using example.com (IANA reserved, always reachable) so the proxy
	// can quickly establish the outbound connection and return a response.
	// A non-routable address would cause the proxy to hang until its own
	// connect_timeout, which is typically longer than our read deadline.
	//   407 → credentials are wrong
	//   200 → credentials accepted, tunnel established
	const probeTarget = "example.com:443"
	authHeader := BasicAuthHeader(username, password)
	req := fmt.Sprintf(
		"CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\nConnection: close\r\n\r\n",
		probeTarget, probeTarget, authHeader,
	)
	if _, err := io.WriteString(conn, req); err != nil {
		return CheckResult{OK: false, Message: fmt.Sprintf("write failed: %v", err), Latency: time.Since(start).String()}
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	elapsed := time.Since(start)
	if err != nil {
		return CheckResult{OK: false, Message: fmt.Sprintf("read response: %v", err), Latency: elapsed.String()}
	}
	drainAndClose(resp)

	if resp.StatusCode == http.StatusProxyAuthRequired {
		return CheckResult{OK: false, Message: "407 Proxy Authentication Required - check credentials", Latency: elapsed.String()}
	}
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
	setDeadline(conn, timeout)

	authHeader := BasicAuthHeader(username, password)
	req := fmt.Sprintf(
		"CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n",
		targetHost, targetHost, authHeader,
	)
	if _, err := io.WriteString(conn, req); err != nil {
		return CheckResult{OK: false, Message: fmt.Sprintf("write failed: %v", err), Latency: time.Since(start).String()}
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	elapsed := time.Since(start)
	if err != nil {
		return CheckResult{OK: false, Message: fmt.Sprintf("read response: %v", err), Latency: elapsed.String()}
	}
	drainAndClose(resp)

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
	setDeadline(conn, timeout)

	authHeader := BasicAuthHeader(username, password)
	req := fmt.Sprintf(
		"GET %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\nConnection: close\r\n\r\n",
		targetURL, hostFromURL(targetURL), authHeader,
	)
	if _, err := io.WriteString(conn, req); err != nil {
		return CheckResult{OK: false, Message: fmt.Sprintf("write failed: %v", err), Latency: time.Since(start).String()}
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	elapsed := time.Since(start)
	if err != nil {
		return CheckResult{OK: false, Message: fmt.Sprintf("read response: %v", err), Latency: elapsed.String()}
	}
	drainAndClose(resp)

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
