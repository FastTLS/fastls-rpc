package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/serverlifecycle"
	"github.com/FastTLS/fastls-rpc/internal/shutdown"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/stats"
)

func TestShutdownRetainsDecoderAndRejectsLateUpstream(t *testing.T) {
	codec := &heldIngressCodec{started: make(chan struct{}), release: make(chan struct{})}
	heldIngressDecoder.Store(codec)
	defer heldIngressDecoder.Store(nil)
	var once sync.Once
	unblock := func() { once.Do(func() { close(codec.release) }) }
	defer unblock()
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer upstream.Close()
	owner, _ := shutdown.New(shutdown.Config{CancelAfter: 20 * time.Millisecond})
	defer owner.Close()
	service := NewGRPCServer()
	service.shutdown = owner
	server, err := service.newTransportServerWithIngress(grpcIngressConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serverlifecycle.ServeGRPCWithController(ctx, listener, server, owner) }()
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.CallContentSubtype(codec.Name())))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	call := make(chan struct{})
	go func() {
		defer close(call)
		_, _ = pb.NewFastlsServiceClient(connection).Fetch(context.Background(), &pb.FetchRequest{Url: upstream.URL})
	}()
	select {
	case <-codec.started:
	case <-time.After(time.Second):
		t.Fatal("decoder not started")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatal("released decoder before completion", err)
	case <-time.After(80 * time.Millisecond):
	}
	wait, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if err := owner.Wait(wait); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("decoder owner lost", err)
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after decoder")
	}
	select {
	case <-call:
	case <-time.After(time.Second):
		t.Fatal("caller remained blocked")
	}
	if hits.Load() != 0 {
		t.Fatal("canceled decode issued upstream request")
	}
}

func TestCooperativeContextSurvivesLargeResponseWrite(t *testing.T) {
	const size = 2 << 20
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", size))) }))
	defer upstream.Close()
	owner, _ := shutdown.New(shutdown.Config{CancelAfter: time.Second})
	defer owner.Close()
	service := NewGRPCServer()
	service.shutdown = owner
	observer := responseContextObserver{done: make(chan error, 1)}
	server, err := service.newTransportServerWithIngress(grpcIngressConfig{}, grpc.StatsHandler(observer))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(8<<20)))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := pb.NewFastlsServiceClient(connection).Fetch(ctx, &pb.FetchRequest{Url: upstream.URL})
	if err != nil {
		t.Fatal("normal large response interrupted before transport completion", err)
	}
	if !response.Ok || len(response.Body) != size {
		t.Fatal("incomplete large response")
	}
	select {
	case err := <-observer.done:
		if err != nil {
			t.Fatal("RPC context canceled before response serialization/send completed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("missing response observation")
	}
}

type responseContextObserver struct{ done chan error }

func (s responseContextObserver) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}
func (s responseContextObserver) HandleRPC(ctx context.Context, event stats.RPCStats) {
	if _, ok := event.(*stats.OutPayload); ok {
		s.done <- ctx.Err()
	}
}
func (s responseContextObserver) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (s responseContextObserver) HandleConn(context.Context, stats.ConnStats) {}
