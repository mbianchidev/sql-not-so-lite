package daemon

import (
	"context"
	"testing"
	"time"
)

func TestParseScanInterval(t *testing.T) {
	if got := parseScanInterval("45m"); got != 45*time.Minute {
		t.Fatalf("parseScanInterval(45m) = %s", got)
	}

	for _, value := range []string{"invalid", "0s", "-1m"} {
		if got := parseScanInterval(value); got != 30*time.Minute {
			t.Fatalf("parseScanInterval(%q) = %s, want 30m", value, got)
		}
	}
}

func TestRunPeriodicScannerRunsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := make(chan struct{}, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		runPeriodicScanner(ctx, time.Millisecond, func(context.Context) (int, error) {
			calls <- struct{}{}
			return 1, nil
		})
	}()

	select {
	case <-calls:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("scheduled scan did not run")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduled scanner did not stop")
	}
}
