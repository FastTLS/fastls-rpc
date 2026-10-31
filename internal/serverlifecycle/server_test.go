package serverlifecycle

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/FastTLS/fastls-rpc/internal/ingress"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type observedHandshakeListener struct {
	*ingress.Listener
	reading chan struct{}
	once    sync.Once
}

func (l *observedHandshakeListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &observedHandshakeConn{Conn: conn, listener: l}, nil
}

type observedHandshakeConn struct {
	net.Conn
	listener *observedHandshakeListener
}

func (c *observedHandshakeConn) Read(data []byte) (int, error) {
	c.listener.once.Do(func() { close(c.listener.reading) })
	return c.Conn.Read(data)
}

func TestServeGRPCForcesUnregisteredHandshakeClosed(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limited, err := ingress.NewListener(inner, 0)
	if err != nil {
		t.Fatal(err)
	}
	listener := &observedHandshakeListener{Listener: limited, reading: make(chan struct{})}
	defer listener.CloseConnections()
	server := grpc.NewServer(grpc.ConnectionTimeout(time.Minute))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveGRPC(ctx, listener, server, 50*time.Millisecond) }()
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-listener.reading:
	case <-time.After(time.Second):
		t.Fatal("server did not begin HTTP2 handshake")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("forced shutdown was reported as graceful")
		}
	case <-time.After(time.Second):
		listener.CloseConnections()
		server.Stop()
		t.Fatal("shutdown waited for 120s handshake timeout")
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var data [4096]byte
	for {
		_, err := conn.Read(data[:])
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatal("raw handshake connection still holds a lease")
			}
			break
		}
	}
}

func TestServeHTTPWaitsForActiveRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeHTTP(ctx, listener, server) }()
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
		requestDone <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP request did not start")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("HTTP server stopped before active request completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP server did not finish graceful shutdown")
	}
}

func TestServeGRPCStopsOnCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeGRPC(ctx, listener, server) }()

	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gRPC server did not stop after cancellation")
	}
	if connection, err := net.DialTimeout("tcp", listener.Addr().String(), 200*time.Millisecond); err == nil {
		connection.Close()
		t.Fatal("gRPC listener still accepts connections after shutdown")
	}
}

func TestServeHTTPForcesCloseAfterTimeout(t *testing.T) {
	started := make(chan struct{})
	handlerCanceled := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
		close(handlerCanceled)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, listener, server, 50*time.Millisecond) }()
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
		requestDone <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP server did not enforce shutdown timeout")
	}
	select {
	case <-handlerCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("active HTTP handler was not canceled")
	}
	select {
	case <-requestDone:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP client remained blocked after forced shutdown")
	}
}

func TestForcedGRPCDoesNotWaitForUncooperativeHandler(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		close(started)
		<-release
		return handler(ctx, request)
	}))
	healthpb.RegisterHealthServer(server, health.NewServer())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	defer func() { close(release); server.Stop() }()
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	go func() {
		_, _ = healthpb.NewHealthClient(connection).Check(context.Background(), &healthpb.HealthCheckRequest{})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler not started")
	}
	done := make(chan error, 1)
	go func() { done <- stopGRPCWithConnections(server, listener, 30*time.Millisecond) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("forced cleanup must fail")
		}
	case <-time.After(time.Second):
		t.Fatal("force path waited for handler")
	}
}
