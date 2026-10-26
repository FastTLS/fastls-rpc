package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/fetchservice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestRequestBudgetExactOversizeCompressedAndRaisedDefault(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, "ok")
	}))
	defer target.Close()
	for _, bodyBytes := range []int{1024, 5 << 20} {
		t.Run(fmt.Sprintf("body_%d", bodyBytes), func(t *testing.T) {
			request := &pb.FetchRequest{Url: target.URL, Method: "POST", Body: strings.Repeat("x", bodyBytes)}
			maximum := proto.Size(request)
			service, err := NewGRPCServerWithLimits(fetchservice.Limits{MaxRequestBytes: int64(maximum)})
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := service.newTransportServer()
			defer server.Stop()
			go server.Serve(listener)
			connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			client := pb.NewFastlsServiceClient(connection)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, compressed := range []bool{false, true} {
				var options []grpc.CallOption
				if compressed {
					options = append(options, grpc.UseCompressor(gzip.Name))
				}
				for _, extra := range []bool{false, true, false} {
					request.Body = strings.Repeat("x", bodyBytes)
					if extra {
						request.Body += "x"
					}
					before := calls.Load()
					result, err := client.Fetch(ctx, request, options...)
					if extra {
						if proto.Size(request) != maximum+1 || status.Code(err) != codes.ResourceExhausted || calls.Load() != before {
							t.Fatalf("oversized/compressed=%v reached target or wrong error: %v", compressed, err)
						}
					} else if err != nil || !result.Ok || result.Body != "ok" {
						t.Fatalf("exact size rejected: %v %v", result, err)
					}
				}
			}
		})
	}
}
