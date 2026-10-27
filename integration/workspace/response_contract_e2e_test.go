//go:build workspace_e2e

package workspace_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/testprocess"
)

type contractResponse struct {
	OK           bool        `json:"ok"`
	Status       int         `json:"status"`
	Protocol     string      `json:"protocol"`
	FinalURL     string      `json:"final_url"`
	HeaderValues http.Header `json:"header_values"`
	Trailers     http.Header `json:"trailers"`
	BodyBase64   string      `json:"body_base64"`
	Body         string      `json:"body"`
	Error        string      `json:"error"`
	ErrorCode    string      `json:"error_code"`
	TimeoutPhase string      `json:"timeout_phase"`
}

func assertWorkspaceServiceLimits(t *testing.T, fetchBinary, rpcBinary, grpcBinary string) {
	t.Helper()
	for _, kind := range []string{"fetch", "jsonrpc", "grpc"} {
		t.Run(kind, func(t *testing.T) {
			started := make(chan struct{}, 1)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/hold":
					started <- struct{}{}
					<-r.Context().Done()
				case "/exact":
					io.WriteString(w, strings.Repeat("x", 128))
				case "/large":
					io.WriteString(w, strings.Repeat("x", 129))
				case "/bomb":
					w.Header().Set("Content-Encoding", "gzip")
					writer := gzip.NewWriter(w)
					io.WriteString(writer, strings.Repeat("x", 4096))
					writer.Close()
				case "/broken":
					w.Header().Set("Content-Encoding", "gzip")
					io.WriteString(w, "bad")
				}
			}))
			defer target.Close()
			binary := map[string]string{"fetch": fetchBinary, "jsonrpc": rpcBinary, "grpc": grpcBinary}[kind]
			address := testprocess.ReserveLoopbackAddress(t)
			process := testprocess.Start(t, binary, "-addr", address, "-max-response-bytes", "128", "-max-inflight", "1")
			var call func(context.Context, string) (contractResponse, error)
			if kind == "grpc" {
				assertGRPCHealth(t, address, process)
				connection, client := newGRPCClient(t, address)
				defer connection.Close()
				call = func(ctx context.Context, path string) (contractResponse, error) {
					response, err := client.Fetch(ctx, &pb.FetchRequest{Url: target.URL + path})
					if err != nil {
						return contractResponse{}, err
					}
					return contractResponse{OK: response.Ok, ErrorCode: response.ErrorCode, Error: response.Error}, nil
				}
			} else {
				testprocess.WaitForHTTPStatus(t, "http://"+address+"/health", 200, process)
				call = func(ctx context.Context, path string) (contractResponse, error) {
					var payload any = map[string]any{"url": target.URL + path}
					endpoint := "/fetch"
					if kind == "jsonrpc" {
						payload = map[string]any{"jsonrpc": "2.0", "method": "fetch", "id": 1, "params": payload}
						endpoint = "/rpc"
					}
					data, _ := json.Marshal(payload)
					request, _ := http.NewRequestWithContext(ctx, "POST", "http://"+address+endpoint, bytes.NewReader(data))
					request.Header.Set("Content-Type", "application/json")
					response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
					if err != nil {
						return contractResponse{}, err
					}
					defer response.Body.Close()
					if kind == "jsonrpc" {
						var envelope struct {
							Result contractResponse `json:"result"`
							Error  any              `json:"error"`
						}
						err = json.NewDecoder(response.Body).Decode(&envelope)
						if envelope.Error != nil {
							return contractResponse{}, fmt.Errorf("RPC error: %v", envelope.Error)
						}
						return envelope.Result, err
					}
					var result contractResponse
					err = json.NewDecoder(response.Body).Decode(&result)
					return result, err
				}
			}
			for _, test := range []struct{ path, code string }{{"/exact", ""}, {"/large", "response_too_large"}, {"/bomb", "response_too_large"}, {"/broken", "decode_error"}} {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				result, err := call(ctx, test.path)
				cancel()
				if err != nil || result.ErrorCode != test.code || result.OK != (test.code == "") {
					t.Fatalf("%s => %+v %v", test.path, result, err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() { call(ctx, "/hold"); close(done) }()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("hold request did not start")
			}
			result, err := call(context.Background(), "/exact")
			if err != nil || result.ErrorCode != "overloaded" {
				t.Fatalf("overload=%+v %v", result, err)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("cancel did not return")
			}
			for deadline := time.Now().Add(2 * time.Second); ; {
				result, err = call(context.Background(), "/exact")
				if err == nil && result.OK {
					break
				}
				if err != nil || result.ErrorCode != "overloaded" || time.Now().After(deadline) {
					t.Fatalf("slot not released: %+v %v", result, err)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func assertWorkspaceResponseContract(t *testing.T, fetchURL, rpcURL, grpcAddress, target, parentProxy string, fetchProcess, rpcProcess *testprocess.Process) {
	t.Helper()
	check := func(t *testing.T, result contractResponse) {
		t.Helper()
		body, err := base64.StdEncoding.DecodeString(result.BodyBase64)
		if !result.OK || result.Status != 200 || result.FinalURL != target || err != nil || !bytes.Equal(body, []byte{0, 255, 128, 1}) {
			t.Fatalf("response mismatch: %+v body=%v err=%v", result, body, err)
		}
		if result.Protocol != "HTTP/2.0" && result.Protocol != "HTTP/1.1" {
			t.Fatalf("missing protocol: %+v", result)
		}
		if !reflect.DeepEqual(result.HeaderValues.Values("X-Repeat"), []string{"one", "two"}) || len(result.HeaderValues.Values("Set-Cookie")) != 2 || result.Trailers.Get("X-Final") != "complete" {
			t.Fatalf("lost headers/trailers: %+v", result)
		}
	}
	for _, route := range []struct{ name, proxy string }{{"direct", ""}, {"mitm", parentProxy}} {
		t.Run(route.name, func(t *testing.T) {
			params := map[string]any{"url": target, "parent_proxy": route.proxy, "include_body_base64": true}
			t.Run("fetch", func(t *testing.T) {
				var result contractResponse
				postJSON(t, fetchURL, params, &result, fetchProcess)
				check(t, result)
			})
			t.Run("jsonrpc", func(t *testing.T) {
				var response struct {
					Result contractResponse `json:"result"`
					Error  any              `json:"error"`
				}
				postJSON(t, rpcURL, map[string]any{"jsonrpc": "2.0", "method": "fetch", "params": params, "id": 7}, &response, rpcProcess)
				if response.Error != nil {
					t.Fatal(response.Error)
				}
				check(t, response.Result)
			})
			t.Run("grpc", func(t *testing.T) {
				conn, client := newGRPCClient(t, grpcAddress)
				defer conn.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				response, err := client.Fetch(ctx, &pb.FetchRequest{Url: target, ParentProxy: route.proxy, IncludeBodyBase64: true})
				if err != nil {
					t.Fatal(err)
				}
				headers, trailers := make(http.Header), make(http.Header)
				for name, values := range response.HeaderValues {
					headers[name] = values.Values
				}
				for name, values := range response.Trailers {
					trailers[name] = values.Values
				}
				check(t, contractResponse{OK: response.Ok, Status: int(response.Status), Protocol: response.Protocol, FinalURL: response.FinalUrl, HeaderValues: headers, Trailers: trailers, BodyBase64: response.BodyBase64, Error: response.Error})
			})
		})
	}
}
