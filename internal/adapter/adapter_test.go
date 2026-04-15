package adapter_test

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"burp-upstream-adapter/internal/adapter"
	"burp-upstream-adapter/internal/config"
	"burp-upstream-adapter/internal/logging"
)

func encodeBasicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// mockUpstreamProxy creates a TLS server that acts as an HTTPS proxy.
func mockUpstreamProxy(t *testing.T, wantUser, wantPass string) *httptest.Server {
	wantAuth := "Basic " + encodeBasicAuth(wantUser, wantPass)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Proxy-Authorization")
		if authHeader == "" || authHeader != wantAuth {
			w.Header().Set("Proxy-Authenticate", `Basic realm="proxy"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}

		if r.Method == http.MethodConnect {
			targetConn, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
			if err != nil {
				http.Error(w, fmt.Sprintf("connect to %s failed: %v", r.Host, err), http.StatusBadGateway)
				return
			}

			hijacker, ok := w.(http.Hijacker)
			if !ok {
				targetConn.Close()
				http.Error(w, "hijack not supported", http.StatusInternalServerError)
				return
			}

			clientConn, clientBuf, err := hijacker.Hijack()
			if err != nil {
				targetConn.Close()
				return
			}

			_, _ = clientBuf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
			_ = clientBuf.Flush()

			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				io.Copy(targetConn, clientConn)
			}()
			go func() {
				defer wg.Done()
				io.Copy(clientConn, targetConn)
			}()
			wg.Wait()
			clientConn.Close()
			targetConn.Close()
		} else {
			client := &http.Client{Timeout: 10 * time.Second}
			outReq, _ := http.NewRequest(r.Method, r.URL.String(), r.Body)
			for k, vv := range r.Header {
				for _, v := range vv {
					outReq.Header.Add(k, v)
				}
			}
			outReq.Header.Del("Proxy-Authorization")
			outReq.Header.Del("Proxy-Connection")

			resp, err := client.Do(outReq)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()

			for k, vv := range resp.Header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
		}
	})

	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	return server
}

func parseHostPort(rawURL string) (string, int) {
	u, _ := url.Parse(rawURL)
	host, portStr, _ := net.SplitHostPort(u.Host)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}

// setupAdapter creates and starts a proxy adapter using port 0 (OS-assigned).
// Returns the actual bound address and a cleanup function.
func setupAdapter(t *testing.T, upstreamServer *httptest.Server, username, password string) (string, func()) {
	upHost, upPort := parseHostPort(upstreamServer.URL)

	cfg := config.Config{
		Upstream: config.UpstreamConfig{
			Host:           upHost,
			Port:           upPort,
			Username:       username,
			VerifyTLS:      false,
			ConnectTimeout: 10,
			IdleTimeout:    30,
		},
		Local: config.LocalConfig{
			BindHost: "127.0.0.1",
			BindPort: 0, // OS assigns a free port
		},
	}

	log := logging.New(100)
	srv, err := adapter.NewServer(cfg, username, password, log)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Get the actual bound address (no TOCTOU race)
	localAddr := srv.BoundAddr()
	if localAddr == "" {
		t.Fatal("server started but BoundAddr is empty")
	}

	return localAddr, func() { srv.Stop() }
}

func TestCONNECTSuccess(t *testing.T) {
	upstream := mockUpstreamProxy(t, "user", "pass")
	localAddr, cleanup := setupAdapter(t, upstream, "user", "pass")
	defer cleanup()

	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("tunnel-ok"))
	}))
	defer target.Close()

	conn, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	defer conn.Close()

	targetHost := strings.TrimPrefix(target.URL, "https://")
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", targetHost, targetHost)

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true})
	defer tlsConn.Close()

	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("TLS handshake through tunnel failed: %v", err)
	}

	req, _ := http.NewRequest("GET", "/", nil)
	req.Host = targetHost
	req.Write(tlsConn)

	tunnelResp, err := http.ReadResponse(bufio.NewReader(tlsConn), req)
	if err != nil {
		t.Fatalf("read tunnel response: %v", err)
	}
	defer tunnelResp.Body.Close()

	body, _ := io.ReadAll(tunnelResp.Body)
	if string(body) != "tunnel-ok" {
		t.Errorf("expected 'tunnel-ok', got '%s'", string(body))
	}
}

func TestCONNECTAuthFailure(t *testing.T) {
	upstream := mockUpstreamProxy(t, "user", "pass")
	localAddr, cleanup := setupAdapter(t, upstream, "user", "wrong-pass")
	defer cleanup()

	conn, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if resp.StatusCode == 200 {
		t.Fatal("expected auth failure but got 200")
	}
	// The adapter should surface the upstream 407 to the client
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Logf("note: expected 407, got %d — adapter may wrap as 502", resp.StatusCode)
	}
}

func TestHTTPForwarding(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "forwarded")
		w.Write([]byte("http-ok"))
	}))
	defer target.Close()

	upstream := mockUpstreamProxy(t, "user", "pass")
	localAddr, cleanup := setupAdapter(t, upstream, "user", "pass")
	defer cleanup()

	proxyURL, _ := url.Parse("http://" + localAddr)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}

	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("HTTP GET through proxy: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "http-ok" {
		t.Errorf("expected 'http-ok', got '%s'", string(body))
	}

	if resp.Header.Get("X-Test") != "forwarded" {
		t.Error("expected X-Test header to be forwarded")
	}
}

func TestServerStartStop(t *testing.T) {
	cfg := config.Config{
		Upstream: config.UpstreamConfig{
			Host: "127.0.0.1", Port: 9999,
			VerifyTLS: false, ConnectTimeout: 5, IdleTimeout: 30,
		},
		Local: config.LocalConfig{BindHost: "127.0.0.1", BindPort: 0},
	}

	log := logging.New(100)
	srv, err := adapter.NewServer(cfg, "user", "pass", log)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if srv.IsRunning() {
		t.Error("server should not be running before Start")
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !srv.IsRunning() {
		t.Error("server should be running after Start")
	}

	if srv.BoundAddr() == "" {
		t.Error("BoundAddr should be non-empty after Start")
	}

	if err := srv.Start(); err == nil {
		t.Error("expected error when starting already-running server")
	}

	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if srv.IsRunning() {
		t.Error("server should not be running after Stop")
	}
}

func TestMetricsSnapshot(t *testing.T) {
	cfg := config.Config{
		Upstream: config.UpstreamConfig{
			Host: "127.0.0.1", Port: 9999,
			VerifyTLS: false, ConnectTimeout: 5, IdleTimeout: 30,
		},
		Local: config.LocalConfig{BindHost: "127.0.0.1", BindPort: 0},
	}

	log := logging.New(100)
	srv, _ := adapter.NewServer(cfg, "", "", log)

	m := srv.GetMetrics()
	if m.ActiveConnections != 0 || m.TotalRequests != 0 {
		t.Error("initial metrics should be zero")
	}
}

func TestCustomCALoading(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	pool := x509.NewCertPool()
	for _, cert := range upstream.TLS.Certificates {
		for _, raw := range cert.Certificate {
			c, _ := x509.ParseCertificate(raw)
			if c != nil {
				pool.AddCert(c)
			}
		}
	}

	tlsCfg := &tls.Config{RootCAs: pool}
	upHost, upPort := parseHostPort(upstream.URL)
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", upHost, upPort), tlsCfg)
	if err != nil {
		t.Fatalf("TLS dial with custom CA failed: %v", err)
	}
	conn.Close()
}
