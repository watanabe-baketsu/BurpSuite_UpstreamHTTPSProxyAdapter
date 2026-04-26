package adapter

import (
	"sync/atomic"
	"time"
)

type Metrics struct {
	ActiveConnections atomic.Int64
	TotalRequests     atomic.Int64
	BytesIn           atomic.Int64
	BytesOut          atomic.Int64
	LastError         atomic.Value // string
	LastErrorAt       atomic.Int64 // unix nano; 0 = no error recorded
}

type MetricsSnapshot struct {
	ActiveConnections int64     `json:"active_connections"`
	TotalRequests     int64     `json:"total_requests"`
	BytesIn           int64     `json:"bytes_in"`
	BytesOut          int64     `json:"bytes_out"`
	LastError         string    `json:"last_error"`
	LastErrorAt       time.Time `json:"last_error_at"`
}

func NewMetrics() *Metrics {
	m := &Metrics{}
	m.LastError.Store("")
	return m
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	lastErr, _ := m.LastError.Load().(string)
	var at time.Time
	if ns := m.LastErrorAt.Load(); ns != 0 {
		at = time.Unix(0, ns)
	}
	return MetricsSnapshot{
		ActiveConnections: m.ActiveConnections.Load(),
		TotalRequests:     m.TotalRequests.Load(),
		BytesIn:           m.BytesIn.Load(),
		BytesOut:          m.BytesOut.Load(),
		LastError:         lastErr,
		LastErrorAt:       at,
	}
}

// SetError records the latest error message and stamps the time so callers
// can distinguish a fresh failure from a stale one (used by the system tray
// to drive the "error" icon state for a short window after each failure).
func (m *Metrics) SetError(err string) {
	m.LastError.Store(err)
	m.LastErrorAt.Store(time.Now().UnixNano())
}

// ClearError wipes the recorded error so the next start of the proxy doesn't
// immediately surface a stale error from a previous run.
func (m *Metrics) ClearError() {
	m.LastError.Store("")
	m.LastErrorAt.Store(0)
}
