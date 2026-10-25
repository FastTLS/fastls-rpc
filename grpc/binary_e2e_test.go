package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/testprocess"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestGRPCBinaryContract(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(append([]byte(r.Method+"|"), body...))
	}))
	defer target.Close()

	binary := testprocess.Build(t, ".", "fastls-grpc")
	address := testprocess.ReserveLoopbackAddress(t)
	process := testprocess.Start(t, binary, "-addr", address)
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := pb.NewFastlsServiceClient(connection)
	waitForGRPCHealth(t, client, process)

	requestContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := client.Fetch(requestContext, &pb.FetchRequest{
		Url:    target.URL,
		Method: http.MethodPost,
		Body:   "binary-grpc",
	})
	if err != nil {
		t.Fatalf("binary gRPC fetch failed: %v\n%s", err, process.Logs())
	}
	if !response.Ok || response.Body != "POST|binary-grpc" {
		t.Fatalf("binary response: %+v", response)
	}
}

func waitForGRPCHealth(t testing.TB, client pb.FastlsServiceClient, process *testprocess.Process) {
	t.Helper()
	var lastErr error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		response, err := client.Health(ctx, &pb.HealthRequest{})
		cancel()
		if err == nil && response.Status == "ok" {
			return
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("gRPC service did not become ready: %v\n%s", lastErr, process.Logs())
}
