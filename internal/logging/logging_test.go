package logging

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewEmpty(t *testing.T) {
	l := New(10)
	if got := l.Entries(); len(got) != 0 {
		t.Fatalf("new logger should have no entries, got %d", len(got))
	}
}

func TestLevelMethodsAppend(t *testing.T) {
	l := New(100)
	l.Info("info %d", 1)
	l.Warn("warn")
	l.Error("err")
	l.Debug("dbg")

	got := l.Entries()
	if len(got) != 4 {
		t.Fatalf("want 4 entries, got %d", len(got))
	}
	wantLevels := []Level{LevelInfo, LevelWarn, LevelError, LevelDebug}
	for i, lvl := range wantLevels {
		if got[i].Level != lvl {
			t.Errorf("entry[%d].Level = %s, want %s", i, got[i].Level, lvl)
		}
	}
	if got[0].Message != "info 1" {
		t.Errorf("Info formatting failed, got %q", got[0].Message)
	}
	if got[0].Time == "" {
		t.Error("expected Time to be set")
	}
}

// TestRingBufferEviction is the contract that protects the long-running app
// from unbounded growth: once maxSize is hit, oldest entries drop off.
func TestRingBufferEviction(t *testing.T) {
	l := New(3)
	l.Info("a")
	l.Info("b")
	l.Info("c")
	l.Info("d")
	l.Info("e")

	got := l.Entries()
	if len(got) != 3 {
		t.Fatalf("want 3 entries after overflow, got %d", len(got))
	}
	if got[0].Message != "c" || got[2].Message != "e" {
		t.Errorf("oldest entries should have been evicted: got %v", []string{got[0].Message, got[1].Message, got[2].Message})
	}
}

// TestEntriesReturnsCopy guards the invariant that callers can mutate the
// returned slice without poisoning the logger's internal storage. If we ever
// "optimise" by returning the slice directly, this test catches it.
func TestEntriesReturnsCopy(t *testing.T) {
	l := New(5)
	l.Info("hello")
	got := l.Entries()
	got[0].Message = "tampered"

	again := l.Entries()
	if again[0].Message == "tampered" {
		t.Fatal("Entries() returned a live reference; mutations leaked back into the logger")
	}
}

func TestClear(t *testing.T) {
	l := New(5)
	l.Info("a")
	l.Info("b")
	l.Clear()
	if got := l.Entries(); len(got) != 0 {
		t.Fatalf("Clear should remove all entries, got %d", len(got))
	}
	// Logger remains usable after Clear.
	l.Info("c")
	if got := l.Entries(); len(got) != 1 || got[0].Message != "c" {
		t.Errorf("logger broken after Clear: %+v", got)
	}
}

// TestSetCallbackFiresOutsideLock is the regression guard for an earlier bug
// where the callback was invoked while holding the internal mutex, so a
// callback that called back into the logger would deadlock. We verify both
// that the callback fires and that it can re-enter Entries() safely.
func TestSetCallbackFiresOutsideLock(t *testing.T) {
	l := New(10)
	var got atomic.Int32
	l.SetCallback(func(e Entry) {
		// Re-enter the logger from inside the callback. If the lock is still
		// held, this deadlocks the test (caught by Go's `-timeout`).
		_ = l.Entries()
		if e.Message == "ping" {
			got.Add(1)
		}
	})
	l.Info("ping")
	if got.Load() != 1 {
		t.Fatalf("callback was not invoked exactly once, got %d", got.Load())
	}
}

func TestSetCallbackNilSafe(t *testing.T) {
	l := New(5)
	// No callback set: log call must not panic.
	l.Info("no observer")
	if got := l.Entries(); len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
}

// TestConcurrentLogging is the race-detector test. Run with `go test -race`.
// We hammer the logger from many goroutines and confirm we end up with the
// expected number of entries (capped by maxSize).
func TestConcurrentLogging(t *testing.T) {
	const writers = 16
	const perWriter = 100
	l := New(writers * perWriter)

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				l.Info("writer %d msg %d", id, j)
			}
		}(i)
	}
	// Concurrent reader to provoke any read-write race.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = l.Entries()
		}
	}()
	wg.Wait()

	got := l.Entries()
	if want := writers * perWriter; len(got) != want {
		t.Fatalf("want %d entries, got %d", want, len(got))
	}
}
