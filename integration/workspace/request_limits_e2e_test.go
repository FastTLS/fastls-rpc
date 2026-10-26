//go:build workspace_e2e

package workspace_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/testprocess"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func assertWorkspaceRequestLimits(t *testing.T, fetchBinary, rpcBinary, grpcBinary string) {
	t.Helper()
	for kind, binary := range map[string]string{"fetch": fetchBinary, "jsonrpc": rpcBinary, "grpc": grpcBinary} {
		t.Run("request-budget-"+kind, func(t *testing.T) {
			for _, invalid := range []string{"-1", "67108865"} {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				data, err := exec.CommandContext(ctx, binary, "-addr", "127.0.0.1:0", "-max-request-bytes", invalid).CombinedOutput()
				cancel()
				if err == nil || ctx.Err() == context.DeadlineExceeded || !strings.Contains(string(data), "max-request-bytes must be") {
					t.Fatalf("invalid CLI did not fail before listening: %v %s", err, data)
				}
			}
			var calls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.Copy(io.Discard, r.Body)
				io.WriteString(w, "ok")
			}))
			defer target.Close()
			address := testprocess.ReserveLoopbackAddress(t)
			process := testprocess.Start(t, binary, "-addr", address, "-max-request-bytes", "1024", "-max-inflight", "1")
			if kind == "grpc" {
				assertGRPCHealth(t, address, process)
				connection, client := newGRPCClient(t, address)
				defer connection.Close()
				request := &pb.FetchRequest{Url: target.URL, Method: "POST", Body: strings.Repeat("x", 900)}
				request.Body += strings.Repeat("x", 1024-proto.Size(request))
				if proto.Size(request) != 1024 {
					t.Fatal("test message size wrong")
				}
				for _, extra := range []bool{false, true, false} {
					if extra {
						request.Body += "x"
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					response, err := client.Fetch(ctx, request)
					cancel()
					if extra {
						request.Body = request.Body[:len(request.Body)-1]
						if status.Code(err) != codes.ResourceExhausted {
							t.Fatal(err)
						}
					} else if err != nil || !response.Ok {
						t.Fatalf("exact limit failed: %v %v", response, err)
					}
				}
			} else {
				testprocess.WaitForHTTPStatus(t, "http://"+address+"/health", 200, process)
				var envelope any = map[string]string{"url": target.URL}
				path := "/fetch"
				if kind == "jsonrpc" {
					path = "/rpc"
					envelope = map[string]any{"jsonrpc": "2.0", "method": "fetch", "id": 1, "params": envelope}
				}
				data, _ := json.Marshal(envelope)
				payload := string(data) + strings.Repeat(" ", 1024-len(data))
				for _, extra := range []int{0, 1, 0} {
					response, err := (&http.Client{Timeout: 5 * time.Second}).Post("http://"+address+path, "application/json", strings.NewReader(payload+strings.Repeat(" ", extra)))
					if err != nil {
						t.Fatal(err)
					}
					data, _ := io.ReadAll(response.Body)
					response.Body.Close()
					want := 200
					if extra > 0 {
						want = 413
					}
					if response.StatusCode != want {
						t.Fatalf("status=%d body=%s", response.StatusCode, data)
					}
				}
			}
			if calls.Load() != 2 {
				t.Fatal("oversize reached target or exact request failed", calls.Load())
			}
		})
	}
}
