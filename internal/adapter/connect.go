package adapter

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"burp-upstream-adapter/internal/upstream"
)

// handleCONNECT processes an HTTP CONNECT request by establishing a tunnel
// through the upstream HTTPS proxy.
//
// Flow:
//  1. TLS-connect to upstream proxy
//  2. Send CONNECT target:port with Proxy-Authorization to upstream
//  3. Read upstream response
//  4. If 200, reply 200 to client and relay bytes bidirectionally
//  5. If error, forward the error response to the client
func (s *Server) handleCONNECT(w http.ResponseWriter, r *http.Request) {
	targetHost := r.Host
	if targetHost == "" {
		targetHost = r.URL.Host
	}
	if targetHost == "" {
		http.Error(w, "CONNECT target missing", http.StatusBadRequest)
		return
	}

	s.log.Info("CONNECT %s", targetHost)
	s.metrics.TotalRequests.Add(1)
	s.metrics.ActiveConnections.Add(1)
	defer s.metrics.ActiveConnections.Add(-1)

	ctx, cancel := context.WithTimeout(s.ctx, s.cfg.ConnectTimeoutDuration())
	defer cancel()

	// Step 1: TLS-connect to upstream proxy
	upstreamConn, err := upstream.DialTLS(ctx, s.cfg.UpstreamAddr(), s.cfg.ConnectTimeoutDuration(), s.tlsCfg)
	if err != nil {
		errMsg := fmt.Sprintf("upstream TLS dial failed: %v", err)
		s.log.Error(errMsg)
		s.metrics.SetError(errMsg)
		http.Error(w, errMsg, http.StatusBadGateway)
		return
	}

	// Step 2: Send CONNECT to upstream proxy with auth
	authHeader := upstream.BasicAuthHeader(s.username, s.password)
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n",
		targetHost, targetHost, authHeader)

	if _, err := io.WriteString(upstreamConn, connectReq); err != nil {
		upstreamConn.Close()
		errMsg := fmt.Sprintf("upstream CONNECT write failed: %v", err)
		s.log.Error(errMsg)
		s.metrics.SetError(errMsg)
		http.Error(w, errMsg, http.StatusBadGateway)
		return
	}

	// Step 3: Read upstream response
	br := bufio.NewReader(upstreamConn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		upstreamConn.Close()
		errMsg := fmt.Sprintf("upstream CONNECT response read failed: %v", err)
		s.log.Error(errMsg)
		s.metrics.SetError(errMsg)
		http.Error(w, errMsg, http.StatusBadGateway)
		return
	}

	if resp.StatusCode != http.StatusOK {
		// Forward error to Burp
		errMsg := fmt.Sprintf("upstream CONNECT rejected: %s", resp.Status)
		s.log.Warn(errMsg)
		s.metrics.SetError(errMsg)

		// Hijack to send raw response
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			upstreamConn.Close()
			http.Error(w, errMsg, http.StatusBadGateway)
			return
		}
		clientConn, clientBuf, err := hijacker.Hijack()
		if err != nil {
			upstreamConn.Close()
			s.log.Error("hijack failed: %v", err)
			return
		}
		defer clientConn.Close()
		defer upstreamConn.Close()
		_ = resp.Write(clientBuf)
		clientBuf.Flush()
		return
	}
	resp.Body.Close()

	// Step 4: Reply 200 to client (Burp)
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstreamConn.Close()
		s.log.Error("ResponseWriter does not support Hijack")
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		upstreamConn.Close()
		s.log.Error("hijack failed: %v", err)
		return
	}

	_, _ = clientBuf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	_ = clientBuf.Flush()

	s.log.Debug("CONNECT tunnel established: %s", targetHost)

	// Step 5: Bidirectional relay
	s.relay(clientConn, upstreamConn, br)
}

func (s *Server) relay(client net.Conn, upstreamConn net.Conn, upstreamBuf *bufio.Reader) {
	var wg sync.WaitGroup
	wg.Add(2)

	// upstream → client
	go func() {
		defer wg.Done()
		n, _ := io.Copy(client, upstreamBuf)
		s.metrics.BytesIn.Add(n)
		closeWrite(client)
	}()

	// client → upstream
	go func() {
		defer wg.Done()
		n, _ := io.Copy(upstreamConn, client)
		s.metrics.BytesOut.Add(n)
		closeWrite(upstreamConn)
	}()

	wg.Wait()
	client.Close()
	upstreamConn.Close()
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
}
