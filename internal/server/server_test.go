package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestServe_WaitsForInFlightRequestOnShutdown(t *testing.T) {
	started := make(chan struct{})
	var finished atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond)
		finished.Store(true)
		io.WriteString(w, "done")
	})
	s := &Server{
		httpSrv: &http.Server{Handler: mux},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.serve(ctx, ln) }()

	type result struct {
		body string
		err  error
	}
	responses := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			responses <- result{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		responses <- result{body: string(body), err: err}
	}()

	<-started
	cancel()

	if err := <-served; err != nil {
		t.Fatalf("serve returned an error: %v", err)
	}
	if !finished.Load() {
		t.Fatal("serve returned while a request was still in flight")
	}
	if r := <-responses; r.err != nil || r.body != "done" {
		t.Fatalf("in-flight request did not complete: body=%q err=%v", r.body, r.err)
	}
}
