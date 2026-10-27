package serverlifecycle

import (
	"context"
	"errors"
	"fmt"
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

func serveHTTP(ctx context.Context, listener net.Listener, server *http.Server, timeout time.Duration) error {
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
	shutdownResult := make(chan error, 1)
	stopCancellation := context.AfterFunc(ctx, func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownContext)
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		shutdownResult <- shutdownErr
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

func serveGRPC(ctx context.Context, listener net.Listener, server *grpc.Server, timeout time.Duration) error {
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
		shutdownResult <- stopGRPCWithConnections(server, listener, timeout)
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

func stopGRPCWithConnections(server *grpc.Server, listener net.Listener, timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		closeConnections(listener)
		server.Stop()
		<-done
		return fmt.Errorf("gRPC graceful shutdown exceeded %s", timeout)
	}
}
