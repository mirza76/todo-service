package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/mirza76/todo-service/internal/config"
	"github.com/mirza76/todo-service/internal/health"
)

// TestServe_GracefulShutdown verifies the shutdown sequence end to end on a
// real TCP listener: readiness flips to 503 while the server keeps serving
// during the drain delay, an in-flight request completes, and serve returns
// cleanly.
func TestServe_GracefulShutdown(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	checker := health.New(logger, time.Second)

	slowStarted := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /readyz", checker.Ready)
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		close(slowStarted)
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "done")
	})

	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	base := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(t.Context())
	shutdown := config.ShutdownConfig{Delay: 200 * time.Millisecond, Timeout: 2 * time.Second}
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, func() {}, &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}, ln, checker, shutdown, logger)
	}()

	if code := get(t, base+"/readyz"); code != http.StatusOK {
		t.Fatalf("readyz before shutdown = %d, want 200", code)
	}

	// Start a slow request, then begin shutdown while it is in flight.
	slowResult := make(chan int, 1)
	go func() { slowResult <- get(t, base+"/slow") }()
	<-slowStarted
	cancel()

	// During the drain delay the server still answers, but reports not-ready.
	time.Sleep(50 * time.Millisecond)
	if code := get(t, base+"/readyz"); code != http.StatusServiceUnavailable {
		t.Errorf("readyz during shutdown = %d, want 503", code)
	}

	if code := <-slowResult; code != http.StatusOK {
		t.Errorf("in-flight request = %d, want 200 (it must not be cut off)", code)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve() = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve() did not return after shutdown")
	}

	// After shutdown, the listener is closed.
	dialer := net.Dialer{Timeout: 200 * time.Millisecond}
	if conn, err := dialer.DialContext(t.Context(), "tcp", ln.Addr().String()); err == nil {
		_ = conn.Close()
		t.Error("listener still accepting connections after shutdown")
	}
}

func get(t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Errorf("new request: %v", err)
		return 0
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Errorf("GET %s: %v", url, err)
		return 0
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}
