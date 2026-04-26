package upstream

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// hostPort splits an httptest.Server URL into ("host", port).
func hostPort(t *testing.T, raw string) (string, int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(portStr)
	return host, p
}

func insecureTLSConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true}
}

// TestCheckTLSSuccess covers the happy path: a reachable TLS server should
// produce OK=true with a non-empty Latency string.
func TestCheckTLSSuccess(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	res := CheckTLS(context.Background(), addr, 5*time.Second, insecureTLSConfig())
	if !res.OK {
		t.Errorf("expected OK, got %+v", res)
	}
	if res.Latency == "" {
		t.Error("expected Latency to be set on success")
	}
}

// TestCheckTLSDialFailure covers the failure surface: a dial against an
// unreachable address must return OK=false with the network error embedded
// in Message rather than panicking.
func TestCheckTLSDialFailure(t *testing.T) {
	// 0 is an invalid port for a connect — guarantees ECONNREFUSED-type error.
	res := CheckTLS(context.Background(), "127.0.0.1:1", 500*time.Millisecond, insecureTLSConfig())
	if res.OK {
		t.Errorf("expected failure for unreachable address, got OK")
	}
	if res.Message == "" {
		t.Error("expected non-empty Message on failure")
	}
}

// TestCheckProxyAuthSuccess covers the 200-from-upstream path: the fake
// proxy returns 200 to a CONNECT, which the healthcheck interprets as
// "credentials accepted".
func TestCheckProxyAuthSuccess(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "expected CONNECT", http.StatusBadRequest)
			return
		}
		// Verify the auth header arrives as Basic with non-empty value.
		if got := r.Header.Get("Proxy-Authorization"); !strings.HasPrefix(got, "Basic ") {
			http.Error(w, "missing Proxy-Authorization", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	res := CheckProxyAuth(context.Background(), addr, 5*time.Second, insecureTLSConfig(), "alice", "secret")
	if !res.OK {
		t.Errorf("expected OK auth result, got %+v", res)
	}
}

// TestCheckProxyAuth407 covers the bad-credentials surface: a 407 must
// produce OK=false with a clear message rather than misclassifying as
// "auth OK" because the response read succeeded.
func TestCheckProxyAuth407(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="proxy"`)
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	res := CheckProxyAuth(context.Background(), addr, 5*time.Second, insecureTLSConfig(), "alice", "wrong")
	if res.OK {
		t.Fatal("expected OK=false for 407 response")
	}
	if !strings.Contains(res.Message, "407") {
		t.Errorf("expected 407 in message, got %q", res.Message)
	}
}

// TestCheckProxyAuthDialFailure covers the early-return when DialTLS itself
// fails. The function must not panic on conn==nil and must surface a Latency.
func TestCheckProxyAuthDialFailure(t *testing.T) {
	res := CheckProxyAuth(context.Background(), "127.0.0.1:1", 500*time.Millisecond, insecureTLSConfig(), "u", "p")
	if res.OK {
		t.Error("expected OK=false on dial failure")
	}
	if res.Latency == "" {
		t.Error("expected Latency even on dial failure")
	}
}

// TestCheckCONNECTSuccess covers the success path of the user-supplied
// target probe.
func TestCheckCONNECTSuccess(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "want CONNECT", http.StatusBadRequest)
			return
		}
		if r.Host != "tunnel.example.com:443" {
			http.Error(w, "wrong host", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	res := CheckCONNECT(context.Background(), addr, 5*time.Second, insecureTLSConfig(), "u", "p", "tunnel.example.com:443")
	if !res.OK {
		t.Errorf("expected OK, got %+v", res)
	}
}

// TestCheckCONNECTRejected covers the non-200 path. The healthcheck must
// surface the upstream status, not the dial latency, in Message.
func TestCheckCONNECTRejected(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusForbidden)
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	res := CheckCONNECT(context.Background(), addr, 5*time.Second, insecureTLSConfig(), "u", "p", "blocked.example.com:443")
	if res.OK {
		t.Fatal("expected OK=false on 403")
	}
	if !strings.Contains(res.Message, "403") {
		t.Errorf("expected 403 in message, got %q", res.Message)
	}
}

// TestCheckHTTPSuccess sends a regular GET via the proxy. Any 2xx/3xx is
// considered OK by the healthcheck (matches the contract used by the
// frontend "Test HTTP" button).
func TestCheckHTTPSuccess(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Healthcheck sends an absolute-URL request line ("GET http://… HTTP/1.1").
		// httptest parses that as r.URL.Scheme="http" + Host populated.
		if r.URL.Scheme != "http" {
			http.Error(w, "want absolute URL", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	res := CheckHTTP(context.Background(), addr, 5*time.Second, insecureTLSConfig(), "u", "p", "http://example.com/")
	if !res.OK {
		t.Errorf("expected OK, got %+v", res)
	}
}

// TestCheckHTTPNon2xx flags a 5xx response as failure. Without this
// distinction the user can't tell apart "proxy reachable" from "the target
// is broken", which is exactly what the diagnostic exists to surface.
func TestCheckHTTPNon2xx(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	res := CheckHTTP(context.Background(), addr, 5*time.Second, insecureTLSConfig(), "u", "p", "http://example.com/")
	if res.OK {
		t.Fatalf("expected OK=false on 503, got %+v", res)
	}
	if !strings.Contains(res.Message, "503") {
		t.Errorf("expected 503 in message, got %q", res.Message)
	}
}

func TestHostFromURL(t *testing.T) {
	cases := map[string]string{
		"http://example.com/path":   "example.com",
		"https://example.com:8443/": "example.com:8443",
		"example.com":               "example.com",
		"example.com/foo":           "example.com",
		"":                          "",
	}
	for in, want := range cases {
		if got := hostFromURL(in); got != want {
			t.Errorf("hostFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}
