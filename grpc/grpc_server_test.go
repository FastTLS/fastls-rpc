package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/serverlifecycle"
	"github.com/FastTLS/fastls-rpc/internal/testsupport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestGRPCServerStopsOnCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterFastlsServiceServer(server, NewGRPCServer())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serverlifecycle.ServeGRPC(ctx, listener, server) }()

	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	requestContext, requestCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer requestCancel()
	response, err := pb.NewFastlsServiceClient(connection).Health(requestContext, &pb.HealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "ok" {
		t.Fatalf("health status = %q", response.Status)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gRPC server did not stop after cancellation")
	}
}

func TestGRPCServerWaitsForActiveRPC(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-releaseRequest
		_, _ = io.WriteString(w, "completed")
	}))
	defer target.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterFastlsServiceServer(server, NewGRPCServer())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serverlifecycle.ServeGRPC(ctx, listener, server) }()
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	rpcDone := make(chan error, 1)
	go func() {
		response, err := pb.NewFastlsServiceClient(connection).Fetch(context.Background(), &pb.FetchRequest{Url: target.URL})
		if err == nil && (!response.Ok || response.Body != "completed") {
			err = status.Errorf(codes.Internal, "unexpected fetch response: %+v", response)
		}
		rpcDone <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("active RPC did not reach downstream target")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("gRPC server stopped before active RPC completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseRequest)
	if err := <-rpcDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gRPC server did not finish graceful shutdown")
	}
}

func TestGRPCFetchWiresHTTP2Settings(t *testing.T) {
	_, err := NewGRPCServer().Fetch(context.Background(), &pb.FetchRequest{
		Url:                 "https://example.test/",
		Http2SettingsString: "not-settings",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status code = %v, want %v (err=%v)", status.Code(err), codes.InvalidArgument, err)
	}
	_, err = NewGRPCServer().Fetch(context.Background(), &pb.FetchRequest{
		Url: "https://example.test/", Fingerprint: &pb.Fingerprint{},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty fingerprint status = %v, want %v (err=%v)", status.Code(err), codes.InvalidArgument, err)
	}
}

func TestGRPCFetchParentProxyPrecedence(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "grpc-parent-proxy")
	}))
	defer target.Close()
	proxy := testsupport.NewConnectProxy(t)

	response, err := NewGRPCServer().Fetch(context.Background(), &pb.FetchRequest{
		Url: target.URL + "/probe", Proxy: "http://127.0.0.1:1", ParentProxy: proxy.URL(),
	})
	if err != nil || !response.Ok || response.Body != "grpc-parent-proxy" || proxy.Connects() != 1 {
		t.Fatalf("unexpected response: response=%+v err=%v", response, err)
	}
}
