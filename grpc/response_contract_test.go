package main

import (
	"context"
	"encoding/base64"
	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRawBodyViewIsOptInAndPreservesDefaultGRPCMessageBudget(t *testing.T) {
	payload := strings.Repeat("x", 3<<20)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, payload) }))
	defer target.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterFastlsServiceServer(server, NewGRPCServer())
	defer server.Stop()
	go server.Serve(listener)
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := pb.NewFastlsServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := client.Fetch(ctx, &pb.FetchRequest{Url: target.URL})
	if err != nil || !result.Ok || result.Body != payload || result.BodyBase64 != "" {
		t.Fatalf("default response budget regression: %v", err)
	}
	result, err = client.Fetch(ctx, &pb.FetchRequest{Url: target.URL, IncludeBodyBase64: true}, grpc.MaxCallRecvMsgSize(16<<20))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := base64.StdEncoding.DecodeString(result.BodyBase64)
	if err != nil || string(wire) != payload {
		t.Fatal("opt-in wire body changed", err)
	}
}
