package server

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/routatic/proxy/internal/config"
)

// TestStart_SIGTERMWaitsForInFlightRequest checks that Start, which the serve
// command returns from directly, does not return while a request that began
// before SIGTERM is still being served.
func TestStart_SIGTERMWaitsForInFlightRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	dir := t.TempDir()
	cfg := &config.Config{
		APIKey:  "test-key",
		Host:    "127.0.0.1",
		Port:    port,
		Storage: &config.StorageConfig{DatabasePath: filepath.Join(dir, "data.db"), RetentionDays: 1},
	}
	srv, err := NewServer(config.NewAtomicConfig(cfg, filepath.Join(dir, "config.json")), nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	started := make(chan struct{})
	var finished atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(time.Second)
		finished.Store(true)
		_, _ = io.WriteString(w, "done")
	})
	srv.mux = mux

	returned := make(chan error, 1)
	go func() { returned <- srv.Start() }()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, dialErr := net.Dial("tcp", addr)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never listened on %s: %v", addr, dialErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	type result struct {
		body string
		err  error
	}
	responses := make(chan result, 1)
	go func() {
		resp, getErr := http.Get("http://" + addr + "/slow")
		if getErr != nil {
			responses <- result{err: getErr}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, readErr := io.ReadAll(resp.Body)
		responses <- result{body: string(body), err: readErr}
	}()

	<-started
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Start returned an error: %v", err)
		}
	case <-time.After(ShutdownDrainTimeout + 5*time.Second):
		t.Fatal("Start did not return after SIGTERM")
	}
	if !finished.Load() {
		t.Fatal("Start returned while a request was still in flight")
	}
	if r := <-responses; r.err != nil || r.body != "done" {
		t.Fatalf("in-flight request did not complete: body=%q err=%v", r.body, r.err)
	}
}
