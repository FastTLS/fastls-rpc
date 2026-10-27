package ingress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func listen(t *testing.T, maximum int) *Listener {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := NewListener(inner, maximum)
	if err != nil {
		inner.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.CloseConnections() })
	return listener
}
func dial(t *testing.T, address string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	return conn
}
func closed(t *testing.T, conn net.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var data [1]byte
	n, err := conn.Read(data[:])
	if n != 0 || err == nil {
		t.Fatalf("expected closed connection: n=%d err=%v", n, err)
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatalf("connection was left open: %v", err)
	}
}
func wait(t *testing.T, probe func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if probe() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("resource did not recover")
}

func TestConnectionsCountBeforeParsingAndReleaseOnce(t *testing.T) {
	listener := listen(t, 2)
	accepted := make(chan net.Conn, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	first := dial(t, listener.Addr().String())
	a := <-accepted
	second := dial(t, listener.Addr().String())
	b := <-accepted
	excess := dial(t, listener.Addr().String())
	closed(t, excess)
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() { defer group.Done(); a.Close() }()
	}
	group.Wait()
	closed(t, first)
	replacement := dial(t, listener.Addr().String())
	c := <-accepted
	excess = dial(t, listener.Addr().String())
	closed(t, excess)
	// Closing the listener stops admission but does not revoke active leases.
	listener.Close()
	if _, err := b.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	if _, err := io.ReadFull(second, data[:]); err != nil || data[0] != 'b' {
		t.Fatal(err)
	}
	if err := listener.CloseConnections(); err != nil {
		t.Fatal(err)
	}
	closed(t, second)
	closed(t, replacement)
	c.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("accept did not stop")
	}
	listener.mu.Lock()
	defer listener.mu.Unlock()
	if len(listener.connections) != 0 {
		t.Fatal("closed connections still hold permits")
	}
}

func TestRequestPermitCancellationAndConcurrentRelease(t *testing.T) {
	gate, _ := NewGate(1)
	release, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Acquire(context.Background()); !errors.Is(err, ErrOverloaded) {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() { defer group.Done(); release() }()
	}
	group.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gate.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release, err = gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func startHTTP(t *testing.T, config HTTPConfig, handler http.Handler) (string, *Listener) {
	t.Helper()
	listener := listen(t, config.MaxConnections)
	server, err := NewHTTPServer(handler, config)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Close()
		listener.CloseConnections()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("HTTP server did not stop")
		}
	})
	return listener.Addr().String(), listener
}
func health(address string) bool {
	client := &http.Client{Timeout: 200 * time.Millisecond, Transport: &http.Transport{DisableKeepAlives: true}}
	response, err := client.Get("http://" + address + "/health")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)
	return response.StatusCode == 200
}

func TestHTTPAdmissionRejectsBeforeSlowBodyAndRecovers(t *testing.T) {
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	address, _ := startHTTP(t, HTTPConfig{Limits: Limits{MaxConnections: 4, MaxAdmittedRequests: 1}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/hold" {
			started <- struct{}{}
			io.Copy(io.Discard, r.Body)
		}
		w.WriteHeader(200)
	}))
	holder := dial(t, address)
	fmt.Fprint(holder, "POST /hold HTTP/1.1\r\nHost: local\r\nContent-Length: 100\r\n\r\nx")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first request not admitted")
	}
	rejected := dial(t, address)
	fmt.Fprint(rejected, "POST /hold HTTP/1.1\r\nHost: local\r\nContent-Length: 100\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(rejected), nil)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 503 || calls.Load() != 1 {
		t.Fatalf("request decoded while full: status=%d calls=%d", response.StatusCode, calls.Load())
	}
	holder.Close()
	wait(t, func() bool { return health(address) })
}

func TestHTTPReadDeadlinesAndIdleConnectionsRecover(t *testing.T) {
	for _, mode := range []string{"headers", "body", "idle"} {
		t.Run(mode, func(t *testing.T) {
			config := HTTPConfig{Limits: Limits{MaxConnections: 1}, ReadHeaderTimeout: 120 * time.Millisecond, ReadTimeout: 180 * time.Millisecond, IdleTimeout: 120 * time.Millisecond}
			address, _ := startHTTP(t, config, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); w.WriteHeader(200) }))
			conn := dial(t, address)
			switch mode {
			case "headers":
				fmt.Fprint(conn, "GET / HTTP/1.1\r\nHo")
			case "body":
				fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: local\r\nContent-Length: 100\r\n\r\nx")
			case "idle":
				fmt.Fprint(conn, "GET /health HTTP/1.1\r\nHost: local\r\n\r\n")
				response, err := http.ReadResponse(bufio.NewReader(conn), nil)
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
			}
			// Body timeout may first produce an error response; consume it to EOF.
			conn.SetReadDeadline(time.Now().Add(time.Second))
			_, err := io.Copy(io.Discard, conn)
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatal("read/idle deadline did not close connection")
			}
			wait(t, func() bool { return health(address) })
		})
	}
}

func TestHTTPHeaderBudgetRejectsBeforeHandler(t *testing.T) {
	var calls atomic.Int32
	address, _ := startHTTP(t, HTTPConfig{Limits: Limits{MaxHeaderBytes: 1024}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	conn := dial(t, address)
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: local\r\nX-Pad: %s\r\n\r\n", strings.Repeat("x", 8192))
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 431 || calls.Load() != 0 {
		t.Fatalf("oversized headers accepted: %d calls=%d", response.StatusCode, calls.Load())
	}
	wait(t, func() bool { return health(address) })
}

func TestIngressConfigurationBounds(t *testing.T) {
	for _, limits := range []Limits{{MaxConnections: -1}, {MaxConnections: 65537}, {MaxAdmittedRequests: -1}, {MaxAdmittedRequests: 65537}, {MaxHeaderBytes: -1}, {MaxHeaderBytes: (16 << 20) + 1}} {
		if limits.Validate() == nil {
			t.Fatal("invalid startup limits accepted", limits)
		}
	}
	for _, config := range []HTTPConfig{{ReadTimeout: -1}, {IdleTimeout: 25 * time.Hour}, {ReadHeaderTimeout: -1}} {
		if config.Validate() == nil {
			t.Fatal("invalid startup duration accepted", config)
		}
	}
}
