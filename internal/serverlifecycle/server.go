package serverlifecycle

import (
	"context"
	"errors"
	"fmt"
	"github.com/FastTLS/fastls-rpc/internal/shutdown"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"
)

const shutdownTimeout = 5 * time.Second

// ServeHTTP runs server until it exits or ctx is canceled. Cancellation stops
// accepting new connections and gives active requests a bounded grace period.
func ServeHTTP(ctx context.Context, listener net.Listener, server *http.Server) error {
	return serveHTTP(ctx, listener, server, shutdownTimeout)
}

func ServeHTTPWithController(ctx context.Context, listener net.Listener, server *http.Server, owner *shutdown.Controller) error {
	return serveHTTP(ctx, listener, server, shutdownTimeout, owner)
}

func serveHTTP(ctx context.Context, listener net.Listener, server *http.Server, timeout time.Duration, owners ...*shutdown.Controller) error {
	if ctx == nil {
		return errors.New("nil server context")
	}
	if listener == nil || server == nil {
		return errors.New("nil HTTP server or listener")
	}
	if timeout <= 0 {
		return errors.New("shutdown timeout must be positive")
	}
	defer closeConnections(listener)
	var owner *shutdown.Controller
	if len(owners) > 0 {
		owner = owners[0]
	}
	shutdownResult := make(chan error, 1)
	stopCancellation := context.AfterFunc(ctx, func() {
		owner.Begin()
		end := time.Now().Add(timeout)
		if shared, ok := owner.Deadline(); ok {
			end = shared
		}
		shutdownContext, cancel := context.WithDeadline(context.Background(), end)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownContext)
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		shutdownResult <- errors.Join(shutdownErr, owner.Wait(shutdownContext))
	})
	err := server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	if stopCancellation() {
		return err
	}
	return errors.Join(err, <-shutdownResult)
}

// ServeGRPC runs server until it exits or ctx is canceled. Cancellation first
// waits for active RPCs, then forces the server to stop after a bounded delay.
func ServeGRPC(ctx context.Context, listener net.Listener, server *grpc.Server) error {
	return serveGRPC(ctx, listener, server, shutdownTimeout)
}

func ServeGRPCWithController(ctx context.Context, listener net.Listener, server *grpc.Server, owner *shutdown.Controller) error {
	return serveGRPC(ctx, listener, server, shutdownTimeout, owner)
}

func serveGRPC(ctx context.Context, listener net.Listener, server *grpc.Server, timeout time.Duration, owners ...*shutdown.Controller) error {
	if ctx == nil {
		return errors.New("nil server context")
	}
	if listener == nil || server == nil {
		return errors.New("nil gRPC server or listener")
	}
	if timeout <= 0 {
		return errors.New("shutdown timeout must be positive")
	}
	defer closeConnections(listener)
	shutdownResult := make(chan error, 1)
	stopCancellation := context.AfterFunc(ctx, func() {
		shutdownResult <- stopGRPCWithConnections(server, listener, timeout, owners...)
	})
	err := server.Serve(listener)
	if errors.Is(err, grpc.ErrServerStopped) {
		err = nil
	}
	if stopCancellation() {
		return err
	}
	return errors.Join(err, <-shutdownResult)
}

func stopGRPC(server *grpc.Server) error {
	return stopGRPCWithConnections(server, nil, shutdownTimeout)
}

func closeConnections(listener net.Listener) {
	if closer, ok := listener.(interface{ CloseConnections() error }); ok {
		_ = closer.CloseConnections()
	}
}

func stopGRPCWithConnections(server *grpc.Server, listener net.Listener, timeout time.Duration, owners ...*shutdown.Controller) error {
	var owner *shutdown.Controller
	if len(owners) > 0 {
		owner = owners[0]
	}
	owner.Begin()
	end := time.Now().Add(timeout)
	if shared, ok := owner.Deadline(); ok {
		end = shared
	}
	ctx, cancel := context.WithDeadline(context.Background(), end)
	defer cancel()
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()
	timer := time.NewTimer(time.Until(end))
	defer timer.Stop()
	select {
	case <-done:
		return owner.Wait(ctx)
	case <-timer.C:
		closeConnections(listener)
		// Stop may wait on a mutex held by GracefulStop and an uncooperative
		// handler. The CLI must still return a forced-shutdown error on time.
		go server.Stop()
		// A handler ignoring cancellation must not make the forced path unbounded.
		return fmt.Errorf("gRPC graceful shutdown exceeded %s", timeout)
	}
}
