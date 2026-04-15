package adapter

import (
	"fmt"
	"io"
	"net/http"
	"net/url"

	"burp-upstream-adapter/internal/upstream"
)

// handleHTTP processes a regular (non-CONNECT) HTTP proxy request.
// It forwards the request through the upstream HTTPS proxy using http.Transport.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	s.log.Info("%s %s", r.Method, r.URL.String())
	s.metrics.TotalRequests.Add(1)
	s.metrics.ActiveConnections.Add(1)
	defer s.metrics.ActiveConnections.Add(-1)

	proxyURL := &url.URL{
		Scheme: "https",
		Host:   s.cfg.UpstreamAddr(),
		User:   url.UserPassword(s.username, s.password),
	}

	transport := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: s.tlsCfg,
	}

	// Clone request for forwarding
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""

	// Add proxy auth header explicitly
	outReq.Header.Set("Proxy-Authorization", upstream.BasicAuthHeader(s.username, s.password))

	// Remove hop-by-hop headers
	removeHopByHop(outReq.Header)

	resp, err := transport.RoundTrip(outReq)
	if err != nil {
		errMsg := fmt.Sprintf("upstream request failed: %v", err)
		s.log.Error(errMsg)
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
