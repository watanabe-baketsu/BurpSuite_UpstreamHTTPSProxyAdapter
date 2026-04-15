package adapter

import "net/http"

// Hop-by-hop headers that must not be forwarded.
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func removeHopByHop(h http.Header) {
	for _, hdr := range hopByHopHeaders {
		h.Del(hdr)
	}
	// Also handle Connection header's listed headers
	if conn := h.Get("Connection"); conn != "" {
		for _, f := range splitCSV(conn) {
			h.Del(f)
		}
	}
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func splitCSV(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			token := trimSpace(s[start:i])
			if token != "" {
				result = append(result, token)
			}
			start = i + 1
		}
	}
	token := trimSpace(s[start:])
	if token != "" {
		result = append(result, token)
	}
	return result
}

func trimSpace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	j := len(s)
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}
