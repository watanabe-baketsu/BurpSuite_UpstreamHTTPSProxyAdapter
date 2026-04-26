package adapter

import (
	"fmt"
	"io"
	"net/http"
)

// handleHTTP processes a regular (non-CONNECT) HTTP proxy request.
// It forwards the request through the upstream HTTPS proxy using the shared transport.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	s.log.Info("%s %s", r.Method, r.URL.String())
	s.metrics.TotalRequests.Add(1)
	s.metrics.ActiveConnections.Add(1)
	defer s.metrics.ActiveConnections.Add(-1)

	// Clone request for forwarding
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""

	// Remove hop-by-hop headers before forwarding.
	// Proxy-Authorization for the upstream is injected by http.Transport
	// via the proxy URL's userinfo — no manual header needed.
	removeHopByHop(outReq.Header)

	resp, err := s.transport.RoundTrip(outReq)
	if err != nil {
		errMsg := fmt.Sprintf("upstream request failed: %v", err)
		s.log.Error("%s", errMsg)
		s.metrics.SetError(errMsg)
		http.Error(w, errMsg, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	removeHopByHop(resp.Header)
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	n, _ := io.Copy(w, resp.Body)
	s.metrics.BytesIn.Add(n)
}
