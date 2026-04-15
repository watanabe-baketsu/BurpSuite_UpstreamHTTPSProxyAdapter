package adapter

import (
	"net/http"
	"testing"
)

func TestRemoveHopByHop(t *testing.T) {
	h := http.Header{}
	h.Set("Connection", "keep-alive")
	h.Set("Keep-Alive", "timeout=5")
	h.Set("Proxy-Authorization", "Basic xxx")
	h.Set("Content-Type", "text/html")
	h.Set("X-Custom", "value")

	removeHopByHop(h)

	if h.Get("Connection") != "" {
		t.Error("Connection header should be removed")
	}
	if h.Get("Keep-Alive") != "" {
		t.Error("Keep-Alive header should be removed")
	}
	if h.Get("Proxy-Authorization") != "" {
		t.Error("Proxy-Authorization header should be removed")
	}
	if h.Get("Content-Type") != "text/html" {
		t.Error("Content-Type should be preserved")
	}
	if h.Get("X-Custom") != "value" {
		t.Error("X-Custom should be preserved")
	}
}

func TestRemoveHopByHopConnectionListed(t *testing.T) {
	// RFC 7230 §6.1: headers listed in Connection must also be removed.
	h := http.Header{}
	h.Set("Connection", "X-Remove-Me, X-Also-Remove")
	h.Set("X-Remove-Me", "should-go")
	h.Set("X-Also-Remove", "should-go-too")
	h.Set("X-Keep", "keep-this")

	removeHopByHop(h)

	if h.Get("X-Remove-Me") != "" {
		t.Error("X-Remove-Me listed in Connection should be removed")
	}
	if h.Get("X-Also-Remove") != "" {
		t.Error("X-Also-Remove listed in Connection should be removed")
	}
	if h.Get("X-Keep") != "keep-this" {
		t.Error("X-Keep should be preserved")
	}
}

func TestCopyHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", "text/html")
	src.Add("Set-Cookie", "a=1")
	src.Add("Set-Cookie", "b=2")

	dst := http.Header{}
	copyHeaders(dst, src)

	if dst.Get("Content-Type") != "text/html" {
		t.Error("Content-Type not copied")
	}
	cookies := dst.Values("Set-Cookie")
	if len(cookies) != 2 {
		t.Errorf("expected 2 Set-Cookie values, got %d", len(cookies))
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"a, b, c", []string{"a", "b", "c"}},
		{"keep-alive", []string{"keep-alive"}},
		{"", nil},
		{"  x  ,  y  ", []string{"x", "y"}},
	}
	for _, tt := range tests {
		got := splitCSV(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}
