package adapter

import (
	"sync"
	"testing"
	"time"
)

func TestNewMetricsZero(t *testing.T) {
	m := NewMetrics()
	s := m.Snapshot()
	if s.ActiveConnections != 0 || s.TotalRequests != 0 || s.BytesIn != 0 || s.BytesOut != 0 {
		t.Errorf("fresh metrics should be zero, got %+v", s)
	}
	if s.LastError != "" {
		t.Errorf("fresh metrics should have empty LastError, got %q", s.LastError)
	}
	if !s.LastErrorAt.IsZero() {
		t.Errorf("fresh metrics should have zero LastErrorAt, got %v", s.LastErrorAt)
	}
}

// TestSetAndClearError covers the regression that motivated keeping
// ClearError on the public surface: after a failure, the tray's error icon
// stays sticky for `errorFreshness` seconds, but a successful restart should
// be able to clear the slate. Without ClearError there's no way to do that.
func TestSetAndClearError(t *testing.T) {
	m := NewMetrics()
	before := time.Now()
	m.SetError("boom")
	s := m.Snapshot()
	if s.LastError != "boom" {
		t.Errorf("LastError = %q, want %q", s.LastError, "boom")
	}
	if s.LastErrorAt.Before(before.Add(-time.Second)) {
		t.Errorf("LastErrorAt should be ~now, got %v", s.LastErrorAt)
	}

	m.ClearError()
	s = m.Snapshot()
	if s.LastError != "" {
		t.Errorf("LastError after Clear should be empty, got %q", s.LastError)
	}
	if !s.LastErrorAt.IsZero() {
		t.Errorf("LastErrorAt after Clear should be zero, got %v", s.LastErrorAt)
	}
}

// TestSnapshotIncludesAllCounters guards against the easy-to-miss case where
// adding a new counter to Metrics is forgotten in Snapshot. We bump every
// field and verify it's reflected.
func TestSnapshotIncludesAllCounters(t *testing.T) {
	m := NewMetrics()
	m.ActiveConnections.Add(2)
	m.TotalRequests.Add(7)
	m.BytesIn.Add(1024)
	m.BytesOut.Add(2048)

	s := m.Snapshot()
	if s.ActiveConnections != 2 {
		t.Errorf("ActiveConnections = %d, want 2", s.ActiveConnections)
	}
	if s.TotalRequests != 7 {
		t.Errorf("TotalRequests = %d, want 7", s.TotalRequests)
	}
	if s.BytesIn != 1024 {
		t.Errorf("BytesIn = %d, want 1024", s.BytesIn)
	}
	if s.BytesOut != 2048 {
		t.Errorf("BytesOut = %d, want 2048", s.BytesOut)
	}
}

// TestConcurrentMetricUpdates is the race-detector test for the metrics
// struct. Many concurrent SetError + counter increments must not race; the
// final totals must match the sum of contributions exactly.
func TestConcurrentMetricUpdates(t *testing.T) {
	m := NewMetrics()
	const writers = 16
	const ops = 100

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				m.TotalRequests.Add(1)
				m.BytesIn.Add(10)
				m.BytesOut.Add(20)
				m.SetError("oops")
			}
		}()
	}
	wg.Wait()

	s := m.Snapshot()
	if want := int64(writers * ops); s.TotalRequests != want {
		t.Errorf("TotalRequests = %d, want %d", s.TotalRequests, want)
	}
	if want := int64(writers * ops * 10); s.BytesIn != want {
		t.Errorf("BytesIn = %d, want %d", s.BytesIn, want)
	}
	if s.LastError != "oops" {
		t.Errorf("LastError = %q, want %q", s.LastError, "oops")
	}
}
