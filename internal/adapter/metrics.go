package adapter

import "sync/atomic"

type Metrics struct {
	ActiveConnections atomic.Int64
	TotalRequests     atomic.Int64
	BytesIn           atomic.Int64
	BytesOut          atomic.Int64
	LastError         atomic.Value // string
}

type MetricsSnapshot struct {
	ActiveConnections int64  `json:"active_connections"`
	TotalRequests     int64  `json:"total_requests"`
	BytesIn           int64  `json:"bytes_in"`
	BytesOut          int64  `json:"bytes_out"`
	LastError         string `json:"last_error"`
}

func NewMetrics() *Metrics {
	m := &Metrics{}
	m.LastError.Store("")
	return m
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	lastErr, _ := m.LastError.Load().(string)
	return MetricsSnapshot{
		ActiveConnections: m.ActiveConnections.Load(),
		TotalRequests:     m.TotalRequests.Load(),
		BytesIn:           m.BytesIn.Load(),
		BytesOut:          m.BytesOut.Load(),
		LastError:         lastErr,
	}
}

func (m *Metrics) SetError(err string) {
	m.LastError.Store(err)
}
