package main

import (
	"testing"
	"time"
)

// TestStatusLabels pins the wording the user sees in the menu bar. If
// either string drifts (e.g. "Stop Proxy" → "Stop"), screenshots in the
// docs and the e2e tests stay in sync because this test catches it first.
func TestStatusLabels(t *testing.T) {
	cases := []struct {
		name           string
		running        bool
		port           int
		wantStatus     string
		wantStartStop  string
	}{
		{"running", true, 18080, "Status: Running (18080)", "Stop Proxy"},
		{"stopped", false, 18080, "Status: Stopped", "Start Proxy"},
		{"running-on-zero-port", true, 0, "Status: Running (0)", "Stop Proxy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotStatus, gotAction := statusLabels(c.running, c.port)
			if gotStatus != c.wantStatus {
				t.Errorf("status = %q, want %q", gotStatus, c.wantStatus)
			}
			if gotAction != c.wantStartStop {
				t.Errorf("action = %q, want %q", gotAction, c.wantStartStop)
			}
		})
	}
}

// TestMetricLabels protects against off-by-one or pluralisation
// regressions in the tray's metric rows.
func TestMetricLabels(t *testing.T) {
	connLabel, reqLabel := metricLabels(0, 0)
	if connLabel != "Active: 0 connections" {
		t.Errorf("connLabel = %q", connLabel)
	}
	if reqLabel != "Total: 0 requests" {
		t.Errorf("reqLabel = %q", reqLabel)
	}
	connLabel, reqLabel = metricLabels(42, 1234)
	if connLabel != "Active: 42 connections" || reqLabel != "Total: 1234 requests" {
		t.Errorf("non-zero metrics formatting wrong: %q / %q", connLabel, reqLabel)
	}
}

// TestTrayTooltipPriority pins the contract that a fresh error wins over
// the running state. This is the visual cue the user relies on to spot
// transient failures; if "running" ever shadowed an error, recent
// breakages would silently disappear from the tray hover text.
func TestTrayTooltipPriority(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name        string
		running     bool
		port        int
		lastErr     string
		lastErrorAt time.Time
		want        string
	}{
		{
			"running-no-error",
			true, 18080, "", time.Time{},
			"Burp Upstream HTTPS Proxy Adapter — Running (port 18080)",
		},
		{
			"stopped-no-error",
			false, 18080, "", time.Time{},
			"Burp Upstream HTTPS Proxy Adapter — Stopped",
		},
		{
			"fresh-error-while-running-shows-error",
			true, 18080, "boom", now,
			"Burp Upstream HTTPS Proxy Adapter — Error: boom",
		},
		{
			"stale-error-falls-back-to-state",
			true, 18080, "old", now.Add(-2 * errorFreshness),
			"Burp Upstream HTTPS Proxy Adapter — Running (port 18080)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := trayTooltip(c.running, c.port, c.lastErr, c.lastErrorAt); got != c.want {
				t.Errorf("trayTooltip = %q, want %q", got, c.want)
			}
		})
	}
}

// errorIsFresh drives the tray's "error" icon state. The contract is:
//   - zero-value time → not fresh (no error has ever been recorded)
//   - within errorFreshness window → fresh
//   - outside window → not fresh
//
// Without these tests the icon either stays sticky after recovery (if the
// zero check is dropped) or never lights up (if the comparison flips).
func TestErrorIsFresh(t *testing.T) {
	if errorIsFresh(time.Time{}) {
		t.Error("zero time should not count as a fresh error")
	}
	if !errorIsFresh(time.Now()) {
		t.Error("right-now error should be fresh")
	}
	// Just inside the window — comfortably fresh.
	if !errorIsFresh(time.Now().Add(-errorFreshness / 2)) {
		t.Error("error within freshness window should be fresh")
	}
	// Just outside the window — must drop back to non-fresh.
	if errorIsFresh(time.Now().Add(-errorFreshness - time.Second)) {
		t.Error("error outside freshness window should not be fresh")
	}
}
